package application_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/mikd1/tag-files/app/internal/application"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprofile"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ctx = context.Background()

type remote struct {
	endpoint string
	offline  atomic.Bool
}

func setup(t *testing.T) (application.Config, *remote) {
	t.Helper()
	const access, secret = "test-app-private-access", "test-app-private-secret"
	installation, err := storageprofile.Prepare(t.TempDir(), aws.Credentials{AccessKeyID: access, SecretAccessKey: secret}, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { installation.Close() })
	if err := installation.RecordReady(); err != nil {
		t.Fatal(err)
	}
	epoch := domain.NewUUID()
	r := &remote{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(io.LimitReader(req.Body, storageprotocol.MaxControlBytes+1))
		if err != nil || req.URL.Path != storageprotocol.ControlPath || !storageprotocol.Authenticate(req, body, access, secret) {
			t.Error("unbound or unauthenticated storage request")
			w.WriteHeader(401)
			return
		}
		if r.offline.Load() {
			w.WriteHeader(503)
			return
		}
		if req.Method == "POST" {
			var b storageprotocol.Binding
			if err := json.Unmarshal(body, &b); err != nil {
				t.Error(err)
				return
			}
			if _, err := installation.BindLibrary(b.StorageID, b.LibraryID); err != nil {
				t.Error(err)
				return
			}
		}
		s, err := installation.Snapshot()
		if err != nil {
			t.Error(err)
			return
		}
		data, _ := json.Marshal(storageprotocol.Identity{Version: 1, StorageID: s.ID, VolumeID: s.VolumeID, LibraryID: s.LibraryID, Epoch: epoch})
		storageprotocol.Reply(w, req, secret, 200, data)
	}))
	t.Cleanup(peer.Close)
	r.endpoint = peer.URL
	p := media.DefaultConfig()
	// No media is executed by these startup tests. Native owned-image tests
	// separately exercise the same factory with the actual pinned tools.
	p.FFmpeg, p.FFprobe, p.MaxOutputBytes = "/bin/true", "/bin/true", 1024*1024
	return application.Config{DataDir: t.TempDir(), Scratch: scratch.Config{Root: t.TempDir(), MaxBytes: 8 * 1024 * 1024}, Storage: objectstore.Config{Endpoint: peer.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: access, SecretKey: secret, HTTPClient: peer.Client()}, Media: p, Version: "test-build", ShutdownTimeout: time.Second}, r
}

func guardedCache(t *testing.T, root string) string {
	t.Helper()
	directory := filepath.Join(root, domain.NewUUID(), "1-"+domain.NewUUID())
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".guard-v1", "source.cache"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}

func serve(t *testing.T, app *application.App) (string, context.CancelFunc, <-chan error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	parent, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- app.Serve(parent, listener) }()
	return "http://" + listener.Addr().String(), stop, done
}

