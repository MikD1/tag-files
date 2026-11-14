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
	outcome, err = coordinator.Refresh(ctx, session.ID)
	if err != nil || len(outcome.Session.Parts) != 2 || outcome.Session.MultipartUploadID.String != resumed.MultipartUploadID.String || outcome.Session.Parts[0].SHA256 != sum {
		t.Fatalf("native restart lost the immutable receiving state: %+v %v", outcome, err)
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
	if err != nil || len(intents) < 3 {
		t.Fatalf("reopen lost independent uncertainty: %+v %v", intents, err)
	}
}
