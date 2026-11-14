package worker

import (
	"context"
	"time"

	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// Cleanup keeps retrying independent receipts after a visible upload is gone.
// An uncertain writer is not settled by exhausting a retry counter.
func Cleanup(c *ingestion.Coordinator, db *store.Store) Handler {
	return Handler{Execute: c.ProcessCleanup,
		Fail: func(ctx context.Context, j dbgen.Job, now time.Time, code string, next *time.Time) error {
			return db.FailJob(ctx, j.ID, j.LeaseGeneration, now, code, next)
		},
		Interrupt: func(ctx context.Context, j dbgen.Job, now time.Time) error {
			return db.ReleaseJob(ctx, j.ID, j.LeaseGeneration, now)
		},
		RetryForever: true,
	}
}
