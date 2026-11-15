package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
	"github.com/mikd1/tag-files/app/internal/worker"
)

type nativeFixture struct {
	t                                               *testing.T
	storageImage, toolImage, prefix, access, secret string
	app, storage, metadataVolume, storageVolume     string
}

func docker(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, e := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if e != nil {
		t.Fatalf("docker %s: %v\n%s", args[0], e, out)
	}
	return strings.TrimSpace(string(out))
}
func createNative(t *testing.T) *nativeFixture {
	t.Helper()
	s, b := os.Getenv("TAG_FILES_STORAGE_IMAGE"), os.Getenv("TAG_FILES_BACKUP_IMAGE")
	if s == "" || b == "" {
		t.Skip("run tools/backup/check-drill for the real Linux Docker restore drill")
	}
	f := &nativeFixture{t: t, storageImage: s, toolImage: b, prefix: "tag-files-backup-" + domain.NewUUID(), access: domain.NewUUID(), secret: domain.NewUUID() + domain.NewUUID()}
	f.app = f.prefix + "-app"
	f.storage = f.prefix + "-storage"
	f.metadataVolume = f.prefix + "-metadata"
	f.storageVolume = f.prefix + "-data"
	f.volume(f.metadataVolume)
	f.volume(f.storageVolume)
	f.container(f.storage, f.storageVolume, true)
	f.container(f.app, f.metadataVolume, false)
	return f
}
func (f *nativeFixture) volume(name string) {
	docker(f.t, "volume", "create", name)
	f.t.Cleanup(func() { docker(f.t, "volume", "rm", name) })
}
func (f *nativeFixture) container(name, volume string, storage bool) {
	args := []string{"create", "--name", name, "--mount", "type=volume,src=" + volume + ",dst=/data"}
	if storage {
		args = append(args, "-p", "127.0.0.1::8333", f.storageImage)
	} else {
		args = append(args, "--entrypoint", "/bin/sh", f.storageImage, "-c", "exit 0")
	}
	docker(f.t, args...)
	f.t.Cleanup(func() { docker(f.t, "rm", "-f", name) })
	if storage {
		root := f.t.TempDir()
		for _, v := range []struct{ name, value string }{{"s3_access_key", f.access}, {"s3_secret_key", f.secret}} {
			p := filepath.Join(root, v.name)
			write(f.t, p, []byte(v.value+"\n"))
			docker(f.t, "cp", p, name+":/run/secrets/"+v.name)
		}
	}
}
func (f *nativeFixture) start(name string) {
	docker(f.t, "start", name)
	deadline := time.Now().Add(80 * time.Second)
	for time.Now().Before(deadline) {
		s := docker(f.t, "inspect", "--format", "{{.State.Running}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", name)
		if s == "true healthy" {
			docker(f.t, "exec", name, "storage-run", "check")
			return
		}
		if strings.HasPrefix(s, "false") {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	f.t.Fatal("restored storage failed preflight:\n" + docker(f.t, "logs", "--tail", "80", name))
}
func (f *nativeFixture) client(name string) *objectstore.Client {
	c, e := objectstore.New(objectstore.Config{Endpoint: "http://" + docker(f.t, "port", name, "8333/tcp"), Region: "us-east-1", Bucket: "tag-files", AccessKey: f.access, SecretKey: f.secret, HTTPClient: &http.Client{Timeout: 60 * time.Second}})
	if e != nil {
		f.t.Fatal(e)
	}
	return c
}
func (f *nativeFixture) host(expectSuccess bool, args ...string) string {
	f.t.Helper()
	cmd := exec.Command("./cold-backup", args...)
	out, e := cmd.CombinedOutput()
	if expectSuccess && e != nil {
		f.t.Fatalf("cold host command: %v\n%s", e, out)
	}
	if !expectSuccess && e == nil {
		f.t.Fatal("unsafe host command succeeded")
	}
	return strings.TrimSpace(string(out))
}
func (f *nativeFixture) restore(archive, suffix string) (string, string, string, Report) {
	f.t.Helper()
	metadata, storage := f.prefix+suffix+"-metadata", f.prefix+suffix+"-data"
	f.volume(metadata)
	f.volume(storage)
	deployment := filepath.Join(f.t.TempDir(), "deployment")
	raw := f.host(true, "restore", "--image", f.toolImage, "--app-container", f.app, "--storage-container", f.storage, "--metadata-volume", metadata, "--storage-volume", storage, "--archive", archive, "--deployment-output", deployment)
	var r Report
	if e := json.Unmarshal([]byte(raw), &r); e != nil {
		f.t.Fatal(e)
	}
	// Stopped holder makes docker cp available without binding native host paths.
	holder := f.prefix + suffix + "-holder"
	f.container(holder, metadata, false)
	dbdir := filepath.Join(f.t.TempDir(), "metadata")
	if e := os.Mkdir(dbdir, 0700); e != nil {
		f.t.Fatal(e)
	}
	docker(f.t, "cp", holder+":/data/.", dbdir)
	server := f.prefix + suffix + "-storage"
	f.container(server, storage, true)
	return server, dbdir, deployment, r
}

type storedFact struct {
	Size                int64
	SHA256, ContentType string
	Metadata            map[string]string
}

func inventory(t *testing.T, c *objectstore.Client) map[string]storedFact {
	t.Helper()
	out := map[string]storedFact{}
	token := ""
	for {
		page, e := c.List(context.Background(), "", token, 1000)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range page.Objects {
			h, e := c.Head(context.Background(), v.Key)
			if e != nil {
				t.Fatal(e)
			}
			g, e := c.Get(context.Background(), v.Key, objectstore.GetOptions{})
			if e != nil {
				t.Fatal(e)
			}
			sum := sha256.New()
			n, e := io.Copy(sum, g.Body)
			g.Body.Close()
			if e != nil || n != h.Size {
				t.Fatal("full GET/size", e)
			}
			if h.Size > 2 {
				r, e := c.Get(context.Background(), v.Key, objectstore.GetOptions{Range: "bytes=1-2"})
				if e != nil {
					t.Fatal(e)
				}
				b, e := io.ReadAll(r.Body)
				r.Body.Close()
				if e != nil || len(b) != 2 || r.ContentRange != fmt.Sprintf("bytes 1-2/%d", h.Size) {
					t.Fatal("range", r.ContentRange, e)
				}
			}
			out[v.Key] = storedFact{h.Size, hex.EncodeToString(sum.Sum(nil)), h.ContentType, h.Metadata}
		}
		if page.NextToken == "" {
			break
		}
		token = page.NextToken
	}
	return out
}
func testPut(t *testing.T, c *objectstore.Client, key string, data []byte, typ string, metadata map[string]string) {
	t.Helper()
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if e := c.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), hash, typ, metadata); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Verify(context.Background(), key, int64(len(data)), hash); e != nil {
		t.Fatal(e)
	}
}
func createUpload(t *testing.T, db *store.Store, c *ingestion.Coordinator, data []byte, size int64, old bool) (ingestion.Session, error) {
	t.Helper()
	now := time.Now()
	if old {
		now = time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	}
	sum := sha256.Sum256(data)
	var batch ingestion.Batch
	e := db.Write(context.Background(), func(tx *sql.Tx) error {
		var e error
		batch, e = ingestion.Create(context.Background(), tx, []ingestion.File{{ClientFileID: domain.NewUUID(), Filename: "same name — 雪.png", Size: size, Fingerprint: hex.EncodeToString(sum[:])}}, nil, ingestion.Limits{MaxFileSize: 20 << 20, PartSize: 5 << 20, MaxParallelTransfers: 2}, now)
		return e
	})
	if e != nil {
		return ingestion.Session{}, e
	}
	s, e := c.EnsureMultipart(context.Background(), batch.Sessions[0].ID)
	if e != nil {
		return s, e
	}
	if len(data) > 0 {
		_, e = c.UploadPart(context.Background(), s.ID, 1, ingestion.PartBinding{Number: 1, Size: int64(len(data)), SHA256: base64.StdEncoding.EncodeToString(sum[:])}, bytes.NewReader(data))
	}
	return s, e
}
func legacyFixture(t *testing.T, db *store.Store, c *objectstore.Client, library string) {
	t.Helper()
	for i, kind := range []string{"Unknown", "Text", "Image", "Video", "Audio"} {
		id := int64(100 + i)
		asset := domain.NewUUID()
		key := "primary/" + library + "/" + asset + "/legacy-" + strings.ToLower(kind)
		data := []byte("immutable legacy " + kind + " 雪")
		sum := sha256.Sum256(data)
		typ := []string{"application/octet-stream", "text/plain", "image/png", "video/mp4", "audio/mpeg"}[i]
		testPut(t, c, key, data, typ, map[string]string{"fixture": "legacy", "exact": "Case-Sensitive"})
		e := db.Write(context.Background(), func(tx *sql.Tx) error {
			if _, e := tx.Exec("INSERT INTO items(id,description,media_kind,uploaded_at_utc,is_favorite,view_count,duration_ticks,state) VALUES(?,?,?,'2001-02-03T04:05:06.0000000Z',1,42,9223372036854775807,'importing')", id, "Описа́ние/雪", kind); e != nil {
				return e
			}
			if _, e := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','tag-files',?,?,?,?,?,'published')", asset, id, key, len(data), hex.EncodeToString(sum[:]), typ, "legacy_preserved"); e != nil {
				return e
			}
			_, e := tx.Exec("UPDATE items SET primary_asset_id=?,state='ready' WHERE id=?", asset, id)
			return e
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	if e := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO tags(id,name) VALUES(71,'雪'),(72,'雪'),(73,'雪A'),(74,'雪a'); INSERT INTO item_tags(item_id,tag_id) VALUES(100,71),(100,72),(104,74)")
		return e
	}); e != nil {
		t.Fatal(e)
	}
}
func checkLegacy(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, e := db.Query("SELECT i.id,i.media_kind,i.description,i.is_favorite,i.view_count,CAST(i.duration_ticks AS TEXT),i.upload_id,i.uploaded_at_utc,group_concat(t.id,',') FROM items i LEFT JOIN item_tags it ON it.item_id=i.id LEFT JOIN tags t ON t.id=it.tag_id WHERE i.id BETWEEN 100 AND 104 GROUP BY i.id ORDER BY i.id")
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		var id, fav, views int64
		var kind, desc, ticks, stamp string
		var upload, tagIDs sql.NullString
		if e = rows.Scan(&id, &kind, &desc, &fav, &views, &ticks, &upload, &stamp, &tagIDs); e != nil {
			t.Fatal(e)
		}
		if id != int64(100+i) || kind != []string{"Unknown", "Text", "Image", "Video", "Audio"}[i] || desc != "Описа́ние/雪" || fav != 1 || views != 42 || ticks != "9223372036854775807" || upload.Valid || stamp != "2001-02-03T04:05:06.0000000Z" {
			t.Fatalf("legacy row changed: %d %s %s %s", id, kind, desc, ticks)
		}
		if i == 0 && tagIDs.String != "71,72" || i == 4 && tagIDs.String != "74" {
			t.Fatalf("legacy tag joins changed: %d %s", id, tagIDs.String)
		}
		i++
	}
	if i != 5 || rows.Err() != nil {
		t.Fatal("legacy rows lost", i, rows.Err())
	}
	rows.Close()
	var names string
	if e = db.QueryRow("SELECT group_concat(name,'|') FROM (SELECT name FROM tags ORDER BY id)").Scan(&names); e != nil || names != "雪|雪|雪A|雪a" {
		t.Fatal("tag values merged/normalized", names, e)
	}
}

