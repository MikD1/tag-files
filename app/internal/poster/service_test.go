package poster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var testContext = context.Background()
var testTime = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

const testEpoch = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func fixture(t *testing.T, source []byte) (*store.Store, dbgen.Job, dbgen.Asset) {
	t.Helper()
	db, err := store.Open(testContext, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	var primary dbgen.Asset
	err = db.Write(testContext, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		library, err := q.GetLibraryMeta(testContext)
		if err != nil {
			return err
		}
		var itemID int64
		if err := tx.QueryRow("INSERT INTO items(media_kind,uploaded_at_utc,state) VALUES('Image',?,'importing') RETURNING id", store.Timestamp(testTime)).Scan(&itemID); err != nil {
			return err
		}
		id := domain.NewUUID()
		_, err = tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,width,height,rotation,provenance,state,media_kind) VALUES(?,?,'primary','tag-files',?,?,?,'image/png',64,48,0,'legacy','published','Image')", id, itemID, "primary/"+library.LibraryID+"/"+id+"/media.png", len(source), digest(source))
		if err != nil {
			return err
		}
		if err := q.MarkItemReady(testContext, dbgen.MarkItemReadyParams{ID: itemID, PrimaryAssetID: sql.NullString{String: id, Valid: true}}); err != nil {
			return err
		}
		primary, err = q.GetCleanupAsset(testContext, id)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(Payload{Version: 1, ItemID: itemID, PrimaryAssetID: id, Profile: Profile})
		_, err = q.CreateJob(testContext, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: strconv.FormatInt(itemID, 10), DedupeKey: "poster/" + id + "/" + Profile, PayloadJson: string(payload), NextAttemptAt: store.Timestamp(testTime), CreatedAt: store.Timestamp(testTime)})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobPoster}, testTime, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return db, job, primary
}
func posterOutput() domain.MediaOutput {
	return domain.MediaOutput{Kind: domain.Image, ContentType: "image/jpeg", Size: 6, SHA256: digest([]byte("poster")), Width: 64, Height: 48}
}
func count(t *testing.T, db *store.Store, table string) int {
	t.Helper()
	var n int
	if err := db.Reader().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

type peer struct {
	mu            sync.Mutex
	objects       map[string][]byte
	puts, deletes int
	mode          string
	epoch         string
}

func service(t *testing.T, db *store.Store, p *peer) *Service {
	t.Helper()
	if p.epoch == "" {
		p.epoch = testEpoch
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
			t.Error("unfenced poster write")
		}
		key := strings.TrimPrefix(r.URL.Path, "/tag-files/")
		switch r.Method {
		case "PUT":
			p.puts++
			data, _ := io.ReadAll(r.Body)
			var n int
			if err := db.Reader().QueryRow("SELECT count(*) FROM assets WHERE object_key=? AND role='poster' AND state='candidate' AND size_bytes=? AND sha256=?", key, len(data), digest(data)).Scan(&n); err != nil || n != 1 {
				t.Error("PUT preceded immutable poster candidate")
			}
			if count(t, db, "cleanup_receipts") < 1 || count(t, db, "object_write_intents") < 1 {
				t.Error("PUT preceded independent recovery records")
			}
			if err := db.Write(testContext, func(tx *sql.Tx) error {
				_, err := tx.Exec("INSERT INTO tags(name) VALUES('poster IO has no SQL lock')")
				return err
			}); err != nil {
				t.Error(err)
			}
			p.objects[key] = data
			if p.mode == "lost" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
		case "HEAD":
			data, ok := p.objects[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		case "GET":
			data, ok := p.objects[key]
			if !ok {
				w.WriteHeader(404)
				return
			}
			if p.mode == "corrupt" {
				data = []byte("broken")
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
		case "DELETE":
			p.deletes++
			delete(p.objects, key)
		default:
			t.Errorf("unexpected poster request %s %s", r.Method, r.URL)
		}
	}))
	t.Cleanup(server.Close)
	client, err := objectstore.New(objectstore.Config{Endpoint: server.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "private", SecretKey: "private", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, client, 3)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestNativePosterPipelineStreamsVerifiedPrimaryAndPublishesIndependently(t *testing.T) {
	if os.Getenv("TAG_FILES_MEDIA_INTEGRATION") != "1" {
		t.Skip("requires actual media tools")
	}
	for _, mode := range []string{"success", "wrong-source", "no-space"} {
		t.Run(mode, func(t *testing.T) {
			var source bytes.Buffer
			if err := png.Encode(&source, image.NewNRGBA(image.Rect(0, 0, 64, 48))); err != nil {
				t.Fatal(err)
			}
			db, job, primary := fixture(t, source.Bytes())
			p := &peer{objects: map[string][]byte{primary.ObjectKey: append([]byte(nil), source.Bytes()...)}}
			s := service(t, db, p)
			if mode == "wrong-source" {
				p.objects[primary.ObjectKey][0] ^= 1
			}
			config := media.DefaultConfig()
			config.MaxOutputBytes = 1024 * 1024
			processor, err := media.New(config)
			if err != nil {
				t.Fatal(err)
			}
			quota := int64(4 * 1024 * 1024)
			if mode == "no-space" {
				quota = 10
			}
			sandbox, err := scratch.Open(scratch.Config{Root: t.TempDir(), MaxBytes: quota})
			if err != nil {
				t.Fatal(err)
			}
			defer sandbox.Close()
			err = s.Process(testContext, job, processor, sandbox, testTime)
			if mode != "success" {
				if err == nil || p.puts != 0 || count(t, db, "assets") != 1 || count(t, db, "jobs") != 1 {
					t.Fatalf("failed poster mutated publication: %v", err)
				}
			} else {
				if err != nil || p.puts != 1 || count(t, db, "assets") != 2 || count(t, db, "jobs") != 0 || count(t, db, "cleanup_receipts") != 0 {
					t.Fatalf("poster pipeline not finished: %v", err)
				}
			}
			item, err := dbgen.New(db.Reader()).GetItem(testContext, primary.ItemID.Int64)
			if err != nil || item.PrimaryAssetID.String != primary.ID || item.State != domain.ItemReady || count(t, db, "upload_sessions") != 0 {
				t.Fatal("poster path disturbed ready item")
			}
		})
	}
}

func TestPosterPublicationRollsBackIfCleanupCommitFails(t *testing.T) {
	db, job, _ := fixture(t, []byte("source"))
	s := service(t, db, &peer{})
	a, err := s.NewCandidate(testContext, job, posterOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(testContext, job, a.ID, bytes.NewBufferString("poster"), testTime); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(testContext, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TRIGGER fail_poster_commit BEFORE DELETE ON cleanup_receipts BEGIN SELECT RAISE(ABORT,'fixture'); END")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(testContext, job, a.ID, testTime); err == nil {
		t.Fatal("commit failure reported success")
	}
	current, err := dbgen.New(db.Reader()).GetCleanupAsset(testContext, a.ID)
	if err != nil || current.State != domain.AssetVerified || current.ItemID.Valid || count(t, db, "jobs") != 2 || count(t, db, "cleanup_receipts") != 1 {
		t.Fatal("publication transaction partially committed")
	}
	if err := db.Write(testContext, func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER fail_poster_commit"); return err }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(testContext, job, a.ID, testTime); err != nil {
		t.Fatal(err)
	}
}

func TestCandidatePersistsImmutableBytesAndIndependentCleanupForLegacyItem(t *testing.T) {
	db, job, primary := fixture(t, []byte("source"))
	s := service(t, db, &peer{})
	a, err := s.NewCandidate(testContext, job, posterOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.NewCandidate(testContext, job, posterOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID || a.ObjectKey == b.ObjectKey || a.ItemID.Valid || a.UploadID.Valid || a.JobID.String != job.ID || a.LeaseGeneration != job.LeaseGeneration || a.Role != "poster" || a.ProfileVersion.String != Profile || a.Sha256 != posterOutput().SHA256 || a.State != domain.AssetCandidate {
		t.Fatalf("candidate not isolated: %+v %+v", a, b)
	}
	if count(t, db, "cleanup_receipts") != 2 || count(t, db, "jobs") != 3 || count(t, db, "upload_sessions") != 0 {
		t.Fatal("candidate lacks independent recovery or recreated upload")
	}
	if _, err := s.NewCandidate(testContext, job, posterOutput(), testTime.Add(2*time.Hour)); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("expired generation allocated: %v", err)
	}
	item, err := dbgen.New(db.Reader()).GetItem(testContext, primary.ItemID.Int64)
	if err != nil || item.State != domain.ItemReady || item.PrimaryAssetID.String != primary.ID {
		t.Fatal("allocation changed primary")
	}
}

func TestPosterPublicationVerifiesBytesAndAtomicallyRemovesOnlyItsCleanup(t *testing.T) {
	for _, mode := range []string{"success", "lost", "corrupt", "preexisting-corrupt"} {
		t.Run(mode, func(t *testing.T) {
			db, job, primary := fixture(t, []byte("source"))
			p := &peer{mode: mode}
			s := service(t, db, p)
			a, err := s.NewCandidate(testContext, job, posterOutput(), testTime)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "preexisting-corrupt" {
				p.objects[a.ObjectKey] = []byte("broken")
			}
			got, err := s.Upload(testContext, job, a.ID, bytes.NewBufferString("poster"), testTime)
			if mode == "lost" {
				if err == nil || count(t, db, "object_write_intents") != 1 {
					t.Fatal("lost PUT settled")
				}
				got, err = s.Upload(testContext, job, a.ID, bytes.NewBufferString("poster"), testTime)
				if err != nil || p.puts != 1 || count(t, db, "object_write_intents") != 1 {
					t.Fatalf("lost PUT blindly repeated: %v puts=%d", err, p.puts)
				}
			}
			if mode == "corrupt" || mode == "preexisting-corrupt" {
				if !errors.Is(err, objectstore.ErrIntegrity) {
					t.Fatalf("corrupt candidate verified: %v", err)
				}
				if _, err := s.Publish(testContext, job, a.ID, testTime); err == nil {
					t.Fatal("unverified poster published")
				}
				if mode == "preexisting-corrupt" && p.puts != 0 {
					t.Fatal("mismatching object overwritten")
				}
				return
			}
			if err != nil || got.State != domain.AssetVerified {
				t.Fatalf("verification: %+v %v", got, err)
			}
			if _, err := s.Publish(testContext, job, a.ID, testTime); err != nil {
				t.Fatal(err)
			}
			if count(t, db, "jobs") != 0 || count(t, db, "cleanup_receipts") != 0 || count(t, db, "upload_sessions") != 0 {
				t.Fatal("publication not atomic")
			}
			published, err := dbgen.New(db.Reader()).GetCleanupAsset(testContext, a.ID)
			if err != nil || published.ItemID != primary.ItemID || published.State != domain.AssetPublished {
				t.Fatalf("poster not attached: %+v %v", published, err)
			}
			if _, err := s.Publish(testContext, job, a.ID, testTime); err != nil {
				t.Fatalf("publication lost response not replayable: %v", err)
			}
			if mode == "lost" {
				if err := s.ReconcileWrites(testContext, a.ID); !errors.Is(err, objectio.ErrUnsettled) {
					t.Fatalf("published unknown writer settled at same epoch: %v", err)
				}
				p.mu.Lock()
				p.epoch = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
				p.mu.Unlock()
				if err := s.ReconcileWrites(testContext, a.ID); err != nil {
					t.Fatal(err)
				}
				if count(t, db, "object_write_intents") != 0 || p.puts != 1 {
					t.Fatal("published reconciliation lost attribution or rewrote bytes")
				}
			}
			item, err := dbgen.New(db.Reader()).GetItem(testContext, primary.ItemID.Int64)
			if err != nil || item.PrimaryAssetID.String != primary.ID || item.State != domain.ItemReady || item.Version != 2 {
				t.Fatal("primary changed or poster version did not advance exactly once")
			}
		})
	}
}

func TestTombstoneAndRemovedPosterJobFenceLatePublication(t *testing.T) {
	db, job, primary := fixture(t, []byte("source"))
	p := &peer{}
	s := service(t, db, p)
	a, err := s.NewCandidate(testContext, job, posterOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(testContext, job, a.ID, bytes.NewBufferString("poster"), testTime); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(testContext, func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE items SET state='deleting' WHERE id=?", primary.ItemID.Int64); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM jobs WHERE id=?", job.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(testContext, job, a.ID, testTime); err == nil {
		t.Fatal("late poster attached to tombstone")
	}
	if err := s.ProcessCleanup(testContext, claimCleanup(t, db), testTime); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "assets") != 1 || count(t, db, "cleanup_receipts") != 0 || count(t, db, "jobs") != 0 {
		t.Fatal("late poster not independently removed")
	}
}

func TestStalePosterLeaseCannotPublishAndCleanupNeverRemovesNewerWinner(t *testing.T) {
	db, old, primary := fixture(t, []byte("source"))
	p := &peer{}
	s := service(t, db, p)
	a, err := s.NewCandidate(testContext, old, posterOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(testContext, old, a.ID, bytes.NewBufferString("poster"), testTime); err != nil {
		t.Fatal(err)
	}
	now := testTime.Add(2 * time.Hour)
	newer, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobPoster}, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(testContext, old, a.ID, testTime); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("clock rollback revived stale publisher: %v", err)
	}
	b, err := s.NewCandidate(testContext, newer, posterOutput(), now)
	if err != nil {
		t.Fatal(err)
	}
	if b.ObjectKey == a.ObjectKey {
		t.Fatal("new generation reused bytes")
	}
	if _, err := s.Upload(testContext, newer, b.ID, bytes.NewBufferString("poster"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Publish(testContext, newer, b.ID, now); err != nil {
		t.Fatal(err)
	}
	cleanupJob, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobCleanup}, now.Add(time.Second), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessCleanup(testContext, cleanupJob, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "assets") != 2 || count(t, db, "jobs") != 0 {
		t.Fatal("stale cleanup did not finish exactly its own candidate")
	}
	p.mu.Lock()
	_, oldExists := p.objects[a.ObjectKey]
	_, newExists := p.objects[b.ObjectKey]
	p.mu.Unlock()
	if oldExists || !newExists {
		t.Fatal("cleanup removed newer winner")
	}
	item, err := dbgen.New(db.Reader()).GetItem(testContext, primary.ItemID.Int64)
	if err != nil || item.State != domain.ItemReady || item.PrimaryAssetID.String != primary.ID {
		t.Fatal("primary changed")
	}
}
