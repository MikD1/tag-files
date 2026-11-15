package application_test

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/application"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/operations"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func TestApplicationPrunesOnlyExpiredOperationalMetadataWhileServing(t *testing.T) {
	c, _ := setup(t)
	db, err := store.Open(ctx, c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * operations.Retention)
	for i := range 2 {
		at := old
		if i == 1 {
			at = time.Now()
		}
		if _, err := operations.Execute(ctx, db, operations.Request{Key: domain.NewUUID(), Method: "POST", Route: "/fixture"}, at, func(*sql.Tx) (operations.Result, error) { return operations.Result{Status: 204}, nil }); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		if err := q.CreateNavigationContext(ctx, dbgen.CreateNavigationContextParams{ID: domain.NewUUID(), OriginJson: `{"url":"/library"}`, OrderedIdsJson: `[]`, CreatedAt: store.Timestamp(old), ExpiresAt: store.Timestamp(old.Add(time.Hour))}); err != nil {
			return err
		}
		batch, err := ingestion.Create(ctx, tx, []ingestion.File{{ClientFileID: domain.NewUUID(), Filename: "retained.png", Size: 7}}, nil, ingestion.DefaultLimits(), old)
		if err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE upload_sessions SET state='failed',error_code='fixture' WHERE id=?", batch.Sessions[0].ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	app, err := application.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	_, stop, done := serve(t, app)
	defer stop()
	// Independent read-only verification observes actual housekeeping, without
	// obtaining a second library owner or mutating through a test-only endpoint.
	observer, err := sql.Open("sqlite3", (&url.URL{Scheme: "file", Path: filepath.Join(c.DataDir, "library.sqlite"), RawQuery: "mode=ro&_query_only=1"}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	deadline := time.Now().Add(2 * time.Second)
	var ops, nav int
	for {
		if err := observer.QueryRow("SELECT count(*) FROM operation_results").Scan(&ops); err != nil {
			t.Fatal(err)
		}
		if err := observer.QueryRow("SELECT count(*) FROM navigation_contexts").Scan(&nav); err != nil {
			t.Fatal(err)
		}
		if ops == 1 && nav == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serving application did not prune expired receipts/contexts", ops, nav)
		}
		time.Sleep(time.Millisecond)
	}
	var sessions int
	if err := observer.QueryRow("SELECT count(*) FROM upload_sessions WHERE state='failed'").Scan(&sessions); err != nil || sessions != 1 {
		t.Fatal("operational retention removed old failed input", sessions, err)
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