func TestDockerColdRestoreFullNativeState(t *testing.T) {
	f := createNative(t)
	started := time.Now()
	f.start(f.storage)
	ctx := context.Background()
	dbroot := t.TempDir()
	db, e := store.Open(ctx, dbroot)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	control, e := storageprotocol.NewClient("http://"+docker(t, "port", f.storage, "8333/tcp"), f.access, f.secret, nil)
	if e != nil {
		t.Fatal(e)
	}
	pair, e := librarybinding.Ensure(ctx, db, control)
	if e != nil {
		t.Fatal(e)
	}
	client, e := f.client(f.storage).AtLibrary(pair.StorageID, pair.LibraryID, pair.VolumeID)
	if e != nil {
		t.Fatal(e)
	}
	manager, e := objectio.New(db, client, 2)
	if e != nil {
		t.Fatal(e)
	}
	uploads, e := ingestion.NewCoordinator(db, manager)
	if e != nil {
		t.Fatal(e)
	}
	legacyFixture(t, db, client, pair.LibraryID)
	var photo bytes.Buffer
	if e = png.Encode(&photo, image.NewNRGBA(image.Rect(0, 0, 13, 9))); e != nil {
		t.Fatal(e)
	}
	ready, e := createUpload(t, db, uploads, photo.Bytes(), int64(photo.Len()), false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = uploads.Complete(ctx, ready.ID); e != nil {
		t.Fatal(e)
	}
	job, e := db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobIngest}, time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(photo.Bytes())
	primary, e := uploads.NewPrimary(ctx, job, ingestion.PrimaryOutput{Kind: domain.Image, Size: int64(photo.Len()), SHA256: hex.EncodeToString(sum[:]), ContentType: "image/png", Width: 13, Height: 9}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = uploads.UploadPrimary(ctx, job, primary.ID, bytes.NewReader(photo.Bytes()), time.Now()); e != nil {
		t.Fatal(e)
	}
	published, e := uploads.PublishPrimary(ctx, job, primary.ID, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	failedData := []byte("retained failed input bytes")
	failed, e := createUpload(t, db, uploads, failedData, int64(len(failedData)), true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = uploads.Complete(ctx, failed.ID); e != nil {
		t.Fatal(e)
	}
	job, e = db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobIngest}, time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = uploads.FailIngest(ctx, job, time.Now(), "invalid_media", nil); e != nil {
		t.Fatal(e)
	}
	idle, e := createUpload(t, db, uploads, nil, 7, true)
	if e != nil {
		t.Fatal(e)
	}
	partialBytes := bytes.Repeat([]byte("p"), 5<<20)
	partial, e := createUpload(t, db, uploads, partialBytes, 5<<20+7, true)
	if e != nil {
		t.Fatal(e)
	}
	parts, e := client.ListParts(ctx, partial.IncomingKey, partial.MultipartUploadID.String)
	partialSum := sha256.Sum256(partialBytes)
	if e != nil || len(parts) != 1 || parts[0].Number != 1 || parts[0].Size != 5<<20 || parts[0].ETag == "" || parts[0].SHA256 != base64.StdEncoding.EncodeToString(partialSum[:]) {
		t.Fatalf("partial fixture lacks independent actual-byte SHA proof: %+v %v", parts, e)
	}
	queued, e := createUpload(t, db, uploads, photo.Bytes(), int64(photo.Len()), true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = uploads.Complete(ctx, queued.ID); e != nil {
		t.Fatal(e)
	}
	interrupted, e := db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobIngest}, time.Now(), time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = uploads.BeginIngest(ctx, interrupted, time.Now()); e != nil {
		t.Fatal(e)
	}
	// Persist and fully verify a candidate, then stop before ready commit. Its
	// source and immutable expected descriptor must survive a native restore.
	candidate, e := uploads.NewPrimary(ctx, interrupted, ingestion.PrimaryOutput{Kind: domain.Image, Size: int64(photo.Len()), SHA256: hex.EncodeToString(sum[:]), ContentType: "image/png", Width: 13, Height: 9}, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if candidate, e = uploads.UploadPrimary(ctx, interrupted, candidate.ID, bytes.NewReader(photo.Bytes()), time.Now()); e != nil {
		t.Fatal(e)
	}
	cancelled, e := createUpload(t, db, uploads, []byte("held cancel part"), 16, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Write(ctx, func(tx *sql.Tx) error { _, e := ingestion.Cancel(ctx, tx, cancelled.ID, time.Now()); return e }); e != nil {
		t.Fatal(e)
	}
	checkLegacy(t, db.Reader())
	expectedObjects := inventory(t, client)
	if len(expectedObjects) < 8 {
		t.Fatal("insufficient native object fixtures")
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(dbroot, ".hidden-private-state"), []byte("included in complete metadata archive"))
	docker(t, "cp", dbroot+"/.", f.app+":/data")
	cfg := filepath.Join(t.TempDir(), "deployment")
	if e = os.Mkdir(cfg, 0700); e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(cfg, "compose.yml"), []byte("# isolated cold backup fixture\nservices: {}\n"))
	destination := filepath.Join(t.TempDir(), "backups")
	backupArgs := []string{"backup", "--image", f.toolImage, "--app-container", f.app, "--storage-container", f.storage, "--metadata-volume", f.metadataVolume, "--storage-volume", f.storageVolume, "--deployment", cfg, "--destination", destination, "--generation", "fixture"}
	f.host(false, backupArgs...) // Storage still runs; the host must refuse before tools mount it.
	docker(t, "stop", "--time", "20", f.storage)
	backupStart := time.Now()
	raw := f.host(true, backupArgs...)
	var backup Report
	if e = json.Unmarshal([]byte(raw), &backup); e != nil {
		t.Fatal(e)
	}
	archive := filepath.Join(destination, "fixture")
	f.host(true, "verify", "--image", f.toolImage, "--archive", archive)
	entries, e := readEntries(filepath.Join(archive, "manifest.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	proofFiles, proofBytes := 0, int64(0)
	for _, entry := range entries {
		if entry.Role == "storage" && entry.Type == "file" && strings.HasPrefix(entry.Path, ".tag-files-mpu-proofs/") {
			proofFiles++
			proofBytes += entry.Size
		}
	}
	t.Logf("cold archive includes %d checksummed multipart proof files / %d bytes; independent source part SHA256=%s", proofFiles, proofBytes, base64.StdEncoding.EncodeToString(partialSum[:]))
	baseline, baselineDB, deployment, restored := f.restore(archive, "-baseline")
	if restored.LibraryID != pair.LibraryID || restored.StorageID != pair.StorageID || restored.RestoredVolumeID == pair.VolumeID {
		t.Fatalf("baseline rebase %+v", restored)
	}
	if secret, e := os.ReadFile(filepath.Join(deployment, "secrets", "s3_secret_key")); e != nil || strings.TrimSpace(string(secret)) != f.secret {
		t.Fatal("secret not preserved", e)
	}
	f.start(baseline)
	readOnly, e := sqliteOpen(filepath.Join(baselineDB, "library.sqlite"), true)
	if e != nil {
		t.Fatal(e)
	}
	checkLegacy(t, readOnly)
	var candidateHash, candidateState string
	var candidateSize, candidateWidth, candidateGeneration int64
	if e = readOnly.QueryRow("SELECT sha256,state,size_bytes,width,lease_generation FROM assets WHERE id=?", candidate.ID).Scan(&candidateHash, &candidateState, &candidateSize, &candidateWidth, &candidateGeneration); e != nil || candidateHash != hex.EncodeToString(sum[:]) || candidateState != "verified" || candidateSize != int64(photo.Len()) || candidateWidth != 13 || candidateGeneration != interrupted.LeaseGeneration {
		t.Fatal("unpublished candidate descriptor changed", e)
	}
	if e = integrity(readOnly); e != nil {
		t.Fatal(e)
	}
	readOnly.Close()
	if got := inventory(t, f.client(baseline)); !reflect.DeepEqual(got, expectedObjects) {
		t.Fatalf("restored full object content/metadata differs: got %+v expected %+v", got, expectedObjects)
	}
	baselineParts, e := f.client(baseline).ListParts(ctx, partial.IncomingKey, partial.MultipartUploadID.String)
	if e != nil || !reflect.DeepEqual(parts, baselineParts) {
		t.Fatalf("native partial MPU differs: %+v %+v %v", parts, baselineParts, e)
	}
	r, e := http.Head("http://" + docker(t, "port", baseline, "8333/tcp") + "/tag-files/" + primary.ObjectKey)
	if e != nil {
		t.Fatal(e)
	}
	r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatalf("restored bucket not private: %d", r.StatusCode)
	}
	docker(t, "stop", "--time", "20", baseline)
	// Native baseline is now stopped and never opened by the runtime. All further
	// recovery/Complete/cleanup/media writes happen on an independent second copy.
	recovery, recoveryDB, _, second := f.restore(archive, "-recovery")
	f.start(recovery)
	recoveryClient := f.client(recovery)
	recoverDB, e := store.Open(ctx, recoveryDB)
	if e != nil {
		t.Fatal(e)
	}
	defer recoverDB.Close()
	recoveryControl, e := storageprotocol.NewClient("http://"+docker(t, "port", recovery, "8333/tcp"), f.access, f.secret, nil)
	if e != nil {
		t.Fatal(e)
	}
	actual, e := librarybinding.Ensure(ctx, recoverDB, recoveryControl)
	if e != nil || actual.VolumeID != second.RestoredVolumeID {
		t.Fatal("runtime pair", actual, e)
	}
	reset, e := ingestion.RecoverInterrupted(ctx, recoverDB, time.Now())
	if e != nil || reset < 1 {
		t.Fatal("interrupted lease not recovered", reset, e)
	}
	newJob, e := dbgen.New(recoverDB.Reader()).GetJob(ctx, interrupted.ID)
	if e != nil || newJob.State != "queued" || newJob.LeaseGeneration <= interrupted.LeaseGeneration {
		t.Fatal("old lease generation survived", newJob, e)
	}
	recoveryClient, e = recoveryClient.AtLibrary(actual.StorageID, actual.LibraryID, actual.VolumeID)
	if e != nil {
		t.Fatal(e)
	}
	ioManager, e := objectio.New(recoverDB, recoveryClient, 2)
	if e != nil {
		t.Fatal(e)
	}
	coordinator, e := ingestion.NewCoordinator(recoverDB, ioManager)
	if e != nil {
		t.Fatal(e)
	}
	if out, e := coordinator.Complete(ctx, ready.ID); e != nil || out.State != "ready" || out.ItemID != published.ItemID {
		t.Fatal("ready Complete replay lost deleted-session mapping", out, e)
	}
	if out, e := coordinator.RecoverSession(ctx, partial.ID); e != nil || out.Session == nil || len(out.Session.Parts) != 1 || out.Session.Parts[0].ETag != parts[0].ETag {
		t.Fatal("restored partial recovery", out, e)
	}
	tail := []byte("correct")
	tailSum := sha256.Sum256(tail)
	if _, e = coordinator.UploadPart(ctx, partial.ID, 1, ingestion.PartBinding{Number: 2, Size: 7, SHA256: base64.StdEncoding.EncodeToString(tailSum[:])}, bytes.NewReader(tail)); e != nil {
		t.Fatal(e)
	}
	if out, e := coordinator.Complete(ctx, partial.ID); e != nil || out.Session == nil || out.Session.State != "queued" {
		t.Fatal("fresh Complete did not verify and queue", out, e)
	}
	whole := append(append([]byte{}, partialBytes...), tail...)
	fullSum := sha256.Sum256(whole)
	if _, e = recoveryClient.Verify(ctx, partial.IncomingKey, int64(len(whole)), hex.EncodeToString(fullSum[:])); e != nil {
		t.Fatal(e)
	}
	// Lost source remains an explicit recoverable error; no age deletion, new MPU,
	// cancellation or implicit part replacement is permitted after a restore.
	if e = recoveryClient.Abort(ctx, idle.IncomingKey, idle.MultipartUploadID.String); e != nil {
		t.Fatal(e)
	}
	if _, e = coordinator.Refresh(ctx, idle.ID); e == nil {
		t.Fatal("lost MPU reported usable")
	}
	if out, e := ingestion.Lookup(ctx, recoverDB.Reader(), idle.ID); e != nil || out.Session == nil || out.Session.MultipartUploadID.String != idle.MultipartUploadID.String {
		t.Fatal("old lost-MPU session deleted/rebound", out, e)
	}
	processorConfig := media.DefaultConfig()
	processorConfig.MaxOutputBytes = 1 << 20
	processor, e := media.New(processorConfig)
	if e != nil {
		t.Fatal(e)
	}
	sandbox, e := scratch.Open(scratch.Config{Root: t.TempDir(), MaxBytes: 20 << 20})
	if e != nil {
		t.Fatal(e)
	}
	defer sandbox.Close()
	posters, e := poster.New(recoverDB, ioManager)
	if e != nil {
		t.Fatal(e)
	}
	runner, e := worker.New(recoverDB, map[domain.JobKind]worker.Handler{domain.JobIngest: worker.Ingestion(coordinator, processor, sandbox), domain.JobPoster: worker.Poster(posters, recoverDB, processor, sandbox), domain.JobCleanup: worker.Cleanup(coordinator, recoverDB, posters)}, worker.Config{})
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 20; i++ {
		ran, e := runner.RunOne(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if !ran {
			break
		}
	}
	if out, e := ingestion.Lookup(ctx, recoverDB.Reader(), queued.ID); e != nil || out.State != "ready" {
		t.Fatal("queued native recovery did not publish", out, e)
	}
	if out, e := ingestion.Lookup(ctx, recoverDB.Reader(), failed.ID); e != nil || out.Session == nil || out.Session.State != "failed" || out.Session.ErrorCode.String != "invalid_media" {
		t.Fatal("old failed input lost/retried", out, e)
	}
	failedHash := sha256.Sum256(failedData)
	if _, e = recoveryClient.Verify(ctx, failed.IncomingKey, int64(len(failedData)), hex.EncodeToString(failedHash[:])); e != nil {
		t.Fatal("failed source deleted", e)
	}
	var receipts, sessions int
	if e = recoverDB.Reader().QueryRow("SELECT count(*) FROM cleanup_receipts WHERE upload_id IN (?,?)", ready.ID, cancelled.ID).Scan(&receipts); e != nil || receipts != 0 {
		t.Fatal("independent ready/cancel cleanup not recovered", receipts, e)
	}
	if e = recoverDB.Reader().QueryRow("SELECT count(*) FROM upload_sessions WHERE id IN (?,?,?)", ready.ID, cancelled.ID, queued.ID).Scan(&sessions); e != nil || sessions != 0 {
		t.Fatal("ready/cancel upload history retained", sessions, e)
	}
	if _, e = recoveryClient.Head(ctx, ready.IncomingKey); e == nil {
		t.Fatal("ready incoming not cleaned")
	}
	if _, e = recoveryClient.Verify(ctx, primary.ObjectKey, primary.SizeBytes, primary.Sha256); e != nil {
		t.Fatal("cleanup removed ready primary", e)
	}
	if _, e = recoveryClient.Head(ctx, candidate.ObjectKey); e == nil {
		t.Fatal("old verified candidate was not independently cleaned after a newer publication")
	}
	// Baseline never ran app recovery: it still has the original durable incoming,
	// original part list and independent receipts after second-copy writes.
	f.start(baseline)
	if got := inventory(t, f.client(baseline)); !reflect.DeepEqual(got, expectedObjects) {
		t.Fatal("recovery writes reached baseline")
	}
	docker(t, "stop", "--time", "20", baseline)
	f.host(true, "verify", "--image", f.toolImage, "--archive", archive)
	t.Logf("Linux native drill verified %d object full hashes/HEAD/Range/metadata, %d confirmed MPU part, %d archived entries and %d native bytes; cold backup/restore/recovery %s; total %s", len(expectedObjects), len(parts), backup.FileCount, backup.Bytes, time.Since(backupStart).Round(time.Millisecond), time.Since(started).Round(time.Millisecond))
}
