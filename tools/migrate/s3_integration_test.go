package migrate

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
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
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/testutil"
)

// The source fixture uses a separate backend and a GET/HEAD-only gateway.
// The target fixture runs the production fsync/private-bucket supervisor image.
func migrationTarget(t *testing.T) *objectstore.Client {
	t.Helper()
	image := os.Getenv("TAG_FILES_MIGRATE_STORAGE_IMAGE")
	if image == "" {
		t.Skip("set TAG_FILES_MIGRATE_STORAGE_IMAGE to an already built owned storage image")
	}
	image = testutil.Docker(t, "", "image", "inspect", "--format", "{{.Id}}", image)
	name := "tag-files-migrate-target-" + domain.NewUUID()
	testutil.Docker(t, "", "volume", "create", name)
	t.Cleanup(func() { testutil.Docker(t, "", "volume", "rm", name) })
	testutil.Docker(t, "", "create", "--name", name, "--mount", "type=volume,src="+name+",dst=/data", "-p", "127.0.0.1::8333", image)
	t.Cleanup(func() { testutil.Docker(t, "", "rm", "-f", name) })
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(testutil.Docker(t, "", "logs", "--tail", "50", name))
		}
	})
	access, secret := domain.NewUUID(), domain.NewUUID()+domain.NewUUID()
	for filename, value := range map[string]string{"s3_access_key": access, "s3_secret_key": secret} {
		path := filepath.Join(t.TempDir(), filename)
		if e := os.WriteFile(path, []byte(value+"\n"), 0600); e != nil {
			t.Fatal(e)
		}
		testutil.Docker(t, "", "cp", path, name+":/run/secrets/"+filename)
	}
	testutil.Docker(t, "", "start", name)
	deadline := time.Now().Add(90 * time.Second)
	for {
		out, e := exec.Command("docker", "inspect", "--format", "{{.State.Running}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", name).Output()
		if e == nil && strings.TrimSpace(string(out)) == "true healthy" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned migration storage did not become healthy")
		}
		time.Sleep(200 * time.Millisecond)
	}
	endpoint := "http://" + testutil.Docker(t, "", "port", name, "8333/tcp")
	c, e := objectstore.New(objectstore.Config{Endpoint: endpoint, Region: "us-east-1", Bucket: "tag-files", AccessKey: access, SecretKey: secret, HTTPClient: &http.Client{Timeout: 90 * time.Second}})
	if e != nil {
		t.Fatal(e)
	}
	control, e := storageprotocol.NewClient(endpoint, access, secret, nil)
	if e != nil {
		t.Fatal(e)
	}
	controls[c] = control
	return c
}

type losePut struct {
	lostComplete         bool
	interruptedMultipart bool
	*objectstore.Client
	lost bool
	puts int
}

func (d *losePut) Put(ctx context.Context, key string, r io.Reader, size int64, sha, ct string, m map[string]string) error {
	d.puts++
	if e := d.Client.Put(ctx, key, r, size, sha, ct, m); e != nil {
		return e
	}
	if !d.lost {
		d.lost = true
		return fmt.Errorf("fixture lost successful PUT response")
	}
	return nil
}

func (d *losePut) Complete(ctx context.Context, key, id string, parts []objectstore.Part) (objectstore.Verified, error) {
	if !d.interruptedMultipart {
		d.interruptedMultipart = true
		return objectstore.Verified{}, fmt.Errorf("fixture interrupted before Complete with durable part acknowledgements")
	}
	v, e := d.Client.Complete(ctx, key, id, parts)
	if e != nil {
		return v, e
	}
	if !d.lostComplete {
		d.lostComplete = true
		return objectstore.Verified{}, fmt.Errorf("fixture lost successful Complete response")
	}
	return v, nil
}

