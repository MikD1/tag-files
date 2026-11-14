package store

import (
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCloseKeepsOwnershipUntilActiveWriterFinishes(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entered, release, writeDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	go func() {
		writeDone <- s.Write(ctx, func(tx *sql.Tx) error {
			close(entered)
			<-release
			_, err := tx.Exec(`INSERT INTO tags(name) VALUES('last-write')`)
			return err
		})
	}()
	<-entered
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case err := <-closed:
		unblock()
		<-writeDone
		t.Fatalf("Close returned during active writer: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	if other, err := Open(ctx, dir); !errors.Is(err, ErrLibraryInUse) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("ownership released early: %v", err)
	}
	unblock()
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(*sql.Tx) error { t.Fatal("closed writer callback invoked"); return nil }); err == nil {
		t.Fatal("closed store accepted writer")
	}
}

func TestCommitChecksLeaseAfterWaitingForWriterAndBeforeCommit(t *testing.T) {
	for _, blockBefore := range []bool{true, false} {
		t.Run(map[bool]string{true: "writer-wait", false: "callback-time"}[blockBefore], func(t *testing.T) {
			s := openTest(t)
			now := time.Now()
			seedJob(t, s, "lease", now)
			job, err := s.ClaimJob(ctx, now, 40*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			var called atomic.Bool
			var unblock func()
			var blockerDone chan error
			if blockBefore {
				entered, release := make(chan struct{}), make(chan struct{})
				blockerDone = make(chan error, 1)
				go func() { blockerDone <- s.Write(ctx, func(*sql.Tx) error { close(entered); <-release; return nil }) }()
				<-entered
				unblock = func() { close(release) }
			}
			done := make(chan error, 1)
			go func() {
				done <- s.CommitJob(ctx, job.ID, job.LeaseGeneration, now, func(tx *sql.Tx) error {
					called.Store(true)
					if !blockBefore {
						time.Sleep(80 * time.Millisecond)
					}
					_, err := tx.Exec(`INSERT INTO tags(name) VALUES('expired-publication')`)
					return err
				})
			}()
			if blockBefore {
				time.Sleep(80 * time.Millisecond)
				unblock()
				if err := <-blockerDone; err != nil {
					t.Fatal(err)
				}
			}
			if err := <-done; !errors.Is(err, ErrLeaseLost) {
				t.Fatalf("expired worker committed: %v", err)
			}
			if blockBefore && called.Load() {
				t.Fatal("expired callback ran after waiting")
			}
			var count int
			if err := s.Reader().QueryRow(`SELECT count(*) FROM tags`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("expired publication retained: %d %v", count, err)
			}
		})
	}
}
