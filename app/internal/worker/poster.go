package worker

import (
	"context"
	"time"

	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func Poster(s *poster.Service, db *store.Store, p *media.Processor, sandbox *scratch.Manager) Handler {
	return Handler{Execute: func(ctx context.Context, j dbgen.Job, now time.Time) error {
		return mediaFailure(s.Process(ctx, j, p, sandbox, now))
	},
		Fail: func(ctx context.Context, j dbgen.Job, now time.Time, code string, next *time.Time) error {
			return db.FailJob(ctx, j.ID, j.LeaseGeneration, now, code, next)
		},
		Interrupt: func(ctx context.Context, j dbgen.Job, now time.Time) error {
			return db.ReleaseJob(ctx, j.ID, j.LeaseGeneration, now)
		},
	}
}
