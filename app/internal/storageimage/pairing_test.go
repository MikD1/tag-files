package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

func TestOwnedStoragePairRejectsForeignEpochBeforeRetiringUnknownWrite(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	endpoint, _ := url.Parse(f.endpoint())
	var target atomic.Pointer[url.URL]
	target.Store(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(endpoint)
	proxy.Director = func(r *http.Request) { httputil.NewSingleHostReverseProxy(target.Load()).Director(r) }
	var losePut atomic.Bool
	losePut.Store(true)
	proxy.ModifyResponse = func(res *http.Response) error {
		if res.Request.Method == "PUT" && losePut.Swap(false) && res.StatusCode == 200 {
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
			return errors.New("lost committed PUT response")
		}
		return nil
	}
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) { w.WriteHeader(502) }
	peer := httptest.NewServer(proxy)
	defer peer.Close()
	httpClient := &http.Client{Timeout: 60 * time.Second}
	control, err := storageprotocol.NewClient(peer.URL, f.access, f.secret, httpClient)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := librarybinding.Ensure(ctx, db, control)
	if err != nil {
		t.Fatal(err)
	}
	state := f.state()
	if state.ID != pair.StorageID || state.LibraryID != pair.LibraryID || state.VolumeID != pair.VolumeID {
		t.Fatalf("pair not stored in owned volume: %+v %+v", pair, state)
	}
	base, err := objectstore.New(objectstore.Config{Endpoint: peer.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: httpClient})
	if err != nil {
		t.Fatal(err)
	}
	bound, err := base.AtLibrary(pair.StorageID, pair.LibraryID, pair.VolumeID)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, bound, 2)
	if err != nil {
		t.Fatal(err)
	}
	asset := domain.NewUUID()
	key := "primary/" + pair.LibraryID + "/" + asset + "/media.png"
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewNRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	data := photo.Bytes()
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	_, err = manager.Put(ctx, func(*sql.Tx) (objectio.Request, error) {
		return objectio.Request{TargetKind: "asset", TargetID: asset, Generation: 1, Operation: objectio.PutObject, Key: key, Arguments: objectio.Arguments{Size: int64(len(data)), SHA256: hash, ContentType: "image/png"}}, nil
	}, bytes.NewReader(data), func(objectstore.Verified) error { t.Error("lost reply reached metadata acknowledgement"); return nil })
	if err == nil {
		t.Fatal("lost response reported success")
	}
	intents, err := manager.ListKey(ctx, key, "", 100)
	if err != nil || len(intents) != 1 {
		t.Fatalf("missing uncertain writer: %v %v", intents, err)
	}
	other := create(t)
	other.access, other.secret = f.access, f.secret
	other.copySecret("s3_access_key", other.access)
	other.copySecret("s3_secret_key", other.secret)
	testutil.Docker(t, "", "start", other.name)
	other.ready()
	otherEndpoint, _ := url.Parse(other.endpoint())
	target.Store(otherEndpoint)
	err = manager.ReconcileAfterRestart(ctx, intents[0], func(objectio.ReconciliationClient, objectio.Intent) error {
		t.Error("foreign storage epoch used as old-backend quiescence proof")
		return nil
	})
	if !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		t.Fatalf("wrong-volume operation not refused: %v", err)
	}
	if _, err := librarybinding.Ensure(ctx, db, control); !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		t.Fatalf("database adopted replacement volume: %v", err)
	}
	if state := other.state(); state.LibraryID != "" {
		t.Fatal("refused replacement volume was bound")
	}
	remaining, err := manager.ListKey(ctx, key, "", 100)
	if err != nil || len(remaining) != 1 {
		t.Fatal("foreign epoch erased old writer", err)
	}
	target.Store(endpoint)
	if err := manager.ReconcileAfterRestart(ctx, intents[0], func(objectio.ReconciliationClient, objectio.Intent) error {
		t.Error("unchanged epoch reconciled")
		return nil
	}); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("same storage lifetime settled unknown PUT: %v", err)
	}
	testutil.Docker(t, "", "kill", "--signal=KILL", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	restarted, _ := url.Parse(f.endpoint())
	target.Store(restarted)
	if err := manager.ReconcileAfterRestart(ctx, intents[0], func(client objectio.ReconciliationClient, _ objectio.Intent) error {
		_, err := client.Verify(ctx, key, int64(len(data)), hash)
		return err
	}); err != nil {
		t.Fatal("actual same-volume restart did not reconcile", err)
	}
	if state := f.state(); state.ID != pair.StorageID || state.LibraryID != pair.LibraryID || state.VolumeID != pair.VolumeID {
		t.Fatal("same volume changed persistent pair")
	}
	remaining, err = manager.ListKey(ctx, key, "", 100)
	if err != nil || len(remaining) != 0 {
		t.Fatal("reconciled writer retained", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if resumed, err := librarybinding.Ensure(ctx, db, control); err != nil || resumed != pair {
		t.Fatalf("paired SQLite failed reopen: %+v %v", resumed, err)
	}
}
