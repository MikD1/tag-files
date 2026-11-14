package worker

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func library(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func enqueue(t *testing.T, db *store.Store, kind domain.JobKind, now time.Time) string {
	t.Helper()
	id := domain.NewUUID()
	if err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := dbgen.New(tx).CreateJob(context.Background(), dbgen.CreateJobParams{ID: id, Type: kind, SubjectID: id, DedupeKey: id, PayloadJson: `{"version":1}`, NextAttemptAt: store.Timestamp(now), CreatedAt: store.Timestamp(now)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return id
}
func handler(db *store.Store, run func(context.Context, dbgen.Job, time.Time) error) Handler {
	return Handler{Execute: run, Fail: func(ctx context.Context, j dbgen.Job, now time.Time, code string, next *time.Time) error {
		return db.FailJob(ctx, j.ID, j.LeaseGeneration, now, code, next)
	}, Interrupt: func(ctx context.Context, j dbgen.Job, now time.Time) error {
		return db.ReleaseJob(ctx, j.ID, j.LeaseGeneration, now)
	}}
}

func TestSingleRunnerRenewsLeaseAndLeavesUnregisteredKindsQueued(t *testing.T) {
	db := library(t)
	now := time.Now()
	poster := enqueue(t, db, domain.JobPoster, now.Add(-time.Second))
	enqueue(t, db, domain.JobIngest, now)
	enqueue(t, db, domain.JobIngest, now)
	entered := make(chan dbgen.Job, 2)
	release := make(chan struct{})
	finished := make(chan struct{}, 2)
	var active atomic.Int64
	h := handler(db, func(ctx context.Context, j dbgen.Job, _ time.Time) error {
		if active.Add(1) != 1 {
			t.Error("concurrent media execution")
		}
		defer active.Add(-1)
		entered <- j
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		err := db.CommitJob(ctx, j.ID, j.LeaseGeneration, time.Now(), func(tx *sql.Tx) error { _, err := tx.Exec("INSERT INTO tags(name) VALUES(?)", j.ID); return err })
		finished <- struct{}{}
		return err
	})
	r, err := New(db, map[domain.JobKind]Handler{domain.JobIngest: h}, Config{Lease: 100 * time.Millisecond, Heartbeat: 10 * time.Millisecond, Poll: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	var first dbgen.Job
	select {
	case first = <-entered:
	case <-time.After(time.Second):
		t.Fatal("worker did not claim")
	}
	if _, err := r.RunOne(context.Background()); !errors.Is(err, ErrRunning) {
		t.Fatalf("second runner admitted: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	current, err := dbgen.New(db.Reader()).GetLeasedJob(context.Background(), dbgen.GetLeasedJobParams{ID: first.ID, Generation: first.LeaseGeneration, Now: sql.NullString{String: store.Timestamp(time.Now()), Valid: true}})
	if err != nil || current.Attempt != 1 {
		t.Fatalf("lease not renewed during active media: %+v %v", current, err)
	}
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("serial queue stalled")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not join")
	}
	var state string
	if err := db.Reader().QueryRow("SELECT state FROM jobs WHERE id=?", poster).Scan(&state); err != nil || state != "queued" {
		t.Fatalf("unregistered poster consumed: %s %v", state, err)
	}
	var n int
	db.Reader().QueryRow("SELECT count(*) FROM tags").Scan(&n)
	if n != 2 {
		t.Fatalf("published %d jobs", n)
	}
}

func TestRetryDelaysPersistAndExhaustWithoutExpiringJob(t *testing.T) {
	db := library(t)
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	id := enqueue(t, db, domain.JobIngest, now)
	h := handler(db, func(context.Context, dbgen.Job, time.Time) error {
		return &Failure{Code: "storage_unavailable", Temporary: true, Err: errors.New("offline")}
	})
	r, err := New(db, map[domain.JobKind]Handler{domain.JobIngest: h}, Config{Lease: time.Minute, Heartbeat: 10 * time.Second, Poll: time.Second, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for i, delay := range []time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second, 0} {
		ran, err := r.RunOne(context.Background())
		if err != nil || !ran {
			t.Fatalf("attempt %d: ran=%v err=%v", i+1, ran, err)
		}
		var state, next, code string
		var attempt int
		if err := db.Reader().QueryRow("SELECT state,next_attempt_at,error_code,attempt FROM jobs WHERE id=?", id).Scan(&state, &next, &code, &attempt); err != nil {
			t.Fatal(err)
		}
		if attempt != i+1 || code != "storage_unavailable" {
			t.Fatalf("wrong attempt/error: %d %s", attempt, code)
		}
		if delay == 0 {
			if state != "failed" {
				t.Fatalf("retry limit ignored: %s", state)
			}
			continue
		}
		if state != "retry_wait" || next != store.Timestamp(now.Add(delay)) {
			t.Fatalf("wrong durable retry: %s %s", state, next)
		}
		if ran, err := r.RunOne(context.Background()); err != nil || ran {
			t.Fatalf("early retry: %v %v", ran, err)
		}
		now = now.Add(delay)
	}
	now = now.AddDate(10, 0, 0)
	if ran, err := r.RunOne(context.Background()); err != nil || ran {
		t.Fatalf("failed job expired/reclaimed: %v %v", ran, err)
	}
}

func TestShutdownWaitsForProducerThenReleasesWithoutConsumingRetry(t *testing.T) {
	db := library(t)
	id := enqueue(t, db, domain.JobIngest, time.Now())
	started := make(chan struct{})
	producer := make(chan struct{})
	cancelled := make(chan struct{})
	h := handler(db, func(ctx context.Context, _ dbgen.Job, _ time.Time) error {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-producer
		return ctx.Err()
	})
	r, err := New(db, map[domain.JobKind]Handler{domain.JobIngest: h}, Config{Lease: time.Minute, Heartbeat: 10 * time.Second, Poll: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("producer not cancelled")
	}
	select {
	case <-done:
		t.Fatal("runner returned with live producer")
	case <-time.After(20 * time.Millisecond):
	}
	close(producer)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not join")
	}
	var state string
	var attempt, generation int64
	if err := db.Reader().QueryRow("SELECT state,attempt,lease_generation FROM jobs WHERE id=?", id).Scan(&state, &attempt, &generation); err != nil || state != "queued" || attempt != 0 || generation < 2 {
		t.Fatalf("interruption lost or charged retry: %s %d %d %v", state, attempt, generation, err)
	}
}

func TestBackgroundReceiptRetryDoesNotStopAtVisibleUploadRetryLimit(t *testing.T) {
	db := library(t)
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	id := enqueue(t, db, domain.JobCleanup, now)
	h := handler(db, func(context.Context, dbgen.Job, time.Time) error {
		return &Failure{Code: "unsettled_writer", Temporary: true, Err: errors.New("previous storage epoch still live")}
	})
	h.RetryForever = true
	r, err := New(db, map[domain.JobKind]Handler{domain.JobCleanup: h}, Config{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		ran, err := r.RunOne(context.Background())
		if err != nil || !ran {
			t.Fatalf("receipt abandoned: %v %v", ran, err)
		}
		var state, next string
		if err := db.Reader().QueryRow("SELECT state,next_attempt_at FROM jobs WHERE id=?", id).Scan(&state, &next); err != nil || state != "retry_wait" {
			t.Fatalf("cleanup stopped retrying: %s %v", state, err)
		}
		if i >= 2 && next != store.Timestamp(now.Add(120*time.Second)) {
			t.Fatalf("background retry not bounded: %s", next)
		}
		now, err = time.Parse("2006-01-02T15:04:05.0000000Z", next)
		if err != nil {
			t.Fatal(err)
		}
	}
}
