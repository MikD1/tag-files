package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

// This opt-in acceptance transfers the actual production maximum. It never
// materializes a 20 GiB byte slice/file in /tmp, and observes a separate normal
// production app executable so Go test/race/client allocations do not inflate
// the measured app RSS. Storage uses its own disposable named Linux volume.
func TestProductionMaximum20GiBUploadBoundedRSSAndRetainedFailure(t *testing.T) {
	if os.Getenv("TAG_FILES_LARGE_UPLOAD") != "1" {
		t.Skip("run scripts/check-large-upload for the actual 20 GiB acceptance")
	}
	workParent := os.Getenv("TAG_FILES_LARGE_WORK_DIR")
	if workParent == "" {
		t.Fatal("TAG_FILES_LARGE_WORK_DIR must name a disk-backed project directory, never /tmp")
	}
	abs, e := filepath.Abs(workParent)
	if e != nil {
		t.Fatal(e)
	}
	if abs == "/tmp" || strings.HasPrefix(abs, "/tmp/") {
		t.Fatal("large scratch cannot use /tmp tmpfs")
	}
	root, e := os.MkdirTemp(abs, "tag-files-large-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	var fs syscall.Statfs_t
	if e = syscall.Statfs(root, &fs); e != nil {
		t.Fatal(e)
	}
	if fs.Bavail*uint64(filesystemBlockSize(fs)) < 100<<30 {
		t.Fatal("20 GiB acceptance requires at least 100 GiB disposable disk headroom")
	}
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	binary := filepath.Join(root, "tag-files")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./cmd/tag-files")
	build.Dir = filepath.Join("..", "..")
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatalf("build app: %v %s", e, output)
	}
	for name, value := range map[string]string{"access": f.access, "secret": f.secret} {
		if e = os.WriteFile(filepath.Join(root, name), []byte(value+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	dataDir, scratchDir := filepath.Join(root, "metadata"), filepath.Join(root, "scratch")
	for _, dir := range []string{dataDir, scratchDir} {
		if e = os.Mkdir(dir, 0700); e != nil {
			t.Fatal(e)
		}
	}
	reserved, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	address := reserved.Addr().String()
	reserved.Close()
	origin := "http://" + address
	logs, e := os.Create(filepath.Join(root, "app.log"))
	if e != nil {
		t.Fatal(e)
	}
	defer logs.Close()
	app := exec.CommandContext(ctx, binary)
	app.Env = append(os.Environ(), "TAG_FILES_LISTEN="+address, "TAG_FILES_DATA_DIR="+dataDir, "TAG_FILES_SCRATCH_DIR="+scratchDir, "TAG_FILES_S3_ENDPOINT="+f.endpoint(), "TAG_FILES_ACCESS_KEY_FILE="+filepath.Join(root, "access"), "TAG_FILES_SECRET_KEY_FILE="+filepath.Join(root, "secret"), "TAG_FILES_SCRATCH_MAX_BYTES=68719476736", "TAG_FILES_MAX_OUTPUT_BYTES=21474836480", "TAG_FILES_MIN_FREE_BYTES=1073741824")
	app.Stdout, app.Stderr = logs, logs
	if e = app.Start(); e != nil {
		t.Fatal(e)
	}
	waited := make(chan error, 1)
	go func() { waited <- app.Wait() }()
	var stopped bool
	defer func() {
		if !stopped {
			_ = app.Process.Kill()
			<-waited
		}
		if t.Failed() {
			b, _ := os.ReadFile(logs.Name())
			t.Logf("app log:\n%s", b)
		}
	}()
	client := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 4}}
	defer client.CloseIdleConnections()
	deadline := time.Now().Add(30 * time.Second)
	for {
		response, e := client.Get(origin + "/health/ready")
		if e == nil {
			response.Body.Close()
			if response.StatusCode == 200 {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("large-upload app failed readiness")
		}
		time.Sleep(50 * time.Millisecond)
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(dataDir, "library.sqlite"), RawQuery: "mode=ro&_query_only=true"}
	db, e := sql.Open("sqlite3", u.String())
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var storageID, libraryID, volumeID string
	if e = db.QueryRowContext(ctx, `SELECT storage_id,library_id,storage_volume_id FROM library_meta WHERE singleton=1`).Scan(&storageID, &libraryID, &volumeID); e != nil {
		t.Fatal(e)
	}
	native, e := f.client().AtLibrary(storageID, libraryID, volumeID)
	if e != nil {
		t.Fatal(e)
	}
	initialNative, e := native.Capacity(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var minNative, minLocal, scratchPeak atomic.Int64
	minNative.Store(initialNative.AvailableBytes)
	initialLocal := int64(fs.Bavail) * filesystemBlockSize(fs)
	minLocal.Store(initialLocal)
	diskCtx, stopDisk := context.WithCancel(ctx)
	diskDone := make(chan struct{})
	go func() {
		defer close(diskDone)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-diskCtx.Done():
				return
			case <-ticker.C:
				capacity, e := native.Capacity(diskCtx)
				if e == nil {
					for old := minNative.Load(); capacity.AvailableBytes < old; old = minNative.Load() {
						if minNative.CompareAndSwap(old, capacity.AvailableBytes) {
							break
						}
					}
				}
				var scratchBytes int64
				_ = filepath.WalkDir(scratchDir, func(path string, entry os.DirEntry, err error) error {
					if err != nil {
						return nil
					}
					if entry.Type().IsRegular() {
						if info, err := entry.Info(); err == nil {
							scratchBytes += info.Size()
						}
					}
					return nil
				})
				for old := scratchPeak.Load(); scratchBytes > old; old = scratchPeak.Load() {
					if scratchPeak.CompareAndSwap(old, scratchBytes) {
						break
					}
				}
				var current syscall.Statfs_t
				if syscall.Statfs(root, &current) == nil {
					free := int64(current.Bavail) * filesystemBlockSize(current)
					for old := minLocal.Load(); free < old; old = minLocal.Load() {
						if minLocal.CompareAndSwap(old, free) {
							break
						}
					}
				}
			}
		}
	}()
	stopDiskOnce := sync.OnceFunc(func() { stopDisk(); <-diskDone })
	defer stopDiskOnce()
	idle, e := linuxRSS(app.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	var peak atomic.Int64
	peak.Store(idle)
	var samples atomic.Int64
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-monitorCtx.Done():
				return
			case <-ticker.C:
				r, e := linuxRSS(app.Process.Pid)
				if e == nil {
					samples.Add(1)
					for old := peak.Load(); r > old; old = peak.Load() {
						if peak.CompareAndSwap(old, r) {
							break
						}
					}
				}
			}
		}
	}()
	stopRSS := sync.OnceFunc(func() { stopMonitor(); <-monitorDone })
	defer stopRSS()
	limits := ingestion.DefaultLimits()
	if limits.MaxFileSize != 20<<30 || limits.PartSize != 32<<20 || limits.MaxParallelTransfers != 2 {
		t.Fatal("production upload limits changed")
	}
	partCount := int(limits.MaxFileSize / limits.PartSize)
	fullHash := sha256.New()
	digests := make([]string, partCount)
	generationStarted := time.Now()
	for number := 1; number <= partCount; number++ {
		sum := sha256.New()
		if _, e = io.CopyBuffer(io.MultiWriter(sum, fullHash), generatedPart(number, limits.PartSize), make([]byte, 128<<10)); e != nil {
			t.Fatal(e)
		}
		digests[number-1] = base64.StdEncoding.EncodeToString(sum.Sum(nil))
	}
	expectedSHA := hex.EncodeToString(fullHash.Sum(nil))
	t.Logf("generated identity for actual %d bytes/%d parts in %s", limits.MaxFileSize, partCount, time.Since(generationStarted))
	request := func(method, path string, body []byte, headers ...string) (int, []byte, error) {
		req, e := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(body))
		if e != nil {
			return 0, nil, e
		}
		req.Header.Set("Content-Type", "application/json")
		for i := 0; i < len(headers); i += 2 {
			req.Header.Set(headers[i], headers[i+1])
		}
		res, e := client.Do(req)
		if e != nil {
			return 0, nil, e
		}
		defer res.Body.Close()
		b, e := io.ReadAll(io.LimitReader(res.Body, 4<<20))
		return res.StatusCode, b, e
	}
	createdBody, _ := json.Marshal(map[string]any{"files": []map[string]any{{"clientFileId": domain.NewUUID(), "filename": "actual-20GiB-invalid-video.mp4", "size": limits.MaxFileSize, "fingerprint": expectedSHA}}})
	status, body, e := request("POST", "/api/v1/upload-sessions", createdBody, "Idempotency-Key", domain.NewUUID())
	if e != nil || status != 201 {
		t.Fatalf("create max upload: %d %s %v", status, body, e)
	}
	var created api.UploadsCreated
	if e = json.Unmarshal(body, &created); e != nil || len(created.Sessions) != 1 {
		t.Fatal(created, e)
	}
	session := created.Sessions[0]
	if created.MaxParallelTransfers != 2 || session.PartSize != limits.PartSize || session.PartCount != partCount || created.MaxFileSize != limits.MaxFileSize {
		t.Fatal("HTTP advertised nonproduction geometry", created)
	}
	uploadStarted := time.Now()
	jobs := make(chan int, partCount)
	for number := 1; number <= partCount; number++ {
		jobs <- number
	}
	close(jobs)
	parts := make([]api.UploadPart, partCount)
	errorsCh := make(chan error, 2)
	var workers sync.WaitGroup
	var transferred atomic.Int64
	var retryCount atomic.Int64
	var active, maxActive atomic.Int64
	for worker := 0; worker < 2; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for number := range jobs {
				n := active.Add(1)
				for old := maxActive.Load(); n > old; old = maxActive.Load() {
					if maxActive.CompareAndSwap(old, n) {
						break
					}
				}
				var acknowledgement []byte
				for attempt := 0; attempt < 10; attempt++ {
					req, e := http.NewRequestWithContext(ctx, "PUT", fmt.Sprintf("%s/api/v1/upload-sessions/%s/parts/%d", origin, session.Id.String(), number), generatedPart(number, limits.PartSize))
					if e != nil {
						active.Add(-1)
						errorsCh <- e
						return
					}
					req.ContentLength = limits.PartSize
					req.Header.Set("Content-Type", "application/octet-stream")
					req.Header.Set("X-Upload-Generation", strconv.FormatInt(session.Generation, 10))
					req.Header.Set("X-Part-SHA256", digests[number-1])
					res, e := client.Do(req)
					if e != nil {
						active.Add(-1)
						errorsCh <- fmt.Errorf("part %d: %w", number, e)
						return
					}
					b, readErr := io.ReadAll(io.LimitReader(res.Body, 1<<20))
					res.Body.Close()
					if readErr == nil && res.StatusCode == 503 && bytes.Contains(b, []byte(`"code":"upload_busy"`)) {
						retryCount.Add(1)
						time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
						continue
					}
					if readErr != nil || res.StatusCode != 200 {
						active.Add(-1)
						errorsCh <- fmt.Errorf("part %d: HTTP %d %s %v", number, res.StatusCode, b, readErr)
						return
					}
					acknowledgement = b
					break
				}
				active.Add(-1)
				if len(acknowledgement) == 0 {
					errorsCh <- fmt.Errorf("part %d remained busy after bounded retry", number)
					return
				}
				if decodeErr := json.Unmarshal(acknowledgement, &parts[number-1]); decodeErr != nil || parts[number-1].Etag == nil {
					errorsCh <- fmt.Errorf("part %d: missing acknowledgement %v", number, decodeErr)
					return
				}
				done := transferred.Add(limits.PartSize)
				if done%(1<<30) == 0 {
					t.Logf("actual HTTP/native MPU: %d/%d GiB, app RSS=%d MiB, elapsed=%s", done>>30, limits.MaxFileSize>>30, peak.Load()>>20, time.Since(uploadStarted))
				}
			}
		}()
	}
	workers.Wait()
	uploadDuration := time.Since(uploadStarted)
	close(errorsCh)
	for e := range errorsCh {
		t.Fatal(e)
	}
	if transferred.Load() != 20<<30 || maxActive.Load() != 2 {
		t.Fatal("full maximum/concurrency was not exercised")
	}
	// Independently inspect actual persisted bindings and the native MPU before
	// issuing Complete. No mocked objectstore, browser or direct-to-S3 upload.

	var incomingKey, mpuID string
	if e = db.QueryRowContext(ctx, `SELECT incoming_key,multipart_upload_id FROM upload_sessions WHERE id=?`, session.Id.String()).Scan(&incomingKey, &mpuID); e != nil {
		t.Fatal(e)
	}
	var confirmed int
	var bytesBound int64
	if e = db.QueryRowContext(ctx, `SELECT count(*),sum(expected_size) FROM upload_parts WHERE session_id=? AND state='confirmed'`, session.Id.String()).Scan(&confirmed, &bytesBound); e != nil || confirmed != 640 || bytesBound != 20<<30 {
		t.Fatalf("durable part inventory: %d %d %v", confirmed, bytesBound, e)
	}

	listed, e := native.ListParts(ctx, incomingKey, mpuID)
	if e != nil || len(listed) != 640 {
		t.Fatal("actual native MPU inventory", len(listed), e)
	}
	for idx, p := range listed {
		if p.Number != idx+1 || p.Size != limits.PartSize || p.ETag != *parts[idx].Etag {
			t.Fatal("native bound part differs", idx, p)
		}
	}
	selectors := make([]map[string]any, partCount)
	for idx, p := range parts {
		selectors[idx] = map[string]any{"partNumber": idx + 1, "etag": *p.Etag}
	}
	completeBody, _ := json.Marshal(map[string]any{"generation": session.Generation, "parts": selectors})
	status, body, e = request("POST", "/api/v1/upload-sessions/"+session.Id.String()+"/complete", completeBody, "Idempotency-Key", domain.NewUUID())
	if e != nil || status != 202 {
		t.Fatalf("Complete maximum: %d %s %v", status, body, e)
	}
	// Deliberately invalid media tests preservation after verified completion;
	// this gate does not claim that a valid 20 GiB movie was transcoded.
	completeStarted := time.Now()
	deadline = time.Now().Add(15 * time.Minute)
	var state, sourceSHA, errorCode string
	for {
		e = db.QueryRowContext(ctx, `SELECT state,coalesce(source_sha256,''),coalesce(error_code,'') FROM upload_sessions WHERE id=?`, session.Id.String()).Scan(&state, &sourceSHA, &errorCode)
		if e != nil {
			t.Fatal(e)
		}
		if state == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("20 GiB completion/media failure did not settle: state=%s sourceSHA=%s", state, sourceSHA)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if sourceSHA != expectedSHA {
		t.Fatalf("whole-source verification differs: got %s expected %s", sourceSHA, expectedSHA)
	}
	if _, e = native.Verify(ctx, incomingKey, 20<<30, expectedSHA); e != nil {
		t.Fatal("failed input was not retained with full bytes/hash", e)
	}
	status, body, e = request("GET", "/api/v1/upload-sessions/"+session.Id.String(), nil)
	if e != nil || status != 200 || !bytes.Contains(body, []byte(`"state":"failed"`)) {
		t.Fatalf("failed input not visible: %d %s %v", status, body, e)
	}
	// Explicit user cancellation removes visible work and independently cleans
	// the 20 GiB incoming key; no age-based expiration or source deletion.
	status, body, e = request("DELETE", "/api/v1/upload-sessions/"+session.Id.String(), nil, "Idempotency-Key", domain.NewUUID())
	if e != nil || status != 202 {
		t.Fatalf("explicit cancel: %d %s %v", status, body, e)
	}
	deadline = time.Now().Add(2 * time.Minute)
	for {
		_, e = native.Head(ctx, incomingKey)
		if e != nil {
			var wire *objectstore.Error
			if !errorsAsObjectNotFound(e, &wire) {
				t.Fatal(e)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("explicit cancel did not clean incoming bytes")
		}
		time.Sleep(200 * time.Millisecond)
	}
	var sessions, receipts, intents int
	if e = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM upload_sessions),(SELECT count(*) FROM cleanup_receipts),(SELECT count(*) FROM object_write_intents)`).Scan(&sessions, &receipts, &intents); e != nil {
		t.Fatal(e)
	}
	if sessions != 0 {
		t.Fatal("cancel left visible upload history")
	}
	for receipts != 0 || intents != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("independent cleanup did not retire: receipts=%d intents=%d", receipts, intents)
		}
		time.Sleep(100 * time.Millisecond)
		if e = db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM upload_sessions),(SELECT count(*) FROM cleanup_receipts),(SELECT count(*) FROM object_write_intents)`).Scan(&sessions, &receipts, &intents); e != nil {
			t.Fatal(e)
		}
	}
	stopDiskOnce()
	t.Logf("observed disk free-byte deltas (shared filesystems, not reservations): native initial=%d minimum=%d delta=%d; project initial=%d minimum=%d delta=%d; busyRetries=%d", initialNative.AvailableBytes, minNative.Load(), initialNative.AvailableBytes-minNative.Load(), initialLocal, minLocal.Load(), initialLocal-minLocal.Load(), retryCount.Load())
	t.Logf("observed scratch apparent-byte peak=%d", scratchPeak.Load())
	stopRSS()
	if peak.Load()-idle > 256<<20 {
		t.Fatalf("app RSS grew beyond constant 256 MiB budget: idle=%d peak=%d", idle, peak.Load())
	}
	if samples.Load() < 5 {
		t.Fatal("insufficient RSS samples")
	}
	t.Logf("20 GiB measured: transferred=%d bytes parts=%d concurrency=%d SHA256=%s idleRSS=%d peakRSS=%d samples=%d upload=%s complete/fail/hash/cancel=%s failure=%s pendingCleanup=%d pendingIntents=%d", transferred.Load(), partCount, maxActive.Load(), expectedSHA, idle, peak.Load(), samples.Load(), uploadDuration, time.Since(completeStarted), errorCode, receipts, intents)
	if e = app.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	select {
	case e = <-waited:
		stopped = true
		if e != nil {
			t.Fatal("app shutdown", e)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("app shutdown did not join")
	}
}

