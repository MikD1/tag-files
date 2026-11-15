package ingestion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"time"

	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
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
	defer func() { err = errors.Join(err, attempt.Close()) }()
	ctx, err = attempt.Context(ctx)
	if err != nil {
		return err
	}
	work, cancel := context.WithCancelCause(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				if err := attempt.Check(); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	defer func() { cancel(nil); <-stopped }()
	cause := func(err error) error {
		if err != nil && work.Err() != nil {
			return context.Cause(work)
		}
		return err
	}
	file, err := os.OpenFile(attempt.Source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	err = c.io.Read(work, func(reader objectio.Reader) error {
		download, err := reader.Get(work, session.IncomingKey, objectstore.GetOptions{})
		if err != nil {
			return err
		}
		defer download.Body.Close()
		if download.Size != session.ExpectedSize {
			return objectstore.ErrIntegrity
		}
		digest := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(file, digest), &ingestReader{ctx: work, reader: io.LimitReader(download.Body, session.ExpectedSize+1)}, make([]byte, 32768))
		if err != nil {
			return err
		}
		if n != session.ExpectedSize || hex.EncodeToString(digest.Sum(nil)) != session.SourceSha256.String {
			return objectstore.ErrIntegrity
		}
		return file.Sync()
	})
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return cause(errors.Join(err, closeErr))
	}
	if err := attempt.Check(); err != nil {
		return err
	}
	prepared, err := processor.Prepare(work, attempt.Source, attempt.Output)
	if err != nil {
		return cause(err)
	}
	if err := attempt.Check(); err != nil {
		return err
	}
	asset, err := c.NewPrimary(work, job, prepared.Output, current())
	if err != nil {
		return cause(err)
	}
	body, err := os.Open(prepared.Path)
	if err != nil {
		return err
	}
	defer body.Close()
	if _, err := c.UploadPrimary(work, job, asset.ID, body, current()); err != nil {
		return cause(err)
	}
	_, err = c.PublishPrimary(work, job, asset.ID, current())
	return cause(err)
}

type ingestReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *ingestReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
