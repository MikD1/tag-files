package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/store"
)

var ctx = context.Background()

const firstKey = "7c171c18-99cc-4938-af78-24f70c7c1111"
const secondKey = "7c171c18-99cc-4938-af78-24f70c7c2222"
const thirdKey = "7c171c18-99cc-4938-af78-24f70c7c3333"

func TestHTTPNavigationRejectsMixedFilterSourcesWith400(t *testing.T) {
	for _, source := range []string{`"q":"cat"`, `"kind":"Image"`} {
		t.Run(source, func(t *testing.T) {
			h := setup(t)
			p := decode[api.Problem](t, h.request("POST", "/api/v1/navigation-contexts", `{"origin":"/library","savedSearchId":1,`+source+`}`, "Idempotency-Key", firstKey), 400)
			if p.Code != "filter_source_conflict" {
				t.Fatal(p)
			}
		})
	}
}

func TestHTTPRejectsTrailingJSONBeforeAnyMutation(t *testing.T) {
	for _, body := range []string{`{"name":"cat"}{"name":"dog"}`, `{"name":"cat"}garbage`, `{"name":"cat"} null`} {
		t.Run(body, func(t *testing.T) {
			h := setup(t)
			decode[api.Problem](t, h.request("POST", "/api/v1/tags", body, "Idempotency-Key", firstKey), 400)
			for _, table := range []string{"tags", "operation_results"} {
				var count int
				if err := h.s.Reader().QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatalf("malformed JSON changed %s: count=%d err=%v", table, count, err)
				}
			}
			// Reusing the key after correcting the protocol error creates once.
			decode[api.TagCommand](t, h.request("POST", "/api/v1/tags", "{\"name\":\"cat\"} \r\n\t", "Idempotency-Key", firstKey), 201)
		})
	}
}

type harness struct {
	s       *store.Store
	handler http.Handler
	t       *testing.T
}

