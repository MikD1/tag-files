package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/mikd1/tag-files/app/internal/capacity"
	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/operations"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/worker"
	"time"
)

type uploadCapacity struct {
	full, unavailable atomic.Bool
	once              sync.Once
	duringObservation func()
}

func (o *uploadCapacity) Capacity(context.Context) (storageprotocol.Capacity, error) {
	if o.duringObservation != nil {
		o.once.Do(o.duringObservation)
	}
	if o.unavailable.Load() {
		return storageprotocol.Capacity{}, diskspace.ErrUnavailable
	}
	free := int64(1 << 40)
	if o.full.Load() {
		free = 0
	}
	inodes := int64(1 << 30)
	return storageprotocol.Capacity{Snapshot: diskspace.Snapshot{FileSystemID: strings.Repeat("a", 64), AvailableBytes: free, AvailableInodes: &inodes, Independent: true}}, nil
}

func uploadHarness(t *testing.T, data []byte) (*harness, *ingestion.Coordinator, *uploadCapacity, *atomic.Int64) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	sandbox, err := scratch.Open(scratch.Config{Root: t.TempDir(), MaxBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sandbox.Close() })
	observed := &uploadCapacity{}
	budget, err := capacity.New(db, sandbox, observed, capacity.Config{DataDir: root, OutputLimit: 1 << 20, PosterLimit: 1 << 10, PartSize: 5 << 20, Parallel: 2, MinFreeBytes: 1, MinFreeInodes: 4})
	if err != nil {
		t.Fatal(err)
	}
	calls := &atomic.Int64{}
	digest := sha256.Sum256(data)
	sum := base64.StdEncoding.EncodeToString(digest[:])
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
		if r.Method == "HEAD" && r.URL.Path == "/tag-files" {
			return
		}
		calls.Add(1)
		switch {
		case r.Method == "GET" && r.URL.Query().Has("uploads"):
			io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
		case r.Method == "POST" && r.URL.Query().Has("uploads"):
			io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>owned</UploadId></InitiateMultipartUploadResult>`)
		case r.Method == "PUT":
			got := sha256.New()
			n, err := io.Copy(got, r.Body)
			if err != nil || n != int64(len(data)) || !bytes.Equal(got.Sum(nil), digest[:]) {
				t.Error("part bytes changed", n, err)
			}
			w.Header().Set("ETag", "part")
			w.Header().Set("X-Amz-Checksum-Sha256", sum)
		case r.Method == "GET" && r.URL.Query().Has("uploadId"):
			fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>part</ETag><Size>%d</Size></Part></ListPartsResult>`, len(data))
		case r.Method == "POST":
			io.WriteString(w, `<CompleteMultipartUploadResult><ETag>source</ETag></CompleteMultipartUploadResult>`)
		case r.Method == "HEAD":
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.Header().Set("ETag", "source")
		case r.Method == "GET":
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			w.Header().Set("ETag", "source")
			w.Write(data)
		default:
			t.Error("unexpected S3 request", r.Method, r.URL)
		}
	}))
	t.Cleanup(peer.Close)
	client, err := objectstore.New(objectstore.Config{Endpoint: peer.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "private", SecretKey: "private", HTTPClient: peer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	uploads, err := ingestion.NewCoordinator(db, manager, budget)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := New(db, Options{Uploads: &UploadOptions{Coordinator: uploads, Budget: budget, Limits: ingestion.Limits{MaxFileSize: 20 << 20, PartSize: 5 << 20, MaxParallelTransfers: 2}}})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{s: db, handler: handler, t: t}, uploads, observed, calls
}

func newUploadBody(size int) string {
	return fmt.Sprintf(`{"files":[{"clientFileId":"%s","filename":"photo.png","size":%d}]}`, domain.NewUUID(), size)
}

func TestHTTPUploadsStreamOutsideJSONLimitAndCompleteWithoutBrowser(t *testing.T) {
	data := bytes.Repeat([]byte("streamed"), 256<<10)
	h, c, _, calls := uploadHarness(t, data)
	body := newUploadBody(len(data))
	created := decode[api.UploadsCreated](t, h.request("POST", "/api/v1/upload-sessions", body, "Idempotency-Key", firstKey), 201)
	if calls.Load() != 0 || len(created.Sessions) != 1 || created.Sessions[0].State != api.UploadSessionState("creating") {
		t.Fatal("creation performed external work", created, calls.Load())
	}
	id := created.Sessions[0].Id.String()
	path := "/api/v1/upload-sessions/" + id
	sum := sha256.Sum256(data)
	part := decode[api.UploadPart](t, h.request("PUT", path+"/parts/1", string(data), "Content-Type", "application/octet-stream", "X-Upload-Generation", "1", "X-Part-SHA256", base64.StdEncoding.EncodeToString(sum[:])), 200)
	if part.Size != int64(len(data)) || value(part.Etag) != "part" {
		t.Fatal(part)
	}
	before := calls.Load()
	complete := `{"generation":1,"parts":[{"partNumber":1,"etag":"part"}]}`
	outcome := decode[api.UploadOutcome](t, h.request("POST", path+"/complete", complete, "Idempotency-Key", secondKey), 202)
	if outcome.JobId == nil || outcome.Session == nil || outcome.Session.State != "completing" || calls.Load() != before {
		t.Fatal("Complete was browser-dependent", outcome, calls.Load(), before)
	}
	// A different key cannot replace the tuple accepted by the durable command.
	decode[api.Problem](t, h.request("POST", path+"/complete", strings.ReplaceAll(complete, `"part"`, `"other"`), "Idempotency-Key", thirdKey), 409)
	runner, err := worker.New(h.s, map[domain.JobKind]worker.Handler{domain.JobComplete: worker.Completion(c)}, worker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := runner.RunOne(ctx); err != nil || !ran {
		t.Fatal("detached completion failed", ran, err)
	}
	queued := decode[api.UploadOutcome](t, h.request("GET", path, ""), 200)
	if queued.Session == nil || queued.Session.State != "queued" || queued.JobId == nil || *queued.JobId == *outcome.JobId {
		t.Fatal(queued)
	}
	batches := decode[[]api.UploadBatch](t, h.request("GET", "/api/v1/upload-batches", ""), 200)
	if len(batches) != 1 || batches[0].Sessions[0].JobId == nil {
		t.Fatal(batches)
	}
	decode[api.JobReceipt](t, h.request("DELETE", path, ""), 202)
	decode[api.Problem](t, h.request("GET", path, ""), 404)
	if got := decode[[]api.UploadBatch](t, h.request("GET", "/api/v1/upload-batches", ""), 200); len(got) != 0 {
		t.Fatal("cancel left history", got)
	}
	op := decode[api.Operation](t, h.request("GET", "/api/v1/operations/"+secondKey, ""), 200)
	if op.State != "failed" || op.HttpStatus != 409 {
		t.Fatal("cancel did not finalize Complete", op)
	}
}

func TestHTTPUploadReplayPrecedesCapacityAndRejectedPartsStayUnread(t *testing.T) {
	h, _, capacity, calls := uploadHarness(t, []byte("correct"))
	body := newUploadBody(7)
	first := h.request("POST", "/api/v1/upload-sessions", body, "Idempotency-Key", firstKey)
	created := decode[api.UploadsCreated](t, first, 201)
	capacity.full.Store(true)
	replay := h.request("POST", "/api/v1/upload-sessions", body, "Idempotency-Key", firstKey)
	if replay.Code != 201 || replay.Body.String() != first.Body.String() {
		t.Fatal("full disk broke replay", replay.Code, replay.Body.String())
	}
	decode[api.Problem](t, h.request("POST", "/api/v1/upload-sessions", strings.Replace(body, "photo.png", "other.png", 1), "Idempotency-Key", firstKey), 409)
	decode[api.Problem](t, h.request("POST", "/api/v1/upload-sessions", newUploadBody(7), "Idempotency-Key", secondKey), 507)
	id := created.Sessions[0].Id.String()
	for _, mode := range []string{"full", "length", "digest", "generation", "content-type"} {
		t.Run(mode, func(t *testing.T) {
			body := &countingUploadBody{}
			req := httptest.NewRequest("PUT", "/api/v1/upload-sessions/"+id+"/parts/1", body)
			req.ContentLength = 7
			req.Header.Set("Content-Type", "application/octet-stream")
			req.Header.Set("X-Upload-Generation", "1")
			sum := sha256.Sum256([]byte("correct"))
			req.Header.Set("X-Part-SHA256", base64.StdEncoding.EncodeToString(sum[:]))
			want := 507
			switch mode {
			case "length":
				req.ContentLength = -1
				want = 411
			case "digest":
				req.Header.Add("X-Part-SHA256", "ambiguous")
				want = 400
			case "generation":
				req.Header.Set("X-Upload-Generation", "9007199254740992")
				want = 400
			case "content-type":
				req.Header.Set("Content-Type", "application/json")
				want = 415
			}
			w := httptest.NewRecorder()
			h.handler.ServeHTTP(w, req)
			if w.Code != want || body.reads != 0 || calls.Load() != 0 {
				t.Fatal("invalid/full request consumed input", w.Code, w.Body.String(), body.reads, calls.Load())
			}
		})
	}
	capacity.full.Store(false)
	decode[api.UploadsCreated](t, h.request("POST", "/api/v1/upload-sessions", newUploadBody(7), "Idempotency-Key", secondKey), 201)
}

type countingUploadBody struct{ reads int }

func (r *countingUploadBody) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }
func (*countingUploadBody) Close() error               { return nil }

func TestHTTPUploadsRequireConfiguredService(t *testing.T) {
	h := setup(t)
	decode[api.Problem](t, h.request("POST", "/api/v1/upload-sessions", newUploadBody(7), "Idempotency-Key", firstKey), 503)
}

func TestHTTPRejectedPartGenerationCannotCreateMultipart(t *testing.T) {
	h, _, _, calls := uploadHarness(t, []byte("correct"))
	created := decode[api.UploadsCreated](t, h.request("POST", "/api/v1/upload-sessions", newUploadBody(7), "Idempotency-Key", firstKey), 201)
	sum := sha256.Sum256([]byte("correct"))
	decode[api.Problem](t, h.request("PUT", "/api/v1/upload-sessions/"+created.Sessions[0].Id.String()+"/parts/1", "correct", "Content-Type", "application/octet-stream", "X-Upload-Generation", "2", "X-Part-SHA256", base64.StdEncoding.EncodeToString(sum[:])), 409)
	if calls.Load() != 0 {
		t.Fatal("stale generation created external resources", calls.Load())
	}
}

func TestHTTPUploadRetryReservesBeforeMutationAndReplaysDuringOutage(t *testing.T) {
	h, c, capacity, _ := uploadHarness(t, []byte("correct"))
	created := decode[api.UploadsCreated](t, h.request("POST", "/api/v1/upload-sessions", newUploadBody(7), "Idempotency-Key", firstKey), 201)
	path := "/api/v1/upload-sessions/" + created.Sessions[0].Id.String()
	sum := sha256.Sum256([]byte("correct"))
	decode[api.UploadPart](t, h.request("PUT", path+"/parts/1", "correct", "Content-Type", "application/octet-stream", "X-Upload-Generation", "1", "X-Part-SHA256", base64.StdEncoding.EncodeToString(sum[:])), 200)
	decode[api.UploadOutcome](t, h.request("POST", path+"/complete", `{"generation":1,"parts":[{"partNumber":1,"etag":"part"}]}`, "Idempotency-Key", secondKey), 202)
	runner, err := worker.New(h.s, map[domain.JobKind]worker.Handler{domain.JobComplete: worker.Completion(c)}, worker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunOne(ctx); err != nil {
		t.Fatal(err)
	}
	job, err := h.s.ClaimJobKinds(ctx, []domain.JobKind{domain.JobIngest}, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.FailIngest(ctx, job, time.Now(), "insufficient_storage", nil); err != nil {
		t.Fatal(err)
	}
	retryPath := "/api/v1/jobs/" + job.ID + "/retry"
	capacity.full.Store(true)
	decode[api.Problem](t, h.request("POST", retryPath, "", "Idempotency-Key", thirdKey), 507)
	failed := decode[api.UploadOutcome](t, h.request("GET", path, ""), 200)
	if failed.State != "failed" || failed.Session == nil || len(failed.Session.Parts) != 1 {
		t.Fatal("rejected retry changed input", failed)
	}
	capacity.full.Store(false)
	first := h.request("POST", retryPath, "", "Idempotency-Key", thirdKey)
	decode[api.JobReceipt](t, first, 202)
	capacity.unavailable.Store(true)
	replay := h.request("POST", retryPath, "", "Idempotency-Key", thirdKey)
	if replay.Code != 202 || replay.Body.String() != first.Body.String() {
		t.Fatal("retry replay required storage", replay.Code, replay.Body.String())
	}
	cancelKey := domain.NewUUID()
	cancel := h.request("POST", "/api/v1/jobs/"+job.ID+"/cancel", "", "Idempotency-Key", cancelKey)
	decode[api.JobReceipt](t, cancel, 202)
	if out := h.request("POST", "/api/v1/jobs/"+job.ID+"/cancel", "", "Idempotency-Key", cancelKey); out.Code != 202 || out.Body.String() != cancel.Body.String() {
		t.Fatal("cancel receipt lost after original job removal", out.Code, out.Body.String())
	}
	op := decode[api.Operation](t, h.request("GET", "/api/v1/operations/"+thirdKey, ""), 200)
	if op.State != "completed" {
		t.Fatal("retired job left retry receipt pending", op)
	}
}

func TestHTTPReadyLookupSurvivesOperationExpiryAndKnownScopeConflict(t *testing.T) {
	h, _, capacity, _ := uploadHarness(t, []byte("correct"))
	id := domain.NewUUID()
	path := "/api/v1/upload-sessions/" + id + "/complete"
	body := `{"generation":1,"parts":[{"partNumber":1,"etag":"part"}]}`
	var typed api.CompleteUpload
	if err := json.Unmarshal([]byte(body), &typed); err != nil {
		t.Fatal(err)
	}
	canonical, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-operations.Retention - time.Hour)
	_, err = operations.Execute(ctx, h.s, operations.Request{Key: firstKey, Method: "POST", Route: path, Body: canonical}, old, func(*sql.Tx) (operations.Result, error) { return operations.Result{Status: 202}, nil })
	if err != nil {
		t.Fatal(err)
	}
	seedReady(t, h.s)
	if err := h.s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE items SET upload_id=? WHERE id=1", id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	capacity.unavailable.Store(true)
	ready := decode[api.UploadOutcome](t, h.request("POST", path, body, "Idempotency-Key", firstKey), 200)
	if value(ready.ItemId) != 1 || ready.State != "ready" {
		t.Fatal(ready)
	}
	decode[api.Problem](t, h.request("POST", path, strings.Replace(body, `"etag":"part"`, `"etag":"other"`, 1), "Idempotency-Key", firstKey), 409)
	if err := h.s.Write(ctx, func(tx *sql.Tx) error { return operations.Prune(ctx, tx, time.Now()) }); err != nil {
		t.Fatal(err)
	}
	decode[api.UploadOutcome](t, h.request("POST", path, body, "Idempotency-Key", firstKey), 200)
}

func TestHTTPConcurrentReceiptWinnerReplaysWhenAdmissionThenFails(t *testing.T) {
	h, _, observed, _ := uploadHarness(t, []byte("correct"))
	body := newUploadBody(7)
	var typed api.NewUploads
	if err := json.Unmarshal([]byte(body), &typed); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		t.Fatal(err)
	}
	observed.full.Store(true)
	observed.duringObservation = func() {
		_, err := operations.Execute(ctx, h.s, operations.Request{Key: firstKey, Method: "POST", Route: "/api/v1/upload-sessions", Body: raw}, time.Now(), func(tx *sql.Tx) (operations.Result, error) {
			batch, err := ingestion.Create(ctx, tx, []ingestion.File{{ClientFileID: typed.Files[0].ClientFileId.String(), Filename: typed.Files[0].Filename, Size: 7}}, nil, ingestion.DefaultLimits(), time.Now())
			if err != nil {
				return operations.Result{}, err
			}
			dto := api.UploadsCreated{OperationId: uuid.MustParse(firstKey), BatchId: uuid.MustParse(batch.ID), Sessions: []api.UploadSession{sessionDTO(batch.Sessions[0])}, MaxParallelTransfers: 2, MaxFileSize: 20 << 20}
			result, err := jsonResult(201, dto, 0)
			return result.Result, err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	decode[api.UploadsCreated](t, h.request("POST", "/api/v1/upload-sessions", body, "Idempotency-Key", firstKey), 201)
	var n int
	if err := h.s.Reader().QueryRow("SELECT count(*) FROM upload_sessions").Scan(&n); err != nil || n != 1 {
		t.Fatal("known winner created duplicate", n, err)
	}
}