// generatedPart varies a bounded 32 KiB pattern by part number. It contains no
// valid media header and does not allocate proportionally to the file size.
func generatedPart(number int, size int64) io.Reader {
	pattern := make([]byte, 32<<10)
	for idx := range pattern {
		pattern[idx] = byte((idx*31 + number*17) % 251)
	}
	return io.LimitReader(&repeatPattern{pattern: pattern}, size)
}

type repeatPattern struct {
	pattern []byte
	offset  int
}

func (r *repeatPattern) Read(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		copied := copy(p, r.pattern[r.offset:])
		p = p[copied:]
		r.offset = (r.offset + copied) % len(r.pattern)
	}
	return n, nil
}
func linuxRSS(pid int) (int64, error) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if e != nil {
		return 0, e
	}
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && f[0] == "VmRSS:" {
			n, e := strconv.ParseInt(f[1], 10, 64)
			return n * 1024, e
		}
	}
	return 0, fmt.Errorf("VmRSS unavailable")
}
func errorsAsObjectNotFound(e error, out **objectstore.Error) bool {
	if !errors.As(e, out) {
		return false
	}
	return (*out).Status == 404 || (*out).Code == "NoSuchKey"
}

func filesystemBlockSize(fs syscall.Statfs_t) int64 {
	if fs.Frsize > 0 {
		return fs.Frsize
	}
	return fs.Bsize
}
