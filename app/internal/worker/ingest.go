package worker

import (
	"context"
	"errors"
	"time"

	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func Ingestion(c *ingestion.Coordinator, p *media.Processor, sandbox *scratch.Manager) Handler {
	return Handler{Execute: func(ctx context.Context, job dbgen.Job, now time.Time) error {
		err := c.ProcessIngest(ctx, job, p, sandbox, now)
		if err == nil || errors.Is(err, context.Canceled) {
			return err
		}
		failure := &Failure{Code: "storage_unavailable", Temporary: true, Err: err}
		switch {
		case errors.Is(err, media.ErrUnsupportedMedia):
			failure.Code, failure.Temporary = "unsupported_media_type", false
		case errors.Is(err, media.ErrUnsupportedCodec):
			failure.Code, failure.Temporary = "unsupported_codec", false
		case errors.Is(err, media.ErrInvalidMedia):
			failure.Code, failure.Temporary = "invalid_media", false
		case errors.Is(err, media.ErrResourceLimit):
			failure.Code, failure.Temporary = "media_limit_exceeded", false
		case errors.Is(err, objectstore.ErrIntegrity):
			failure.Code, failure.Temporary = "object_integrity", false
		case errors.Is(err, scratch.ErrInsufficientStorage):
			failure.Code = "insufficient_storage"
		}
		return failure
	}, Fail: c.FailIngest, Interrupt: c.InterruptIngest}
}
