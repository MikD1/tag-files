// Package deletion owns irreversible item hiding and the independently retained
// key inventory used to finish and verify deletion after storage restart.
package deletion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrIncompatible = errors.New("unsupported or inconsistent item deletion")

const Reason = "delete"

type Result struct {
	Absent           bool
	JobID, ReceiptID string
}
type plan struct {
	Version       int    `json:"version"`
	LibraryID     string `json:"libraryId"`
	ItemID        int64  `json:"itemId"`
	UploadID      string `json:"uploadId,omitempty"`
	ResourceCount int64  `json:"resourceCount"`
}
type jobPayload struct {
	Version   int    `json:"version"`
	ReceiptID string `json:"receiptId"`
	ItemID    int64  `json:"itemId"`
}

func nullableID(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: true} }
func DecodeJob(j dbgen.Job) (string, error) {
	var p jobPayload
	decoder := json.NewDecoder(strings.NewReader(j.PayloadJson))
	decoder.DisallowUnknownFields()
	if j.Type != domain.JobDelete || decoder.Decode(&p) != nil || !json.Valid([]byte(j.PayloadJson)) || p.Version != 1 || domain.ValidateUUID(p.ReceiptID) != nil || domain.ValidateID(p.ItemID) != nil || j.SubjectID != strconv.FormatInt(p.ItemID, 10) || j.DedupeKey != "delete/"+p.ReceiptID {
		return "", ErrIncompatible
	}
	return p.ReceiptID, nil
}

func ValidateJob(ctx context.Context, q *dbgen.Queries, job dbgen.Job) (plan, error) {
	id, err := DecodeJob(job)
	if err != nil {
		return plan{}, err
	}
	r, err := q.GetCleanupReceipt(ctx, id)
	if err != nil {
		return plan{}, err
	}
	p, err := Validate(ctx, q, r)
	if err != nil {
		return p, err
	}
	if job.SubjectID != strconv.FormatInt(p.ItemID, 10) {
		return p, ErrIncompatible
	}
	return p, nil
}

// Request only performs SQL. A repeat reuses pending work even after metadata
// is removed, until the independent inventory has passed restart verification.
func Request(ctx context.Context, tx *sql.Tx, id, version int64, now time.Time) (Result, error) {
	if domain.ValidateID(id) != nil || version < 1 {
		return Result{}, domain.Invalid("item", "Invalid item or version")
	}
	q := dbgen.New(tx)
	receipt, err := q.FindItemDeletion(ctx, nullableID(id))
	if err == nil {
		if _, err := Validate(ctx, q, receipt); err != nil {
			return Result{}, err
		}
		job, err := q.GetDeletionJob(ctx, "delete/"+receipt.ID)
		if err != nil {
			return Result{}, err
		}
		if _, err := DecodeJob(job); err != nil {
			return Result{}, err
		}
		if job.SubjectID != strconv.FormatInt(id, 10) {
			return Result{}, ErrIncompatible
		}
		return Result{JobID: job.ID, ReceiptID: receipt.ID}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}
	item, err := q.GetDeletionItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Result{Absent: true}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if item.State != domain.ItemReady && item.State != domain.ItemMissing {
		return Result{}, ErrIncompatible
	}
	if item.Version != version {
		return Result{}, domain.ErrStaleVersion
	}
	posterJobs, err := q.ListItemPosterJobs(ctx, strconv.FormatInt(id, 10))
	if err != nil {
		return Result{}, err
	}
	for _, job := range posterJobs {
		payload, err := poster.DecodeJob(job)
		if err != nil {
			return Result{}, err
		}
		if payload.ItemID != id || !item.PrimaryAssetID.Valid || payload.PrimaryAssetID != item.PrimaryAssetID.String {
			return Result{}, ErrIncompatible
		}
	}
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return Result{}, err
	}
	n, err := q.TombstoneItem(ctx, dbgen.TombstoneItemParams{ID: id, Version: version})
	if err != nil {
		return Result{}, err
	}
	if n != 1 {
		return Result{}, domain.ErrStaleVersion
	}
	if err := q.DeleteItemPosterJobs(ctx, strconv.FormatInt(id, 10)); err != nil {
		return Result{}, err
	}
	assetCount, err := q.CountItemAssets(ctx, nullableID(id))
	if err != nil {
		return Result{}, err
	}
	resourceCount := assetCount
	if item.UploadID.Valid {
		resourceCount++
	}
	p := plan{Version: 1, LibraryID: library.LibraryID, ItemID: id, UploadID: item.UploadID.String, ResourceCount: resourceCount}
	body, err := json.Marshal(p)
	if err != nil {
		return Result{}, err
	}
	receiptID, jobID := domain.NewUUID(), domain.NewUUID()
	timestamp := store.Timestamp(now)
	receipt, err = q.CreateDeletionReceipt(ctx, dbgen.CreateDeletionReceiptParams{ID: receiptID, UploadID: item.UploadID, ItemID: nullableID(id), PayloadJson: string(body), CreatedAtUtc: timestamp})
	if err != nil {
		return Result{}, err
	}
	if err := q.SnapshotDeletionAssets(ctx, dbgen.SnapshotDeletionAssetsParams{ReceiptID: receiptID, ItemID: nullableID(id)}); err != nil {
		return Result{}, err
	}
	if item.UploadID.Valid {
		if err := q.SnapshotDeletionIncoming(ctx, dbgen.SnapshotDeletionIncomingParams{ReceiptID: receiptID, AssetID: item.UploadID.String, ObjectKey: "incoming/" + library.LibraryID + "/" + item.UploadID.String + "/source"}); err != nil {
			return Result{}, err
		}
	}
	if _, err := Validate(ctx, q, receipt); err != nil {
		return Result{}, err
	}
	body, err = json.Marshal(jobPayload{Version: 1, ReceiptID: receiptID, ItemID: id})
	if err != nil {
		return Result{}, err
	}
	if _, err := q.CreateJob(ctx, dbgen.CreateJobParams{ID: jobID, Type: domain.JobDelete, SubjectID: strconv.FormatInt(id, 10), DedupeKey: "delete/" + receiptID, PayloadJson: string(body), NextAttemptAt: timestamp, CreatedAt: timestamp}); err != nil {
		return Result{}, err
	}
	if err := q.AdvanceSearchGeneration(ctx); err != nil {
		return Result{}, err
	}
	return Result{ReceiptID: receiptID, JobID: jobID}, nil
}

