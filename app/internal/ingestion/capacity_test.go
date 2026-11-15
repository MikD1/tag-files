package ingestion

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/mikd1/tag-files/app/internal/scratch"
)

type admissionFunc func(context.Context) error

func (f admissionFunc) Check(ctx context.Context) error { return f(ctx) }

type unreadBody struct{ read bool }

func (r *unreadBody) Read([]byte) (int, error) { r.read = true; return 0, io.EOF }

func TestCapacityRefusalDoesNotDispatchDataOrFreezeCompletion(t *testing.T) {
	db := database(t)
	s := smallSession(t, db)
	base := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("capacity refusal dispatched a write")
		}
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	}))
	c, err := NewCoordinator(db, base.io, admissionFunc(func(context.Context) error { return scratch.ErrInsufficientStorage }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.EnsureMultipart(testContext, s.ID); !errors.Is(err, scratch.ErrInsufficientStorage) {
		t.Fatal("MPU creation bypassed capacity", err)
	}
	transaction(t, db, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, s.ID, 1, "owned-mpu") })
	sum := sha256.Sum256([]byte("source!"))
	binding := PartBinding{Number: 1, Size: 7, SHA256: base64.StdEncoding.EncodeToString(sum[:])}
	body := &unreadBody{}
	if _, err := c.UploadPart(testContext, s.ID, 1, binding, body); !errors.Is(err, scratch.ErrInsufficientStorage) || body.read {
		t.Fatal("507 consumed the browser body", err, body.read)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO upload_parts(session_id,part_number,expected_size,sha256,etag,state) VALUES(?,1,7,?,'\"confirmed\"','confirmed')", s.ID, binding.SHA256)
		return err
	})
	if _, err := c.Complete(testContext, s.ID); !errors.Is(err, scratch.ErrInsufficientStorage) {
		t.Fatal("Complete bypassed capacity", err)
	}
	current, err := c.session(testContext, s.ID)
	if err != nil || current.State != "receiving" || current.CompletionManifest.Valid || len(current.Parts) != 1 {
		t.Fatal("capacity refusal discarded or froze resumable work", err)
	}
	var intents int
	if err := db.Reader().QueryRow("SELECT count(*) FROM object_write_intents").Scan(&intents); err != nil || intents != 0 {
		t.Fatal("capacity refusal dispatched a journalled request", intents, err)
	}
}
