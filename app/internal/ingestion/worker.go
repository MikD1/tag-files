package ingestion

import (
	"context"
	"database/sql"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func (c *Coordinator) BeginIngest(ctx context.Context, job dbgen.Job, now time.Time) (dbgen.UploadSession, error) {
	started := time.Now()
	var session dbgen.UploadSession
	err := c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		var err error
		session, err = leasedUpload(ctx, q, job, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		if session.State == domain.SessionQueued {
			if err := q.MarkUploadProcessing(ctx, session.ID); err != nil {
				return err
			}
			session, err = q.GetUploadSession(ctx, session.ID)
			return err
		}
		return nil
	})
	return session, err
}

// FailIngest commits the visible session and job outcome together. Retry waits
// retain input/MPU/parts/candidates; only explicit retry/cancel ends an error.
func (c *Coordinator) FailIngest(ctx context.Context, job dbgen.Job, now time.Time, code string, retryAt *time.Time) error {
	started := time.Now()
	if len(code) < 1 || len(code) > 100 {
		return domain.Invalid("errorCode", "Invalid worker error code")
	}
	state, jobState, next := domain.SessionFailed, domain.JobFailed, now
	if retryAt != nil {
		state, jobState, next = domain.SessionQueued, domain.JobRetryWait, *retryAt
	}
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		session, err := leasedUpload(ctx, q, job, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		n, err := q.SetUploadWorkerState(ctx, dbgen.SetUploadWorkerStateParams{ID: session.ID, Generation: session.Generation, State: state, ErrorCode: sql.NullString{String: code, Valid: true}})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		n, err = q.FailLeasedJob(ctx, dbgen.FailLeasedJobParams{ID: job.ID, Generation: job.LeaseGeneration, Now: sql.NullString{String: store.Timestamp(now.Add(time.Since(started))), Valid: true}, State: jobState, NextAttemptAt: store.Timestamp(next), ErrorCode: sql.NullString{String: code, Valid: true}})
		if err != nil {
			return err
		}
		if n != 1 {
			return store.ErrLeaseLost
		}
		return nil
	})
}

func Retry(ctx context.Context, tx *sql.Tx, id string, now time.Time) (Outcome, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Outcome{}, err
	}
	q := dbgen.New(tx)
	result, err := lookup(ctx, q, id)
	if err != nil || result.State == "ready" {
		return result, err
	}
	session := result.Session
	if session.State == domain.SessionQueued || session.State == domain.SessionProcessing {
		return result, nil
	}
	if session.State != domain.SessionFailed || !session.SourceSha256.Valid || !validHash(session.SourceSha256.String) {
		return Outcome{}, ErrConflict
	}
	job, err := q.GetIngestionJob(ctx, "ingest/"+id)
	if err != nil {
		return Outcome{}, missing(err)
	}
	payload, err := decodeIngestJob(job)
	if err != nil || payload.UploadID != session.ID || payload.Generation != session.Generation {
		return Outcome{}, ErrConflict
	}
	n, err := q.RetryIngestionJob(ctx, dbgen.RetryIngestionJobParams{ID: job.ID, LeaseGeneration: job.LeaseGeneration, NextAttemptAt: store.Timestamp(now)})
	if err != nil {
		return Outcome{}, err
	}
	if n != 1 {
		return Outcome{}, ErrConflict
	}
	n, err = q.SetUploadWorkerState(ctx, dbgen.SetUploadWorkerStateParams{ID: id, Generation: session.Generation, State: domain.SessionQueued})
	if err != nil {
		return Outcome{}, err
	}
	if n != 1 {
		return Outcome{}, ErrConflict
	}
	return lookup(ctx, q, id)
}

func (c *Coordinator) InterruptIngest(ctx context.Context, job dbgen.Job, now time.Time) error {
	started := time.Now()
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		session, err := leasedUpload(ctx, q, job, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		if _, err := q.SetUploadWorkerState(ctx, dbgen.SetUploadWorkerStateParams{ID: session.ID, Generation: session.Generation, State: domain.SessionQueued}); err != nil {
			return err
		}
		current := now.Add(time.Since(started))
		n, err := q.ReleaseLeasedJob(ctx, dbgen.ReleaseLeasedJobParams{ID: job.ID, Generation: job.LeaseGeneration, Now: sql.NullString{String: store.Timestamp(current), Valid: true}, NextAttemptAt: store.Timestamp(current)})
		if err != nil {
			return err
		}
		if n != 1 {
			return store.ErrLeaseLost
		}
		return nil
	})
}