func ownedKey(library string, a dbgen.DeletionAsset) bool {
	return a.Bucket == "tag-files" && domain.OwnedObjectKey(library, a.AssetID, a.Role, a.ObjectKey)
}

// Validate preflights all immutable inventory pages. It accepts metadata
// absence only as pending deletion work, and never authorizes a foreign key.
func readPlan(ctx context.Context, q *dbgen.Queries, receipt dbgen.CleanupReceipt) (plan, error) {
	var p plan
	d := json.NewDecoder(strings.NewReader(receipt.PayloadJson))
	d.DisallowUnknownFields()
	if receipt.Reason != Reason || receipt.PayloadVersion != 1 || receipt.Generation != 1 || d.Decode(&p) != nil || !json.Valid([]byte(receipt.PayloadJson)) || p.Version != 1 || domain.ValidateUUID(p.LibraryID) != nil || domain.ValidateID(p.ItemID) != nil || receipt.ItemID != nullableID(p.ItemID) || p.ResourceCount < 0 || (p.UploadID != "" && domain.ValidateUUID(p.UploadID) != nil) || p.UploadID != receipt.UploadID.String {
		return p, ErrIncompatible
	}
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return p, err
	}
	if library.LibraryID != p.LibraryID {
		return p, ErrIncompatible
	}
	item, err := q.GetDeletionItem(ctx, p.ItemID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if err == nil && (item.State != domain.ItemDeleting || item.UploadID != receipt.UploadID) {
		return p, ErrIncompatible
	}
	jobs, err := q.ListItemPosterJobs(ctx, strconv.FormatInt(p.ItemID, 10))
	if err != nil {
		return p, err
	}
	if len(jobs) != 0 {
		return p, ErrIncompatible
	}
	if p.UploadID != "" {
		if _, err := q.GetUploadSession(ctx, p.UploadID); !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return p, err
			}
			return p, ErrIncompatible
		}
		if _, err := q.GetIngestionJob(ctx, "ingest/"+p.UploadID); !errors.Is(err, sql.ErrNoRows) {
			if err != nil {
				return p, err
			}
			return p, ErrIncompatible
		}
	}
	return p, nil
}

func Validate(ctx context.Context, q *dbgen.Queries, receipt dbgen.CleanupReceipt) (plan, error) {
	p, err := readPlan(ctx, q, receipt)
	if err != nil {
		return p, err
	}
	n, err := q.CountDeletionAssets(ctx, receipt.ID)
	if err != nil {
		return p, err
	}
	if n != p.ResourceCount {
		return p, ErrIncompatible
	}
	_, itemErr := q.GetDeletionItem(ctx, p.ItemID)
	itemPresent := itemErr == nil
	if itemErr != nil && !errors.Is(itemErr, sql.ErrNoRows) {
		return p, itemErr
	}
	cursor := ""
	for {
		rows, err := q.ListDeletionAssets(ctx, dbgen.ListDeletionAssetsParams{ReceiptID: receipt.ID, AfterID: cursor, LimitCount: 100})
		if err != nil {
			return p, err
		}
		for _, a := range rows {
			if !ownedKey(p.LibraryID, a) || a.AbsenceEpoch.Valid && domain.ValidateUUID(a.AbsenceEpoch.String) != nil {
				return p, ErrIncompatible
			}
			if !itemPresent && !a.AbsenceEpoch.Valid {
				return p, ErrIncompatible
			}
			if a.Role == "incoming" {
				if a.AssetID != p.UploadID || p.UploadID == "" {
					return p, ErrIncompatible
				}
				cursor = a.ObjectKey
				continue
			}
			live, err := q.GetCleanupAsset(ctx, a.AssetID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return p, err
			}
			if itemPresent != (err == nil) || !itemPresent && !a.AbsenceEpoch.Valid {
				return p, ErrIncompatible
			}
			if err == nil && (live.ItemID != nullableID(p.ItemID) || live.ObjectKey != a.ObjectKey || live.Bucket != a.Bucket || live.Role != a.Role || live.SizeBytes != a.SizeBytes.Int64 || live.Sha256 != a.Sha256.String || live.ContentType != a.ContentType.String || live.LeaseGeneration != a.LeaseGeneration || live.JobID != a.JobID) {
				return p, ErrIncompatible
			}
			if a.JobID.Valid {
				if _, err := q.GetJob(ctx, a.JobID.String); !errors.Is(err, sql.ErrNoRows) {
					if err != nil {
						return p, err
					}
					return p, ErrIncompatible
				}
			}
			cursor = a.ObjectKey
		}
		if len(rows) < 100 {
			break
		}
	}
	return p, nil
}
