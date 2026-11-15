package poster

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"

	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// Process reads the immutable ready primary, never incoming or an upload
// session. A poster failure leaves the item/primary usable and independently
// retryable; every candidate already owns a durable cleanup receipt.
func (s *Service) Process(ctx context.Context, job dbgen.Job, p *media.Processor, sandbox *scratch.Manager, now time.Time) error {
	started := time.Now()
	current := func() time.Time { return now.Add(time.Since(started)) }
	var primary dbgen.Asset
	finished := false
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		payload, source, err := sourceForLease(ctx, q, job, current())
		if err != nil {
			return err
		}
		primary = source
		_, err = q.GetPublishedPoster(ctx, dbgen.GetPublishedPosterParams{ItemID: integer(payload.ItemID), ProfileVersion: text(Profile)})
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		n, err := q.DeleteLeasedJob(ctx, dbgen.DeleteLeasedJobParams{ID: job.ID, Generation: job.LeaseGeneration, Now: text(store.Timestamp(current()))})
		if err != nil {
			return err
		}
		if n != 1 {
			return store.ErrLeaseLost
		}
		finished = true
		return nil
	})
	if err != nil || finished {
		return err
	}
	a, err := sandbox.Begin(job.ID, job.LeaseGeneration, primary.SizeBytes, p.PosterOutputLimit())
	if err != nil {
		return err
	}
	return a.Run(ctx, func(work context.Context) error {
		file, err := os.OpenFile(a.Source, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = s.io.CopyVerified(work, primary.ObjectKey, primary.SizeBytes, primary.Sha256, file)
		if err == nil {
			err = file.Sync()
		}
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			return errors.Join(err, closeErr)
		}
		if err := a.Check(); err != nil {
			return err
		}
		prepared, err := p.PreparePoster(work, a.Source, a.Output)
		if err != nil {
			return err
		}
		if err := a.Check(); err != nil {
			return err
		}
		candidate, err := s.NewCandidate(work, job, prepared.Output, current())
		if err != nil {
			return err
		}
		body, err := os.Open(prepared.Path)
		if err != nil {
			return err
		}
		defer body.Close()
		if _, err := s.Upload(work, job, candidate.ID, body, current()); err != nil {
			return err
		}
		_, err = s.Publish(work, job, candidate.ID, current())
		return err
	})
}