func TestRealS3ArchiveCopyLostResponseResumeAndFullVerify(t *testing.T) {
	if os.Getenv("TAG_FILES_MIGRATE_INTEGRATION") != "1" {
		t.Skip("set TAG_FILES_MIGRATE_INTEGRATION=1 for disposable S3 rehearsal")
	}
	t.Setenv("TAG_FILES_INTEGRATION", "1")
	raw := testutil.StartStorage(t)
	sdk := raw.S3()
	for _, bucket := range []string{"library", "temporary", "thumbnail", "history"} {
		if _, e := sdk.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); e != nil {
			t.Fatal(e)
		}
	}
	sourceFixture := fixtureSource()
	large := bytes.Repeat([]byte("actual-s3-multipart\x00"), 700000)
	for idx, o := range sourceFixture.objects {
		if o.Key == "video.mp4" {
			sourceFixture.objects[idx].Size = int64(len(large))
			sourceFixture.data[o.Identity()] = large
		}
	}
	// Independent source fixture has the two orphan versions represented by two
	// keys in this unversioned Seaweed fixture. A separate versioned bucket below covers the actual S3 version API.
	// Native MinIO deployment rehearsal remains a separate explicit gate.
	for _, o := range sourceFixture.objects {
		key := o.Key
		if o.VersionID != "" {
			key += "-" + o.VersionID
		}
		if _, e := sdk.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(o.Bucket), Key: aws.String(key), Body: bytes.NewReader(sourceFixture.data[o.Identity()]), ContentType: aws.String(o.ContentType), Metadata: o.Metadata, Tagging: aws.String("legacy-tag=preserved")}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := sdk.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String("history"), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); e != nil {
		t.Fatal(e)
	}
	for _, body := range []string{"old version bytes", "new version bytes"} {
		if _, e := sdk.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String("history"), Key: aws.String("猫/version.bin"), Body: strings.NewReader(body), ContentType: aws.String("application/octet-stream"), Metadata: map[string]string{"version": "retained"}, Tagging: aws.String("version-tag=preserved")}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := sdk.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String("history"), Key: aws.String("猫/version.bin")}); e != nil {
		t.Fatal(e)
	}
	targetURL, _ := url.Parse(raw.Endpoint)
	proxy := httputil.NewSingleHostReverseProxy(targetURL)
	readonly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer readonly.Close()
	source, e := NewS3Source(objectstore.Config{Endpoint: readonly.URL, Region: "us-east-1", AccessKey: raw.AccessKey, SecretKey: raw.SecretKey, HTTPClient: &http.Client{Timeout: 90 * time.Second}}, []string{"tag-files", "library", "temporary", "thumbnail", "history"})
	if e != nil {
		t.Fatal(e)
	}
	// The source endpoint itself refuses writes, irrespective of setup credentials.
	req, _ := http.NewRequest(http.MethodPut, readonly.URL+"/library/forbidden", strings.NewReader("no"))
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("source gateway accepted a write")
	}
	p := legacyFixture(t)
	dir := filepath.Join(t.TempDir(), "archive")
	started := time.Now()
	m, e := Scan(ctx, ScanConfig{Database: p, Archive: dir, SnapshotID: "isolated-seaweed-source", RollbackReference: "isolated source volume retained until fixture cleanup", RollbackVerified: true, ArchiveReserveBytes: 1}, source)
	if e != nil {
		t.Fatal(e)
	}
	versions, markers := 0, 0
	for _, o := range m.Objects {
		if o.Bucket == "history" {
			if o.DeleteMarker {
				markers++
			} else {
				versions++
			}
		}
	}
	if versions != 2 || markers != 1 {
		t.Fatalf("version archive incomplete: versions=%d markers=%d", versions, markers)
	}
	target := migrationTarget(t)
	db, e := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	// Discover the persistent library pair using the same authenticated control
	// extension as the production application, then fence all object requests.
	// Client's endpoint is deliberately read from fixture helper only via control.
	// The helper exposes identity through the client below.
	control := targetControl(t, target)
	pair, e := librarybinding.Ensure(ctx, db, control)
	if e != nil {
		t.Fatal(e)
	}
	target, e = target.AtLibrary(pair.StorageID, pair.LibraryID, pair.VolumeID)
	if e != nil {
		t.Fatal(e)
	}
	epoch, e := target.StorageEpoch(ctx)
	if e != nil {
		t.Fatal(e)
	}
	target, e = target.AtEpoch(epoch)
	if e != nil {
		t.Fatal(e)
	}
	plans := makePlans(m, pair.LibraryID)
	var planned int64
	var partialObserved int64
	for _, plan := range plans {
		planned += plan.Size
	}
	c := ApplyConfig{Archive: dir, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: pair.StorageID + "/" + pair.VolumeID, PartSize: 5 << 20, SinglePutLimit: 5 << 20, Budget: Budget{WritePeakBytes: 1 << 20, MetadataBytes: 1 << 20, ReserveBytes: 1}, Capacity: func(ctx context.Context) (int64, int64, error) {
		v, e := target.Capacity(ctx)
		if e != nil {
			return 0, 0, e
		}
		if v.AvailableInodes == nil {
			return 0, 0, fmt.Errorf("missing capacity")
		}
		// Independently observe native HEAD/ListParts bytes and constrain the
		// signed real capacity to an exactly sized logical T+W+D+R pool. This
		// makes partial-MPU resume depend on proven stored-part credit even
		// though the disposable real volume has ample physical free space.
		var used, partialUsed int64
		for _, plan := range plans {
			object, headErr := target.Head(ctx, plan.Key)
			if headErr == nil {
				used += object.Size
				continue
			}
			if !notFound(headErr) {
				return 0, 0, headErr
			}
			active, listErr := target.ListMultipart(ctx, plan.Key)
			if listErr != nil {
				return 0, 0, listErr
			}
			for _, upload := range active {
				if upload.Key != "" && upload.Key != plan.Key {
					continue
				}
				parts, partsErr := target.ListParts(ctx, plan.Key, upload.UploadID)
				if partsErr != nil {
					return 0, 0, partsErr
				}
				for _, part := range parts {
					used += part.Size
					partialUsed += part.Size
				}
			}
		}
		partialObserved = max(partialObserved, partialUsed)
		return min(v.AvailableBytes, planned+(2<<20)+1-used), *v.AvailableInodes, nil
	}}
	lost := &losePut{Client: target}
	interruptions := 0
	for attempt := 0; attempt < 4; attempt++ {
		e = Apply(ctx, c, m, source, db, lost)
		if e == nil {
			break
		}
		if !strings.Contains(e.Error(), "fixture lost successful") && !strings.Contains(e.Error(), "fixture interrupted before Complete") {
			t.Fatal(e)
		}
		interruptions++
	}
	if e != nil || interruptions != 3 || !lost.lost || !lost.lostComplete || !lost.interruptedMultipart {
		t.Fatalf("lost-response recovery incomplete: interruptions=%d error=%v", interruptions, e)
	}
	report, e := Verify(ctx, c, m, source, db, target)
	if e != nil || !report.Complete {
		t.Fatal(report, e)
	}
	if e = Apply(ctx, c, m, source, db, target); e != nil {
		t.Fatal(e)
	}
	if partialObserved < 5<<20 {
		t.Fatalf("native capacity observer did not account for partial MPU bytes: %d", partialObserved)
	}
	t.Logf("full S3 rehearsal: %d objects, %d bytes, %d category sets, %d native partial bytes in exact logical T+W+D+R pool, %s", report.ObjectsVerified, report.BytesVerified, len(report.Categories), partialObserved, time.Since(started))
}

// Test-only control registry avoids exposing gateway credentials in production
// Client APIs or printing them in fixture output.
var controls = map[*objectstore.Client]*storageprotocol.Client{}

func targetControl(t *testing.T, c *objectstore.Client) *storageprotocol.Client {
	t.Helper()
	v := controls[c]
	if v == nil {
		t.Fatal("fixture control missing")
	}
	return v
}
