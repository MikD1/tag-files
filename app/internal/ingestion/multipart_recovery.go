package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type extraMultipartPlan struct {
	Version   int    `json:"version"`
	LibraryID string `json:"libraryId"`
	UploadID  string `json:"uploadId"`
	Key       string `json:"key"`
	KeptID    string `json:"keptMultipartId"`
	ExtraID   string `json:"extraMultipartId"`
}

func validMultipartID(id string) bool {
	return len(id) > 0 && len(id) <= 4096 && utf8.ValidString(id) && !strings.ContainsAny(id, "\x00\r\n")
}

func enqueueCleanup(ctx context.Context, q *dbgen.Queries, reason, uploadID string, generation int64, payload string, now time.Time) (receiptID, jobID string, err error) {
	receiptID = domain.NewUUID()
	timestamp := store.Timestamp(now)
	_, err = q.CreateCleanupReceipt(ctx, dbgen.CreateCleanupReceiptParams{ID: receiptID, Reason: reason, UploadID: sql.NullString{String: uploadID, Valid: true}, PayloadJson: payload, Generation: generation, CreatedAtUtc: timestamp})
	if err != nil {
		return "", "", err
	}
	jobPayload, err := json.Marshal(struct {
		Version   int    `json:"version"`
		ReceiptID string `json:"receiptId"`
	}{Version: 1, ReceiptID: receiptID})
	if err != nil {
		return "", "", err
	}
	job, err := q.CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobCleanup, SubjectID: receiptID, DedupeKey: "cleanup/" + receiptID, PayloadJson: string(jobPayload), NextAttemptAt: timestamp, CreatedAt: timestamp})
	if err != nil {
		return "", "", err
	}
	return receiptID, job.ID, nil
}
func (c *Coordinator) inventoryMultipart(ctx context.Context, session Session) (Session, error) {
	// Decode all prior work before choosing an MPU or scheduling deletion.
	// An older application must not adopt/abort resources whose future intent
	// format it cannot interpret. Pagination keeps this preflight bounded.
	cursor := ""
	for {
		intents, err := c.io.ListKey(ctx, session.IncomingKey, cursor, 100)
		if err != nil {
			return Session{}, err
		}
		for _, intent := range intents {
			if intent.TargetKind != "upload" || intent.TargetID != session.ID {
				return Session{}, ErrConflict
			}
			cursor = intent.ID
		}
		if len(intents) < 100 {
			break
		}
	}
	var current Session
	err := c.io.Read(ctx, func(reader objectio.Reader) error {
		var err error
		current, err = c.inventoryMultipartWith(ctx, session, reader)
		return err
	})
	return current, err
}

