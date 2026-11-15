package storageprofile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

func TestCapacityControlMeasuresOnlyAuthenticatedBoundPhysicalRoot(t *testing.T) {
	p := prepare(t, t.TempDir())
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	epoch := domain.NewUUID()
	var forwarded atomic.Int64
	peer := httptest.NewServer(pairingGate(p, appCredentials(), epoch, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { forwarded.Add(1); w.WriteHeader(500) })))
	defer peer.Close()
	c, err := storageprotocol.NewClient(peer.URL, appCredentials().AccessKeyID, appCredentials().SecretAccessKey, peer.Client())
	if err != nil {
		t.Fatal(err)
	}
	library := domain.NewUUID()
	identity, err := c.Bind(context.Background(), p.State.ID, library, p.State.VolumeID)
	if err != nil {
		t.Fatal(err)
	}
	capacity, err := c.Capacity(context.Background(), identity.StorageID, library, identity.VolumeID)
	direct, statErr := diskspace.Measure(p.root)
	if err != nil || statErr != nil || capacity.StorageID != identity.StorageID || capacity.LibraryID != library || capacity.VolumeID != identity.VolumeID || capacity.Epoch != epoch || capacity.FileSystemID != direct.FileSystemID || !diskspace.Valid(capacity.Snapshot) {
		t.Fatalf("capacity: %+v direct=%+v err=%v/%v", capacity, direct, err, statErr)
	}
	if _, err := c.Capacity(context.Background(), domain.NewUUID(), library, identity.VolumeID); !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		t.Fatal("foreign capacity accepted", err)
	}
	bad, err := storageprotocol.NewClient(peer.URL, appCredentials().AccessKeyID, "wrong-secret", peer.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Capacity(context.Background(), identity.StorageID, library, identity.VolumeID); !errors.Is(err, storageprotocol.ErrAuthentication) {
		t.Fatal("unauthenticated capacity accepted", err)
	}
	if forwarded.Load() != 0 {
		t.Fatal("capacity forwarded to S3")
	}
	// Moving the original root leaves its lock alive but invalidates the path.
	// A copied marker on the replacement must not authenticate its free space.
	old := p.root + "-old"
	if err := os.Rename(p.root, old); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(old)
	if err := os.Mkdir(p.root, 0700); err != nil {
		t.Fatal(err)
	}
	marker, err := os.ReadFile(filepath.Join(old, StateFilename))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.root, StateFilename), marker, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Capacity(context.Background(), identity.StorageID, library, identity.VolumeID); err == nil {
		t.Fatal("capacity from a replacement physical root was accepted")
	}
}
