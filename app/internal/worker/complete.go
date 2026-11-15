package worker

import (
	"context"
	"errors"
	"time"

	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func Completion(c *ingestion.Coordinator) Handler {
	return Handler{Execute: func(ctx context.Context, job dbgen.Job, now time.Time) error {
		err := c.ProcessComplete(ctx, job, now)
		if errors.Is(err, ingestion.ErrConflict) {
			return &Failure{Code: "upload_conflict", Err: err}
		}
		return mediaFailure(err)
	}, Fail: c.FailComplete, Interrupt: c.InterruptComplete}
}
