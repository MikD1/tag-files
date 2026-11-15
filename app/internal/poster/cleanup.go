package poster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/cleanup"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

const CleanupReason = "poster_candidate"

var ErrIncompatibleCleanup = errors.New("unsupported or invalid poster cleanup receipt")

type candidatePlan struct {
	Version        int    `json:"version"`
	LibraryID      string `json:"libraryId"`
	ItemID         int64  `json:"itemId"`
	PrimaryAssetID string `json:"primaryAssetId"`
	AssetID        string `json:"assetId"`
	JobID          string `json:"jobId"`
	Generation     int64  `json:"generation"`
	Key            string `json:"key"`
	Size           int64  `json:"size"`
	SHA256         string `json:"sha256"`
	Profile        string `json:"profile"`
}

// ValidateCleanup is SQL-only startup preflight. A receipt can validly exist
// while its producer still runs; this does not grant removal permission.
func ValidateCleanup(ctx context.Context, q *dbgen.Queries, receipt dbgen.CleanupReceipt) (candidatePlan, error) {
	var p candidatePlan
	decoder := json.NewDecoder(strings.NewReader(receipt.PayloadJson))
	decoder.DisallowUnknownFields()
	if receipt.Reason != CleanupReason || receipt.PayloadVersion != 1 || receipt.UploadID.Valid || decoder.Decode(&p) != nil || !json.Valid([]byte(receipt.PayloadJson)) || p.Version != 1 || p.Profile != Profile || domain.ValidateUUID(p.LibraryID) != nil || domain.ValidateUUID(p.AssetID) != nil || domain.ValidateUUID(p.PrimaryAssetID) != nil || domain.ValidateUUID(p.JobID) != nil || domain.ValidateID(p.ItemID) != nil || receipt.ItemID != integer(p.ItemID) || p.Generation < 1 || receipt.Generation != p.Generation || p.Key != posterKey(p.LibraryID, p.AssetID) || !validOutput(domain.MediaOutput{Kind: domain.Image, ContentType: "image/jpeg", Size: p.Size, SHA256: p.SHA256, Width: 1, Height: 1}) {
		return p, ErrIncompatibleCleanup
	}
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return p, err
	}
	if library.LibraryID != p.LibraryID {
		return p, ErrConflict
	}
	a, err := q.GetCleanupAsset(ctx, p.AssetID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if err == nil && (a.ItemID.Valid || a.Role != "poster" || a.Bucket != "tag-files" || a.JobID != text(p.JobID) || a.LeaseGeneration != p.Generation || a.ObjectKey != p.Key || a.SizeBytes != p.Size || a.Sha256 != p.SHA256 || a.ContentType != "image/jpeg" || a.ProfileVersion != text(Profile) || (a.State != domain.AssetCandidate && a.State != domain.AssetVerified)) {
		return p, ErrConflict
	}
	job, err := q.GetJob(ctx, p.JobID)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	payload, err := DecodeJob(job)
	if err != nil {
		return p, err
	}
	if payload.ItemID != p.ItemID || payload.PrimaryAssetID != p.PrimaryAssetID || job.LeaseGeneration < p.Generation {
		return p, ErrConflict
	}
	return p, nil
}
func candidateFence(ctx context.Context, q *dbgen.Queries, receipt dbgen.CleanupReceipt) (candidatePlan, error) {
	p, err := ValidateCleanup(ctx, q, receipt)
	if err != nil {
		return p, err
	}
	job, err := q.GetJob(ctx, p.JobID)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if job.LeaseGeneration > p.Generation || job.State == domain.JobFailed || job.State == domain.JobRetryWait {
		return p, nil
	}
	// Wall time/lease expiry alone does not retire an admitted writer. A new
	// claim/reset increments generation; fail changes state before cleanup.
	return p, objectio.ErrUnsettled
}

