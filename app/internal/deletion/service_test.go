package deletion

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

const epoch1 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const epoch2 = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const epoch3 = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

type peer struct {
	mu          sync.Mutex
	objects     map[string][]byte
	uploads     map[string][]string
	epoch, mode string
	deletes     int
	aborts      int
}

func service(t *testing.T, db *store.Store, p *peer) *Service {
	t.Helper()
	if p.epoch == "" {
		p.epoch = epoch1
	}
	if p.objects == nil {
		p.objects = map[string][]byte{}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		w.Header().Set("X-Tag-Files-Storage-Epoch", p.epoch)
		if r.Method == "HEAD" && r.URL.Path == "/tag-files" {
			return
		}
		if r.Header.Get("X-Tag-Files-Expected-Storage-Epoch") != p.epoch {
			t.Error("unfenced deletion")
		}
		key := strings.TrimPrefix(r.URL.Path, "/tag-files/")
		switch r.Method {
		case "GET":
			if r.URL.Query().Has("uploads") {
				w.Header().Set("Content-Type", "application/xml")
				fmt.Fprint(w, "<ListMultipartUploadsResult><Bucket>tag-files</Bucket><IsTruncated>false</IsTruncated>")
				for key, ids := range p.uploads {
					for _, id := range ids {
						fmt.Fprintf(w, "<Upload><Key>%s</Key><UploadId>%s</UploadId></Upload>", key, id)
					}
				}
				fmt.Fprint(w, "</ListMultipartUploadsResult>")
				return
			}
			t.Errorf("unexpected deletion GET %s", r.URL)
		case "HEAD":
			data, ok := p.objects[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		case "DELETE":
			if count(t, db, "deletion_assets") == 0 || count(t, db, "cleanup_receipts") == 0 {
				t.Error("DELETE preceded independent ownership")
			}
			if err := db.Write(ctx, func(tx *sql.Tx) error {
				_, err := tx.Exec("INSERT INTO tags(name) VALUES('deletion does not hold SQL during S3')")
				return err
			}); err != nil {
				t.Error(err)
			}
			if id := r.URL.Query().Get("uploadId"); id != "" {
				p.aborts++
				ids := p.uploads[key]
				remaining := make([]string, 0, len(ids))
				for _, v := range ids {
					if v != id {
						remaining = append(remaining, v)
					}
				}
				p.uploads[key] = remaining
				return
			}
			p.deletes++
			delete(p.objects, key)
			if p.mode == "lost-delete" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
		default:
			t.Errorf("unexpected deletion request %s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(server.Close)
	client, err := objectstore.New(objectstore.Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "private", SecretKey: "private", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, client, 2)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func claim(t *testing.T, db *store.Store) dbgen.Job {
	t.Helper()
	job, err := db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobDelete}, now.Add(time.Second), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return job
}
func keyFor(t *testing.T, db *store.Store, asset string) string {
	t.Helper()
	a, err := dbgen.New(db.Reader()).GetCleanupAsset(ctx, asset)
	if err != nil {
		t.Fatal(err)
	}
	return a.ObjectKey
}

func TestDeleteRetainsKeysThroughRestartAndReDeletesResurrectedObject(t *testing.T) {
	db, id, asset := fixture(t)
	key := keyFor(t, db, asset)
	p := &peer{objects: map[string][]byte{key: []byte("content"), "unknown/foreign": []byte("keep")}}
	s := service(t, db, p)
	result := request(t, db, id, 1)
	job := claim(t, db)
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, ErrAwaitingRestart) {
		t.Fatalf("deletion not retained for restart: %v", err)
	}
	if count(t, db, "items") != 0 || count(t, db, "assets") != 0 || count(t, db, "deletion_assets") != 1 || count(t, db, "cleanup_receipts") != 1 {
		t.Fatal("metadata or restart inventory has wrong lifetime")
	}
	if again := request(t, db, id, 1); again != result {
		t.Fatal("lost response lost deletion identity")
	}
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, ErrAwaitingRestart) {
		t.Fatal(err)
	}
	if p.deletes != 1 {
		t.Fatal("absent object was unnecessarily re-deleted")
	}
	p.mu.Lock()
	p.epoch = epoch2
	p.objects[key] = []byte("resurrected")
	p.mu.Unlock()
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, ErrAwaitingRestart) {
		t.Fatalf("new DELETE forgot its restart obligation: %v", err)
	}
	p.mu.Lock()
	p.epoch = epoch3
	p.mu.Unlock()
	if err := s.Process(ctx, job, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "jobs") != 0 || count(t, db, "cleanup_receipts") != 0 || count(t, db, "deletion_assets") != 0 {
		t.Fatal("verified work retained as history")
	}
	if !request(t, db, id, 1).Absent {
		t.Fatal("verified deletion not absent")
	}
	p.mu.Lock()
	_, exists := p.objects["unknown/foreign"]
	deleted := p.deletes
	p.mu.Unlock()
	if !exists || deleted != 2 {
		t.Fatal("foreign object affected or resurrected object not deleted")
	}
}

func TestUnknownPrimaryPutKeepsTombstoneUntilWriterQuiesces(t *testing.T) {
	db, id, asset := fixture(t)
	key := keyFor(t, db, asset)
	p := &peer{objects: map[string][]byte{key: []byte("content")}}
	s := service(t, db, p)
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE assets SET job_id=?,lease_generation=1 WHERE id=?", domain.NewUUID(), asset); err != nil {
			return err
		}
		_, err := dbgen.New(tx).CreateObjectWriteIntent(ctx, dbgen.CreateObjectWriteIntentParams{ID: domain.NewUUID(), OwnerID: domain.NewUUID(), TargetKind: "asset", TargetID: asset, Generation: 1, Operation: "put_object", ObjectKey: key, StorageEpoch: epoch1, PayloadJson: `{"size":7,"sha256":"` + strings.Repeat("a", 64) + `","contentType":"audio/flac"}`, CreatedAtUtc: store.Timestamp(now)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	request(t, db, id, 1)
	job := claim(t, db)
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("same epoch settled unknown PUT: %v", err)
	}
	if count(t, db, "items") != 1 || count(t, db, "assets") != 1 || count(t, db, "object_write_intents") != 1 {
		t.Fatal("unknown writer lost attribution")
	}
	p.mu.Lock()
	p.epoch = epoch2
	p.objects[key] = []byte("lateput")
	p.mu.Unlock()
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, ErrAwaitingRestart) {
		t.Fatalf("late object not retained for DELETE verification: %v", err)
	}
	if count(t, db, "items") != 0 || count(t, db, "object_write_intents") != 0 {
		t.Fatal("quiesced writer not reconciled")
	}
	p.mu.Lock()
	p.epoch = epoch3
	p.mu.Unlock()
	if err := s.Process(ctx, job, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestLostDeleteResponsePersistsEpochBeforeForwardingAndRecovers(t *testing.T) {
	db, id, asset := fixture(t)
	key := keyFor(t, db, asset)
	p := &peer{objects: map[string][]byte{key: []byte("content")}, mode: "lost-delete"}
	s := service(t, db, p)
	result := request(t, db, id, 1)
	job := claim(t, db)
	if err := s.Process(ctx, job, now.Add(time.Second)); err == nil {
		t.Fatal("lost DELETE response reported completed")
	}
	var epoch string
	if err := db.Reader().QueryRow("SELECT absence_epoch FROM deletion_assets WHERE receipt_id=?", result.ReceiptID).Scan(&epoch); err != nil || epoch != epoch1 {
		t.Fatalf("DELETE not journaled before forwarding: %q %v", epoch, err)
	}
	p.mu.Lock()
	p.mode = ""
	p.epoch = epoch2
	p.mu.Unlock()
	if err := s.Process(ctx, job, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "items") != 0 || count(t, db, "cleanup_receipts") != 0 {
		t.Fatal("lost DELETE not recovered")
	}
}

func TestDeletionWaitsForIndependentPosterCandidateCleanup(t *testing.T) {
	db, id, primary := fixture(t)
	key := keyFor(t, db, primary)
	p := &peer{objects: map[string][]byte{key: []byte("content")}}
	s := service(t, db, p)
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE items SET media_kind='Image' WHERE id=?", id); err != nil {
			return err
		}
		_, err := dbgen.New(tx).CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: strconv.FormatInt(id, 10), DedupeKey: "poster/" + primary + "/poster-v1", PayloadJson: `{"version":1,"itemId":` + strconv.FormatInt(id, 10) + `,"primaryAssetId":"` + primary + `","profile":"poster-v1"}`, NextAttemptAt: store.Timestamp(now), CreatedAt: store.Timestamp(now)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	j, err := db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobPoster}, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := poster.New(db, s.io)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ps.NewCandidate(ctx, j, domain.MediaOutput{Kind: domain.Image, ContentType: "image/jpeg", Size: 6, SHA256: strings.Repeat("b", 64), Width: 64, Height: 48}, now)
	if err != nil {
		t.Fatal(err)
	}
	p.objects[a.ObjectKey] = []byte("poster")
	request(t, db, id, 1)
	job := claim(t, db)
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("metadata removed before child cleanup: %v", err)
	}
	if count(t, db, "items") != 1 || count(t, db, "cleanup_receipts") != 2 {
		t.Fatal("child cleanup lost ownership")
	}
	child, err := db.ClaimJobKinds(ctx, []domain.JobKind{domain.JobCleanup}, now.Add(time.Second), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.ProcessCleanup(ctx, child, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, ErrAwaitingRestart) {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.epoch = epoch2
	p.mu.Unlock()
	if err := s.Process(ctx, job, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "items") != 0 || count(t, db, "assets") != 0 || count(t, db, "jobs") != 0 {
		t.Fatal("dependent deletion did not finish")
	}
}

func TestStaleEpochCheckpointCannotReplaceNewerDeletion(t *testing.T) {
	db, id, asset := fixture(t)
	key := keyFor(t, db, asset)
	p := &peer{objects: map[string][]byte{keyFor(t, db, asset): []byte("content")}}
	s := service(t, db, p)
	result := request(t, db, id, 1)
	job := claim(t, db)
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, ErrAwaitingRestart) {
		t.Fatal(err)
	}
	old, err := dbgen.New(db.Reader()).GetDeletionAsset(ctx, dbgen.GetDeletionAssetParams{ReceiptID: result.ReceiptID, ObjectKey: key})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.load(ctx, result.ReceiptID, true)
	if err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return now.Add(time.Second) }
	if err := s.recordEpoch(ctx, job, plan, old, epoch2, clock); err != nil {
		t.Fatal(err)
	}
	if err := s.recordEpoch(ctx, job, plan, old, epoch1, clock); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("old request downgraded checkpoint: %v", err)
	}
	fresh, err := dbgen.New(db.Reader()).GetDeletionAsset(ctx, dbgen.GetDeletionAssetParams{ReceiptID: result.ReceiptID, ObjectKey: key})
	if err != nil || fresh.AbsenceEpoch.String != epoch2 {
		t.Fatal("new checkpoint lost")
	}
}

