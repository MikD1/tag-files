package worker

import (
	"context"
	"errors"
	"time"

	"github.com/mikd1/tag-files/app/internal/deletion"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func Deletion(s *deletion.Service, db *store.Store) Handler {
	return Handler{Execute: func(ctx context.Context, j dbgen.Job, now time.Time) error {
		err := s.Process(ctx, j, now)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		code := "deletion_pending"
		if errors.Is(err, deletion.ErrAwaitingRestart) {
			code = "deletion_awaiting_storage_restart"
		}
		return &Failure{Code: code, Temporary: true, Err: err}
	}, Fail: func(ctx context.Context, j dbgen.Job, now time.Time, code string, next *time.Time) error {
		return db.FailJob(ctx, j.ID, j.LeaseGeneration, now, code, next)
	},
		Interrupt: func(ctx context.Context, j dbgen.Job, now time.Time) error {
			return db.ReleaseJob(ctx, j.ID, j.LeaseGeneration, now)
		}, RetryForever: true}
}
