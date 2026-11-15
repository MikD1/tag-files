package httpapi

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func deletionFixture(t *testing.T, h *harness) int64 {
	t.Helper()
	var id int64
	if err := h.s.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		meta, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		if err := tx.QueryRow("INSERT INTO items(media_kind,uploaded_at_utc,state) VALUES('Text','2001-01-01T00:00:00.0000000Z','importing') RETURNING id").Scan(&id); err != nil {
			return err
		}
		asset := domain.NewUUID()
		if _, err := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','tag-files',?,0,?,'text/plain','legacy','published')", asset, id, "primary/"+meta.LibraryID+"/"+asset+"/media.txt", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"); err != nil {
			return err
		}
		return q.MarkItemReady(ctx, dbgen.MarkItemReadyParams{ID: id, PrimaryAssetID: sql.NullString{String: asset, Valid: true}})
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestHTTPDeleteHidesItemReplaysDurableWorkAndRequiresVersion(t *testing.T) {
	h := setup(t)
	id := deletionFixture(t, h)
	path := fmt.Sprintf("/api/v1/items/%d", id)
	decode[api.Problem](t, h.request("DELETE", path, ""), 400)
	decode[api.Problem](t, h.request("DELETE", path, "", "If-Match", `"2"`), 412)
	decode[api.Item](t, h.request("GET", path, ""), 200)
	first := decode[api.JobReceipt](t, h.request("DELETE", path, "", "If-Match", `"1"`), 202)
	decode[api.Problem](t, h.request("GET", path, ""), 404)
	again := decode[api.JobReceipt](t, h.request("DELETE", path, "", "If-Match", `"1"`), 202)
	if again.JobId != first.JobId {
		t.Fatal("repeat DELETE created a new cleanup")
	}
	response := h.request("DELETE", "/api/v1/items/99999", "", "If-Match", `"1"`)
	if response.Code != 204 || response.Body.Len() != 0 {
		t.Fatalf("absent DELETE: %d %s", response.Code, response.Body)
	}
}
