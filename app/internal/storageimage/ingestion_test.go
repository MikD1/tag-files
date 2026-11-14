package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

func TestIngestionOwnedImageLostCreateAndPartResponsesRecoverImmutableBindings(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	libraryDirectory := t.TempDir()
	db, err := store.Open(ctx, libraryDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var batch ingestion.Batch
	const partSize = 5 * 1024 * 1024
	err = db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		batch, err = ingestion.Create(ctx, tx, []ingestion.File{{ClientFileID: domain.NewUUID(), Filename: "real-S3.bin", Size: partSize + 7}}, nil, ingestion.Limits{MaxFileSize: 20 * 1024 * 1024, PartSize: partSize, MaxParallelTransfers: 2}, time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	session := batch.Sessions[0]
	endpoint, _ := url.Parse(f.endpoint())
	proxy := httputil.NewSingleHostReverseProxy(endpoint)
	var target atomic.Pointer[url.URL]
	target.Store(endpoint)
	proxy.Director = func(r *http.Request) {
		httputil.NewSingleHostReverseProxy(target.Load()).Director(r)
	}
	var creates, uploads, completes atomic.Int64
	var loseCreate, losePart, loseComplete atomic.Bool
	loseCreate.Store(true)
	losePart.Store(true)
	loseComplete.Store(true)
	proxy.ModifyResponse = func(res *http.Response) error {
		r := res.Request
		shouldLose := false
		if r.Method == "POST" && r.URL.Query().Has("uploads") {
			creates.Add(1)
			shouldLose = loseCreate.Swap(false)
		}
		if r.Method == "PUT" && r.URL.Query().Has("partNumber") {
			uploads.Add(1)
			if res.StatusCode == 200 {
				shouldLose = losePart.Swap(false)
			}
		}
		if r.Method == "POST" && r.URL.Query().Has("uploadId") {
			completes.Add(1)
			shouldLose = loseComplete.Swap(false)
		}
		if shouldLose && res.StatusCode == 200 {
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			return errors.New("injected loss after storage accepted the write")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(502) }
	peer := httptest.NewServer(proxy)
	defer peer.Close()
	client, err := objectstore.New(objectstore.Config{Endpoint: peer.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: &http.Client{Timeout: 60 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := ingestion.NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.EnsureMultipart(ctx, session.ID); err == nil {
		t.Fatal("injected lost Create response reported success")
	}
	resumed, err := coordinator.EnsureMultipart(ctx, session.ID)
	if err != nil || !resumed.MultipartUploadID.Valid || resumed.State != domain.SessionReceiving || creates.Load() != 1 {
		t.Fatalf("native Create recovery: %+v %v creates=%d", resumed, err, creates.Load())
	}
	first := bytes.Repeat([]byte("a"), partSize)
	digest := sha256.Sum256(first)
	sum := base64.StdEncoding.EncodeToString(digest[:])
	binding := ingestion.PartBinding{Number: 1, Size: partSize, SHA256: sum}
	if _, err := coordinator.UploadPart(ctx, session.ID, 1, binding, bytes.NewBuffer(first)); err == nil {
		t.Fatal("injected lost part response reported success")
	}
	outcome, err := coordinator.Refresh(ctx, session.ID)
	if err != nil || len(outcome.Session.Parts) != 1 || outcome.Session.Parts[0].State != "confirmed" || outcome.Session.Parts[0].SHA256 != sum {
		t.Fatalf("native ListParts recovery without checksum: %+v %v", outcome, err)
	}
	before := uploads.Load()
	different := binding
	different.SHA256 = base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := coordinator.UploadPart(ctx, session.ID, 1, different, bytes.NewReader(first)); !errors.Is(err, ingestion.ErrConflict) || uploads.Load() != before {
		t.Fatal("different same-number content reached native storage")
	}
	second := []byte("correct")
	secondDigest := sha256.Sum256(second)
	tail := ingestion.PartBinding{Number: 2, Size: 7, SHA256: base64.StdEncoding.EncodeToString(secondDigest[:])}
	if _, err := coordinator.UploadPart(ctx, session.ID, 1, tail, strings.NewReader("corrupt")); err == nil {
		t.Fatal("native storage accepted a false body digest")
	}
	if _, err := coordinator.UploadPart(ctx, session.ID, 1, tail, bytes.NewBuffer(second)); err != nil {
		t.Fatal(err)
	}
	outcome, err = coordinator.Refresh(ctx, session.ID)
	if err != nil || len(outcome.Session.Parts) != 2 || outcome.Session.Parts[1].State != "confirmed" {
		t.Fatalf("tail retry failed: %+v %v", outcome, err)
	}
	intents, err := manager.List(ctx, "", 100)
	if err != nil || len(intents) < 2 {
		t.Fatalf("unknown writes disappeared after matching listing: %+v %v", intents, err)
	}
	for _, intent := range intents {
		if intent.State != "uncertain" {
			t.Fatalf("unexpected retained state: %+v", intent)
		}
	}
	listed, err := client.ListMultipart(ctx, session.IncomingKey)
	if err != nil || len(listed) != 1 || listed[0].UploadID != resumed.MultipartUploadID.String {
		t.Fatalf("native retry created orphan MPU: %+v %v", listed, err)
	}
	extra, err := f.client().CreateMultipart(ctx, session.IncomingKey, "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	foreignKey := session.IncomingKey + "-neighbour"
	foreign, err := f.client().CreateMultipart(ctx, foreignKey, "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldEpoch, err := client.StorageEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "kill", "--signal", "KILL", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	endpoint, _ = url.Parse(f.endpoint())
	target.Store(endpoint)
	newEpoch, err := client.StorageEpoch(ctx)
	if err != nil || newEpoch == oldEpoch {
		t.Fatalf("native restart did not establish a new storage lifetime: %q %v", newEpoch, err)
	}
	outcome, err = coordinator.RecoverSession(ctx, session.ID)
	if err != nil || len(outcome.Session.Parts) != 2 || outcome.Session.MultipartUploadID.String != resumed.MultipartUploadID.String || outcome.Session.Parts[0].SHA256 != sum {
		t.Fatalf("native restart lost the immutable receiving state: %+v %v", outcome, err)
	}
	intents, err = manager.List(ctx, "", 100)
	if err != nil || len(intents) != 0 {
		t.Fatalf("active-session barrier did not reconcile old writes: %+v %v", intents, err)
	}
	cleanup, err := db.ClaimJob(ctx, time.Now(), time.Minute)
	if err != nil || cleanup.Type != domain.JobCleanup {
		t.Fatalf("extra native MPU has no durable cleanup: %+v %v", cleanup, err)
	}
	if err := coordinator.ProcessCleanup(ctx, cleanup, time.Now()); err != nil {
		t.Fatal(err)
	}
	listed, err = client.ListMultipart(ctx, session.IncomingKey)
	if err != nil || len(listed) != 2 {
		t.Fatalf("native extra cleanup removed selected/foreign MPU: %+v %v", listed, err)
	}
	for _, mpu := range listed {
		if mpu.UploadID == extra || (mpu.Key == session.IncomingKey && mpu.UploadID != resumed.MultipartUploadID.String) || (mpu.Key == foreignKey && mpu.UploadID != foreign) {
			t.Fatalf("wrong native MPU survived cleanup: %+v", listed)
		}
	}
	if _, err := coordinator.Complete(ctx, session.ID); err == nil {
		t.Fatal("injected lost native Complete response reported success")
	}
	outcome, err = coordinator.Complete(ctx, session.ID)
	full := sha256.New()
	full.Write(first)
	full.Write(second)
	if err != nil || outcome.Session.State != domain.SessionQueued || outcome.JobID == "" || outcome.Session.SourceSha256.String != hex.EncodeToString(full.Sum(nil)) || completes.Load() != 1 {
		t.Fatalf("native frozen/full-source recovery: %+v %v completes=%d", outcome, err, completes.Load())
	}
	jobID := outcome.JobID
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	// Reopen the real SQLite library and create fresh in-process coordinators;
	// queued identity and independent uncertainty must survive process state.
	db, err = store.Open(ctx, libraryDirectory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager, err = objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err = ingestion.NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err = coordinator.Complete(ctx, session.ID)
	if err != nil || outcome.JobID != jobID || completes.Load() != 1 {
		t.Fatalf("reopened Complete repeated work: %+v %v", outcome, err)
	}
	intents, err = manager.List(ctx, "", 100)
	if err != nil || len(intents) != 1 || intents[0].Operation != objectio.CompleteMPU {
		t.Fatalf("reopen lost independent uncertainty: %+v %v", intents, err)
	}
}

func TestIngestionOwnedImageCancelKeepsUnknownCompleteUntilRestartReconciliation(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	directory := t.TempDir()
	db, err := store.Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	var batch ingestion.Batch
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		batch, err = ingestion.Create(ctx, tx, []ingestion.File{{ClientFileID: domain.NewUUID(), Filename: "cancel.bin", Size: 7}}, nil, ingestion.DefaultLimits(), time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	session := batch.Sessions[0]
	endpoint, _ := url.Parse(f.endpoint())
	var target atomic.Pointer[url.URL]
	target.Store(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(endpoint)
	proxy.Director = func(r *http.Request) { httputil.NewSingleHostReverseProxy(target.Load()).Director(r) }
	accepted, release := make(chan struct{}), make(chan struct{})
	proxy.ModifyResponse = func(res *http.Response) error {
		if res.Request.Method == "POST" && res.Request.URL.Query().Has("uploadId") && res.StatusCode == 200 {
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			close(accepted)
			<-release
			return errors.New("injected lost Complete response after durable storage success")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(502) }
	peer := httptest.NewServer(proxy)
	defer peer.Close()
	client, err := objectstore.New(objectstore.Config{Endpoint: peer.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: &http.Client{Timeout: 60 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, client, 3)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err := ingestion.NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.EnsureMultipart(ctx, session.ID); err != nil {
		t.Fatal(err)
	}
	data := []byte("correct")
	sum := sha256.Sum256(data)
	if _, err := coordinator.UploadPart(ctx, session.ID, 1, ingestion.PartBinding{Number: 1, Size: 7, SHA256: base64.StdEncoding.EncodeToString(sum[:])}, bytes.NewBuffer(data)); err != nil {
		t.Fatal(err)
	}
	completed := make(chan error, 1)
	go func() { _, err := coordinator.Complete(ctx, session.ID); completed <- err }()
	select {
	case <-accepted:
	case <-ctx.Done():
		t.Fatal("native Complete did not reach accepted response")
	}
	var cancellation ingestion.Cancellation
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		cancellation, err = ingestion.Cancel(ctx, tx, session.ID, time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if cancellation.State != "cancelled" || cancellation.ReceiptID == "" || cancellation.JobID == "" {
		t.Fatalf("native cancellation: %+v", cancellation)
	}
	job, err := db.ClaimJob(ctx, time.Now(), 2*time.Minute)
	if err != nil || job.ID != cancellation.JobID {
		t.Fatalf("claim native cleanup: %+v %v", job, err)
	}
	drainCtx, stopDrain := context.WithTimeout(ctx, 50*time.Millisecond)
	err = coordinator.ProcessCleanup(drainCtx, job, time.Now())
	stopDrain()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup did not wait for known native writer: %v", err)
	}
	if _, err := f.client().Head(ctx, session.IncomingKey); err != nil {
		t.Fatal("cleanup removed source before known writer drained", err)
	}
	close(release)
	if err := <-completed; err == nil {
		t.Fatal("lost Complete reported success after cancel")
	}
	if _, err := ingestion.Lookup(ctx, db.Reader(), session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("late Complete recreated visible upload", err)
	}
	if err := coordinator.ProcessCleanup(ctx, job, time.Now()); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("unknown same-epoch writer was discarded: %v", err)
	}
	if _, err := f.client().Head(ctx, session.IncomingKey); err == nil {
		t.Fatal("independent cleanup did not remove incoming")
	}
	var pending int
	if err := db.Reader().QueryRow("SELECT COUNT(*) FROM cleanup_receipts WHERE id=?", cancellation.ReceiptID).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("same-epoch cleanup receipt disappeared")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	manager, err = objectio.New(db, client, 3)
	if err != nil {
		t.Fatal(err)
	}
	coordinator, err = ingestion.NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ProcessCleanup(ctx, job, time.Now()); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("DB reopen/empty source substituted for settlement: %v", err)
	}
	oldEpoch, err := client.StorageEpoch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "kill", "--signal", "KILL", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	endpoint, _ = url.Parse(f.endpoint())
	target.Store(endpoint)
	epoch, err := client.StorageEpoch(ctx)
	if err != nil || epoch == oldEpoch {
		t.Fatal("native restart barrier unavailable", err)
	}
	if err := coordinator.ProcessCleanup(ctx, job, time.Now()); err != nil {
		t.Fatalf("native barrier/owned result reconciliation: %v", err)
	}
	for _, table := range []string{"upload_sessions", "upload_parts", "jobs", "cleanup_receipts", "object_write_intents"} {
		var count int
		if err := db.Reader().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("native cleanup retained %s=%d: %v", table, count, err)
		}
	}
	mpus, err := f.client().ListMultipart(ctx, session.IncomingKey)
	if err != nil || len(mpus) != 0 {
		t.Fatalf("native MPU survived settled cleanup: %+v %v", mpus, err)
	}
}
