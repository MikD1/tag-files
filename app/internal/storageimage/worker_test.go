package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/deletion"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/mediaserve"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
	"github.com/mikd1/tag-files/app/internal/testutil"
	"github.com/mikd1/tag-files/app/internal/worker"
)

func TestIngestionWorkerOwnedImageProcessesPhotoAndRetainsFailedInput(t *testing.T) {
	if os.Getenv("TAG_FILES_MEDIA_INTEGRATION") != "1" {
		t.Skip("requires real media tools and owned storage image")
	}
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	directory := t.TempDir()
	db, err := store.Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	client, err := objectstore.New(objectstore.Config{Endpoint: f.endpoint(), Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: &http.Client{Timeout: 60 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	control, err := storageprotocol.NewClient(f.endpoint(), f.access, f.secret, &http.Client{Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pair, err := librarybinding.Ensure(ctx, db, control)
	if err != nil {
		t.Fatal(err)
	}
	client, err = client.AtLibrary(pair.StorageID, pair.LibraryID, pair.VolumeID)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ingestion.NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewNRGBA(image.Rect(0, 0, 13, 9))); err != nil {
		t.Fatal(err)
	}
	inputs := [][]byte{photo.Bytes(), []byte("not a photo or a video")}
	var batch ingestion.Batch
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		batch, err = ingestion.Create(ctx, tx, []ingestion.File{{ClientFileID: domain.NewUUID(), Filename: "source.jpg", Size: int64(len(inputs[0]))}, {ClientFileID: domain.NewUUID(), Filename: "invalid.png", Size: int64(len(inputs[1]))}}, nil, ingestion.DefaultLimits(), time.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for i, session := range batch.Sessions {
		sum := sha256.Sum256(inputs[i])
		if _, err := c.EnsureMultipart(ctx, session.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := c.UploadPart(ctx, session.ID, 1, ingestion.PartBinding{Number: 1, Size: int64(len(inputs[i])), SHA256: base64.StdEncoding.EncodeToString(sum[:])}, bytes.NewBuffer(inputs[i])); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Complete(ctx, session.ID); err != nil {
			t.Fatal(err)
		}
	}
	config := media.DefaultConfig()
	config.MaxOutputBytes = 1024 * 1024
	p, err := media.New(config)
	if err != nil {
		t.Fatal(err)
	}
	sandbox, err := scratch.Open(scratch.Config{Root: t.TempDir(), MaxBytes: 8 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	posters, err := poster.New(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	r, err := worker.New(db, map[domain.JobKind]worker.Handler{domain.JobIngest: worker.Ingestion(c, p, sandbox), domain.JobPoster: worker.Poster(posters, db, p, sandbox), domain.JobCleanup: worker.Cleanup(c, db, posters)}, worker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if ran, err := r.RunOne(ctx); err != nil || !ran {
			t.Fatalf("native worker attempt: %v %v", ran, err)
		}
	}
	if ran, err := r.RunOne(ctx); err != nil || ran {
		t.Fatalf("invalid input retried automatically: %v %v", ran, err)
	}
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
	ready, err := ingestion.Lookup(ctx, db.Reader(), batch.Sessions[0].ID)
	if err != nil || ready.State != "ready" {
		t.Fatalf("photo not ready: %+v %v", ready, err)
	}
	var key, hash string
	var size, width, height int64
	if err := db.Reader().QueryRow("SELECT a.object_key,a.sha256,a.size_bytes,a.width,a.height FROM assets a JOIN items i ON i.primary_asset_id=a.id WHERE i.id=?", ready.ItemID).Scan(&key, &hash, &size, &width, &height); err != nil || width != 13 || height != 9 {
		t.Fatalf("native worker descriptor: %dx%d %v", width, height, err)
	}
	if _, err := client.Verify(ctx, key, size, hash); err != nil {
		t.Fatal("ready primary not durable", err)
	}
	primaryKey := key
	if err := db.Reader().QueryRow("SELECT object_key,sha256,size_bytes,width,height FROM assets WHERE item_id=? AND role='poster' AND state='published' AND profile_version='poster-v1'", ready.ItemID).Scan(&key, &hash, &size, &width, &height); err != nil || width != 13 || height != 9 {
		t.Fatalf("native poster descriptor: %dx%d %v", width, height, err)
	}
	if _, err := client.Verify(ctx, key, size, hash); err != nil {
		t.Fatal("poster not durable", err)
	}
	deletedKeys := []string{primaryKey, key}
	var storageError *objectstore.Error
	if _, err := client.Head(ctx, batch.Sessions[0].IncomingKey); !errors.As(err, &storageError) || storageError.Status != 404 {
		t.Fatalf("worker did not clean ready source: %v", err)
	}
	failed, err := ingestion.Lookup(ctx, db.Reader(), batch.Sessions[1].ID)
	if err != nil || failed.State != "failed" || failed.Session.ErrorCode.String != "unsupported_media_type" {
		t.Fatalf("invalid source not visible as failure: %+v %v", failed, err)
	}
	sum := sha256.Sum256(inputs[1])
	if _, err := client.Verify(ctx, batch.Sessions[1].IncomingKey, int64(len(inputs[1])), hex.EncodeToString(sum[:])); err != nil {
		t.Fatal("failed source discarded", err)
	}
	var attempts int
	if err := db.Reader().QueryRow("SELECT attempt FROM jobs WHERE id=?", failed.JobID).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatalf("invalid media automatically retried: %d %v", attempts, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := ingestion.Lookup(ctx, db.Reader(), batch.Sessions[0].ID); err != nil || result.State != "ready" || result.ItemID != ready.ItemID {
		t.Fatal("ready lost on library reopen", err)
	}
	if result, err := ingestion.Lookup(ctx, db.Reader(), batch.Sessions[1].ID); err != nil || result.State != "failed" {
		t.Fatal("failed input disappeared on reopen", err)
	}
	// Delete the ready primary/poster through the actual JSON route, retain
	// exact keys after metadata removal, then verify them after storage restart.
	manager, err = objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	streamer, err := mediaserve.New(db, manager, mediaserve.Config{})
	if err != nil {
		t.Fatal(err)
	}
	h, err := httpapi.New(db, httpapi.Options{Media: streamer})
	if err != nil {
		t.Fatal(err)
	}
	item, err := dbgen.New(db.Reader()).GetItem(ctx, ready.ItemID)
	if err != nil {
		t.Fatal(err)
	}
	mediaRequest := func(method, path string, headers ...string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		for i := 0; i < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		out := httptest.NewRecorder()
		h.ServeHTTP(out, req)
		return out
	}
	mediaURL := "/media/" + item.PrimaryAssetID.String
	whole := mediaRequest("GET", mediaURL)
	if whole.Code != 200 || !bytes.Equal(whole.Body.Bytes(), inputs[0]) {
		t.Fatalf("native media bytes: %d %s", whole.Code, whole.Body.String())
	}
	partial := mediaRequest("GET", mediaURL, "Range", "bytes=0-7")
	if partial.Code != 206 || !bytes.Equal(partial.Body.Bytes(), inputs[0][:8]) || partial.Header().Get("Content-Range") != fmt.Sprintf("bytes 0-7/%d", len(inputs[0])) {
		t.Fatalf("native Range: %d %v", partial.Code, partial.Header())
	}
	if head := mediaRequest("HEAD", mediaURL, "Range", "bytes=0-7"); head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != fmt.Sprint(len(inputs[0])) {
		t.Fatalf("native HEAD: %d %v", head.Code, head.Header())
	}
	if cached := mediaRequest("GET", mediaURL, "If-None-Match", whole.Header().Get("ETag")); cached.Code != 304 || cached.Body.Len() != 0 {
		t.Fatalf("native conditional: %d %s", cached.Code, cached.Body.String())
	}
	if multi := mediaRequest("GET", mediaURL, "Range", "bytes=0-1,8-11"); multi.Code != 206 || !strings.HasPrefix(multi.Header().Get("Content-Type"), "multipart/byteranges") {
		t.Fatalf("native multipart: %d %v", multi.Code, multi.Header())
	}
	oldPoster, err := dbgen.New(db.Reader()).GetPublishedPoster(ctx, dbgen.GetPublishedPosterParams{ItemID: sql.NullInt64{Int64: ready.ItemID, Valid: true}, ProfileVersion: sql.NullString{String: poster.Profile, Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(ctx, oldPoster.ObjectKey); err != nil {
		t.Fatal(err)
	}
	if missing := mediaRequest("GET", "/media/"+oldPoster.ID); missing.Code != 404 {
		t.Fatalf("native missing poster: %d %s", missing.Code, missing.Body.String())
	}
	posters, err = poster.New(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	rebuildScratch, err := scratch.Open(scratch.Config{Root: t.TempDir(), MaxBytes: 8 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	rebuild, err := worker.New(db, map[domain.JobKind]worker.Handler{domain.JobPoster: worker.Poster(posters, db, p, rebuildScratch)}, worker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := rebuild.RunOne(ctx); err != nil || !ran {
		t.Fatalf("native missing poster rebuild: %v %v", ran, err)
	}
	if err := rebuildScratch.Close(); err != nil {
		t.Fatal(err)
	}
	newPoster, err := dbgen.New(db.Reader()).GetPublishedPoster(ctx, dbgen.GetPublishedPosterParams{ItemID: sql.NullInt64{Int64: ready.ItemID, Valid: true}, ProfileVersion: sql.NullString{String: poster.Profile, Valid: true}})
	if err != nil || newPoster.ID == oldPoster.ID {
		t.Fatal("poster bytes were overwritten instead of rebuilt", err)
	}
	deletedKeys = append(deletedKeys, newPoster.ObjectKey)
	if _, err := client.Verify(ctx, newPoster.ObjectKey, newPoster.SizeBytes, newPoster.Sha256); err != nil {
		t.Fatal("rebuilt poster not verified", err)
	}
	item, err = dbgen.New(db.Reader()).GetItem(ctx, ready.ItemID)
	if err != nil || item.State != domain.ItemReady || item.ViewCount != 0 {
		t.Fatal("media availability changed views or primary", err)
	}
	deleteRequest := func(handler http.Handler) *httptest.ResponseRecorder {
		req := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/items/%d", ready.ItemID), nil)
		req.Header.Set("If-Match", fmt.Sprintf(`"%d"`, item.Version))
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	out := deleteRequest(h)
	if out.Code != 202 {
		t.Fatalf("native DELETE: %d %s", out.Code, out.Body.String())
	}
	var receipt api.JobReceipt
	if err := json.Unmarshal(out.Body.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if hidden := mediaRequest("GET", mediaURL); hidden.Code != 404 {
		t.Fatalf("deleted URL stayed public: %d", hidden.Code)
	}
	manager, err = objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	del, err := deletion.New(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	deleteRunner, err := worker.New(db, map[domain.JobKind]worker.Handler{domain.JobDelete: worker.Deletion(del, db)}, worker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := deleteRunner.RunOne(ctx); err != nil || !ran {
		t.Fatalf("native delete worker: %v %v", ran, err)
	}
	job, err := dbgen.New(db.Reader()).GetJob(ctx, receipt.JobId.String())
	if err != nil || job.State != domain.JobRetryWait || job.ErrorCode.String != "deletion_awaiting_storage_restart" {
		t.Fatalf("restart verification forgotten: %+v %v", job, err)
	}
	if _, err := dbgen.New(db.Reader()).GetDeletionItem(ctx, ready.ItemID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted metadata remains: %v", err)
	}
	if repeat := deleteRequest(h); repeat.Code != 202 {
		t.Fatalf("pending deletion replay: %d %s", repeat.Code, repeat.Body.String())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "kill", "--signal", "KILL", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	db, err = store.Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	control, err = storageprotocol.NewClient(f.endpoint(), f.access, f.secret, &http.Client{Timeout: 60 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pair, err = librarybinding.Ensure(ctx, db, control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ingestion.RecoverInterrupted(ctx, db, time.Now()); err != nil {
		t.Fatal(err)
	}
	client, err = objectstore.New(objectstore.Config{Endpoint: f.endpoint(), Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: &http.Client{Timeout: 60 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	client, err = client.AtLibrary(pair.StorageID, pair.LibraryID, pair.VolumeID)
	if err != nil {
		t.Fatal(err)
	}
	manager, err = objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	del, err = deletion.New(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	deleteRunner, err = worker.New(db, map[domain.JobKind]worker.Handler{domain.JobDelete: worker.Deletion(del, db)}, worker.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if ran, err := deleteRunner.RunOne(ctx); err != nil || !ran {
		t.Fatalf("native restart verification: %v %v", ran, err)
	}
	if _, err := dbgen.New(db.Reader()).GetJob(ctx, receipt.JobId.String()); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("verified deletion remains as job history", err)
	}
	h, err = httpapi.New(db, httpapi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if done := deleteRequest(h); done.Code != 204 {
		t.Fatalf("verified deletion replay: %d %s", done.Code, done.Body.String())
	}
	for _, key := range deletedKeys {
		var wire *objectstore.Error
		if _, err := client.Head(ctx, key); !errors.As(err, &wire) || wire.Status != 404 {
			t.Fatalf("deleted object returned after restart: %s %v", key, err)
		}
	}
	c, err = ingestion.NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Complete(ctx, batch.Sessions[0].ID); err == nil {
		t.Fatal("old Complete resurrected deleted item")
	}
	if _, err := client.Verify(ctx, batch.Sessions[1].IncomingKey, int64(len(inputs[1])), hex.EncodeToString(sum[:])); err != nil {
		t.Fatal("unrelated failed source lost during deletion", err)
	}
}
