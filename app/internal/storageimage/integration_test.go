package storageimage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/storageprofile"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

type fixture struct {
	t                            *testing.T
	name, volume, access, secret string
}

func create(t *testing.T) *fixture {
	return createWithVolume(t, nil)
}

func createWithVolume(t *testing.T, volumeOptions []string) *fixture {
	t.Helper()
	image := os.Getenv("TAG_FILES_STORAGE_IMAGE")
	if image == "" {
		t.Skip("run scripts/check-storage-image to test the built production image")
	}
	f := &fixture{t: t, name: "tag-files-image-" + domain.NewUUID(), access: domain.NewUUID(), secret: domain.NewUUID() + domain.NewUUID()}
	f.volume = f.name
	volumeArgs := append([]string{"volume", "create"}, volumeOptions...)
	volumeArgs = append(volumeArgs, f.volume)
	testutil.Docker(t, "", volumeArgs...)
	t.Cleanup(func() { testutil.Docker(t, "", "volume", "rm", f.volume) })
	testutil.Docker(t, "", "create", "--name", f.name, "--mount", "type=volume,src="+f.volume+",dst=/data", "-p", "127.0.0.1::8333", image)
	t.Cleanup(func() { testutil.Docker(t, "", "rm", "-f", f.name) })
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("storage logs:\n" + testutil.Docker(t, "", "logs", "--tail", "100", f.name))
		}
	})
	f.copySecret("s3_access_key", f.access)
	f.copySecret("s3_secret_key", f.secret)
	return f
}

func (f *fixture) copySecret(name, value string) {
	f.t.Helper()
	file := filepath.Join(f.t.TempDir(), name)
	if err := os.WriteFile(file, []byte(value+"\n"), 0600); err != nil {
		f.t.Fatal(err)
	}
	testutil.Docker(f.t, "", "cp", file, f.name+":/run/secrets/"+name)
}
func (f *fixture) state() storageprofile.StorageState {
	f.t.Helper()
	raw := testutil.Docker(f.t, "", "exec", f.name, "cat", "/data/"+storageprofile.StateFilename)
	var state storageprofile.StorageState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		f.t.Fatal(err)
	}
	return state
}
func (f *fixture) ready() {
	f.t.Helper()
	deadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "inspect", "--format", "{{.State.Running}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", f.name).Output()
		if err == nil && strings.TrimSpace(string(out)) == "true healthy" {
			return
		}
		if err == nil && strings.HasPrefix(string(out), "false") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	f.t.Fatal("storage image did not become healthy:\n" + testutil.Docker(f.t, "", "logs", "--tail", "65", f.name))
}
func (f *fixture) endpoint() string {
	return "http://" + testutil.Docker(f.t, "", "port", f.name, "8333/tcp")
}
func (f *fixture) client() *objectstore.Client {
	f.t.Helper()
	c, err := objectstore.New(objectstore.Config{Endpoint: f.endpoint(), Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: &http.Client{Timeout: 60 * time.Second}})
	if err != nil {
		f.t.Fatal(err)
	}
	return c
}
func (f *fixture) refused() {
	f.t.Helper()
	deadline := time.Now().Add(70 * time.Second)
	client := &http.Client{Timeout: 200 * time.Millisecond}
	var address string
	iteration := 0
	for time.Now().Before(deadline) {
		if iteration%20 == 0 {
			state := testutil.Docker(f.t, "", "inspect", "--format", "{{.State.Running}} {{.State.ExitCode}}", f.name)
			if strings.HasPrefix(state, "false ") {
				if state == "false 0" {
					f.t.Fatal("unsafe storage exited successfully")
				}
				return
			}
		}
		if address == "" {
			raw, err := exec.Command("docker", "port", f.name, "8333/tcp").Output()
			if err == nil {
				address = strings.TrimSpace(string(raw))
			}
		}
		if address != "" {
			if res, err := client.Head("http://" + address + "/tag-files"); err == nil {
				res.Body.Close()
				f.t.Fatalf("unsafe restart exposed S3 (HTTP %d)", res.StatusCode)
			}
		}
		iteration++
		time.Sleep(2 * time.Millisecond)
	}
	f.t.Fatal("unsafe storage did not refuse startup")
}

func TestProductionStorageImageRefusesUnsafeAccessBeforeNetworkExposure(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	raw := testutil.Docker(t, "", "exec", f.name, "cat", "/data/config/s3.json")
	control, err := storageprofile.PrivateCredentials([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	api := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) { return control, nil })}, func(o *s3.Options) { o.BaseEndpoint = aws.String(f.endpoint()); o.UsePathStyle = true })
	_, err = api.PutPublicAccessBlock(context.Background(), &s3.PutPublicAccessBlockInput{Bucket: aws.String("tag-files"), PublicAccessBlockConfiguration: &types.PublicAccessBlockConfiguration{BlockPublicAcls: aws.Bool(false), IgnorePublicAcls: aws.Bool(false), BlockPublicPolicy: aws.Bool(false), RestrictPublicBuckets: aws.Bool(false)}})
	if err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "stop", "--time", "20", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.refused()
}

