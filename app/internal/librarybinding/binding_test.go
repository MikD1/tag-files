package librarybinding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/storageprofile"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ctx = context.Background()

type diskRemote struct {
	installation *storageprofile.Installation
	root, epoch  string
	lose         bool
}

func (r *diskRemote) Identity(context.Context) (storageprotocol.Identity, error) {
	s, err := r.installation.Snapshot()
	return storageprotocol.Identity{Version: 1, StorageID: s.ID, LibraryID: s.LibraryID, Epoch: r.epoch, VolumeID: s.VolumeID}, err
}
func (r *diskRemote) Bind(_ context.Context, storage, library, volume string) (storageprotocol.Identity, error) {
	_, err := r.installation.BindLibrary(storage, library)
	if err != nil {
		return storageprotocol.Identity{}, err
	}
	if r.lose {
		r.lose = false
		return storageprotocol.Identity{}, errors.New("lost durable binding reply")
	}
	return r.Identity(ctx)
}
func remote(t *testing.T) *diskRemote {
	t.Helper()
	root := t.TempDir()
	p, err := storageprofile.Prepare(root, aws.Credentials{AccessKeyID: "test-app-private-access-key", SecretAccessKey: "test-app-private-secret-key"}, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return &diskRemote{installation: p, root: root, epoch: domain.NewUUID()}
}
func library(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLibraryBindingSurvivesLostStorageReplyAndFailedSQLiteCommit(t *testing.T) {
	for _, mode := range []string{"lost-reply", "failed-sql"} {
		t.Run(mode, func(t *testing.T) {
			db := library(t)
			r := remote(t)
			before, err := dbgen.New(db.Reader()).GetLibraryMeta(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "lost-reply" {
				r.lose = true
			} else if err := db.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.Exec(`CREATE TRIGGER fail_pair BEFORE UPDATE OF storage_id ON library_meta BEGIN SELECT RAISE(ABORT,'injected'); END`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := Ensure(ctx, db, r); err == nil {
				t.Fatal("injected failure acknowledged")
			}
			meta, err := dbgen.New(db.Reader()).GetLibraryMeta(ctx)
			if err != nil || meta.StorageID.Valid {
				t.Fatalf("unacknowledged pair stored: %+v %v", meta, err)
			}
			state, err := r.installation.Snapshot()
			if err != nil || state.LibraryID != before.LibraryID {
				t.Fatalf("storage did not retain first durable identity: %+v %v", state, err)
			}
			if mode == "failed-sql" {
				if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER fail_pair"); return err }); err != nil {
					t.Fatal(err)
				}
			}
			pair, err := Ensure(ctx, db, r)
			if err != nil || pair.LibraryID != before.LibraryID || pair.StorageID != state.ID {
				t.Fatalf("pair did not resume: %+v %v", pair, err)
			}
			for _, replacement := range []any{nil, domain.NewUUID()} {
				if err := db.Write(ctx, func(tx *sql.Tx) error {
					_, err := tx.Exec("UPDATE library_meta SET storage_id=?", replacement)
					return err
				}); err == nil {
					t.Fatal("durable storage pair changed")
				}
			}
		})
	}
}

func TestLibraryBindingRefusesForeignVolumesLibrariesAndPartialRestore(t *testing.T) {
	db := library(t)
	r := remote(t)
	unbound, _ := r.installation.Snapshot()
	pair, err := Ensure(ctx, db, r)
	if err != nil {
		t.Fatal(err)
	}
	other := remote(t)
	if _, err := Ensure(ctx, db, other); !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		t.Fatalf("replacement volume adopted: %v", err)
	}
	otherState, _ := other.installation.Snapshot()
	if otherState.LibraryID != "" {
		t.Fatal("refused replacement volume was mutated")
	}
	foreign := library(t)
	if _, err := Ensure(ctx, foreign, r); !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		t.Fatalf("foreign library admitted: %v", err)
	}
	foreignMeta, _ := dbgen.New(foreign.Reader()).GetLibraryMeta(ctx)
	if foreignMeta.StorageID.Valid {
		t.Fatal("foreign library was bound")
	}
	// Restore an older unbound marker with the same storage ID, while keeping
	// the newer paired SQLite: this mixed generation must not be silently fixed.
	r.installation.Close()
	data, err := json.Marshal(unbound)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.root, storageprofile.StateFilename), data, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := storageprofile.Prepare(r.root, aws.Credentials{AccessKeyID: "test-app-private-access-key", SecretAccessKey: "test-app-private-secret-key"}, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	r.installation = p
	r.epoch = domain.NewUUID()
	if _, err := Ensure(ctx, db, r); !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		t.Fatalf("mixed restore was rebound: %v", err)
	}
	state, _ := p.Snapshot()
	meta, _ := dbgen.New(db.Reader()).GetLibraryMeta(ctx)
	if state.LibraryID != "" || meta.StorageID.String != pair.StorageID {
		t.Fatal("refusal changed the restored pair")
	}
}