// inventoryMultipartWith commits one stable chosen identity and independent
// receipts for all observed extras. Listing never changes an attached MPU.
func (c *Coordinator) inventoryMultipartWith(ctx context.Context, session Session, reader objectio.Reader) (Session, error) {
	listed, err := reader.ListMultipart(ctx, session.IncomingKey)
	if err != nil {
		return Session{}, err
	}
	ids := map[string]bool{}
	for _, mpu := range listed {
		if mpu.Key == session.IncomingKey {
			if !validMultipartID(mpu.UploadID) {
				return Session{}, objectstore.ErrIntegrity
			}
			ids[mpu.UploadID] = true
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	var current Session
	err = c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		library, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		current, err = readSession(ctx, q, session.ID)
		if err != nil {
			return err
		}
		if current.Generation != session.Generation || current.IncomingKey != session.IncomingKey || current.IncomingKey != incomingKey(library.LibraryID, current.ID) {
			return ErrConflict
		}
		if !current.MultipartUploadID.Valid {
			if len(ordered) == 0 {
				return nil
			}
			if err := AttachMultipart(ctx, tx, current.ID, current.Generation, ordered[0]); err != nil {
				return err
			}
			current, err = readSession(ctx, q, current.ID)
			if err != nil {
				return err
			}
		}
		if !validMultipartID(current.MultipartUploadID.String) {
			return ErrConflict
		}
		for _, id := range ordered {
			if id == current.MultipartUploadID.String {
				continue
			}
			_, err := q.FindDuplicateMultipartReceipt(ctx, dbgen.FindDuplicateMultipartReceiptParams{UploadID: sql.NullString{String: current.ID, Valid: true}, MultipartID: id})
			if err == nil {
				continue
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			payload, err := json.Marshal(extraMultipartPlan{Version: 1, LibraryID: library.LibraryID, UploadID: current.ID, Key: current.IncomingKey, KeptID: current.MultipartUploadID.String, ExtraID: id})
			if err != nil {
				return err
			}
			if _, _, err := enqueueCleanup(ctx, q, "duplicate_mpu", current.ID, current.Generation, string(payload), time.Now()); err != nil {
				return err
			}
		}
		return nil
	})
	return current, err
}
func decodeExtraMultipart(receipt dbgen.CleanupReceipt) (extraMultipartPlan, error) {
	var plan extraMultipartPlan
	decoder := json.NewDecoder(strings.NewReader(receipt.PayloadJson))
	decoder.DisallowUnknownFields()
	if receipt.PayloadVersion != 1 || receipt.Reason != "duplicate_mpu" || decoder.Decode(&plan) != nil || !json.Valid([]byte(receipt.PayloadJson)) || plan.Version != 1 || domain.ValidateUUID(plan.LibraryID) != nil || domain.ValidateUUID(plan.UploadID) != nil || !receipt.UploadID.Valid || receipt.UploadID.String != plan.UploadID || plan.Key != incomingKey(plan.LibraryID, plan.UploadID) || !validMultipartID(plan.KeptID) || !validMultipartID(plan.ExtraID) || plan.KeptID == plan.ExtraID || receipt.Generation < 1 {
		return plan, ErrIncompatibleCleanup
	}
	return plan, nil
}
func checkExtraMultipartFence(ctx context.Context, q *dbgen.Queries, receipt dbgen.CleanupReceipt, plan extraMultipartPlan) error {
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return err
	}
	if library.LibraryID != plan.LibraryID {
		return ErrConflict
	}
	current, err := q.GetUploadSession(ctx, plan.UploadID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Generation < receipt.Generation || current.IncomingKey != plan.Key || !current.MultipartUploadID.Valid || current.MultipartUploadID.String != plan.KeptID {
		return ErrConflict
	}
	return nil
}
func (c *Coordinator) processExtraMultipart(ctx context.Context, job dbgen.Job, receipt dbgen.CleanupReceipt, now time.Time) error {
	started := time.Now()
	plan, err := decodeExtraMultipart(receipt)
	if err != nil {
		return err
	}
	if err := checkExtraMultipartFence(ctx, dbgen.New(c.db.Reader()), receipt, plan); err != nil {
		return err
	}
	if err := c.io.Drain(ctx, plan.Key, objectio.CreateMPU); err != nil {
		return err
	}
	err = c.io.RemoveOwned(ctx, func(client objectio.ReconciliationClient) error {
		if err := checkExtraMultipartFence(ctx, dbgen.New(c.db.Reader()), receipt, plan); err != nil {
			return err
		}
		if err := client.Abort(ctx, plan.Key, plan.ExtraID); err != nil {
			return err
		}
		listed, err := client.ListMultipart(ctx, plan.Key)
		if err != nil {
			return err
		}
		for _, mpu := range listed {
			if mpu.Key == plan.Key && mpu.UploadID == plan.ExtraID {
				return objectio.ErrUnsettled
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return c.db.CommitJob(ctx, job.ID, job.LeaseGeneration, now.Add(time.Since(started)), func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		current, err := q.GetCleanupReceipt(ctx, receipt.ID)
		if err != nil {
			return missing(err)
		}
		if current.PayloadVersion != receipt.PayloadVersion || current.PayloadJson != receipt.PayloadJson || current.Generation != receipt.Generation {
			return ErrConflict
		}
		if err := checkExtraMultipartFence(ctx, q, current, plan); err != nil {
			return err
		}
		return q.DeleteCleanupReceipt(ctx, receipt.ID)
	})
}