func TestProductionStorageImagePrivateDurableRestartAndNegativeFsync(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	first := f.state()
	if first.Phase != "ready" {
		t.Fatalf("healthy without durable ready: %+v", first)
	}
	c := f.client()
	probe, err := http.Head(f.endpoint() + "/tag-files")
	if err != nil {
		t.Fatal(err)
	}
	probe.Body.Close()
	if values := probe.Header.Values("X-Tag-Files-Storage-Epoch"); len(values) != 1 {
		t.Fatalf("gateway published ambiguous epochs: %v", values)
	}
	epoch, err := c.StorageEpoch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c, err = c.AtEpoch(epoch)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("production volume\x00"), 65536)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	putStarted := time.Now()
	if err := c.Put(context.Background(), "objects/日本語", bytes.NewReader(data), int64(len(data)), hash, "application/octet-stream", nil); err != nil {
		t.Fatal(err)
	}
	t.Logf("first physical object allocation and PUT: %s", time.Since(putStarted))
	if files := testutil.Docker(t, "", "exec", f.name, "find", "/data/filerldb2", "-type", "f"); files == "" {
		t.Fatal("running filer did not use the complete-volume LevelDB2 backend")
	}
	res, err := http.Get(f.endpoint() + "/tag-files/objects/%E6%97%A5%E6%9C%AC%E8%AA%9E")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("anonymous media returned %d", res.StatusCode)
	}
	// The app identity can stream data, but cannot administer storage or buckets.
	api := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: f.access, SecretAccessKey: f.secret}, nil
	})}, func(o *s3.Options) { o.BaseEndpoint = aws.String(f.endpoint()); o.UsePathStyle = true })
	if _, err := api.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String("unexpected-bucket")}); err == nil {
		t.Fatal("app identity could create another bucket")
	}
	head, err := c.Head(context.Background(), "objects/日本語")
	if err != nil {
		t.Fatal(err)
	}
	rangeObject, err := c.Get(context.Background(), "objects/日本語", objectstore.GetOptions{Range: "bytes=3-11", IfMatch: head.ETag})
	if err != nil {
		t.Fatal(err)
	}
	rangeBytes, err := io.ReadAll(rangeObject.Body)
	rangeObject.Body.Close()
	if err != nil || !bytes.Equal(rangeBytes, data[3:12]) {
		t.Fatalf("S3 gateway changed Range bytes: %v", err)
	}
	for _, options := range []objectstore.GetOptions{{IfNoneMatch: head.ETag}, {Range: "bytes=999999999-"}} {
		_, err := c.Get(context.Background(), "objects/日本語", options)
		var wire *objectstore.Error
		want := 304
		if options.Range != "" {
			want = 416
		}
		if !errors.As(err, &wire) || wire.Status != want {
			t.Fatalf("gateway conditional/range status: %v, want %d", err, want)
		}
	}
	mpuKey := "incoming/image-partial"
	mpu, err := c.CreateMultipart(context.Background(), mpuKey, "application/octet-stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	partData := bytes.Repeat([]byte("p"), 5*1024*1024)
	partHash := sha256.Sum256(partData)
	bound, err := c.UploadPart(context.Background(), mpuKey, mpu, 1, int64(len(partData)), base64.StdEncoding.EncodeToString(partHash[:]), bytes.NewReader(partData))
	if err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "kill", "--signal", "KILL", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	if f.state() != first {
		t.Fatal("restart changed the owned identity or config")
	}
	base := f.client()
	newEpoch, err := base.StorageEpoch(context.Background())
	if err != nil || newEpoch == epoch {
		t.Fatalf("backend restart kept live epoch: %q %v", newEpoch, err)
	}
	stale, err := base.AtEpoch(epoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stale.CreateMultipart(context.Background(), "stale/forbidden", "application/octet-stream", nil); err == nil {
		t.Fatal("old storage epoch created a new MPU after restart")
	} else {
		var wire *objectstore.Error
		if !errors.As(err, &wire) || wire.Status != 409 || wire.Code != "StorageEpochChanged" {
			t.Fatalf("stale epoch result: %v", err)
		}
	}
	if uploads, err := base.ListMultipart(context.Background(), "stale/"); err != nil || len(uploads) != 0 {
		t.Fatalf("stale request reached the backend: %+v %v", uploads, err)
	}
	c, err = base.AtEpoch(newEpoch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Verify(context.Background(), "objects/日本語", int64(len(data)), hash); err != nil {
		t.Fatal(err)
	}
	parts, err := c.ListParts(context.Background(), mpuKey, mpu)
	if err != nil || len(parts) != 1 || parts[0].Number != 1 || parts[0].Size != int64(len(partData)) || parts[0].ETag != bound.ETag {
		t.Fatalf("confirmed MPU part lost on image restart: %+v, %v", parts, err)
	}
	tail := []byte("durable last part")
	tailHash := sha256.Sum256(tail)
	last, err := c.UploadPart(context.Background(), mpuKey, mpu, 2, int64(len(tail)), base64.StdEncoding.EncodeToString(tailHash[:]), bytes.NewReader(tail))
	if err != nil {
		t.Fatal(err)
	}
	completed, err := c.Complete(context.Background(), mpuKey, mpu, []objectstore.Part{bound, last})
	full := sha256.Sum256(append(partData, tail...))
	if err != nil || completed.SHA256 != hex.EncodeToString(full[:]) {
		t.Fatalf("gateway MPU full verification: %v", err)
	}
	testutil.Docker(t, "fs.configure -locationPrefix=/ -fsync=false -apply\n", "exec", "-i", f.name, "weed", "shell", "-master=127.0.0.1:9333", "-filer=127.0.0.1:8888")
	// The health probe must fail now, and startup must refuse before binding S3.
	if err := exec.Command("docker", "exec", f.name, "storage-run", "check").Run(); err == nil {
		t.Fatal("health accepted disabled fsync")
	}
	testutil.Docker(t, "", "stop", "--time", "20", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.refused()
}

func TestProductionStorageImageResumesKilledInitialization(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	first := f.initializing()
	testutil.Docker(t, "", "kill", "--signal", "KILL", f.name)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	if after := f.state(); after.ID != first.ID || after.Phase != "ready" {
		t.Fatalf("lost init identity: %+v", after)
	}
}

func (f *fixture) initializing() storageprofile.StorageState {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var first storageprofile.StorageState
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "exec", f.name, "cat", "/data/"+storageprofile.StateFilename).Output()
		if err == nil && json.Unmarshal(out, &first) == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if first.Phase != "initializing" {
		f.t.Fatalf("did not observe durable initialization intent: %+v", first)
	}
	return first
}

func TestProductionStorageImageGracefulStopDuringInitialization(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	first := f.initializing()
	testutil.Docker(t, "", "stop", "--time", "20", f.name)
	if code := testutil.Docker(t, "", "inspect", "--format", "{{.State.ExitCode}}", f.name); code != "0" {
		t.Fatalf("SIGTERM during init did not drain gracefully: exit %s", code)
	}
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	if f.state().ID != first.ID {
		t.Fatal("graceful stop lost initialization identity")
	}
}

func TestProductionStorageImageRefusesUnownedData(t *testing.T) {
	f := create(t)
	path := filepath.Join(t.TempDir(), "foreign.data")
	if err := os.WriteFile(path, []byte("preserve unowned storage bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	testutil.Docker(t, "", "cp", path, f.name+":/data/foreign.data")
	testutil.Docker(t, "", "start", f.name)
	f.refused()
	got := testutil.Docker(t, "", "run", "--rm", "--mount", "type=volume,src="+f.volume+",dst=/data,readonly", "--entrypoint", "cat", os.Getenv("TAG_FILES_STORAGE_IMAGE"), "/data/foreign.data")
	if got != "preserve unowned storage bytes" {
		t.Fatal("startup refusal modified unowned data")
	}
}

func TestProductionStorageImageRefusesChangedCredentials(t *testing.T) {
	f := create(t)
	testutil.Docker(t, "", "start", f.name)
	f.ready()
	before := f.state()
	testutil.Docker(t, "", "stop", "--time", "20", f.name)
	f.copySecret("s3_secret_key", domain.NewUUID()+domain.NewUUID())
	testutil.Docker(t, "", "start", f.name)
	f.refused()
	// Inspect stopped volume without starting a writer or requiring host mount paths.
	raw := testutil.Docker(t, "", "run", "--rm", "--mount", "type=volume,src="+f.volume+",dst=/data,readonly", "--entrypoint", "cat", os.Getenv("TAG_FILES_STORAGE_IMAGE"), "/data/"+storageprofile.StateFilename)
	var after storageprofile.StorageState
	if json.Unmarshal([]byte(raw), &after) != nil || after != before {
		t.Fatal("credential refusal rewrote ownership state")
	}
}
