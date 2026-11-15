package ingestion

import (
	"context"
	"database/sql"
	"math"
	"time"

	"github.com/mikd1/tag-files/app/internal/deletion"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// RecoverInterrupted is startup-only, after verified persistent pairing and
// before admission/claims. No source/candidate/journal/receipt is removed.
func RecoverInterrupted(ctx context.Context, db *store.Store, now time.Time) (int, error) {
	changed := 0
	err := db.RecoverStartup(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		library, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		if !library.StorageID.Valid || !library.StorageVolumeID.Valid || !storageprotocol.ValidVolumeID(library.StorageVolumeID.String) {
			return storageprotocol.ErrBindingMismatch
		}
		if err := objectio.PreflightJournal(ctx, tx); err != nil {
			return err
		}
		after := ""
		for {
			jobs, err := q.ListJobsForStartup(ctx, dbgen.ListJobsForStartupParams{AfterID: after, LimitCount: 100})
			if err != nil {
				return err
			}
			for _, job := range jobs {
				if err := validateStartupJob(ctx, q, library, job); err != nil {
					return err
				}
				if job.State == domain.JobRunning {
					if job.LeaseGeneration == math.MaxInt64 {
						return ErrConflict
					}
					n, err := q.ResetInterruptedJob(ctx, dbgen.ResetInterruptedJobParams{ID: job.ID, LeaseGeneration: job.LeaseGeneration, NextAttemptAt: store.Timestamp(now)})
					if err != nil {
						return err
					}
					if n != 1 {
						return store.ErrLeaseLost
					}
					changed++
					if job.Type == domain.JobIngest {
						payload, err := decodeIngestJob(job)
						if err != nil {
							return err
						}
						if _, err := q.SetUploadWorkerState(ctx, dbgen.SetUploadWorkerStateParams{ID: payload.UploadID, Generation: payload.Generation, State: domain.SessionQueued}); err != nil {
							return err
						}
					}
				} else if job.State == domain.JobRetryWait {
					bound := store.Timestamp(now.Add(domain.JobRetryDelay(job.Attempt)))
					n, err := q.BoundRetryWait(ctx, dbgen.BoundRetryWaitParams{ID: job.ID, Bound: bound})
					if err != nil {
						return err
					}
					changed += int(n)
				} else if job.State == domain.JobQueued {
					n, err := q.MakeQueuedJobAvailable(ctx, dbgen.MakeQueuedJobAvailableParams{ID: job.ID, Now: store.Timestamp(now)})
					if err != nil {
						return err
					}
					changed += int(n)
				}
				after = job.ID
			}
			if len(jobs) < 100 {
				return nil
			}
		}
	})
	if err != nil {
		return 0, err
	}
	return changed, nil
}

func validateStartupJob(ctx context.Context, q *dbgen.Queries, library dbgen.LibraryMetum, job dbgen.Job) error {
	if domain.ValidateUUID(job.ID) != nil {
		return ErrConflict
	}
	if _, err := time.Parse("2006-01-02T15:04:05.0000000Z", job.NextAttemptAt); err != nil {
		return ErrConflict
	}
	switch job.Type {
	case domain.JobComplete:
		p, err := decodeCompleteJob(job)
		if err != nil {
			return err
		}
		session, err := readSession(ctx, q, p.UploadID)
		if err != nil {
			return err
		}
		if session.Generation != p.Generation || session.IncomingKey != incomingKey(library.LibraryID, session.ID) || session.SourceSha256.Valid || !session.MultipartUploadID.Valid || (session.State != domain.SessionCompleting && session.State != domain.SessionFailed) || ((job.State == domain.JobFailed) != (session.State == domain.SessionFailed)) {
			return ErrConflict
		}
		if session.CompletionManifest.Valid {
			parts, err := decodeManifest(session.CompletionManifest.String)
			if err != nil {
				return err
			}
			if len(parts) != len(p.Parts) {
				return ErrConflict
			}
			for i, part := range parts {
				if part.Number != p.Parts[i].Number || part.ETag != p.Parts[i].ETag {
					return ErrConflict
				}
			}
		}
		return nil
	case domain.JobIngest:
		payload, err := decodeIngestJob(job)
		if err != nil {
			return err
		}
		session, err := q.GetUploadSession(ctx, payload.UploadID)
		if err != nil {
			return err
		}
		if session.Generation != payload.Generation || session.IncomingKey != incomingKey(library.LibraryID, session.ID) || !session.SourceSha256.Valid || !validHash(session.SourceSha256.String) || !session.CompletionManifest.Valid {
			return ErrConflict
		}
		if session.State != domain.SessionQueued && session.State != domain.SessionProcessing && session.State != domain.SessionFailed {
			return ErrConflict
		}
		if (job.State == domain.JobFailed) != (session.State == domain.SessionFailed) || (job.State != domain.JobRunning && session.State == domain.SessionProcessing) {
			return ErrConflict
		}
		if _, err := decodeManifest(session.CompletionManifest.String); err != nil {
			return err
		}
		return nil
	case domain.JobCleanup:
		id, err := cleanupReceiptID(job)
		if err != nil {
			return err
		}
		receipt, err := q.GetCleanupReceipt(ctx, id)
		if err != nil {
			return err
		}
		if receipt.Reason == "duplicate_mpu" {
			plan, err := decodeExtraMultipart(receipt)
			if err != nil {
				return err
			}
			return checkExtraMultipartFence(ctx, q, receipt, plan)
		}
		if receipt.Reason == poster.CleanupReason {
			_, err := poster.ValidateCleanup(ctx, q, receipt)
			return err
		}
		plan, err := decodeCancellation(receipt)
		if err != nil {
			return err
		}
		return checkCancellationFence(ctx, q, receipt, plan)
	case domain.JobDelete:
		_, err := deletion.ValidateJob(ctx, q, job)
		return err
	case domain.JobPoster:
		payload, err := poster.DecodeJob(job)
		if err != nil {
			return err
		}
		item, err := q.GetItem(ctx, payload.ItemID)
		if err != nil {
			return err
		}
		if !item.PrimaryAssetID.Valid || item.PrimaryAssetID.String != payload.PrimaryAssetID {
			return ErrConflict
		}
		asset, err := q.GetCleanupAsset(ctx, payload.PrimaryAssetID)
		if err != nil {
			return err
		}
		if !asset.ItemID.Valid || asset.ItemID.Int64 != item.ID || asset.Role != "primary" {
			return ErrConflict
		}
		return nil
	default:
		return ErrConflict
	}
}
