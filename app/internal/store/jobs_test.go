package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func seedJob(t *testing.T, s *Store, id string, at time.Time) {
	t.Helper()
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO jobs(id,type,subject_id,dedupe_key,payload_json,next_attempt_at,created_at) VALUES(?,'ingest',?,?,'{"version":1}',?,?)`, id, id, id, Timestamp(at), Timestamp(at))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestJobReclaimFencesStaleWorkerAndCommitsAtomically(t *testing.T) {
	s := openTest(t)
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	seedJob(t, s, "a", now)
	first, err := s.ClaimJob(ctx, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if first.Attempt != 1 || first.LeaseGeneration != 1 {
		t.Fatalf("first claim: %+v", first)
	}
	if _, err := s.ClaimJob(ctx, now.Add(time.Second), time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("double claim: %v", err)
	}
	reclaimed, err := s.ClaimJob(ctx, now.Add(time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.Attempt != 2 || reclaimed.LeaseGeneration != 2 {
		t.Fatalf("reclaim: %+v", reclaimed)
	}
	later := now.Add(time.Minute + time.Second)
	if err := s.HeartbeatJob(ctx, first.ID, first.LeaseGeneration, later, time.Minute); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale heartbeat: %v", err)
	}
	if err := s.CommitJob(ctx, first.ID, first.LeaseGeneration, later, func(*sql.Tx) error { t.Fatal("stale callback ran"); return nil }); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale commit: %v", err)
	}
	abort := errors.New("publication failed")
	if err := s.CommitJob(ctx, reclaimed.ID, reclaimed.LeaseGeneration, later, func(tx *sql.Tx) error {
		mustExec(t, tx, `INSERT INTO tags(name) VALUES('should-rollback')`)
		return abort
	}); !errors.Is(err, abort) {
		t.Fatal(err)
	}
	var n int
	s.Reader().QueryRow(`SELECT count(*) FROM tags`).Scan(&n)
	if n != 0 {
		t.Fatal("partial publication committed")
	}
	if err := s.CommitJob(ctx, reclaimed.ID, reclaimed.LeaseGeneration, later, func(tx *sql.Tx) error { mustExec(t, tx, `INSERT INTO tags(name) VALUES('published')`); return nil }); err != nil {
		t.Fatal(err)
	}
	s.Reader().QueryRow(`SELECT count(*) FROM jobs`).Scan(&n)
	if n != 0 {
		t.Fatal("successful job archived instead of removed")
	}
	if err := s.CommitJob(ctx, reclaimed.ID, reclaimed.LeaseGeneration, later, func(*sql.Tx) error { return nil }); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("repeated commit: %v", err)
	}
}

func TestConcurrentClaimsAreUniqueAndFailedJobsDoNotExpire(t *testing.T) {
	s := openTest(t)
	now := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ {
		seedJob(t, s, fmt.Sprintf("job-%02d", i), now)
	}
	var wg sync.WaitGroup
	got := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, err := s.ClaimJob(ctx, now, time.Minute)
			if err != nil {
				t.Error(err)
				return
			}
			got <- job.ID
		}()
	}
	wg.Wait()
	close(got)
	seen := map[string]bool{}
	for id := range got {
		if seen[id] {
			t.Fatalf("duplicate claim %s", id)
		}
		seen[id] = true
	}
	if len(seen) != 12 {
		t.Fatalf("claimed %d", len(seen))
	}
	if err := s.FailJob(ctx, "job-00", 1, now.Add(time.Second), "unsupported_codec", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.FailJob(ctx, "job-01", 1, now.Add(time.Second), "storage_unavailable", ptrTime(now.Add(30*time.Second))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimJob(ctx, now.Add(29*time.Second), time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retry too soon: %v", err)
	}
	retry, err := s.ClaimJob(ctx, now.Add(30*time.Second), time.Minute)
	if err != nil || retry.ID != "job-01" {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	var state string
	if err := s.Reader().QueryRow(`SELECT state FROM jobs WHERE id='job-00'`).Scan(&state); err != nil || state != "failed" {
		t.Fatalf("failed disappeared: %s %v", state, err)
	}
	// A failed task is never reclaimed because of its age.
	for i := 0; i < 11; i++ {
		if _, err := s.ClaimJob(ctx, now.AddDate(10, 0, 0), time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ClaimJob(ctx, now.AddDate(10, 0, 0), time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("failed task reclaimed: %v", err)
	}
}

func ptrTime(v time.Time) *time.Time { return &v }

func TestJobLeaseValidationAndHeartbeat(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC()
	seedJob(t, s, "a", now)
	if _, err := s.ClaimJob(ctx, now, 0); err == nil {
		t.Fatal("zero lease accepted")
	}
	job, err := s.ClaimJob(ctx, now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.HeartbeatJob(ctx, job.ID, job.LeaseGeneration, now.Add(30*time.Second), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimJob(ctx, now.Add(61*time.Second), time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("heartbeat not durable: %v", err)
	}
	if err := s.CommitJob(ctx, job.ID, job.LeaseGeneration, now.Add(90*time.Second), func(*sql.Tx) error { return nil }); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("expired lease committed: %v", err)
	}
}
