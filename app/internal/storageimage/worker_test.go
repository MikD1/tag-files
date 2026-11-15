package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
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
	if err := db.Reader().QueryRow("SELECT object_key,sha256,size_bytes,width,height FROM assets WHERE item_id=? AND role='poster' AND state='published' AND profile_version='poster-v1'", ready.ItemID).Scan(&key, &hash, &size, &width, &height); err != nil || width != 13 || height != 9 {
		t.Fatalf("native poster descriptor: %dx%d %v", width, height, err)
	}
	if _, err := client.Verify(ctx, key, size, hash); err != nil {
		t.Fatal("poster not durable", err)
	}
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
}
