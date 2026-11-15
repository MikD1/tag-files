package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/scratch"
)

func TestMetadata507KeepsOperationReplayAndDeletionAvailable(t *testing.T) {
	h := setup(t)
	original := h.request("POST", "/api/v1/tags", `{"name":"retained"}`, "Idempotency-Key", firstKey)
	decode[api.TagCommand](t, original, 201)
	if err := h.s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO collections(name) VALUES('cleanup')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	limited, err := New(h.s, Options{Clock: func() time.Time { return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) }, BeforeMutation: func(_ context.Context) error { return scratch.ErrInsufficientStorage }})
	if err != nil {
		t.Fatal(err)
	}
	h.handler = limited
	p := decode[api.Problem](t, h.request("POST", "/api/v1/tags", `{"name":"no-room"}`, "Idempotency-Key", secondKey), 507)
	if p.Code != "insufficient_storage" {
		t.Fatal(p)
	}
	replay := h.request("POST", "/api/v1/tags", `{"name":"retained"}`, "Idempotency-Key", firstKey)
	if replay.Code != 201 || !bytes.Equal(replay.Body.Bytes(), original.Body.Bytes()) {
		t.Fatal("disk admission hid an already committed operation")
	}
	if out := h.request(http.MethodDelete, "/api/v1/collections/1", "", "If-Match", `"1"`); out.Code != 204 {
		t.Fatalf("space pressure blocked a cleanup command: %d %s", out.Code, out.Body.String())
	}
	var tags, receipts int
	if err := h.s.Reader().QueryRow("SELECT count(*) FROM tags").Scan(&tags); err != nil || tags != 1 {
		t.Fatal("507 changed user data", tags, err)
	}
	if err := h.s.Reader().QueryRow("SELECT count(*) FROM operation_results").Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("temporary507 consumed the operation key", receipts, err)
	}
}
