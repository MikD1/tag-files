package storageprofile

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

func TestAuthenticatedBindingControlDoesNotForwardAndRejectsWrongCredentials(t *testing.T) {
	p := prepare(t, t.TempDir())
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	epoch := domain.NewUUID()
	var forwarded atomic.Int64
	server := httptest.NewServer(pairingGate(p, appCredentials(), epoch, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) })))
	defer server.Close()
	c, err := storageprotocol.NewClient(server.URL, appCredentials().AccessKeyID, appCredentials().SecretAccessKey, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := c.Identity(context.Background())
	if err != nil || identity.StorageID != p.State.ID || identity.LibraryID != "" || identity.Epoch != epoch {
		t.Fatalf("identity: %+v %v", identity, err)
	}
	library := domain.NewUUID()
	identity, err = c.Bind(context.Background(), identity.StorageID, library, identity.VolumeID)
	if err != nil || identity.LibraryID != library {
		t.Fatalf("control binding: %+v %v", identity, err)
	}
	if _, err := c.Bind(context.Background(), identity.StorageID, domain.NewUUID(), identity.VolumeID); !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		t.Fatalf("different library bound: %v", err)
	}
	bad, err := storageprotocol.NewClient(server.URL, appCredentials().AccessKeyID, "wrong-secret", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.Identity(context.Background()); !errors.Is(err, storageprotocol.ErrAuthentication) {
		t.Fatalf("bad credentials accepted: %v", err)
	}
	state, err := LoadState(p.root)
	if err != nil || state.LibraryID != library || forwarded.Load() != 0 {
		t.Fatalf("binding was not private/durable: %+v %d %v", state, forwarded.Load(), err)
	}
}

func TestS3IdentityFenceRejectsChangedVolumeOrLibraryBeforeReadingBody(t *testing.T) {
	p := prepare(t, t.TempDir())
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	library := domain.NewUUID()
	if _, err := p.BindLibrary(p.State.ID, library); err != nil {
		t.Fatal(err)
	}
	forwarded := 0
	handler := pairingGate(p, appCredentials(), domain.NewUUID(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded++; w.WriteHeader(200) }))
	for _, wrong := range []string{"storage", "library", "volume", "duplicate", "correct"} {
		body := &countingReader{}
		request := httptest.NewRequest("PUT", "/tag-files/primary/test", body)
		request.Header.Set(storageprotocol.ExpectedStorageHeader, p.State.ID)
		request.Header.Set(storageprotocol.ExpectedLibraryHeader, library)
		request.Header.Set(storageprotocol.ExpectedVolumeHeader, p.State.VolumeID)
		switch wrong {
		case "storage":
			request.Header.Set(storageprotocol.ExpectedStorageHeader, domain.NewUUID())
		case "library":
			request.Header.Set(storageprotocol.ExpectedLibraryHeader, domain.NewUUID())
		case "volume":
			request.Header.Set(storageprotocol.ExpectedVolumeHeader, strings.Repeat("e", 64))
		case "duplicate":
			request.Header.Add(storageprotocol.ExpectedStorageHeader, p.State.ID)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if wrong == "correct" {
			if response.Code != 200 {
				t.Fatal("valid owner refused")
			}
			continue
		}
		if response.Code != 409 || !strings.Contains(response.Body.String(), storageprotocol.IdentityMismatchCode) || body.reads != 0 || forwarded != 0 {
			t.Fatalf("wrong pair reached storage: %s %d reads=%d forwarded=%d", wrong, response.Code, body.reads, forwarded)
		}
	}
	if forwarded != 1 {
		t.Fatalf("valid request forwarded %d times", forwarded)
	}
}

type countingReader struct{ reads int }

func (r *countingReader) Read(p []byte) (int, error) {
	r.reads++
	return 0, errors.New("body must not be read before identity fencing")
}
