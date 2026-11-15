package worker

import (
	"context"
	"time"

	"github.com/mikd1/tag-files/app/internal/cleanup"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// Cleanup keeps retrying independent receipts after a visible upload is gone.
// An uncertain writer is not settled by exhausting a retry counter.
func Cleanup(c *ingestion.Coordinator, db *store.Store, posters *poster.Service) Handler {
	return Handler{Execute: func(ctx context.Context, j dbgen.Job, now time.Time) error {
		id, err := cleanup.DecodeJob(j)
		if err != nil {
			return err
		}
		receipt, err := dbgen.New(db.Reader()).GetCleanupReceipt(ctx, id)
		if err != nil {
			return err
		}
		if receipt.Reason == poster.CleanupReason {
			if posters == nil {
				return poster.ErrIncompatibleCleanup
			}
			return posters.ProcessCleanup(ctx, j, now)
		}
		return c.ProcessCleanup(ctx, j, now)
	},
		Fail: func(ctx context.Context, j dbgen.Job, now time.Time, code string, next *time.Time) error {
			return db.FailJob(ctx, j.ID, j.LeaseGeneration, now, code, next)
		},
		Interrupt: func(ctx context.Context, j dbgen.Job, now time.Time) error {
			return db.ReleaseJob(ctx, j.ID, j.LeaseGeneration, now)
		},
		RetryForever: true,
	}
}
