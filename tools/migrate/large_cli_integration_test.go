package migrate

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
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

const largeCLIStorageImage = "sha256:70aa8659fd1622ffce9c39a65024c83b3619c248eb8a71c55ccc6af6f0e17ea5"
const largeCLIBytes int64 = 2 << 30
const largeCLIPartBytes int64 = 16 << 20
const largeCLIRSSLimit int64 = 256 << 20 // Harness regression bound, not a product memory budget.

func largeJSON(t testing.TB, path string, value any) {
	t.Helper()
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func largeCopy(t testing.TB, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer dst.Close()
	if _, err = io.CopyBuffer(dst, struct{ io.Reader }{src}, make([]byte, 128<<10)); err != nil {
		t.Fatal(err)
	}
	if err = dst.Sync(); err != nil {
		t.Fatal(err)
	}
}

func largeFileHash(t testing.TB, path string) (int64, string) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, struct{ io.Reader }{f}, make([]byte, 128<<10))
	if err != nil {
		t.Fatal(err)
	}
	return n, hex.EncodeToString(h.Sum(nil))
}

// A sparse local seed avoids a 2 GiB RAM allocation. Filesystem sparsity is
// measured, not assumed. Every transfer, archive and verification reads all
// actual bytes; each part has distinct position markers.
func largeSeed(t *testing.T, path string) (string, map[int]string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err = f.Truncate(largeCLIBytes); err != nil {
		t.Fatal(err)
	}
	for offset := int64(0); offset < largeCLIBytes; offset += largeCLIPartBytes {
		marker := []byte(fmt.Sprintf("tag-files-large-cli-offset-%020d", offset))
		for _, position := range []int64{offset, offset + largeCLIPartBytes - int64(len(marker))} {
			if _, err = f.WriteAt(marker, position); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = f.Sync(); err != nil {
		t.Fatal(err)
	}
	whole := sha256.New()
	parts := map[int]string{}
	buffer := make([]byte, 128<<10)
	for number := 1; number <= int(largeCLIBytes/largeCLIPartBytes); number++ {
		part := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(whole, part), io.NewSectionReader(f, int64(number-1)*largeCLIPartBytes, largeCLIPartBytes), buffer)
		if err != nil || n != largeCLIPartBytes {
			t.Fatalf("seed part %d: bytes=%d error=%v", number, n, err)
		}
		parts[number] = base64.StdEncoding.EncodeToString(part.Sum(nil))
	}
	return hex.EncodeToString(whole.Sum(nil)), parts
}

type largeTarget struct{ name, endpoint, access, secret string }

func largeStartTarget(t *testing.T, work string) largeTarget {
	t.Helper()
	image := os.Getenv("TAG_FILES_MIGRATE_STORAGE_IMAGE")
	if image == "" {
		image = largeCLIStorageImage
	}
	if actual := testutil.Docker(t, "", "image", "inspect", "--format", "{{.Id}}", image); actual != largeCLIStorageImage {
		t.Fatalf("large CLI target must be frozen image %s, got %s", largeCLIStorageImage, actual)
	}
	f := largeTarget{name: "tag-files-migrate-large-" + domain.NewUUID(), access: domain.NewUUID(), secret: domain.NewUUID() + domain.NewUUID()}
	testutil.Docker(t, "", "volume", "create", f.name)
	t.Cleanup(func() { testutil.Docker(t, "", "volume", "rm", f.name) })
	testutil.Docker(t, "", "create", "--name", f.name, "--mount", "type=volume,src="+f.name+",dst=/data", "-p", "127.0.0.1::8333", largeCLIStorageImage)
	t.Cleanup(func() { testutil.Docker(t, "", "rm", "-f", f.name) })
	for filename, value := range map[string]string{"s3_access_key": f.access, "s3_secret_key": f.secret} {
		path := filepath.Join(work, filename)
		if err := os.WriteFile(path, []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		testutil.Docker(t, "", "cp", path, f.name+":/run/secrets/"+filename)
	}
	testutil.Docker(t, "", "start", f.name)
	deadline := time.Now().Add(90 * time.Second)
	for {
		state := testutil.Docker(t, "", "inspect", "--format", "{{.State.Running}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", f.name)
		if state == "true healthy" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("large CLI target did not become healthy")
		}
		time.Sleep(200 * time.Millisecond)
	}
	f.endpoint = "http://" + testutil.Docker(t, "", "port", f.name, "8333/tcp")
	return f
}

func (f largeTarget) client(t *testing.T) *objectstore.Client {
	t.Helper()
	control, err := storageprotocol.NewClient(f.endpoint, f.access, f.secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := control.Identity(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c, err := objectstore.New(objectstore.Config{Endpoint: f.endpoint, Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: &http.Client{Timeout: 10 * time.Minute}})
	if err == nil {
		c, err = c.AtLibrary(id.StorageID, id.LibraryID, id.VolumeID)
	}
	if err == nil {
		c, err = c.AtEpoch(id.Epoch)
	}
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The proxy preserves Host, Authorization, storage epoch/pair headers, bodies
// and response proofs. Only the fourth UploadPart has a test-controlled gate.
type largeObservation struct {
	mu       sync.Mutex
	log      *json.Encoder
	file     *os.File
	counts   map[int]int
	gates    int
	released [2]sync.Once
	writeErr error
	blocked  chan int
	releases [2]chan struct{}
}

func largeObserver(t *testing.T, root string) *largeObservation {
	t.Helper()
	f, err := os.Create(filepath.Join(root, "http.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	o := &largeObservation{file: f, log: json.NewEncoder(f), counts: map[int]int{}, blocked: make(chan int, 2), releases: [2]chan struct{}{make(chan struct{}), make(chan struct{})}}
	t.Cleanup(func() { f.Close() })
	return o
}

func (o *largeObservation) record(v any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.log.Encode(v); err != nil && o.writeErr == nil {
		o.writeErr = err
	}
}

func (o *largeObservation) release(gate int) { o.released[gate].Do(func() { close(o.releases[gate]) }) }

func (o *largeObservation) proxy(t *testing.T, endpoint, side string) *httptest.Server {
	t.Helper()
	u, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	p.Transport = &http.Transport{Proxy: nil, MaxIdleConnsPerHost: 8, ResponseHeaderTimeout: 10 * time.Minute}
	p.ModifyResponse = func(r *http.Response) error {
		o.record(map[string]any{"time": now(), "side": side, "event": "response", "method": r.Request.Method, "path": r.Request.URL.Path, "part": r.Request.URL.Query().Get("partNumber"), "status": r.StatusCode})
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if side == "source" && r.Method != http.MethodGet && r.Method != http.MethodHead {
			o.record(map[string]any{"time": now(), "side": side, "event": "denied", "method": r.Method, "status": 403})
			w.WriteHeader(http.StatusForbidden)
			return
		}
		number, _ := strconv.Atoi(r.URL.Query().Get("partNumber"))
		if side == "target" && r.Method == http.MethodPut && number != 0 {
			o.mu.Lock()
			gate := -1
			if number == 4 && o.gates < 2 {
				gate = o.gates
				o.gates++
			}
			o.mu.Unlock()
			if gate >= 0 {
				o.record(map[string]any{"time": now(), "side": side, "event": "held_before_dispatch", "part": number, "gate": gate})
				o.blocked <- gate
				select {
				case <-o.releases[gate]:
				case <-r.Context().Done():
					return
				}
				if gate == 0 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
			}
			o.mu.Lock()
			o.counts[number]++
			o.mu.Unlock()
		}
		o.record(map[string]any{"time": now(), "side": side, "event": "forwarded", "method": r.Method, "path": r.URL.Path, "part": number})
		p.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
	return server
}

type largeStage struct {
	Name            string  `json:"name"`
	PID             int     `json:"pid"`
	Seconds         float64 `json:"seconds"`
	ExitCode        int     `json:"exitCode"`
	Signal          string  `json:"signal,omitempty"`
	SampleCount     int     `json:"sampleCount"`
	SampledPeakRSS  int64   `json:"sampledPeakRSSBytes"`
	ProcessPeakRSS  int64   `json:"processPeakRSSBytes"`
	RegressionBound int64   `json:"regressionBoundBytes"`
}

type largeProcess struct {
	cmd    *exec.Cmd
	done   chan struct{}
	result largeStage
	err    error
}

func largeStartCLI(t *testing.T, root, binary, config, stage, command string) *largeProcess {
	t.Helper()
	stdout, err := os.Create(filepath.Join(root, stage+".stdout"))
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(filepath.Join(root, stage+".stderr"))
	if err != nil {
		t.Fatal(err)
	}
	rss, err := os.Create(filepath.Join(root, stage+".rss.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, 20*time.Minute)
	p := &largeProcess{cmd: exec.CommandContext(deadline, binary, command, "-config", config), done: make(chan struct{})}
	p.cmd.Stdout, p.cmd.Stderr = stdout, stderr
	start := time.Now()
	if err = p.cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	p.result = largeStage{Name: stage, PID: p.cmd.Process.Pid, RegressionBound: largeCLIRSSLimit}
	go func() {
		defer close(p.done)
		defer cancel()
		defer stdout.Close()
		defer stderr.Close()
		defer rss.Close()
		stop := make(chan struct{})
		sampled := make(chan struct{})
		go func() {
			defer close(sampled)
			enc := json.NewEncoder(rss)
			tick := time.NewTicker(20 * time.Millisecond)
			defer tick.Stop()
			for {
				b, readErr := os.ReadFile(fmt.Sprintf("/proc/%d/status", p.cmd.Process.Pid))
				if readErr == nil {
					var resident, high int64
					for _, line := range strings.Split(string(b), "\n") {
						fields := strings.Fields(line)
						if len(fields) >= 2 && (fields[0] == "VmRSS:" || fields[0] == "VmHWM:") {
							n, _ := strconv.ParseInt(fields[1], 10, 64)
							if fields[0] == "VmRSS:" {
								resident = n * 1024
								p.result.SampledPeakRSS = max(p.result.SampledPeakRSS, resident)
							} else {
								high = n * 1024
								p.result.SampledPeakRSS = max(p.result.SampledPeakRSS, high)
							}
						}
					}
					p.result.SampleCount++
					_ = enc.Encode(map[string]any{"elapsedSeconds": time.Since(start).Seconds(), "pid": p.cmd.Process.Pid, "rssBytes": resident, "highWaterBytes": high})
				}
				select {
				case <-stop:
					return
				case <-tick.C:
				}
			}
		}()
		p.err = p.cmd.Wait()
		close(stop)
		<-sampled
		p.result.Seconds = time.Since(start).Seconds()
		p.result.ExitCode = p.cmd.ProcessState.ExitCode()
		if usage, ok := p.cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			p.result.ProcessPeakRSS = usage.Maxrss * 1024 // This opt-in test requires Linux.
		}
		if status, ok := p.cmd.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			p.result.Signal = status.Signal().String()
		}
	}()
	t.Cleanup(func() {
		select {
		case <-p.done:
		default:
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
	return p
}

func largeWaitCLI(t *testing.T, root string, p *largeProcess, killed bool) largeStage {
	t.Helper()
	<-p.done
	largeJSON(t, filepath.Join(root, p.result.Name+".json"), p.result)
	if killed {
		if p.err == nil || p.result.Signal != "killed" {
			t.Fatalf("first copy did not end by SIGKILL: %+v error=%v", p.result, p.err)
		}
	} else if p.err != nil {
		b, _ := os.ReadFile(filepath.Join(root, p.result.Name+".stderr"))
		t.Fatalf("CLI %s failed: %v\n%s", p.result.Name, p.err, b)
	}
	if p.result.SampleCount == 0 || max(p.result.SampledPeakRSS, p.result.ProcessPeakRSS) > largeCLIRSSLimit {
		t.Fatalf("CLI RSS regression bound exceeded/no samples: %+v", p.result)
	}
	t.Logf("CLI %s: %.3fs exit=%d signal=%s peakRSS=%d samples=%d", p.result.Name, p.result.Seconds, p.result.ExitCode, p.result.Signal, max(p.result.SampledPeakRSS, p.result.ProcessPeakRSS), p.result.SampleCount)
	return p.result
}

func largeReadDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_query_only=true&_busy_timeout=5000"}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type largeCheckpoint struct {
	ObjectID, Key, UploadID, Epoch string
	PartSize                       int64
	Parts                          []objectstore.Part
	Native                         []objectstore.Part
}

func largeInspectCheckpoint(t *testing.T, path string, target largeTarget, expected map[int]string) largeCheckpoint {
	t.Helper()
	db := largeReadDB(t, path)
	defer db.Close()
	var c largeCheckpoint
	if err := db.QueryRow(`SELECT o.id,o.object_key,m.upload_id,m.epoch,m.part_size FROM multipart m JOIN objects o ON o.id=m.object_id WHERE o.size=?`, largeCLIBytes).Scan(&c.ObjectID, &c.Key, &c.UploadID, &c.Epoch, &c.PartSize); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT p.number,p.size,p.sha256,p.etag FROM parts p JOIN part_acknowledgements a ON p.object_id=a.object_id AND p.number=a.number WHERE p.object_id=? AND a.upload_id=? AND p.size=a.size AND p.sha256=a.sha256 AND p.etag=a.etag ORDER BY p.number`, c.ObjectID, c.UploadID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var p objectstore.Part
		if err = rows.Scan(&p.Number, &p.Size, &p.SHA256, &p.ETag); err != nil {
			t.Fatal(err)
		}
		if p.Size != largeCLIPartBytes || p.SHA256 != expected[p.Number] || p.ETag == "" {
			t.Fatalf("durable ACK differs from independently hashed source part: %+v", p)
		}
		c.Parts = append(c.Parts, p)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if c.PartSize != largeCLIPartBytes || len(c.Parts) != 3 {
		t.Fatalf("expected exactly three durable ACKs before dispatch 4: %+v", c)
	}
	c.Native, err = target.client(t).ListParts(ctx, c.Key, c.UploadID)
	if err != nil || !reflect.DeepEqual(c.Parts, c.Native) {
		t.Fatalf("authoritative native parts differ from immutable local ACK/bindings: local=%+v native=%+v error=%v", c.Parts, c.Native, err)
	}
	return c
}

func largeAwaitGate(t *testing.T, observer *largeObservation, process *largeProcess, expected int) {
	t.Helper()
	select {
	case gate := <-observer.blocked:
		if gate != expected {
			t.Fatalf("wrong dispatch gate %d, expected %d", gate, expected)
		}
	case <-process.done:
		t.Fatalf("copy exited before multipart gate: %v", process.err)
	case <-time.After(12 * time.Minute):
		t.Fatal("copy did not reach durable multipart checkpoint")
	}
}

func largeCatalog(t *testing.T, path, evidence string) {
	t.Helper()
	db := largeReadDB(t, path)
	defer db.Close()
	// Independently authored expected projections from legacyFixture, without
	// using the importer, archive model, makePlans or verifyCatalog as the oracle.
	tables := []struct {
		name, query string
		want        [][]any
	}{
		{"items", `SELECT id,media_kind,description,uploaded_at_utc,collection_id,is_favorite,view_count,last_view_at_utc,duration_ticks,state FROM items ORDER BY id`, [][]any{
			{11, "Unknown", nil, "2025-06-22T20:09:14.1234567Z", 7, 0, 0, nil, nil, "ready"},
			{12, "Text", "", "2025-06-23T20:09:14.1234567Z", nil, 1, 9, "2025-06-24T00:00:00.0000000Z", 0, "ready"},
			{13, "Image", "猫", "2025-06-24T20:09:14.0000000Z", 9, 0, 3, nil, int64(9223372036854775807), "ready"},
			{14, "Video", nil, "2025-06-25T20:09:14.0000000Z", 7, 1, 4, nil, int64(-9223372036854775808), "ready"},
			{15, "Audio", "audio", "2025-06-26T20:09:14.0000000Z", nil, 0, 7, nil, int64(9007199254740993), "ready"},
		}},
		{"tags", `SELECT id,name FROM tags ORDER BY id`, [][]any{{1, "猫"}, {2, "猫"}, {3, "Cat"}, {4, "cat"}, {5, "unused"}}},
		{"collections", `SELECT id,name FROM collections ORDER BY id`, [][]any{{7, "同じ"}, {9, "同じ"}}},
		{"joins", `SELECT item_id,tag_id FROM item_tags ORDER BY item_id,tag_id`, [][]any{{11, 1}, {11, 2}, {12, 3}, {13, 4}, {14, 2}, {15, 1}}},
		{"categories", `SELECT id,name,query_text,legacy_source_text,media_kind FROM saved_searches ORDER BY id`, [][]any{{3, "duplicates", "Cat || cat", "Cat || cat", nil}, {4, "all", "", nil, nil}, {5, "case", "Cat || cat", "Cat || cat", "Text"}, {6, "negative", "!unused", "!unused", nil}}},
	}
	observed := map[string]any{}
	for _, table := range tables {
		rows, err := db.Query(table.query)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		got := [][]any{}
		for rows.Next() {
			values := make([]any, len(columns))
			args := make([]any, len(columns))
			for i := range values {
				args[i] = &values[i]
			}
			if err = rows.Scan(args...); err != nil {
				t.Fatal(err)
			}
			got = append(got, values)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		wantJSON, _ := json.Marshal(table.want)
		gotJSON, _ := json.Marshal(got)
		if !bytes.Equal(wantJSON, gotJSON) {
			t.Fatalf("independent %s projection differs: got %s want %s", table.name, gotJSON, wantJSON)
		}
		observed[table.name] = map[string]any{"expected": table.want, "observed": got}
	}
	largeJSON(t, evidence, observed)
}

func TestLargeLegacyMigrationCLIResumeAndRSS(t *testing.T) {
	if os.Getenv("TAG_FILES_MIGRATE_LARGE_INTEGRATION") != "1" {
		t.Skip("set TAG_FILES_MIGRATE_LARGE_INTEGRATION=1; see scripts/check-migration-large")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("large CLI RSS rehearsal requires Linux /proc and Linux rusage units")
	}
	started := time.Now()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(repo, ".performance.migration-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("retained large migration evidence: %s", root)
	work := filepath.Join(root, "work")
	for _, dir := range []string{"source", "seed", "tmp", "journal", "target"} {
		if err = os.MkdirAll(filepath.Join(work, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", filepath.Join(work, "tmp"))
	t.Setenv("TAG_FILES_INTEGRATION", "1")
	evidence := map[string]any{"status": "running", "startedAt": now(), "sourceBackend": testutil.SeaweedImage, "targetImage": largeCLIStorageImage, "largeObjectBytes": largeCLIBytes, "partBytes": largeCLIPartBytes, "expectedParts": 128, "rssRegressionBoundBytes": largeCLIRSSLimit, "host": runtime.GOOS + "/" + runtime.GOARCH, "limits": "synthetic sparse-marker bytes; separate native SeaweedFS source, not actual MinIO; no video encoding, real library, 100GB sizing, Mac or physical durability claim"}
	defer func() {
		evidence["seconds"] = time.Since(started).Seconds()
		evidence["finishedAt"] = now()
		if t.Failed() {
			evidence["status"] = "failed"
		}
		largeJSON(t, filepath.Join(root, "evidence.json"), evidence)
	}()
	t.Cleanup(func() {
		for _, path := range []string{filepath.Join(work, "source"), filepath.Join(work, "source", "legacy.sqlite")} {
			if err := os.Chmod(path, 0700); err != nil && !os.IsNotExist(err) {
				t.Errorf("restore own fixture permissions for cleanup: %v", err)
			}
		}
		removeErr := os.RemoveAll(work)
		if removeErr != nil {
			t.Errorf("remove only owned work/secrets: %v", removeErr)
		}
		absent := map[string]bool{}
		if resources, ok := evidence["ownedResources"].(map[string]string); ok {
			for key, name := range resources {
				kind := "volume"
				if strings.HasSuffix(key, "Container") {
					kind = "container"
				}
				err := exec.Command("docker", kind, "inspect", name).Run()
				absent[kind+":"+name] = err != nil
				if err == nil {
					t.Errorf("owned %s still exists: %s", kind, name)
				}
			}
		}
		largeJSON(t, filepath.Join(root, "cleanup.json"), map[string]any{"at": now(), "workAndCredentialsRemoved": removeErr == nil, "absentOwnedResources": absent})
	})
	binary := filepath.Join(root, "tag-files-migrate")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/tag-files-migrate")
	build.Env = append([]string{}, os.Environ()...)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build normal CLI: %v\n%s", err, out)
	}
	_, binarySHA := largeFileHash(t, binary)
	evidence["binarySHA256"] = binarySHA
	for _, command := range []struct {
		name string
		args []string
	}{{"build-version.txt", []string{"go", "version", "-m", binary}}, {"source-head.txt", []string{"git", "rev-parse", "HEAD"}}, {"source-status.txt", []string{"git", "status", "--short"}}} {
		out, err := exec.Command(command.args[0], command.args[1:]...).CombinedOutput()
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(root, command.name), out, 0600); err != nil {
			t.Fatal(err)
		}
	}
	largePath := filepath.Join(work, "seed", "video.mp4")
	largeSHA, partHashes := largeSeed(t, largePath)
	evidence["largeObjectSHA256"] = largeSHA
	largeJSON(t, filepath.Join(root, "expected-part-sha256.json"), partHashes)
	raw := testutil.StartStorage(t)
	target := largeStartTarget(t, work)
	evidence["sourceNativeImageID"] = testutil.Docker(t, "", "inspect", "--format", "{{.Image}}", raw.Container)
	evidence["ownedResources"] = map[string]string{"sourceContainer": raw.Container, "sourceVolume": raw.Volume, "targetContainer": target.name, "targetVolume": target.name}
	disk := []map[string]any{}
	measureDisk := func(stage string) {
		var stat syscall.Statfs_t
		if err := syscall.Statfs(root, &stat); err != nil {
			t.Fatal(err)
		}
		entry := map[string]any{"stage": stage, "at": now(), "projectAvailableBytes": stat.Bavail * uint64(stat.Frsize), "sourceNativeAllocatedKiB": testutil.Docker(t, "", "exec", raw.Container, "du", "-sk", "/data"), "targetNativeAllocatedKiB": testutil.Docker(t, "", "exec", target.name, "du", "-sk", "/data"), "hostWorkAllocatedKiB": testutil.Docker(t, "", "run", "--rm", "--network", "none", "--mount", "type=bind,src="+work+",dst=/fixture,readonly", "--entrypoint", "/bin/busybox", largeCLIStorageImage, "du", "-sk", "/fixture")}
		disk = append(disk, entry)
		largeJSON(t, filepath.Join(root, "disk.json"), disk)
	}
	measureDisk("empty_backends_sparse_seed")
	// Deliberately replace StartStorage.S3's five-second HTTP client. Both the
	// source setup and full independent reads have bounded ten-minute clients.
	sdk := s3.NewFromConfig(aws.Config{Region: "us-east-1", HTTPClient: &http.Client{Timeout: 10 * time.Minute}, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: raw.AccessKey, SecretAccessKey: raw.SecretKey}, nil
	})}, func(o *s3.Options) {
		o.BaseEndpoint, o.UsePathStyle = aws.String(raw.Endpoint), true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	for _, bucket := range []string{"library", "temporary", "thumbnail", "history"} {
		if _, err = sdk.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
			t.Fatal(err)
		}
	}
	type expectedObject struct {
		Size                int64
		SHA256, ContentType string
		Metadata, Tags      map[string]string
	}
	expected := map[string]expectedObject{}
	fixture := fixtureSource()
	for _, o := range fixture.objects {
		// SeaweedFS infers extension MIME for generic octet-stream uploads.
		// Seed explicit legacy types so exact source/target preservation has
		// an independently known expectation, including an opaque temporary.
		if ct := map[string]string{"unknown.bin": "application/x-tag-files-rehearsal", "text.txt": "text/plain", "image.jpg": "image/jpeg", "video.mp4": "video/mp4", "audio.aac": "audio/aac"}[o.Key]; ct != "" {
			o.ContentType = ct
		} else if o.ContentType == "" {
			o.ContentType = "application/x-tag-files-rehearsal"
		}
		key := o.Key
		if o.VersionID != "" {
			key += "-" + o.VersionID
		}
		var body io.ReadSeeker = bytes.NewReader(fixture.data[o.Identity()])
		size, sha := o.Size, digest(fixture.data[o.Identity()])
		if o.Key == "video.mp4" {
			f, err := os.Open(largePath)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			body, size, sha = f, largeCLIBytes, largeSHA
		}
		if _, err = sdk.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.Bucket), Key: aws.String(key), Body: body, ContentLength: aws.Int64(size), ContentType: aws.String(o.ContentType), Metadata: o.Metadata, Tagging: aws.String("legacy-tag=preserved")}); err != nil {
			t.Fatal(err)
		}
		identity := (SourceObject{Bucket: o.Bucket, Key: key}).Identity()
		expected[identity] = expectedObject{size, sha, o.ContentType, o.Metadata, map[string]string{"legacy-tag": "preserved"}}
	}
	if _, err = sdk.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String("history"), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"old version bytes", "new version bytes"} {
		o, err := sdk.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("history"), Key: aws.String("猫/version.bin"), Body: strings.NewReader(body), ContentType: aws.String("application/octet-stream"), Metadata: map[string]string{"version": "retained"}, Tagging: aws.String("version-tag=preserved")})
		if err != nil {
			t.Fatalf("seed real source versions: %v", err)
		}
		if aws.ToString(o.VersionId) == "" {
			t.Fatalf("seed real source versions: %v", err)
		}
		identity := (SourceObject{Bucket: "history", Key: "猫/version.bin", VersionID: aws.ToString(o.VersionId)}).Identity()
		expected[identity] = expectedObject{int64(len(body)), digest([]byte(body)), "application/octet-stream", map[string]string{"version": "retained"}, map[string]string{"version-tag": "preserved"}}
	}
	if _, err = sdk.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("history"), Key: aws.String("猫/version.bin")}); err != nil {
		t.Fatal(err)
	}
	largeJSON(t, filepath.Join(root, "expected-source-objects.json"), expected)
	measureDisk("seeded_source")
	observer := largeObserver(t, root)
	readonly := observer.proxy(t, raw.Endpoint, "source")
	observedTarget := observer.proxy(t, target.endpoint, "target")
	t.Cleanup(func() { observer.release(0); observer.release(1) })
	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequest(method, readonly.URL+"/library/forbidden", strings.NewReader("no"))
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 403 {
			t.Fatalf("read-only source gateway accepted %s", method)
		}
	}
	database := filepath.Join(work, "source", "legacy.sqlite")
	largeCopy(t, legacyFixture(t), database)
	_, sourceDBHash := largeFileHash(t, database)
	evidence["sourceSQLiteSHA256Before"] = sourceDBHash
	err = os.Chmod(database, 0444)
	if err != nil {
		t.Fatal(err)
	}
	if writable, err := os.OpenFile(database, os.O_WRONLY, 0); err == nil {
		writable.Close()
		t.Fatal("frozen source SQLite unexpectedly writable")
	}
	evidence["sourceSQLiteMode"] = "0444; independent O_WRONLY refused"
	secret := func(name, value string) string {
		path := filepath.Join(work, name)
		if err := os.WriteFile(path, []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	archive, journal := filepath.Join(work, "archive"), filepath.Join(work, "journal", "migration.sqlite")
	config := filepath.Join(work, "config.json")
	settings := map[string]any{
		"Source":   map[string]any{"Endpoint": readonly.URL, "Region": "us-east-1", "Buckets": []string{"tag-files", "library", "temporary", "thumbnail", "history"}, "AccessKeyFile": secret("source_access", raw.AccessKey), "SecretKeyFile": secret("source_secret", raw.SecretKey)},
		"Target":   map[string]any{"Endpoint": observedTarget.URL, "Region": "us-east-1", "AccessKeyFile": secret("target_access", target.access), "SecretKeyFile": secret("target_secret", target.secret)},
		"Database": database, "Archive": archive, "SnapshotID": "synthetic-large-cli-frozen", "RollbackReference": "separate disposable native source volume retained through verification", "RollbackVerified": true, "ArchiveReserveBytes": 1,
		"TargetMetadata": filepath.Join(work, "target"), "Journal": journal, "Budget": Budget{WritePeakBytes: 32 << 20, MetadataBytes: 16 << 20, ReserveBytes: 1}, "PartSize": largeCLIPartBytes, "SinglePutLimit": largeCLIPartBytes,
	}
	largeJSON(t, config, settings)
	stages := []largeStage{}
	stages = append(stages, largeWaitCLI(t, root, largeStartCLI(t, root, binary, config, "scan", "scan"), false))
	measureDisk("scan_archived")
	// Decode only the small manifest; full archive hashing is streamed below.
	manifestFile, err := os.Open(filepath.Join(archive, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	err = json.NewDecoder(io.LimitReader(manifestFile, 8<<20)).Decode(&manifest)
	manifestFile.Close()
	if err != nil {
		t.Fatal(err)
	}
	markers := 0
	for _, o := range manifest.Objects {
		if o.DeleteMarker {
			markers++
			continue
		}
		want, ok := expected[o.Identity()]
		if !ok || o.Size != want.Size || o.SHA256 != want.SHA256 || o.ContentType != want.ContentType || !equalMetadata(o.Metadata, want.Metadata) || !reflect.DeepEqual(o.Tags, want.Tags) {
			t.Fatalf("archive source object differs from independently seeded expectation: %+v want %+v", o, want)
		}
		n, hash := largeFileHash(t, filepath.Join(archive, o.ArchivePath))
		if n != want.Size || hash != want.SHA256 {
			t.Fatal("independent full archive hash differs")
		}
	}
	if len(manifest.Objects) != len(expected)+1 || markers != 1 {
		t.Fatal("lost source objects/real version/delete marker")
	}
	for _, filename := range []string{"manifest.json", "rows.jsonl", "complete"} {
		largeCopy(t, filepath.Join(archive, filename), filepath.Join(root, "archive-"+filename))
	}
	first := largeStartCLI(t, root, binary, config, "copy-interrupted", "copy")
	largeAwaitGate(t, observer, first, 0)
	checkpoint := largeInspectCheckpoint(t, journal, target, partHashes)
	largeJSON(t, filepath.Join(root, "checkpoint-before-kill.json"), checkpoint)
	measureDisk("three_durable_parts_before_SIGKILL")
	if err = first.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	stages = append(stages, largeWaitCLI(t, root, first, true))
	observer.release(0)
	resumed := largeStartCLI(t, root, binary, config, "copy-resumed", "copy")
	largeAwaitGate(t, observer, resumed, 1)
	after := largeInspectCheckpoint(t, journal, target, partHashes)
	largeJSON(t, filepath.Join(root, "checkpoint-after-restart.json"), after)
	if !reflect.DeepEqual(checkpoint, after) {
		t.Fatal("acknowledged remote/local multipart identities or proofs changed across CLI restart")
	}
	observer.mu.Lock()
	for number := 1; number <= 3; number++ {
		if observer.counts[number] != 1 {
			t.Errorf("acknowledged part %d was resent: %d forwards", number, observer.counts[number])
		}
	}
	observer.mu.Unlock()
	observer.release(1)
	stages = append(stages, largeWaitCLI(t, root, resumed, false))
	measureDisk("resumed_copy_complete")
	stages = append(stages, largeWaitCLI(t, root, largeStartCLI(t, root, binary, config, "verify", "verify"), false))
	measureDisk("full_verify_complete")
	largeJSON(t, filepath.Join(root, "stages.json"), stages)
	output, err := os.Open(filepath.Join(root, "verify.stdout"))
	if err != nil {
		t.Fatal(err)
	}
	var report Report
	err = json.NewDecoder(io.LimitReader(output, 4<<20)).Decode(&report)
	output.Close()
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, object := range expected {
		total += object.Size
	}
	wantCategories := []CategoryResult{{3, []int64{12, 13}, []int64{12, 13}}, {4, []int64{11, 12, 13, 14, 15}, []int64{11, 12, 13, 14, 15}}, {5, []int64{12}, []int64{12}}, {6, []int64{11, 12, 13, 14, 15}, []int64{11, 12, 13, 14, 15}}}
	if !report.Complete || report.ObjectsVerified != len(expected) || report.BytesVerified != total || report.SourceObjectCount != len(expected) || report.SourceBytes != total || !reflect.DeepEqual(report.Categories, wantCategories) || len(report.Exceptions) != 0 {
		t.Fatalf("CLI report differs from independent expected totals/category ID sets: %+v", report)
	}
	largeCatalog(t, filepath.Join(work, "target", "library.sqlite"), filepath.Join(root, "catalog.json"))
	j := largeReadDB(t, journal)
	rows, err := j.Query(`SELECT source_id,object_key,size,sha256,content_type,metadata_json,state FROM objects ORDER BY source_id`)
	if err != nil {
		t.Fatal(err)
	}
	targetClient := target.client(t)
	for rows.Next() {
		var sourceID, key, sha, ct, metadataJSON, state string
		var n int64
		if err = rows.Scan(&sourceID, &key, &n, &sha, &ct, &metadataJSON, &state); err != nil {
			t.Fatal(err)
		}
		want, ok := expected[sourceID]
		if !ok || n != want.Size || sha != want.SHA256 || ct != want.ContentType || state != "imported" {
			t.Fatal("independent target mapping mismatch")
		}
		get, err := targetClient.Get(ctx, key, objectstore.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		actual, err := io.CopyBuffer(h, get.Body, make([]byte, 128<<10))
		get.Body.Close()
		if err != nil || actual != want.Size || hex.EncodeToString(h.Sum(nil)) != want.SHA256 || get.ContentType != want.ContentType || !equalMetadata(get.Metadata, want.Metadata) {
			t.Fatalf("independent full target hash/metadata mismatch: key=%s error=%v", key, err)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		t.Fatal(err)
	}
	var ackCount int
	if err = j.QueryRow(`SELECT count(*) FROM part_acknowledgements`).Scan(&ackCount); err != nil || ackCount != 128 {
		t.Fatalf("missing durable final ACKs: count=%d error=%v", ackCount, err)
	}
	observer.mu.Lock()
	counts := make(map[int]int, len(observer.counts))
	for number, count := range observer.counts {
		counts[number] = count
	}
	logErr := observer.writeErr
	observer.mu.Unlock()
	if logErr != nil {
		t.Fatalf("incomplete raw HTTP observation log: %v", logErr)
	}
	largeJSON(t, filepath.Join(root, "forwarded-upload-parts.json"), counts)
	if len(counts) != 128 {
		t.Fatalf("wrong number of distinct parts: %d", len(counts))
	}
	for number := 1; number <= 128; number++ {
		if counts[number] != 1 {
			t.Fatalf("part %d forwarded %d times, expected once across interruption/restart", number, counts[number])
		}
	}
	j.Close()
	_, afterDBHash := largeFileHash(t, database)
	evidence["sourceSQLiteSHA256After"] = afterDBHash
	if sourceDBHash != afterDBHash {
		t.Fatal("frozen read-only source SQLite bytes changed")
	}
	for from, to := range map[string]string{database: "legacy.sqlite", filepath.Join(work, "target", "library.sqlite"): "target-library.sqlite", journal: "migration-journal.sqlite"} {
		largeCopy(t, from, filepath.Join(root, to))
	}
	// Checkpoint evidence above is authoritative even if WAL was nonempty when
	// interrupted. The final CLI has closed/checkpointed its own SQLite stores.
	evidence["stages"], evidence["verifiedObjects"], evidence["verifiedBytes"] = stages, len(expected), total
	evidence["retainedAcknowledgedParts"], evidence["forwardedParts"] = 3, counts
	evidence["sourceMutationMethodsRejected"] = []string{"PUT", "POST", "DELETE", "PATCH"}
	evidence["status"] = "passed"
	t.Logf("real normal CLI scan/copy(SIGKILL)/resume/verify: %d objects / %d actual bytes, 128 unique 16MiB parts, 3 immutable native/local ACKs retained; %s", len(expected), total, time.Since(started))
}
