package objectstore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

func TestLibraryBoundS3ClientPreservesIdentityAcrossEpochsAndLeavesBaseUnbound(t *testing.T) {
	storage, library, epoch := domain.NewUUID(), domain.NewUUID(), domain.NewUUID()
	volume := strings.Repeat("b", 64)
	seen := make(chan [4]string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- [4]string{r.Header.Get(storageprotocol.ExpectedStorageHeader), r.Header.Get(storageprotocol.ExpectedLibraryHeader), r.Header.Get(storageprotocol.ExpectedEpochHeader), r.Header.Get(storageprotocol.ExpectedVolumeHeader)}
		w.Header().Set(storageprotocol.EpochHeader, epoch)
	}))
	defer server.Close()
	base, err := New(Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "private", SecretKey: "private", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := base.AtLibrary(storage, library, volume)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bound.AtLibrary(domain.NewUUID(), library, volume); !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		t.Fatalf("bound client rebound: %v", err)
	}
	active, err := bound.AtEpoch(epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := active.Head(context.Background(), "owned-key"); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != [4]string{storage, library, epoch, volume} {
		t.Fatalf("identity lost at epoch bind: %v", got)
	}
	if _, err := base.Head(context.Background(), "owned-key"); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != [4]string{} {
		t.Fatalf("base client mutated: %v", got)
	}
	if _, err := bound.Head(context.Background(), "owned-key"); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != [4]string{storage, library, "", volume} {
		t.Fatalf("epoch bind mutated parent: %v", got)
	}
}

func TestLibraryBoundEpochRequiresAuthenticatedPersistentIdentity(t *testing.T) {
	for _, mode := range []string{"old-gateway", "foreign-identity"} {
		t.Run(mode, func(t *testing.T) {
			storage, library, epoch := domain.NewUUID(), domain.NewUUID(), domain.NewUUID()
			volume := strings.Repeat("c", 64)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == storageprotocol.ControlPath {
					if mode == "old-gateway" {
						w.WriteHeader(404)
						return
					}
					body, _ := json.Marshal(storageprotocol.Identity{Version: 1, StorageID: domain.NewUUID(), LibraryID: library, VolumeID: volume, Epoch: epoch})
					storageprotocol.Reply(w, r, "private", 200, body)
					return
				}
				w.Header().Set(storageprotocol.EpochHeader, epoch)
			}))
			defer server.Close()
			base, err := New(Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "private", SecretKey: "private", HTTPClient: server.Client()})
			if err != nil {
				t.Fatal(err)
			}
			bound, err := base.AtLibrary(storage, library, volume)
			if err != nil {
				t.Fatal(err)
			}
			_, err = bound.StorageEpoch(context.Background())
			want := storageprotocol.ErrAuthentication
			if mode == "foreign-identity" {
				want = storageprotocol.ErrBindingMismatch
			}
			if !errors.Is(err, want) {
				t.Fatalf("transient epoch trusted without persistent identity: %v", err)
			}
		})
	}
}
