package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/application"
	"github.com/mikd1/tag-files/app/internal/capacity"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

func TestApplicationOwnedImageRecoversInterruptedIngestAndServesPublishedMedia(t *testing.T) {
	if os.Getenv("TAG_FILES_MEDIA_INTEGRATION") != "1" {
		t.Skip("requires actual media tools and owned storage")
	}
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	cfg := application.Config{DataDir: t.TempDir(), Scratch: scratch.Config{Root: t.TempDir(), MaxBytes: 8 * 1024 * 1024}, Storage: objectstore.Config{Endpoint: f.endpoint(), Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret}, Media: media.DefaultConfig(), Version: "native-test"}
	cfg.Media.MaxOutputBytes = 1024 * 1024
	db, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	control, err := storageprotocol.NewClient(cfg.Storage.Endpoint, f.access, f.secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := librarybinding.Ensure(ctx, db, control)
	if err != nil {
		t.Fatal(err)
	}
	client, err := objectstore.New(cfg.Storage)
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
	uploads, err := ingestion.NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewNRGBA(image.Rect(0, 0, 13, 9))); err != nil {
		t.Fatal(err)
	}
	sandbox, err := scratch.Open(cfg.Scratch)
	if err != nil {
		t.Fatal(err)
	}
	defer sandbox.Close()
	processor, err := media.New(cfg.Media)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := capacity.New(db, sandbox, client, capacity.Config{DataDir: cfg.DataDir, OutputLimit: processor.OutputLimit(), PosterLimit: processor.PosterOutputLimit(), PartSize: ingestion.DefaultLimits().PartSize, Parallel: 2, MinFreeBytes: 1, MinFreeInodes: 4})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := httpapi.New(db, httpapi.Options{Uploads: &httpapi.UploadOptions{Coordinator: uploads, Budget: budget, Limits: ingestion.DefaultLimits()}})
	if err != nil {
		t.Fatal(err)
	}
	transport := httptest.NewServer(handler)
	defer transport.Close()
	request := func(method, path string, body []byte, headers ...string) []byte {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, transport.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		res, err := transport.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatal("native upload HTTP", res.StatusCode, string(raw), err)
		}
		return raw
	}
	newBody, _ := json.Marshal(map[string]any{"files": []map[string]any{{"clientFileId": domain.NewUUID(), "filename": "photo.png", "size": photo.Len()}}})
	var created api.UploadsCreated
	if err := json.Unmarshal(request("POST", "/api/v1/upload-sessions", newBody, "Idempotency-Key", domain.NewUUID()), &created); err != nil || len(created.Sessions) != 1 {
		t.Fatal("native batch", created, err)
	}
	session := struct{ ID string }{created.Sessions[0].Id.String()}
	sum := sha256.Sum256(photo.Bytes())
	var part api.UploadPart
	if err := json.Unmarshal(request("PUT", "/api/v1/upload-sessions/"+session.ID+"/parts/1", photo.Bytes(), "Content-Type", "application/octet-stream", "X-Upload-Generation", "1", "X-Part-SHA256", base64.StdEncoding.EncodeToString(sum[:])), &part); err != nil || part.Etag == nil {
		t.Fatal(part, err)
	}
	completeBody, _ := json.Marshal(map[string]any{"generation": 1, "parts": []map[string]any{{"partNumber": 1, "etag": *part.Etag}}})
	operationKey := domain.NewUUID()
	var accepted api.UploadOutcome
	if err := json.Unmarshal(request("POST", "/api/v1/upload-sessions/"+session.ID+"/complete", completeBody, "Idempotency-Key", operationKey), &accepted); err != nil || accepted.State != "pending" || accepted.JobId == nil {
		t.Fatal("native Complete not durable", accepted, err)
	}
	transport.Close()
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
	interrupted, err := db.ClaimJob(ctx, time.Now(), time.Minute)
	if err != nil || interrupted.Type != domain.JobComplete {
		t.Fatal("could not leave an interrupted generation", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	app, err := application.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	origin := "http://" + listener.Addr().String()
	run, stop := context.WithCancel(ctx)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- app.Serve(run, listener) }()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	get := func(path string) (int, []byte) {
		t.Helper()
		response, err := httpClient.Get(origin + path)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, body
	}
	var item api.Item
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		status, body := get("/api/v1/items")
		if status != 200 {
			t.Fatalf("catalog during software encode: %d %s", status, body)
		}
		var page api.ItemPage
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 && page.Items[0].ContentUrl != nil && page.Items[0].PosterUrl != nil {
			item = page.Items[0]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if item.ContentUrl == nil || item.PosterUrl == nil {
		t.Fatal("factory did not recover and run ingest/poster work")
	}
	if status, body := get("/api/v1/operations/" + operationKey); status != 200 {
		t.Fatalf("native final receipt %d %s", status, body)
	} else {
		var op api.Operation
		if err := json.Unmarshal(body, &op); err != nil || op.State != "completed" || op.HttpStatus != 200 {
			t.Fatal("native ready did not resolve Complete receipt", op, err)
		}
	}
	if status, body := get("/api/v1/upload-batches"); status != 200 || string(body) != "[]" {
		t.Fatalf("native ready left upload history: %d %s", status, body)
	}
	if status, body := get(*item.ContentUrl); status != 200 || !bytes.Equal(body, photo.Bytes()) {
		t.Fatalf("published native media: %d %d bytes", status, len(body))
	}
	if status, body := get(*item.PosterUrl); status != 200 || len(body) < 3 || !bytes.Equal(body[:3], []byte{0xff, 0xd8, 0xff}) {
		t.Fatalf("published native poster: %d %d bytes", status, len(body))
	}
	if status, body := get("/health/ready"); status != 200 {
		t.Fatalf("owned native runtime not ready: %d %s", status, body)
	}
	// A live but unavailable owned storage must affect readiness only. Pausing
	// retains this fixture's ephemeral host mapping while exercising real I/O.
	paused := true
	testutil.Docker(t, "", "pause", f.name)
	defer func() {
		if paused {
			testutil.Docker(t, "", "unpause", f.name)
		}
	}()
	if status, body := get("/health/ready"); status != 503 {
		t.Fatalf("paused real storage still ready: %d %s", status, body)
	}
	for _, path := range []string{"/health/live", "/api/v1/tags"} {
		if status, body := get(path); status != 200 {
			t.Fatalf("storage outage blocked %s: %d %s", path, status, body)
		}
	}
	testutil.Docker(t, "", "unpause", f.name)
	paused = false
	if status, body := get("/health/ready"); status != 200 {
		t.Fatalf("resumed owned storage remained unavailable: %d %s", status, body)
	}
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native runtime failed to stop")
	}
	if err := app.Ready(ctx); !errors.Is(err, application.ErrNotReady) {
		t.Fatal("stopped native app still ready", err)
	}
	db, err = store.Open(ctx, cfg.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := dbgen.New(db.Reader()).FindReadyItemByUpload(ctx, sql.NullString{String: session.ID, Valid: true}); err != nil {
		t.Fatal("ready commit did not survive runtime close", err)
	}
	var sources, intents, liveJobs int
	if err := db.Reader().QueryRow("SELECT count(*) FROM upload_sessions").Scan(&sources); err != nil || sources != 0 {
		t.Fatal("ready retained visible upload", sources, err)
	}
	if err := db.Reader().QueryRow("SELECT count(*) FROM object_write_intents").Scan(&intents); err != nil || intents != 0 {
		t.Fatal("known native writes did not settle", intents, err)
	}
	if err := db.Reader().QueryRow("SELECT count(*) FROM jobs WHERE state='running'").Scan(&liveJobs); err != nil || liveJobs != 0 {
		t.Fatal("runtime left a running generation", liveJobs, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	checkApplicationCommand(t, ctx, cfg, listener.Addr().String())
}

func checkApplicationCommand(t *testing.T, ctx context.Context, cfg application.Config, address string) {
	t.Helper()
	root := t.TempDir()
	binary := filepath.Join(root, "tag-files")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/tag-files")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build runtime command: %v %s", err, output)
	}
	for name, value := range map[string]string{"access": cfg.Storage.AccessKey, "secret": cfg.Storage.SecretKey} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := os.Create(filepath.Join(root, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	command := exec.CommandContext(ctx, binary)
	command.Env = append(os.Environ(), "TAG_FILES_LISTEN="+address, "TAG_FILES_DATA_DIR="+cfg.DataDir, "TAG_FILES_SCRATCH_DIR="+cfg.Scratch.Root, "TAG_FILES_S3_ENDPOINT="+cfg.Storage.Endpoint, "TAG_FILES_ACCESS_KEY_FILE="+filepath.Join(root, "access"), "TAG_FILES_SECRET_KEY_FILE="+filepath.Join(root, "secret"), "TAG_FILES_SCRATCH_MAX_BYTES=8388608", "TAG_FILES_MAX_OUTPUT_BYTES=1048576")
	command.Stdout, command.Stderr = logs, logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	var waitErr error
	waited := make(chan struct{})
	go func() { waitErr = command.Wait(); close(waited) }()
	defer func() { _ = command.Process.Kill(); <-waited }()
	client := &http.Client{Timeout: 2 * time.Second}
	ready := false
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/health/ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				ready = true
				break
			}
		}
		select {
		case <-waited:
			data, _ := os.ReadFile(logs.Name())
			t.Fatalf("server exited during startup: %v %s", waitErr, data)
		default:
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(logs.Name())
		t.Fatalf("application command did not become ready: %s", data)
	}
	check := exec.CommandContext(ctx, binary, "check")
	check.Env = command.Env
	if output, err := check.CombinedOutput(); err != nil {
		t.Fatalf("runtime health subcommand: %v %s", err, output)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
		if waitErr != nil {
			data, _ := os.ReadFile(logs.Name())
			t.Fatalf("runtime SIGTERM: %v %s", waitErr, data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runtime command did not join shutdown after SIGTERM")
	}
	db, err := store.Open(ctx, cfg.DataDir)
	if err != nil {
		t.Fatal("SIGTERM retained library owner", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	sandbox, err := scratch.Open(cfg.Scratch)
	if err != nil {
		t.Fatal("SIGTERM retained scratch owner", err)
	}
	if err := sandbox.Close(); err != nil {
		t.Fatal(err)
	}
}
