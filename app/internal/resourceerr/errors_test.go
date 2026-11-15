package resourceerr_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/resourceerr"
	"github.com/mikd1/tag-files/app/internal/store"
)

func TestSQLiteFullIsRecognizedAfterRealPageLimitRollback(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Write(ctx, func(tx *sql.Tx) error {
		var pages, limit int
		if err := tx.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
			return err
		}
		if err := tx.QueryRow(fmt.Sprintf("PRAGMA max_page_count=%d", pages+2)).Scan(&limit); err != nil {
			return err
		}
		if _, err := tx.Exec("CREATE TABLE full_fixture(payload BLOB)"); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT INTO full_fixture VALUES(zeroblob(1048576))")
		return err
	})
	if err == nil || !resourceerr.Insufficient(fmt.Errorf("metadata commit: %w", err)) {
		t.Fatal("real SQLite FULL was not classified", err)
	}
	var items int
	if err := db.Reader().QueryRow("SELECT count(*) FROM items").Scan(&items); err != nil || items != 0 {
		t.Fatal("failed write damaged metadata readability", items, err)
	}
}

func TestS3InsufficientStorageUsesActualSDKError(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(507)
		_, _ = w.Write([]byte(`<Error><Code>InsufficientStorage</Code><Message>No capacity</Message></Error>`))
	}))
	defer peer.Close()
	c, err := objectstore.New(objectstore.Config{Endpoint: peer.URL, Bucket: "tag-files", Region: "us-east-1", AccessKey: "test-private-access-key", SecretKey: "test-private-secret-key"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Put(context.Background(), "owned-key", strings.NewReader("saved"), 5, fmt.Sprintf("%x", sha256.Sum256([]byte("saved"))), "image/jpeg", nil)
	if err == nil || !resourceerr.Insufficient(err) {
		t.Fatal("S3 full was not classified", err)
	}
}
