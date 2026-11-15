package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

// This is a bounded, opt-in acceptance probe, not a general benchmark. The
// observed server is a separate, normal production executable, never the race
// test process. The 10,000 catalog rows are SQL metadata fixtures; only the video
// uploaded through HTTP and its derivatives are real S3 objects.
func TestProductionCatalogPerformanceDuringSoftwareEncode(t *testing.T) {
	if os.Getenv("TAG_FILES_PERFORMANCE") != "1" {
		t.Skip("run scripts/check-performance for the real S3/FFmpeg performance probe")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("this probe observes Linux /proc process RSS and encode lifetimes")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp(os.Getenv("TAG_FILES_PERFORMANCE_WORK_DIR"), "tag-files-performance-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	version, err := exec.CommandContext(ctx, "ffmpeg", "-version").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("profile: %s/%s CPUs=%d; %s; storage=%s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), strings.SplitN(string(version), "\n", 2)[0], os.Getenv("TAG_FILES_STORAGE_IMAGE"))
	binary := filepath.Join(root, "tag-files")
	build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./cmd/tag-files")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build normal production executable: %v %s", err, output)
	}
	binaryBytes, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	binarySHA := sha256.Sum256(binaryBytes)
	t.Logf("normal executable SHA256=%x (race detector is only in the test/client)", binarySHA)
	dataDir, scratchDir := filepath.Join(root, "metadata"), filepath.Join(root, "scratch")
	seedPerformanceCatalog(t, ctx, dataDir)
	if err := os.Mkdir(scratchDir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"access": f.access, "secret": f.secret} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	origin := "http://" + address
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 12}}
	defer client.CloseIdleConnections()
	logs, err := os.Create(filepath.Join(root, "app.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logs.Close()
	defer func() {
		if t.Failed() {
			raw, _ := os.ReadFile(logs.Name())
			t.Logf("production app log:\n%s", raw)
		}
	}()
	var app *exec.Cmd
	var waited chan error
	stop := func(signal os.Signal) error {
		if app == nil || app.Process == nil {
			return nil
		}
		if err := app.Process.Signal(signal); err != nil {
			return err
		}
		select {
		case err := <-waited:
			app = nil
			if signal == syscall.SIGKILL {
				return nil
			}
			return err
		case <-time.After(15 * time.Second):
			return fmt.Errorf("production app did not stop")
		}
	}
	defer func() { _ = stop(syscall.SIGKILL) }()
	start := func() time.Duration {
		t.Helper()
		began := time.Now()
		app = exec.CommandContext(ctx, binary)
		app.Env = append(os.Environ(), "TAG_FILES_LISTEN="+address, "TAG_FILES_DATA_DIR="+dataDir, "TAG_FILES_SCRATCH_DIR="+scratchDir, "TAG_FILES_S3_ENDPOINT="+f.endpoint(), "TAG_FILES_ACCESS_KEY_FILE="+filepath.Join(root, "access"), "TAG_FILES_SECRET_KEY_FILE="+filepath.Join(root, "secret"))
		app.Stdout, app.Stderr = logs, logs
		if err := app.Start(); err != nil {
			t.Fatal(err)
		}
		waited = make(chan error, 1)
		command, done := app, waited
		go func() { done <- command.Wait() }()
		for time.Since(began) < 30*time.Second {
			response, err := client.Get(origin + "/health/ready")
			if err == nil {
				response.Body.Close()
				if response.StatusCode == 200 {
					return time.Since(began)
				}
			}
			select {
			case err := <-waited:
				app = nil
				t.Fatalf("production app exited at startup: %v", err)
			default:
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("production startup exceeded 30s")
		return 0
	}
	startup := start()
	request := func(method, path string, body []byte, headers ...string) ([]byte, time.Duration, error) {
		began := time.Now()
		req, err := http.NewRequestWithContext(ctx, method, origin+path, bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		for index := 0; index < len(headers); index += 2 {
			req.Header.Set(headers[index], headers[index+1])
		}
		response, err := client.Do(req)
		if err != nil {
			return nil, time.Since(began), err
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if err == nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
			err = fmt.Errorf("%s %s: HTTP%d %s", method, path, response.StatusCode, raw)
		}
		return raw, time.Since(began), err
	}
	paths := []string{
		"/api/v1/items?pageSize=24",
		"/api/v1/items?pageSize=24&q=" + url.QueryEscape("tag0001 || tag0002"),
		"/api/v1/items?pageSize=24&q=" + url.QueryEscape("(tag0001 || tag0002) && !tag0003"),
		"/api/v1/tags", "/api/v1/tag-statistics", "/api/v1/collections",
	}
	for _, path := range paths {
		raw, _, err := request("GET", path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(path, "/api/v1/items?") {
			var page api.ItemPage
			if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 24 || page.TotalItems < 24 || (path == paths[0] && page.TotalItems != 10000) {
				t.Fatalf("fixture did not yield 24 useful results from 10000 catalog rows: %s %v", raw, err)
			}
		} else {
			var rows []json.RawMessage
			want := 2000
			if path == "/api/v1/collections" {
				want = 350
			}
			if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != want {
				t.Fatalf("organization directory %s: got%d want%d: %v", path, len(rows), want, err)
			}
		}
	}
	for _, path := range paths {
		for range 5 {
			if _, _, err := request("GET", path, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, path := range paths {
		var durations []time.Duration
		for range 100 {
			_, elapsed, err := request("GET", path, nil)
			if err != nil {
				t.Fatal(err)
			}
			durations = append(durations, elapsed)
		}
		performanceLatency(t, "warm", path, durations, 0)
	}
	idle, err := linuxRSS(app.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if idle > 300<<20 {
		t.Fatalf("idle API RSS exceeds 300MiB: %d", idle)
	}
	t.Logf("startup ready=%s; warm idle API RSS=%d bytes", startup, idle)

	// Source generation is outside the measured encode window and uses two
	// software threads. The production worker will independently transcode it
	// with its unchanged veryfast/libx264, CRF23, two-thread default profile.
	source := filepath.Join(root, "source.mp4")
	generate := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30:duration=24", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "32", "-pix_fmt", "yuv420p", "-threads", "2", source)
	if output, err := generate.CombinedOutput(); err != nil {
		t.Fatalf("generate actual video: %v %s", err, output)
	}
	video, err := os.ReadFile(source)
	if err != nil || len(video) > 32<<20 {
		t.Fatalf("expected bounded one-part source, bytes=%d: %v", len(video), err)
	}
	sum := sha256.Sum256(video)
	createBody, _ := json.Marshal(map[string]any{"files": []map[string]any{{"clientFileId": domain.NewUUID(), "filename": "performance-1080p.mp4", "size": len(video), "fingerprint": hex.EncodeToString(sum[:])}}})
	raw, _, err := request("POST", "/api/v1/upload-sessions", createBody, "Idempotency-Key", domain.NewUUID())
	if err != nil {
		t.Fatal(err)
	}
	var created api.UploadsCreated
	if err := json.Unmarshal(raw, &created); err != nil || len(created.Sessions) != 1 {
		t.Fatal("create video session", string(raw), err)
	}
	session := created.Sessions[0]
	raw, _, err = request("PUT", "/api/v1/upload-sessions/"+session.Id.String()+"/parts/1", video, "Content-Type", "application/octet-stream", "X-Upload-Generation", strconv.FormatInt(session.Generation, 10), "X-Part-SHA256", base64.StdEncoding.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	var part api.UploadPart
	if err := json.Unmarshal(raw, &part); err != nil || part.Etag == nil {
		t.Fatal("upload real source", string(raw), err)
	}
	completeBody, _ := json.Marshal(map[string]any{"generation": session.Generation, "parts": []map[string]any{{"partNumber": 1, "etag": *part.Etag}}})
	completeKey := domain.NewUUID()
	if _, _, err := request("POST", "/api/v1/upload-sessions/"+session.Id.String()+"/complete", completeBody, "Idempotency-Key", completeKey); err != nil {
		t.Fatal(err)
	}
	encodePID := 0
	deadline := time.Now().Add(15 * time.Second)
	for encodePID == 0 && time.Now().Before(deadline) {
		encodePID = performanceEncodeChild(app.Process.Pid)
		time.Sleep(10 * time.Millisecond)
	}
	if encodePID == 0 {
		t.Fatal("no actual production libx264 encode child observed")
	}
	encodeBegan := time.Now()
	readURL := url.URL{Scheme: "file", Path: filepath.Join(dataDir, "library.sqlite"), RawQuery: "mode=ro&_query_only=true"}
	db, err := sql.Open("sqlite3", readURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var group sync.WaitGroup
	errors := make(chan error, len(paths)+2)
	for _, path := range paths {
		group.Go(func() {
			var durations []time.Duration
			overlap := 0
			for range 100 {
				active := performanceIsEncode(encodePID)
				_, elapsed, err := request("GET", path, nil)
				if err != nil {
					errors <- err
					return
				}
				durations = append(durations, elapsed)
				if active && performanceIsEncode(encodePID) {
					overlap++
				}
			}
			performanceLatency(t, "encode", path, durations, overlap)
		})
	}
	group.Go(func() {
		var durations []time.Duration
		overlap := 0
		for index := range 100 {
			version := index + 1
			body, _ := json.Marshal(map[string]any{"items": []map[string]int{{"id": 1, "version": version}, {"id": 2, "version": version}}, "tagIds": []int{1 + index%2*2, 2 + index%2*2}, "collectionId": 1 + index%2})
			active := performanceIsEncode(encodePID)
			_, elapsed, err := request("POST", "/api/v1/items/bulk-metadata", body, "Idempotency-Key", domain.NewUUID())
			if err != nil {
				errors <- err
				return
			}
			durations = append(durations, elapsed)
			if active && performanceIsEncode(encodePID) {
				overlap++
			}
		}
		performanceLatency(t, "encode", "atomic two-item metadata write", durations, overlap)
	})
	group.Go(func() {
		// A single SQLite statement sees both rows and their tag sets in the
		// same WAL snapshot while the HTTP bulk writer atomically replaces them.
		seenVersions := make(map[int]bool)
		for range 100 {
			rows, err := db.QueryContext(ctx, `SELECT version,collection_id,(SELECT group_concat(tag_id,',') FROM (SELECT tag_id FROM item_tags WHERE item_id=i.id ORDER BY tag_id)) FROM items i WHERE id IN (1,2) ORDER BY id`)
			if err != nil {
				errors <- err
				return
			}
			var last string
			count := 0
			for rows.Next() {
				var version, collection int
				var tags string
				if err := rows.Scan(&version, &collection, &tags); err != nil {
					rows.Close()
					errors <- err
					return
				}
				seenVersions[version] = true
				value := fmt.Sprintf("%d/%d/%s", version, collection, tags)
				if (collection == 1 && tags != "1,2") || (collection == 2 && tags != "3,4") || (collection != 1 && collection != 2) || (count > 0 && last != value) {
					rows.Close()
					errors <- fmt.Errorf("torn atomic metadata snapshot: %q versus %q", last, value)
					return
				}
				last = value
				count++
			}
			if err := rows.Err(); err != nil || count != 2 {
				rows.Close()
				errors <- fmt.Errorf("atomic metadata snapshot rows=%d: %v", count, err)
				return
			}
			rows.Close()
			time.Sleep(5 * time.Millisecond)
		}
		if len(seenVersions) < 2 {
			errors <- fmt.Errorf("atomic snapshot reader did not observe concurrent writer progress")
		}
	})
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	if t.Failed() {
		t.FailNow()
	}
	if !performanceIsEncode(encodePID) {
		t.Fatal("encode finished before controlled interruption; recovery was not exercised")
	}
	encodeRSS, err := linuxRSS(encodePID)
	if err != nil {
		t.Fatal(err)
	}
	apiRSS, err := linuxRSS(app.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual source bytes=%d SHA256=%x; encode overlap=%s; observed API RSS=%d FFmpeg RSS=%d (separate processes)", len(video), sum, time.Since(encodeBegan), apiRSS, encodeRSS)
	if err := stop(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	restart := start()
	recoveredBegan := time.Now()
	var ready api.UploadOutcome
	for time.Since(recoveredBegan) < 30*time.Second {
		raw, _, err := request("GET", "/api/v1/upload-sessions/"+session.Id.String(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &ready); err != nil {
			t.Fatal(err)
		}
		if ready.State == "ready" {
			break
		}
		if ready.State == "failed" {
			t.Fatalf("interrupted real encode failed recovery: %s", raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if ready.State != "ready" || ready.ItemId == nil {
		t.Fatal("interrupted single-item queue did not publish within 30s of readiness")
	}
	var operation api.Operation
	raw, _, err = request("GET", "/api/v1/operations/"+completeKey, nil)
	if err != nil || json.Unmarshal(raw, &operation) != nil || operation.State != "completed" {
		t.Fatalf("Complete receipt not resolved after restart: %s %v", raw, err)
	}
	var page api.ItemPage
	raw, _, err = request("GET", "/api/v1/items?pageSize=24", nil)
	if err != nil || json.Unmarshal(raw, &page) != nil || page.TotalItems != 10001 {
		t.Fatalf("catalog not retained after restart: %s %v", raw, err)
	}
	var item api.Item
	raw, _, err = request("GET", fmt.Sprintf("/api/v1/items/%d", *ready.ItemId), nil)
	if err != nil || json.Unmarshal(raw, &item) != nil || item.ContentUrl == nil {
		t.Fatalf("real video not published: %s %v", raw, err)
	}
	mediaResponse, err := client.Get(origin + *item.ContentUrl)
	if err != nil {
		t.Fatal(err)
	}
	actualBytes, copyErr := io.Copy(io.Discard, mediaResponse.Body)
	mediaResponse.Body.Close()
	if copyErr != nil || mediaResponse.StatusCode != 200 || actualBytes == 0 {
		t.Fatal("actual S3 canonical content unreadable", actualBytes, copyErr)
	}
	var version1, version2 int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT version FROM items WHERE id=1),(SELECT version FROM items WHERE id=2)`).Scan(&version1, &version2); err != nil || version1 != 101 || version2 != 101 {
		t.Fatal("metadata writes lost across abrupt restart", version1, version2, err)
	}
	t.Logf("recovery: restart ready=%s; interrupted video ready after readiness=%s; canonical GET=%d actual bytes; 100 committed two-item writes retained; 10001 catalog rows", restart, time.Since(recoveredBegan), actualBytes)
	if err := stop(syscall.SIGTERM); err != nil {
		t.Fatal("graceful final shutdown", err)
	}
}

func seedPerformanceCatalog(t *testing.T, ctx context.Context, directory string) {
	t.Helper()
	db, err := store.Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	err = db.Write(ctx, func(tx *sql.Tx) error {
		for id := 1; id <= 2000; id++ {
			if _, err := tx.ExecContext(ctx, `INSERT INTO tags(id,name) VALUES(?,?)`, id, fmt.Sprintf("tag%04d", id)); err != nil {
				return err
			}
		}
		for id := 1; id <= 350; id++ {
			if _, err := tx.ExecContext(ctx, `INSERT INTO collections(id,name) VALUES(?,?)`, id, fmt.Sprintf("Collection%03d", id)); err != nil {
				return err
			}
		}
		for id := 1; id <= 10000; id++ {
			// These ready rows exercise the real catalog predicate and joins.
			// Their zero-byte descriptors are SQL-only, never claimed as S3
			// objects or fetched as media by this metadata latency probe.
			collection := 1 + id%350
			tags := make([]int, 10)
			if id >= 3 && id <= 18 {
				tags = make([]int, 11)
			}
			for index := range tags {
				tags[index] = 1 + (id*7+index*137)%2000
			}
			if id <= 2 {
				collection, tags = 1, []int{1, 2}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO items(id,media_kind,uploaded_at_utc,state,collection_id) VALUES(?,'Image',?,'importing',?)`, id, fmt.Sprintf("2026-11-%02dT00:00:00.0000000Z", 1+id%28), collection); err != nil {
				return err
			}
			asset := fmt.Sprintf("performance-metadata-only-%d", id)
			if _, err := tx.ExecContext(ctx, `INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,width,height,provenance,state) VALUES(?,?,'primary','tag-files',?,0,?,'image/png',32,24,'performance-metadata-only','verified')`, asset, id, asset, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE items SET primary_asset_id=?,state='ready' WHERE id=?`, asset, id); err != nil {
				return err
			}
			for _, tag := range tags {
				if _, err := tx.ExecContext(ctx, `INSERT INTO item_tags VALUES(?,?)`, id, tag); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var items, tags, links, collections int
	if err := db.Reader().QueryRowContext(ctx, `SELECT (SELECT count(*) FROM items),(SELECT count(*) FROM tags),(SELECT count(*) FROM item_tags),(SELECT count(*) FROM collections)`).Scan(&items, &tags, &links, &collections); err != nil || items != 10000 || tags != 2000 || links != 100000 || collections != 350 {
		t.Fatalf("growth SQL profile differs: items=%d tags=%d links=%d collections=%d: %v", items, tags, links, collections, err)
	}
	t.Log("SQL fixture: 10000 ready metadata items, 10000 unwritten zero-byte asset descriptors, 2000 tags, 100000 item-tag links, 350 collections; zero actual S3 objects/bytes synthesized")
}

func performanceLatency(t *testing.T, phase, name string, durations []time.Duration, overlap int) {
	t.Helper()
	slices.Sort(durations)
	median, p95, maximum := durations[len(durations)/2], durations[(len(durations)*95+99)/100-1], durations[len(durations)-1]
	t.Logf("%s %s: n=%d overlap=%d median=%s p95=%s max=%s", phase, name, len(durations), overlap, median, p95, maximum)
	if phase == "encode" && overlap < 20 {
		t.Errorf("%s: fewer than 20 complete requests overlapped the actual encode", name)
	}
	if maximum > 2*time.Second {
		t.Errorf("%s %s exceeded 2s warm API-result budget: %s", phase, name, maximum)
	}
	if strings.Contains(name, "&q=") && p95 > 300*time.Millisecond {
		t.Errorf("%s search p95 exceeded 300ms budget: %s", phase, p95)
	}
}

func performanceIsEncode(pid int) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	return err == nil && bytes.Contains(raw, []byte("\x00libx264\x00"))
}

func performanceEncodeChild(parent int) int {
	tasks, _ := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", parent))
	for _, task := range tasks {
		raw, _ := os.ReadFile(task)
		for _, id := range strings.Fields(string(raw)) {
			pid, _ := strconv.Atoi(id)
			if performanceIsEncode(pid) {
				return pid
			}
		}
	}
	return 0
}