func setup(t *testing.T) *harness {
	t.Helper()
	s, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h, err := New(s, Options{Clock: func() time.Time { return time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{s: s, handler: h, t: t}
}
func (h *harness) request(method, path, body string, headers ...string) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.handler.ServeHTTP(w, req)
	spec, err := api.GetSwagger()
	if strings.Contains(path, "/parts/") {
		// Binary operations are deliberately excluded from the generated JSON
		// handler subset; validate their responses against the source contract.
		spec, err = openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "..", "api", "openapi.yaml"))
	}
	if err != nil {
		h.t.Fatal(err)
	}
	router, err := gorillamux.NewRouter(spec)
	if err != nil {
		h.t.Fatal(err)
	}
	route, params, err := router.FindRoute(req)
	if err == nil {
		validation := &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}, Status: w.Code, Header: w.Header(), Body: io.NopCloser(bytes.NewReader(w.Body.Bytes()))}
		if err := openapi3filter.ValidateResponse(ctx, validation); err != nil {
			h.t.Fatalf("response does not match OpenAPI (%s %s): %v\n%s", method, path, err, w.Body.String())
		}
	}
	return w
}
func decode[T any](t *testing.T, w *httptest.ResponseRecorder, status int) T {
	t.Helper()
	if w.Code != status {
		t.Fatalf("HTTP %d want %d: %s", w.Code, status, w.Body.String())
	}
	var out T
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}
func seedReady(t *testing.T, s *store.Store) {
	t.Helper()
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		for id := 1; id <= 2; id++ {
			asset := fmt.Sprintf("af6b458c-4c68-4071-b068-00000000000%d", id)
			if _, err := tx.Exec(`INSERT INTO items(id,media_kind,uploaded_at_utc,duration_ticks,state) VALUES(?,'Video','2026-01-01T00:00:00.0000000Z',123456789,'importing')`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','fixture',?,1,?,'video/mp4','fixture','verified')`, asset, id, asset, strings.Repeat("a", 64)); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE items SET state='ready',primary_asset_id=? WHERE id=?`, asset, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPCreatesReplaySameBodyAndExposeOperationResults(t *testing.T) {
	h := setup(t)
	first := h.request("POST", "/api/v1/tags", `{"name":"cat"}`, "Idempotency-Key", firstKey)
	created := decode[api.TagCommand](t, first, 201)
	if created.Resource.Id != 1 || created.Resource.Name != "cat" || created.OperationId.String() != firstKey || first.Header().Get("ETag") != `"1"` {
		t.Fatal(created)
	}
	replay := h.request("POST", "/api/v1/tags", `{ "name": "cat" }`, "Idempotency-Key", firstKey)
	if replay.Code != 201 || replay.Body.String() != first.Body.String() || replay.Header().Get("ETag") != first.Header().Get("ETag") {
		t.Fatalf("changed replay: %s", replay.Body.String())
	}
	problem := decode[api.Problem](t, h.request("POST", "/api/v1/tags", `{"name":"dog"}`, "Idempotency-Key", firstKey), 409)
	if problem.Code != "idempotency_conflict" {
		t.Fatal(problem)
	}
	tags := decode[[]api.Tag](t, h.request("GET", "/api/v1/tags", ""), 200)
	if len(tags) != 1 {
		t.Fatal(tags)
	}
	op := decode[api.Operation](t, h.request("GET", "/api/v1/operations/"+firstKey, ""), 200)
	if op.HttpStatus != 201 || string(op.State) != "completed" {
		t.Fatal(op)
	}
}

func TestHTTPMetadataVersionsBulkRollbackAndLegacyAPIVariants(t *testing.T) {
	h := setup(t)
	seedReady(t, h.s)
	decode[api.TagCommand](t, h.request("POST", "/api/v1/tags", `{"name":"cat"}`, "Idempotency-Key", firstKey), 201)
	decode[api.CollectionCommand](t, h.request("POST", "/api/v1/collections", `{"name":"One"}`, "Idempotency-Key", secondKey), 201)
	item := decode[api.Item](t, h.request("PATCH", "/api/v1/items/1", `{"tagIds":[1],"collectionId":1}`, "If-Match", `"1"`), 200)
	if item.Version != 2 || item.DurationSeconds.IsNull() || item.DurationSeconds.MustGet() != 12.3456789 {
		t.Fatal(item)
	}
	decode[api.Problem](t, h.request("POST", "/api/v1/items/bulk-metadata", `{"items":[{"id":1,"version":2},{"id":2,"version":999}],"tagIds":[],"collectionId":null}`, "Idempotency-Key", thirdKey), 412)
	unchanged := decode[api.Item](t, h.request("GET", "/api/v1/items/1", ""), 200)
	if len(unchanged.Tags) != 1 || unchanged.Version != 2 {
		t.Fatal("partial bulk save")
	}
	decode[api.BulkCommand](t, h.request("POST", "/api/v1/items/bulk-tags", `{"itemIds":[1,2],"mode":"replace","tagIds":[1]}`, "Idempotency-Key", "e932bb66-cf6f-4e30-b1a1-f7c596928be1"), 200)
	decode[api.BulkCommand](t, h.request("POST", "/api/v1/items/bulk-collection", `{"itemIds":[1,2],"collectionId":1}`, "Idempotency-Key", "e932bb66-cf6f-4e30-b1a1-f7c596928be2"), 200)
	detail := decode[api.CollectionDetail](t, h.request("GET", "/api/v1/collections/1?pageSize=1&page=2", ""), 200)
	if detail.Members.TotalItems != 2 || len(detail.Members.Items) != 1 || detail.Collection.ItemCount != 2 {
		t.Fatal(detail)
	}
	w := h.request("DELETE", "/api/v1/collections/1", "", "If-Match", `"1"`)
	if w.Code != 204 {
		t.Fatalf("delete %d: %s", w.Code, w.Body.String())
	}
	for id := 1; id <= 2; id++ {
		item := decode[api.Item](t, h.request("GET", fmt.Sprintf("/api/v1/items/%d", id), ""), 200)
		if !item.CollectionId.IsNull() || len(item.Tags) != 1 {
			t.Fatal("collection delete lost metadata")
		}
	}
}

func TestHTTPSavedQueryRenameNameOnlyAndNullableKind(t *testing.T) {
	h := setup(t)
	seedReady(t, h.s)
	decode[api.TagCommand](t, h.request("POST", "/api/v1/tags", `{"name":"a"}`, "Idempotency-Key", firstKey), 201)
	decode[api.Item](t, h.request("PATCH", "/api/v1/items/1", `{"tagIds":[1],"collectionId":null}`, "If-Match", `"1"`), 200)
	created := decode[api.SavedSearchCommand](t, h.request("POST", "/api/v1/saved-searches", `{"name":"A","queryText":"a && !x","mediaKind":"Video"}`, "Idempotency-Key", secondKey), 201)
	before, _ := json.Marshal(created.Resource.QueryAst)
	decode[api.Tag](t, h.request("PATCH", "/api/v1/tags/1", `{"name":"x"}`, "If-Match", `"1"`), 200)
	page := decode[api.ItemPage](t, h.request("GET", "/api/v1/items?savedSearchId=1", ""), 200)
	if page.TotalItems != 1 || page.Items[0].Id != 1 {
		t.Fatal(page)
	}
	renamed := decode[api.SavedSearch](t, h.request("PATCH", "/api/v1/saved-searches/1", `{"name":"Renamed"}`, "If-Match", `"2"`), 200)
	after, _ := json.Marshal(renamed.QueryAst)
	if !bytes.Equal(before, after) || renamed.MediaKind.IsNull() || renamed.QueryText != "x && !x" {
		t.Fatal(renamed)
	}
	cleared := decode[api.SavedSearch](t, h.request("PATCH", "/api/v1/saved-searches/1", `{"mediaKind":null}`, "If-Match", `"3"`), 200)
	if !cleared.MediaKind.IsNull() {
		t.Fatal("NULL kind ignored")
	}
	decode[api.Problem](t, h.request("PATCH", "/api/v1/tags/1", `{"name":"stale"}`, "If-Match", `"1"`), 412)
	w := h.request("DELETE", "/api/v1/saved-searches/1", "", "If-Match", `"4"`)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHTTPViewsFavoriteSearchAndNavigation(t *testing.T) {
	h := setup(t)
	seedReady(t, h.s)
	for i := 0; i < 2; i++ {
		decode[api.Item](t, h.request("PUT", "/api/v1/items/1/favorite", `{"isFavorite":true}`), 200)
		decode[api.Item](t, h.request("POST", "/api/v1/items/1/views", `{"eventId":"ef0a096c-ec10-4537-a197-f652da91265f"}`), 200)
	}
	item := decode[api.Item](t, h.request("GET", "/api/v1/items/1", ""), 200)
	if !item.IsFavorite || item.ViewCount != 1 || item.Version != 3 {
		t.Fatal(item)
	}
	created := decode[api.NavigationCreated](t, h.request("POST", "/api/v1/navigation-contexts", `{"origin":"/library?sort=UploadedAsc&page=1","orderedIds":[2,1]}`, "Idempotency-Key", firstKey), 201)
	neighbors := decode[api.Neighbors](t, h.request("GET", "/api/v1/navigation-contexts/"+created.ContextId.String()+"/neighbors?itemId=2", ""), 200)
	if neighbors.NextId.MustGet() != 1 || !neighbors.PreviousId.IsNull() {
		t.Fatal(neighbors)
	}
	page := decode[api.ItemPage](t, h.request("GET", "/api/v1/items?sort=VideoDurationAsc&page=1&pageSize=1", ""), 200)
	if page.TotalItems != 2 || page.TotalPages != 2 || len(page.Items) != 1 {
		t.Fatal(page)
	}
	decode[api.ItemPage](t, h.request("GET", "/api/v1/items/1/similar", ""), 200)
}

func TestHTTPProblemsAreJSONBoundedAndDoNotLeakInternalFields(t *testing.T) {
	h := setup(t)
	seedReady(t, h.s)
	for _, tc := range []struct {
		method, path, body string
		headers            []string
		status             int
	}{
		{"GET", "/api/v1/items/999", "", nil, 404}, {"GET", "/api/v1/items?q=cat%24", "", nil, 422},
		{"GET", "/api/v1/items?page=0", "", nil, 400}, {"GET", "/api/v1/items/9007199254740992", "", nil, 400},
		{"POST", "/api/v1/tags", `{"name":"cat"}`, nil, 400},
		{"POST", "/api/v1/tags", `{"name":"a_b"}`, []string{"Idempotency-Key", firstKey}, 422},
		{"POST", "/api/v1/tags", `{"name":"cat","extra":true}`, []string{"Idempotency-Key", secondKey}, 400},
		{"PATCH", "/api/v1/items/1", `{"tagIds":[],"collectionId":null}`, nil, 400},
		{"POST", "/api/v1/tags", `{"name":"` + strings.Repeat("x", 2*1024*1024) + `"}`, []string{"Idempotency-Key", thirdKey}, 413},
	} {
		w := h.request(tc.method, tc.path, tc.body, tc.headers...)
		problem := decode[api.Problem](t, w, tc.status)
		if w.Header().Get("Content-Type") != "application/problem+json" || problem.TraceId.String() == "" || problem.Status != tc.status {
			t.Fatalf("problem %+v", problem)
		}
	}
	missing := h.request("GET", "/api/v1/not-a-route", "")
	decode[api.Problem](t, missing, 404)
	item := h.request("GET", "/api/v1/items/1", "")
	for _, forbidden := range []string{"durationTicks", "objectKey", "bucket", "originalFilename", "title", "displayName"} {
		if strings.Contains(item.Body.String(), forbidden) {
			t.Fatalf("internal field leaked %s", forbidden)
		}
	}
}
