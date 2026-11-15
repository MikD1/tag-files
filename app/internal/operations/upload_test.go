package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
)

func TestInspectReplaysWithoutWriterAndChecksScopeBeforeExpiry(t *testing.T) {
	s, _ := library(t)
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	if _, err := Execute(ctx, s, request(), now, func(*sql.Tx) (Result, error) { return Result{Status: 201, Body: json.RawMessage(`{"id":1}`)}, nil }); err != nil {
		t.Fatal(err)
	}
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() { done <- s.Write(ctx, func(*sql.Tx) error { close(locked); <-release; return nil }) }()
	<-locked
	probe, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	op, found, err := Inspect(probe, s.Reader(), request(), now)
	cancel()
	close(release)
	if writerErr := <-done; writerErr != nil {
		t.Fatal(writerErr)
	}
	if err != nil || !found || op.Result.Status != 201 {
		t.Fatal("read-only replay waited for the writer", op, found, err)
	}
	changed := request()
	changed.Body = json.RawMessage(`{"name":"changed"}`)
	if _, _, err := Inspect(ctx, s.Reader(), changed, now.Add(Retention+time.Second)); !errors.Is(err, ErrKeyConflict) {
		t.Fatal("expired key lost its known scope", err)
	}
	if _, found, err := Inspect(ctx, s.Reader(), request(), now.Add(Retention)); !found || !errors.Is(err, ErrOperationExpired) {
		t.Fatal("expiry was extended", found, err)
	}
}

func TestUploadFinalReceiptCommitsWithDomainChangesAndPrunesItsLink(t *testing.T) {
	s, _ := library(t)
	now := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)
	upload := domain.NewUUID()
	req := Request{Key: key, Method: "POST", Route: "/api/v1/upload-sessions/" + upload + "/complete", Body: json.RawMessage(`{"generation":1}`)}
	if _, err := Execute(ctx, s, req, now, func(tx *sql.Tx) (Result, error) {
		if err := Link(ctx, tx, key, "upload_complete", upload); err != nil {
			return Result{}, err
		}
		return Result{Status: 202, Body: json.RawMessage(`{"state":"pending"}`)}, nil
	}); err != nil {
		t.Fatal("deferred operation link could not precede its receipt", err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER refuse_result BEFORE UPDATE OF http_status ON operation_results WHEN NEW.http_status=200 BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	finish := func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO tags(name) VALUES('atomic-result')"); err != nil {
			return err
		}
		return ResolveUpload(ctx, tx, upload, 42)
	}
	if err := s.Write(ctx, finish); err == nil {
		t.Fatal("injected receipt failure was acknowledged")
	}
	op, err := Get(ctx, s.Reader(), key, now)
	if err != nil || op.State != "pending" || op.Result.Status != 202 {
		t.Fatal("failed finalization changed receipt", op, err)
	}
	var n int
	if err := s.Reader().QueryRow("SELECT count(*) FROM tags").Scan(&n); err != nil || n != 0 {
		t.Fatal("domain changes escaped failed receipt commit", n, err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER refuse_result"); return err }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, finish); err != nil {
		t.Fatal(err)
	}
	op, err = Get(ctx, s.Reader(), key, now)
	var body struct {
		State, UploadID, OperationID string
		ItemID                       int64
	}
	if err == nil {
		err = json.Unmarshal(op.Result.Body, &body)
	}
	if err != nil || op.State != "completed" || op.Result.Status != 200 || body.State != "ready" || body.UploadID != upload || body.OperationID != key || body.ItemID != 42 {
		t.Fatal("final upload result was not durable", op, body, err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error { return Prune(ctx, tx, now.Add(Retention)) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Reader().QueryRow("SELECT count(*) FROM operation_targets").Scan(&n); err != nil || n != 0 {
		t.Fatal("operation retention left an orphan link", n, err)
	}
}
