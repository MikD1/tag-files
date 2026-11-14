package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/organization"
	"github.com/mikd1/tag-files/app/internal/store"
)

var ctx = context.Background()

const key = "a22dd4d6-851d-4c55-9071-f117a568a000"

func library(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}
func request() Request {
	return Request{Key: key, Method: "POST", Route: "/api/v1/tags", Body: json.RawMessage(`{"name":"cat"}`)}
}

func TestCommandOutcomeSurvivesLostResponseAndRestart(t *testing.T) {
	s, dir := library(t)
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	first, err := Execute(ctx, s, request(), now, func(tx *sql.Tx) (Result, error) {
		tag, err := organization.CreateTag(ctx, tx, "cat")
		if err != nil {
			return Result{}, err
		}
		body, _ := json.Marshal(map[string]any{"id": tag.ID, "name": tag.Name, "version": tag.Version})
		return Result{Status: 201, Body: body, ETag: `"1"`, Location: "/api/v1/tags/1"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replayed, err := Execute(ctx, s, request(), now.Add(time.Hour), func(*sql.Tx) (Result, error) { t.Fatal("replayed command ran twice"); return Result{}, nil })
	if err != nil {
		t.Fatal(err)
	}
	if string(replayed.Body) != string(first.Body) || replayed.Status != 201 || replayed.ETag != first.ETag || replayed.Location != first.Location {
		t.Fatalf("changed replay %+v", replayed)
	}
	stored, err := Get(ctx, s.Reader(), key, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != "completed" || stored.Result.Status != 201 || stored.ID != key {
		t.Fatalf("operation %+v", stored)
	}
	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM tags`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("duplicate tag %d %v", n, err)
	}
}

func TestConcurrentSameKeyExecutesExactlyOnceAndChangedRequestConflicts(t *testing.T) {
	s, _ := library(t)
	now := time.Now()
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := Execute(ctx, s, request(), now, func(tx *sql.Tx) (Result, error) {
				calls.Add(1)
				_, err := organization.CreateTag(ctx, tx, "cat")
				return Result{Status: 201, Body: json.RawMessage(`{"id":1}`)}, err
			})
			if err != nil || result.Status != 201 {
				t.Errorf("command %+v: %v", result, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("executed %d times", calls.Load())
	}
	for _, changed := range []Request{{Key: key, Method: "POST", Route: "/api/v1/tags", Body: json.RawMessage(`{"name":"dog"}`)}, {Key: key, Method: "POST", Route: "/api/v1/collections", Body: request().Body}} {
		if _, err := Execute(ctx, s, changed, now, func(*sql.Tx) (Result, error) { t.Fatal("conflicting callback ran"); return Result{}, nil }); !errors.Is(err, ErrKeyConflict) {
			t.Fatalf("conflict: %v", err)
		}
	}
}

func TestFailedCommandRollsBackBusinessChangesButRetainsExplicitFailure(t *testing.T) {
	s, _ := library(t)
	now := time.Now()
	_, err := Execute(ctx, s, request(), now, func(tx *sql.Tx) (Result, error) {
		if _, err := organization.CreateTag(ctx, tx, "cat"); err != nil {
			return Result{}, err
		}
		return Result{Status: 422, Problem: true, Body: json.RawMessage(`{"status":422,"code":"test_failure"}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM tags`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed command retained mutation %d %v", n, err)
	}
	op, err := Get(ctx, s.Reader(), key, now)
	if err != nil || op.State != "failed" || !op.Result.Problem {
		t.Fatalf("failure missing %+v %v", op, err)
	}
	replay, err := Execute(ctx, s, request(), now, func(*sql.Tx) (Result, error) { t.Fatal("failed command repeated"); return Result{}, nil })
	if err != nil || replay.Status != 422 {
		t.Fatalf("failed replay %+v %v", replay, err)
	}
}

func TestUnexpectedFailureRollsBackBothResultAndMetadata(t *testing.T) {
	s, _ := library(t)
	now := time.Now()
	abort := errors.New("database or implementation failure")
	_, err := Execute(ctx, s, request(), now, func(tx *sql.Tx) (Result, error) {
		if _, err := organization.CreateTag(ctx, tx, "cat"); err != nil {
			return Result{}, err
		}
		return Result{}, abort
	})
	if !errors.Is(err, abort) {
		t.Fatal(err)
	}
	for _, table := range []string{"tags", "operation_results"} {
		var n int
		if err := s.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("partial %s %d %v", table, n, err)
		}
	}
}

func TestOperationRetentionDoesNotDeleteUploads(t *testing.T) {
	s, _ := library(t)
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	_, err := Execute(ctx, s, request(), now, func(*sql.Tx) (Result, error) {
		return Result{Status: 202, Body: json.RawMessage(`{"jobId":"queued"}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	before, err := Get(ctx, s.Reader(), key, now.Add(7*24*time.Hour-time.Nanosecond))
	if err != nil || before.State != "pending" {
		t.Fatalf("before retention %+v %v", before, err)
	}
	if _, err := Get(ctx, s.Reader(), key, now.Add(7*24*time.Hour)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("expired lookup: %v", err)
	}
	if _, err := Execute(ctx, s, request(), now.Add(7*24*time.Hour), func(*sql.Tx) (Result, error) { t.Fatal("expired replay ran"); return Result{}, nil }); !errors.Is(err, ErrOperationExpired) {
		t.Fatalf("expired replay: %v", err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO upload_sessions(id,batch_id,client_file_id,client_filename,expected_size,part_size,part_count,incoming_key,state,created_at_utc) VALUES('old','batch','file','old.jpg',3,5242880,1,'incoming/old','failed','2000-01-01T00:00:00.0000000Z')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return Prune(ctx, tx, now.AddDate(10, 0, 0)) }); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM upload_sessions`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("retention removed queue %d %v", n, err)
	}
}

func TestReplayCannotUseClockSampleFromBeforeWriterWait(t *testing.T) {
	s, _ := library(t)
	created := time.Now().UTC().Truncate(time.Microsecond).Add(-Retention)
	if _, err := Execute(ctx, s, request(), created, func(*sql.Tx) (Result, error) {
		return Result{Status: 201, Body: json.RawMessage(`{"id":1}`)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	locked, release, writerDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		writerDone <- s.Write(ctx, func(*sql.Tx) error {
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		close(started)
		_, err := Execute(ctx, s, request(), created.Add(Retention-time.Nanosecond), func(*sql.Tx) (Result, error) {
			t.Error("expired receipt ran the command")
			return Result{}, nil
		})
		done <- err
	}()
	<-started
	// Keep the writer occupied beyond the sampled receipt expiry.
	time.Sleep(20 * time.Millisecond)
	close(release)
	if err := <-writerDone; err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrOperationExpired) {
		t.Fatalf("stale clock replay: %v", err)
	}
}
