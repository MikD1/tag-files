package mediaserve_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/deletion"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi"
	"github.com/mikd1/tag-files/app/internal/mediaserve"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ctx = context.Background()
var modified = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)

const epoch = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

var content = []byte("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ")

type fixture struct {
	db                       *store.Store
	manager                  *objectio.Manager
	handler                  http.Handler
	id, key                  string
	item                     int64
	heads, gets              atomic.Int64
	missing                  atomic.Bool
	getMissing               atomic.Bool
	badRange                 atomic.Bool
	truncated                atomic.Bool
	block                    atomic.Bool
	allowDelete              atomic.Bool
	entered, release, closed chan struct{}
	lastRange                atomic.Value
}

func setup(t testing.TB, kind, contentType string, sizes ...int64) *fixture {
	t.Helper()
	f := &fixture{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f.db = db
	t.Cleanup(func() { db.Close() })
	f.id = domain.NewUUID()
	size := int64(len(content))
	if len(sizes) > 0 {
		size = sizes[0]
	}
	sum := sha256.Sum256(content)
	if size != int64(len(content)) {
		digest := sha256.New()
		if _, err := io.CopyN(digest, zeroReader{}, size); err != nil {
			t.Fatal(err)
		}
		copy(sum[:], digest.Sum(nil))
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		meta, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		f.key = "primary/" + meta.LibraryID + "/" + f.id + "/media.bin"
		if err := tx.QueryRow("INSERT INTO items(media_kind,uploaded_at_utc,state) VALUES(?,'2001-01-01T00:00:00.0000000Z','importing') RETURNING id", kind).Scan(&f.item); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','tag-files',?,?,?,?,'legacy','published')", f.id, f.item, f.key, size, hex.EncodeToString(sum[:]), contentType); err != nil {
			return err
		}
		return q.MarkItemReady(ctx, dbgen.MarkItemReadyParams{ID: f.item, PrimaryAssetID: sql.NullString{String: f.id, Valid: true}})
	}); err != nil {
		t.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epoch)
		if r.Method == "HEAD" && r.URL.Path == "/tag-files" {
			return
		}
		if r.Header.Get("X-Tag-Files-Expected-Storage-Epoch") != epoch {
			t.Error("unfenced media read")
		}
		if r.URL.Path != "/tag-files/"+f.key {
			t.Error("foreign object requested")
			w.WriteHeader(404)
			return
		}
		if r.Method == "DELETE" && f.allowDelete.Load() {
			f.missing.Store(true)
			w.WriteHeader(204)
			return
		}
		if r.Method == "HEAD" {
			f.heads.Add(1)
		} else if r.Method == "GET" {
			f.gets.Add(1)
			f.lastRange.Store(r.Header.Get("Range"))
		} else {
			t.Error("media issued a write")
		}
		if f.missing.Load() {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" && f.getMissing.Load() {
			w.WriteHeader(404)
			return
		}
		if err := db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.Exec("INSERT INTO tags(name) VALUES('media IO has no SQL transaction')")
			return err
		}); err != nil {
			t.Error(err)
		}
		w.Header().Set("ETag", `"media-v1"`)
		if r.Method == "GET" && (f.block.Load() || f.truncated.Load()) {
			defer close(f.closed)
			w.Header().Set("Content-Length", strconv.Itoa(len(content)))
			w.WriteHeader(200)
			_, _ = w.Write(content[:1])
			w.(http.Flusher).Flush()
			close(f.entered)
			if f.truncated.Load() {
				return
			}
			select {
			case <-f.release:
				_, _ = w.Write(content[1:])
			case <-r.Context().Done():
			}
			return
		}
		if r.Method == "GET" && f.badRange.Load() {
			w.Header().Set("Content-Length", "4")
			w.Header().Set("Content-Range", "bytes 2-5/36")
			w.WriteHeader(206)
			_, _ = w.Write([]byte("2345"))
			return
		}
		if size != int64(len(content)) {
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
			w.WriteHeader(200)
			if r.Method == "GET" {
				_, _ = io.CopyN(w, zeroReader{}, size)
			}
			return
		}
		http.ServeContent(w, r, "owned", modified, bytes.NewReader(content))
	}))
	t.Cleanup(peer.Close)
	client, err := objectstore.New(objectstore.Config{Endpoint: peer.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "private", SecretKey: "private", HTTPClient: peer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	f.manager, err = objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	media, err := mediaserve.New(db, f.manager, mediaserve.Config{})
	if err != nil {
		t.Fatal(err)
	}
	f.handler, err = httpapi.New(db, httpapi.Options{Media: media})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMissingPosterKeepsPrimaryReadyAndQueuesRebuild(t *testing.T) {
	f := setup(t, "Image", "image/png")
	primaryID := f.id
	f.id = domain.NewUUID()
	if err := f.db.Write(ctx, func(tx *sql.Tx) error {
		meta, err := dbgen.New(tx).GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		f.key = "previews/" + meta.LibraryID + "/" + f.id + "/poster-v1/poster.jpg"
		_, err = tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,profile_version,state) VALUES(?,?,'poster','tag-files',?,?,?,'image/jpeg','derived','poster-v1','published')", f.id, f.item, f.key, len(content), strings.Repeat("a", 64))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.missing.Store(true)
	if w := f.request("GET"); w.Code != 404 {
		t.Fatalf("missing poster: %d %s", w.Code, w.Body)
	}
	item, err := dbgen.New(f.db.Reader()).GetItem(ctx, f.item)
	if err != nil || item.State != domain.ItemReady || item.PrimaryAssetID.String != primaryID || item.Version != 2 {
		t.Fatalf("missing poster disturbed primary: %+v %v", item, err)
	}
	job, err := dbgen.New(f.db.Reader()).FindPosterJob(ctx, "poster/"+primaryID+"/poster-v1")
	if err != nil || job.State != domain.JobQueued {
		t.Fatalf("poster recovery not durable: %+v %v", job, err)
	}
	if w := f.request("HEAD"); w.Code != 404 || w.Body.Len() != 0 {
		t.Fatal("missing HEAD returned a body")
	}
	if f.heads.Load() != 1 {
		t.Fatal("missing poster was repeatedly fetched")
	}
}

func TestMediaRejectsWrongRangeAndMissingGetBeforeResponseBytes(t *testing.T) {
	for _, mode := range []string{"wrong-range", "missing-get"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, "Video", "video/mp4")
			status := 502
			if mode == "wrong-range" {
				f.badRange.Store(true)
			} else {
				f.getMissing.Store(true)
				status = 404
			}
			w := f.request("GET", "Range", "bytes=0-3")
			if w.Code != status || w.Header().Get("Content-Type") != "application/problem+json" || strings.Contains(w.Body.String(), f.key) {
				t.Fatalf("bad storage response leaked media/keys: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestEarlyBrowserDisconnectCancelsDownstreamAndDrainsKey(t *testing.T) {
	f := setup(t, "Video", "video/mp4")
	f.block.Store(true)
	server := httptest.NewServer(f.handler)
	defer server.Close()
	resp, err := server.Client().Get(server.URL + "/media/" + f.id)
	if err != nil {
		t.Fatal(err)
	}
	var first [1]byte
	if _, err := io.ReadFull(resp.Body, first[:]); err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("browser disconnect did not cancel S3")
	}
	deadline, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := f.manager.Drain(deadline, f.key, ""); err != nil {
		t.Fatal("read registration leaked", err)
	}
}

func TestDeleteHidesNewReadsAndWaitsForAdmittedStream(t *testing.T) {
	f := setup(t, "Video", "video/mp4")
	f.block.Store(true)
	f.allowDelete.Store(true)
	server := httptest.NewServer(f.handler)
	defer server.Close()
	resp, err := server.Client().Get(server.URL + "/media/" + f.id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := f.db.Write(ctx, func(tx *sql.Tx) error { _, err := deletion.Request(ctx, tx, f.item, 1, time.Now()); return err }); err != nil {
		t.Fatal(err)
	}
	if w := f.request("GET"); w.Code != 404 {
		t.Fatalf("new read after tombstone: %d", w.Code)
	}
	s, err := deletion.New(f.db, f.manager)
	if err != nil {
		t.Fatal(err)
	}
	job, err := f.db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobDelete}, time.Now().Add(time.Second), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Process(ctx, job, time.Now().Add(time.Second)) }()
	select {
	case err := <-done:
		t.Fatalf("deletion passed live stream: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(f.release)
	data, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Equal(data, content) {
		t.Fatalf("admitted response truncated: %q %v", data, err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, deletion.ErrAwaitingRestart) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("deletion did not resume")
	}
}

func TestTruncatedMediaAbortsConnectionInsteadOfCompletingBinaryResponse(t *testing.T) {
	f := setup(t, "Video", "video/mp4")
	f.truncated.Store(true)
	server := httptest.NewServer(f.handler)
	defer server.Close()
	resp, err := server.Client().Get(server.URL + "/media/" + f.id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err == nil || len(data) >= len(content) || bytes.Contains(data, []byte("problem")) {
		t.Fatalf("truncated response completed or appended JSON: %q %v", data, err)
	}
}
func (f *fixture) request(method string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/media/"+f.id, nil)
	for i := 0; i < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestMediaStreamsExactRangesAndConditionalHeaders(t *testing.T) {
	f := setup(t, "Video", "video/mp4")
	for _, tc := range []struct {
		name, method           string
		headers                []string
		status                 int
		body, rangeOut, length string
	}{
		{"whole", "GET", nil, 200, string(content), "", "36"},
		{"head", "HEAD", nil, 200, "", "", "36"},
		{"closed", "GET", []string{"Range", "bytes=3-6"}, 206, "3456", "bytes 3-6/36", "4"},
		{"open", "GET", []string{"Range", "bytes=33-"}, 206, "XYZ", "bytes 33-35/36", "3"},
		{"suffix", "GET", []string{"Range", "bytes=-3"}, 206, "XYZ", "bytes 33-35/36", "3"},
		{"clamp", "GET", []string{"Range", "bytes=33-999999999999999999999"}, 206, "XYZ", "bytes 33-35/36", "3"},
		{"unsatisfiable", "GET", []string{"Range", "bytes=36-"}, 416, "", "bytes */36", "0"},
		{"invalid", "GET", []string{"Range", "bytes=5-3"}, 416, "", "bytes */36", "0"},
		{"unknown-unit", "GET", []string{"Range", "items=0-2"}, 200, string(content), "", "36"},
		{"head-ignores-range", "HEAD", []string{"Range", "bytes=3-6"}, 200, "", "", "36"},
		{"etag", "GET", []string{"If-None-Match", `"other,tag", W/"media-v1"`}, 304, "", "", ""},
		{"date", "GET", []string{"If-Modified-Since", modified.Format(http.TimeFormat)}, 304, "", "", ""},
		{"etag-precedence", "GET", []string{"If-None-Match", `"different"`, "If-Modified-Since", modified.Format(http.TimeFormat)}, 200, string(content), "", "36"},
		{"if-range", "GET", []string{"Range", "bytes=0-2", "If-Range", `"media-v1"`}, 206, "012", "bytes 0-2/36", "3"},
		{"if-range-weak", "GET", []string{"Range", "bytes=0-2", "If-Range", `W/"media-v1"`}, 200, string(content), "", "36"},
		{"if-range-date", "GET", []string{"Range", "bytes=0-2", "If-Range", modified.Format(http.TimeFormat)}, 206, "012", "bytes 0-2/36", "3"},
		{"if-range-new-date", "GET", []string{"Range", "bytes=0-2", "If-Range", modified.Add(time.Hour).Format(http.TimeFormat)}, 200, string(content), "", "36"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := f.request(tc.method, tc.headers...)
			if w.Code != tc.status || w.Body.String() != tc.body || w.Header().Get("Content-Range") != tc.rangeOut || w.Header().Get("Content-Length") != tc.length {
				t.Fatalf("response %d %q headers=%v", w.Code, w.Body.String(), w.Header())
			}
			if w.Header().Get("ETag") != `"media-v1"` || w.Header().Get("Accept-Ranges") != "bytes" {
				t.Fatal("representation headers missing")
			}
		})
	}
	var views int64
	var last sql.NullString
	if err := f.db.Reader().QueryRow("SELECT view_count,last_view_at_utc FROM items WHERE id=?", f.item).Scan(&views, &last); err != nil || views != 0 || last.Valid {
		t.Fatal("media read counted a view", err)
	}
}

func TestMediaMultipartRangesUseExactBytesAndLength(t *testing.T) {
	f := setup(t, "Image", "image/png")
	w := f.request("GET", "Range", "bytes=0-2,6-8")
	if w.Code != 206 {
		t.Fatalf("multipart status: %d %s", w.Code, w.Body)
	}
	typeName, params, err := mime.ParseMediaType(w.Header().Get("Content-Type"))
	if err != nil || typeName != "multipart/byteranges" {
		t.Fatal("no multipart response", err)
	}
	reader := multipart.NewReader(bytes.NewReader(w.Body.Bytes()), params["boundary"])
	for i, want := range []string{"012", "678"} {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		if err != nil || string(data) != want || part.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("part%d: %q %v", i, data, part.Header)
		}
	}
	if _, err := reader.NextPart(); err != io.EOF {
		t.Fatal("extra ranges", err)
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(w.Body.Len()) || w.Header().Get("Content-Range") != "" || f.gets.Load() != 2 {
		t.Fatal("multipart framing/IO mismatch")
	}
}

func TestLegacyAndActiveMediaAreSafeDownloads(t *testing.T) {
	for _, tc := range []struct{ kind, mime string }{{"Text", "text/html"}, {"Unknown", "image/svg+xml"}, {"Audio", "audio/flac"}, {"Image", "image/svg+xml"}} {
		t.Run(tc.kind+tc.mime, func(t *testing.T) {
			f := setup(t, tc.kind, tc.mime)
			w := f.request("GET")
			if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), content) || w.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("unsafe legacy response: %d %v", w.Code, w.Header())
			}
		})
	}
}

func TestDeletedForeignAndCandidateMediaNeverReachStorage(t *testing.T) {
	for _, mode := range []string{"deleting", "candidate", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, "Image", "image/png")
			if err := f.db.Write(ctx, func(tx *sql.Tx) error {
				switch mode {
				case "deleting":
					_, err := tx.Exec("UPDATE items SET state='deleting' WHERE id=?", f.item)
					return err
				case "candidate":
					if _, err := tx.Exec("UPDATE items SET state='missing',primary_asset_id=NULL WHERE id=?", f.item); err != nil {
						return err
					}
					_, err := tx.Exec("UPDATE assets SET state='candidate',item_id=NULL WHERE id=?", f.id)
					return err
				case "foreign":
					_, err := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'poster','tag-files','foreign/bytes',1,?,'image/jpeg','legacy','published')", domain.NewUUID(), f.item, strings.Repeat("a", 64))
					if err != nil {
						return err
					}
					return tx.QueryRow("SELECT id FROM assets WHERE object_key='foreign/bytes'").Scan(&f.id)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if w := f.request("GET"); w.Code != 404 {
				t.Fatalf("private media exposed: %d %s", w.Code, w.Body)
			}
			if f.gets.Load() != 0 || f.heads.Load() != 0 {
				t.Fatal("unavailable media reached S3")
			}
		})
	}
}

func TestMissingPrimaryMarksAvailabilityWithoutRemovingMetadata(t *testing.T) {
	f := setup(t, "Image", "image/png")
	f.missing.Store(true)
	if w := f.request("GET"); w.Code != 404 {
		t.Fatalf("missing media: %d %s", w.Code, w.Body)
	}
	item, err := dbgen.New(f.db.Reader()).GetItem(ctx, f.item)
	if err != nil || item.State != domain.ItemMissing || item.Version != 2 || item.ViewCount != 0 {
		t.Fatalf("missing state: %+v %v", item, err)
	}
	if f.gets.Load() != 0 {
		t.Fatal("missing HEAD forwarded GET")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
func BenchmarkMediaHTTPStreaming(b *testing.B) {
	for _, size := range []int64{8 * 1024 * 1024, 64 * 1024 * 1024} {
		b.Run(strconv.FormatInt(size, 10), func(b *testing.B) {
			f := setup(b, "Video", "video/mp4", size)
			server := httptest.NewServer(f.handler)
			b.Cleanup(server.Close)
			b.SetBytes(size)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				resp, err := server.Client().Get(server.URL + "/media/" + f.id)
				if err != nil {
					b.Fatal(err)
				}
				n, copyErr := io.Copy(io.Discard, resp.Body)
				closeErr := resp.Body.Close()
				if copyErr != nil || closeErr != nil || resp.StatusCode != 200 || n != size {
					b.Fatalf("streamed=%d status=%d errors=%v/%v", n, resp.StatusCode, copyErr, closeErr)
				}
			}
		})
	}
}
