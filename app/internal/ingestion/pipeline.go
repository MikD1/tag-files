package ingestion

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// ProcessIngest owns one synchronous attempt. All I/O/processes return before
// its private scratch is removed; incoming stays in S3 through the ready commit.
// The single runner supplies heartbeat/cancellation and commits any failure.
func (c *Coordinator) ProcessIngest(ctx context.Context, job dbgen.Job, processor *media.Processor, sandbox *scratch.Manager, now time.Time) (err error) {
	started := time.Now()
	current := func() time.Time { return now.Add(time.Since(started)) }
	session, err := c.BeginIngest(ctx, job, current())
	if err != nil {
		return err
	}
	attempt, err := sandbox.Begin(job.ID, job.LeaseGeneration, session.ExpectedSize, processor.OutputLimit())
	if err != nil {
		return err
	}
	return attempt.Run(ctx, func(work context.Context) error {
		file, err := os.OpenFile(attempt.Source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = c.io.CopyVerified(work, session.IncomingKey, session.ExpectedSize, session.SourceSha256.String, file)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if err := attempt.Check(); err != nil {
			return err
		}
		prepared, err := processor.Prepare(work, attempt.Source, attempt.Output)
		if err != nil {
			return err
		}
		if err := attempt.Check(); err != nil {
			return err
		}
		asset, err := c.NewPrimary(work, job, prepared.Output, current())
		if err != nil {
			return err
		}
		body, err := os.Open(prepared.Path)
		if err != nil {
			return err
		}
		defer body.Close()
		if _, err := c.UploadPrimary(work, job, asset.ID, body, current()); err != nil {
			return err
		}
		_, err = c.PublishPrimary(work, job, asset.ID, current())
		return err
	})
}