func request(t *testing.T, origin, path string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Get(origin + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func TestApplicationOwnsStartupRecoveryAdmissionAndJoinedShutdown(t *testing.T) {
	c, remote := setup(t)
	cache := guardedCache(t, c.Scratch.Root)
	unknown := filepath.Join(c.Scratch.Root, "unmarked-input")
	if err := os.WriteFile(unknown, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	app, err := application.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	if err := app.Ready(ctx); err == nil {
		t.Fatal("claimed ready before worker/HTTP startup")
	}
	if _, err := os.Stat(cache); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("guarded crash scratch not reclaimed", err)
	}
	if data, err := os.ReadFile(unknown); err != nil || string(data) != "keep" {
		t.Fatal("unmarked scratch was removed", err)
	}
	origin, stop, done := serve(t, app)
	defer stop()
	if status, data := request(t, origin, "/health/ready"); status != 200 {
		t.Fatalf("fully initialized runtime unavailable: %d %s", status, data)
	}
	if status, data := request(t, origin, "/api/v1/system/status"); status != 200 || !json.Valid(data) {
		t.Fatalf("system status: %d %s", status, data)
	}
	remote.offline.Store(true)
	if status, _ := request(t, origin, "/health/ready"); status != 503 {
		t.Fatal("unavailable storage still ready", status)
	}
	if status, _ := request(t, origin, "/health/live"); status != 200 {
		t.Fatal("storage outage killed process liveness", status)
	}
	if status, _ := request(t, origin, "/api/v1/tags"); status != 200 {
		t.Fatal("storage outage blocked SQL reads", status)
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not join HTTP/worker")
	}
	if err := app.Ready(ctx); err == nil {
		t.Fatal("stopped runtime still ready")
	}
	db, err := store.Open(ctx, c.DataDir)
	if err != nil {
		t.Fatal("shutdown retained library lock", err)
	}
	defer db.Close()
	meta, err := dbgen.New(db.Reader()).GetLibraryMeta(ctx)
	if err != nil || !meta.StorageID.Valid || !meta.StorageVolumeID.Valid {
		t.Fatal("factory did not durably bind the pair", err)
	}
	sandbox, err := scratch.Open(c.Scratch)
	if err != nil {
		t.Fatal("shutdown retained scratch lock", err)
	}
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestApplicationRefusesFutureWorkBeforeReclaimAndReleasesFailedStartup(t *testing.T) {
	c, _ := setup(t)
	db, err := store.Open(ctx, c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := dbgen.New(tx).CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: "1", DedupeKey: "unknown", PayloadJson: `{"version":999}`, NextAttemptAt: store.Timestamp(time.Now()), CreatedAt: store.Timestamp(time.Now())})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	cache := guardedCache(t, c.Scratch.Root)
	if app, err := application.Open(ctx, c); err == nil {
		app.Close()
		t.Fatal("future work was admitted")
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal("failed preflight removed scratch", err)
	}
	db, err = store.Open(ctx, c.DataDir)
	if err != nil {
		t.Fatal("failed startup retained SQLite owner", err)
	}
	defer db.Close()
	var count int
	if err := db.Reader().QueryRow("SELECT count(*) FROM jobs WHERE payload_json=?", `{"version":999}`).Scan(&count); err != nil || count != 1 {
		t.Fatal("unknown work was altered", count, err)
	}
	sandbox, err := scratch.Open(c.Scratch)
	if err != nil {
		t.Fatal("failed startup retained scratch owner", err)
	}
	sandbox.Close()
}

func TestApplicationRejectsAliasedDataAndScratchRoots(t *testing.T) {
	c, _ := setup(t)
	c.Scratch.Root = c.DataDir
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	c.DataDir, err = filepath.Rel(working, c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if app, err := application.Open(ctx, c); err == nil {
		app.Close()
		t.Fatal("relative/absolute aliases allowed scratch reclamation in the library root")
	}
}

func TestApplicationRefusesForeignStorageBeforeReclaim(t *testing.T) {
	c, _ := setup(t)
	app, err := application.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	foreign, _ := setup(t)
	foreign.DataDir = c.DataDir
	cache := guardedCache(t, foreign.Scratch.Root)
	if app, err := application.Open(ctx, foreign); !errors.Is(err, storageprotocol.ErrBindingMismatch) {
		if app != nil {
			app.Close()
		}
		t.Fatal("foreign storage was admitted", err)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatal("pair rejection reclaimed scratch", err)
	}
}

func TestApplicationRefusesLiveScratchOwnerAndReleasesLibrary(t *testing.T) {
	c, _ := setup(t)
	owner, err := scratch.Open(c.Scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if app, err := application.Open(ctx, c); !errors.Is(err, scratch.ErrInUse) {
		if app != nil {
			app.Close()
		}
		t.Fatal("live scratch producer was not a startup gate", err)
	}
	db, err := store.Open(ctx, c.DataDir)
	if err != nil {
		t.Fatal("rejected startup retained library owner", err)
	}
	defer db.Close()
	meta, err := dbgen.New(db.Reader()).GetLibraryMeta(ctx)
	if err != nil || meta.StorageID.Valid {
		t.Fatal("startup changed persistent pair before scratch quiescence", err)
	}
}

type clientFunc func(*http.Request) (*http.Response, error)

func (f clientFunc) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestApplicationKeepsOwnersUntilForcedHTTPShutdownActuallyJoinsHandler(t *testing.T) {
	c, _ := setup(t)
	c.ShutdownTimeout = 20 * time.Millisecond
	base := c.Storage.HTTPClient
	var block atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	c.Storage.HTTPClient = clientFunc(func(r *http.Request) (*http.Response, error) {
		if block.Load() {
			close(entered)
			<-release // Deliberately ignores cancellation, unlike net/http.
		}
		return base.Do(r)
	})
	app, err := application.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	origin, stop, done := serve(t, app)
	defer app.Close()
	defer stop()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	if status, _ := request(t, origin, "/health/live"); status != 200 {
		t.Fatal(status)
	}
	block.Store(true)
	clientDone := make(chan error, 1)
	go func() {
		response, err := http.Get(origin + "/health/ready")
		if response != nil {
			response.Body.Close()
		}
		clientDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("readiness request did not reach storage")
	}
	stop()
	select {
	case err := <-clientDone:
		if err == nil {
			t.Fatal("forced shutdown did not close the waiting HTTP connection")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP shutdown did not reach its bounded forced-close phase")
	}
	if db, err := store.Open(ctx, c.DataDir); !errors.Is(err, store.ErrLibraryInUse) {
		if db != nil {
			db.Close()
		}
		t.Fatal("SQLite ownership released while an admitted handler still runs", err)
	}
	if sandbox, err := scratch.Open(c.Scratch); !errors.Is(err, scratch.ErrInUse) {
		if sandbox != nil {
			sandbox.Close()
		}
		t.Fatal("scratch ownership released while an admitted handler still runs", err)
	}
	select {
	case err := <-done:
		t.Fatal("Serve returned before handler joined", err)
	default:
	}
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("forced shutdown deadline was not reported", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler returned but shutdown did not complete")
	}
}

func TestApplicationShutdownJoinsActivePosterReadAndRequeuesWithoutRetryCharge(t *testing.T) {
	c, _ := setup(t)
	db, err := store.Open(ctx, c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	var job dbgen.Job
	var itemID int64
	assetID := domain.NewUUID()
	key := ""
	err = db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		meta, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		key = "primary/" + meta.LibraryID + "/" + assetID + "/media.jpg"
		if err := tx.QueryRow("INSERT INTO items(media_kind,uploaded_at_utc,state) VALUES('Image',?,'importing') RETURNING id", store.Timestamp(time.Now())).Scan(&itemID); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','tag-files',?,128,?,'image/jpeg','legacy','published')", assetID, itemID, key, strings.Repeat("a", 64)); err != nil {
			return err
		}
		if err := q.MarkItemReady(ctx, dbgen.MarkItemReadyParams{ID: itemID, PrimaryAssetID: sql.NullString{String: assetID, Valid: true}}); err != nil {
			return err
		}
		job, err = poster.Enqueue(ctx, tx, itemID, assetID, time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	base := c.Storage.HTTPClient
	entered, stopped := make(chan struct{}), make(chan struct{})
	c.Storage.HTTPClient = clientFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == "GET" && r.URL.Path == "/tag-files/"+key {
			if r.Header.Get(storageprotocol.ExpectedEpochHeader) == "" || r.Header.Get(storageprotocol.ExpectedLibraryHeader) == "" {
				t.Error("background source read was not bound")
			}
			close(entered)
			<-r.Context().Done()
			close(stopped)
			return nil, r.Context().Err()
		}
		return base.Do(r)
	})
	app, err := application.Open(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	_, stop, done := serve(t, app)
	defer stop()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("the registered poster worker did not start its source read")
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker read was not cancelled/joined")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("runtime returned while its source read was live")
	}
	db, err = store.Open(ctx, c.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	current, err := dbgen.New(db.Reader()).GetJob(ctx, job.ID)
	if err != nil || current.State != domain.JobQueued || current.Attempt != 0 || current.LeaseGeneration < 2 {
		t.Fatalf("interruption spent a retry or retained its old claim: %+v %v", current, err)
	}
	item, err := dbgen.New(db.Reader()).GetItem(ctx, itemID)
	if err != nil || item.State != domain.ItemReady || item.PrimaryAssetID.String != assetID {
		t.Fatal("poster interruption changed primary availability", err)
	}
	sandbox, err := scratch.Open(c.Scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	if count, err := sandbox.Reclaim(); err != nil || count != 0 {
		t.Fatal("joined shutdown left a live/disposable attempt", count, err)
	}
}