func TestFutureJournalPagePreventsAnyDelete(t *testing.T) {
	db, id, asset := fixture(t)
	key := keyFor(t, db, asset)
	p := &peer{objects: map[string][]byte{key: []byte("content")}}
	s := service(t, db, p)
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE assets SET lease_generation=1 WHERE id=?", asset); err != nil {
			return err
		}
		for i := 1; i <= 101; i++ {
			version := 1
			if i == 101 {
				version = 2
			}
			if _, err := tx.Exec("INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_version,payload_json,state,created_at_utc) VALUES(?,?,'asset',?,1,'put_object',?,?,?,?,'uncertain',?)", fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i), domain.NewUUID(), asset, key, epoch1, version, `{"size":7,"sha256":"`+strings.Repeat("a", 64)+`","contentType":"audio/flac"}`, store.Timestamp(now)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	request(t, db, id, 1)
	if err := s.Process(ctx, claim(t, db), now.Add(time.Second)); !errors.Is(err, objectio.ErrIncompatibleIntent) {
		t.Fatalf("later future intent guessed: %v", err)
	}
	if p.deletes != 0 || count(t, db, "items") != 1 || count(t, db, "object_write_intents") != 101 {
		t.Fatal("future codec lost data")
	}
}

func TestDeletionRetainsIncomingKeyAfterEarlierReadyCleanup(t *testing.T) {
	db, id, asset := fixture(t)
	key := keyFor(t, db, asset)
	uploadID := domain.NewUUID()
	meta, err := dbgen.New(db.Reader()).GetLibraryMeta(ctx)
	if err != nil {
		t.Fatal(err)
	}
	incoming := "incoming/" + meta.LibraryID + "/" + uploadID + "/source"
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE items SET upload_id=? WHERE id=?", uploadID, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p := &peer{objects: map[string][]byte{key: []byte("content"), incoming: []byte("retained source")}, uploads: map[string][]string{incoming: {"owned-mpu"}, incoming + "-foreign": {"foreign-mpu"}}}
	s := service(t, db, p)
	request(t, db, id, 1)
	job := claim(t, db)
	if count(t, db, "deletion_assets") != 2 {
		t.Fatal("deletion forgot the known incoming key after ready cleanup")
	}
	if err := s.Process(ctx, job, now.Add(time.Second)); !errors.Is(err, ErrAwaitingRestart) {
		t.Fatal(err)
	}
	p.mu.Lock()
	_, exists := p.objects[incoming]
	p.epoch = epoch2
	p.mu.Unlock()
	if exists {
		t.Fatal("incoming bytes not removed")
	}
	if err := s.Process(ctx, job, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	aborts := p.aborts
	foreign := len(p.uploads[incoming+"-foreign"])
	p.mu.Unlock()
	if aborts != 1 || foreign != 1 {
		t.Fatalf("MPU ownership lost: aborts=%d foreign=%d", aborts, foreign)
	}
}
