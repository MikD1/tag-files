package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os/exec"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/capacity"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

type untouchedPart struct{ read bool }

func (b *untouchedPart) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }

func TestCapacityOwnedImageRealENOSPCKeepsSourceAndRejectsUnreadPart(t *testing.T) {
	// A bounded, disposable RAM-backed volume is a fault fixture, not evidence
	// of persistent storage durability. The ordinary owned tests keep named disk
	// volumes and supply restart/hash evidence independently.
	f := createWithVolume(t, []string{"--driver", "local", "--opt", "type=tmpfs", "--opt", "device=tmpfs", "--opt", "o=size=256m"})
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx, stop := context.WithTimeout(context.Background(), 90*time.Second)
	defer stop()
	dataDir := t.TempDir()
	db, err := store.Open(ctx, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	control, err := storageprotocol.NewClient(f.endpoint(), f.access, f.secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := librarybinding.Ensure(ctx, db, control)
	if err != nil {
		t.Fatal(err)
	}
	client, err := objectstore.New(objectstore.Config{Endpoint: f.endpoint(), Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret})
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
	sandbox, err := scratch.Open(scratch.Config{Root: t.TempDir(), MaxBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	budget, err := capacity.New(db, sandbox, client, capacity.Config{DataDir: dataDir, OutputLimit: 1 << 20, PosterLimit: 1 << 20, PartSize: 32 << 20, Parallel: 2, MinFreeBytes: 4 << 20, MinFreeInodes: 16})
	if err != nil {
		t.Fatal(err)
	}
	uploads, err := ingestion.NewCoordinator(db, manager, budget)
	if err != nil {
		t.Fatal(err)
	}
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewNRGBA(image.Rect(0, 0, 13, 9))); err != nil {
		t.Fatal(err)
	}
	var batch ingestion.Batch
	extra, err := budget.Files([]int64{int64(photo.Len()), 7})
	if err != nil {
		t.Fatal(err)
	}
	err = budget.Admit(ctx, extra, func() error {
		return db.Write(ctx, func(tx *sql.Tx) error {
			var err error
			batch, err = ingestion.Create(ctx, tx, []ingestion.File{{ClientFileID: domain.NewUUID(), Filename: "sole-source.png", Size: int64(photo.Len())}, {ClientFileID: domain.NewUUID(), Filename: "pending.png", Size: 7}}, nil, ingestion.DefaultLimits(), time.Now())
			return err
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range batch.Sessions {
		if _, err := uploads.EnsureMultipart(ctx, session.ID); err != nil {
			t.Fatal(err)
		}
	}
	first, second := batch.Sessions[0], batch.Sessions[1]
	sum := sha256.Sum256(photo.Bytes())
	if _, err := uploads.UploadPart(ctx, first.ID, 1, ingestion.PartBinding{Number: 1, Size: int64(photo.Len()), SHA256: base64.StdEncoding.EncodeToString(sum[:])}, bytes.NewReader(photo.Bytes())); err != nil {
		t.Fatal(err)
	}
	if _, err := uploads.Complete(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	before, err := client.Verify(ctx, first.IncomingKey, int64(photo.Len()), fmt.Sprintf("%x", sum))
	if err != nil {
		t.Fatal(err)
	}
	fill := exec.CommandContext(ctx, "docker", "exec", f.name, "dd", "if=/dev/zero", "of=/data/.capacity-fill", "bs=1048576")
	output, fillErr := fill.CombinedOutput()
	defer func() { testutil.Docker(t, "", "exec", f.name, "rm", "-f", "/data/.capacity-fill") }()
	observed, err := client.Capacity(ctx)
	if fillErr == nil || err != nil || observed.AvailableBytes >= 4<<20 {
		t.Fatalf("fixture did not reach actual bounded ENOSPC: fill=%v capacity=%+v err=%v output=%s", fillErr, observed, err, output)
	}
	body := &untouchedPart{}
	partSum := sha256.Sum256([]byte("another"))
	if _, err := uploads.UploadPart(ctx, second.ID, 1, ingestion.PartBinding{Number: 1, Size: 7, SHA256: base64.StdEncoding.EncodeToString(partSum[:])}, body); !errors.Is(err, capacity.ErrInsufficientStorage) || body.read {
		t.Fatal("actual full volume consumed or forwarded a new part", err, body.read)
	}
	if replay, err := uploads.Complete(ctx, first.ID); err != nil || replay.State != "pending" || replay.Session == nil || replay.Session.State != domain.SessionQueued || replay.Session.SourceSha256.String != before.SHA256 {
		t.Fatal("full disk hid a previously committed source outcome", err, replay.State)
	}
	testutil.Docker(t, "", "exec", f.name, "rm", "-f", "/data/.capacity-fill")
	if _, err := client.Verify(ctx, first.IncomingKey, before.Size, before.SHA256); err != nil {
		t.Fatal("capacity refusal lost the only durable source", err)
	}
	if err := budget.Check(ctx); err != nil {
		t.Fatal("freeing the bounded fixture did not restore admission", err)
	}
	var intents, parts int
	if err := db.Reader().QueryRow("SELECT count(*) FROM object_write_intents").Scan(&intents); err != nil || intents != 0 {
		t.Fatal("refusal created an external write intent", intents, err)
	}
	if err := db.Reader().QueryRow("SELECT count(*) FROM upload_parts WHERE session_id=?", second.ID).Scan(&parts); err != nil || parts != 0 {
		t.Fatal("refusal changed the pending part binding", parts, err)
	}
}
