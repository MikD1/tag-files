package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrLeaseLost = errors.New("job lease is expired or belongs to another worker generation")

// Timestamp fixes UTC precision at .NET ticks (100 ns) for chronological TEXT
// comparison. Never use time.Duration to store a historical duration_ticks.
func Timestamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.0000000Z") }

func nullableTimestamp(t time.Time) sql.NullString {
	return sql.NullString{String: Timestamp(t), Valid: true}
}

func (s *Store) ClaimJob(ctx context.Context, now time.Time, lease time.Duration) (job dbgen.Job, err error) {
	if lease <= 0 {
		return job, fmt.Errorf("lease must be positive")
	}
	err = s.Write(ctx, func(tx *sql.Tx) error {
		var e error
		job, e = dbgen.New(tx).ClaimJob(ctx, dbgen.ClaimJobParams{LeaseUntil: nullableTimestamp(now.Add(lease)), Now: Timestamp(now)})
		return e
	})
	return job, err
}

func affectedLease(n int64, err error) error {
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}

func (s *Store) HeartbeatJob(ctx context.Context, id string, generation int64, now time.Time, lease time.Duration) error {
	if lease <= 0 {
		return fmt.Errorf("lease must be positive")
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		n, err := dbgen.New(tx).HeartbeatJob(ctx, dbgen.HeartbeatJobParams{ID: id, Generation: generation, Now: nullableTimestamp(now), Until: nullableTimestamp(now.Add(lease))})
		return affectedLease(n, err)
	})
}

// CommitJob applies a worker's SQLite publication and removes the job in ONE
// fenced transaction. All S3 and media work must have finished beforehand.
func (s *Store) CommitJob(ctx context.Context, id string, generation int64, now time.Time, publish func(*sql.Tx) error) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		if _, err := q.GetLeasedJob(ctx, dbgen.GetLeasedJobParams{ID: id, Generation: generation, Now: nullableTimestamp(now)}); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrLeaseLost
			}
			return err
		}
		if err := publish(tx); err != nil {
			return err
		}
		n, err := q.DeleteLeasedJob(ctx, dbgen.DeleteLeasedJobParams{ID: id, Generation: generation, Now: nullableTimestamp(now)})
		return affectedLease(n, err)
	})
}

// FailJob retains permanent failures until explicit retry/cancel. A retry time
// schedules execution; it never gives permission to expire the session/input.
func (s *Store) FailJob(ctx context.Context, id string, generation int64, now time.Time, code string, retryAt *time.Time) error {
	state, next := "failed", now
	if retryAt != nil {
		state, next = "retry_wait", *retryAt
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		n, err := dbgen.New(tx).FailLeasedJob(ctx, dbgen.FailLeasedJobParams{ID: id, Generation: generation, Now: nullableTimestamp(now), State: state, NextAttemptAt: Timestamp(next), ErrorCode: sql.NullString{String: code, Valid: true}})
		return affectedLease(n, err)
	})
}
