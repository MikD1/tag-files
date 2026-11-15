package capacity

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
)

type observer func(context.Context) (storageprotocol.Capacity, error)

func (f observer) Capacity(ctx context.Context) (storageprotocol.Capacity, error) { return f(ctx) }

func TestAdmissionReservesPendingBatchAcrossConcurrentCreationAndReopen(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	db, err := store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	sandbox, err := scratch.Open(scratch.Config{Root: t.TempDir(), MaxBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	remote := observer(func(context.Context) (storageprotocol.Capacity, error) {
		// The measurement must not run inside even a read-only SQL transaction
		// that monopolizes the production writer.
		if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.Exec("INSERT INTO tags(name) VALUES('writer-free')"); return err }); err != nil {
			return storageprotocol.Capacity{}, err
		}
		return storageprotocol.Capacity{Snapshot: space("a", 1_510_000, true)}, nil
	})
	config := Config{DataDir: root, OutputLimit: 100, PosterLimit: 8, PartSize: 32 << 20, Parallel: 2, MinFreeBytes: 1, MinFreeInodes: 4}
	manager, err := New(db, sandbox, remote, config)
	if err != nil {
		t.Fatal(err)
	}
	sizes := make([]int64, 500)
	files := make([]ingestion.File, 500)
	for i := range sizes {
		sizes[i] = 1000
		files[i] = ingestion.File{ClientFileID: domain.NewUUID(), Filename: "photo.jpg", Size: 1000}
	}
	extra, err := manager.Files(sizes)
	if err != nil {
		t.Fatal(err)
	}
	create := func() error {
		return manager.Admit(ctx, extra, func() error {
			return db.Write(ctx, func(tx *sql.Tx) error {
				_, err := ingestion.Create(ctx, tx, files, nil, ingestion.DefaultLimits(), time.Now())
				return err
			})
		})
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Go(func() { results <- create() })
	}
	wg.Wait()
	close(results)
	accepted, full := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrInsufficientStorage) {
			full++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || full != 1 {
		t.Fatalf("concurrent admissions overspent observed space: accepted%d full%d", accepted, full)
	}
	var count int
	if err := db.Reader().QueryRow("SELECT count(*) FROM upload_sessions").Scan(&count); err != nil || count != 500 {
		t.Fatal("rejected intent leaked into SQLite", count, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	manager, err = New(db, sandbox, remote, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Check(ctx); err != nil {
		t.Fatal("500 files charged 500 maximum encodes instead of one serial working set", err)
	}
	if err := create(); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatal("restart forgot pending incoming reservations", err)
	}
}