func (s *Service) fencedPlan(ctx context.Context, id string) (candidatePlan, error) {
	tx, err := s.db.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return candidatePlan{}, err
	}
	defer tx.Rollback()
	q := dbgen.New(tx)
	receipt, err := q.GetCleanupReceipt(ctx, id)
	if err != nil {
		return candidatePlan{}, err
	}
	p, err := candidateFence(ctx, q, receipt)
	if err != nil {
		return p, err
	}
	return p, tx.Commit()
}
func planIntent(p candidatePlan, i objectio.Intent) error {
	if i.TargetKind != "asset" || i.TargetID != p.AssetID || i.Generation != p.Generation || i.Key != p.Key || i.Operation != objectio.PutObject || i.Arguments.Size != p.Size || i.Arguments.SHA256 != p.SHA256 || i.Arguments.ContentType != "image/jpeg" {
		return ErrConflict
	}
	return nil
}
func (s *Service) preflightWrites(ctx context.Context, p candidatePlan) error {
	cursor := ""
	for {
		intents, err := s.io.ListKey(ctx, p.Key, cursor, 100)
		if err != nil {
			return err
		}
		for _, i := range intents {
			if err := planIntent(p, i); err != nil {
				return err
			}
			cursor = i.ID
		}
		if len(intents) < 100 {
			return nil
		}
	}
}
func removeCandidate(ctx context.Context, c objectio.ReconciliationClient, key string) error {
	_, err := c.Head(ctx, key)
	if missingObject(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := c.Delete(ctx, key); err != nil {
		return err
	}
	_, err = c.Head(ctx, key)
	if missingObject(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return objectio.ErrUnsettled
}
func (s *Service) ProcessCleanup(ctx context.Context, job dbgen.Job, now time.Time) error {
	started := time.Now()
	id, err := cleanup.DecodeJob(job)
	if err != nil {
		return err
	}
	p, err := s.fencedPlan(ctx, id)
	if err != nil {
		return err
	}
	if err := s.io.Drain(ctx, p.Key, ""); err != nil {
		return err
	}
	if err := s.preflightWrites(ctx, p); err != nil {
		return err
	}
	remove := func(c objectio.ReconciliationClient) error {
		current, err := s.fencedPlan(ctx, id)
		if err != nil {
			return err
		}
		if current != p {
			return ErrConflict
		}
		return removeCandidate(ctx, c, p.Key)
	}
	if err := s.io.RemoveOwned(ctx, remove); err != nil {
		return err
	}
	cursor := ""
	unsettled := false
	for {
		intents, err := s.io.ListKey(ctx, p.Key, cursor, 100)
		if err != nil {
			return err
		}
		for _, i := range intents {
			if err := planIntent(p, i); err != nil {
				return err
			}
			err := s.io.ReconcileAfterRestart(ctx, i, func(c objectio.ReconciliationClient, current objectio.Intent) error {
				if err := planIntent(p, current); err != nil {
					return err
				}
				return remove(c)
			})
			if errors.Is(err, objectio.ErrUnsettled) {
				unsettled = true
			} else if err != nil {
				return err
			}
			cursor = i.ID
		}
		if len(intents) < 100 {
			break
		}
	}
	if unsettled {
		return objectio.ErrUnsettled
	}
	return s.db.CommitJob(ctx, job.ID, job.LeaseGeneration, now.Add(time.Since(started)), func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		fresh, err := q.GetCleanupReceipt(ctx, id)
		if err != nil {
			return err
		}
		current, err := candidateFence(ctx, q, fresh)
		if err != nil {
			return err
		}
		if current != p {
			return ErrConflict
		}
		n, err := q.CountWritesForKey(ctx, p.Key)
		if err != nil {
			return err
		}
		if n != 0 {
			return objectio.ErrUnsettled
		}
		if _, err := q.DeleteOrphanPoster(ctx, p.AssetID); err != nil {
			return err
		}
		return q.DeleteCleanupReceipt(ctx, id)
	})
}
