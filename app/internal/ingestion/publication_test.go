package ingestion

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func processingJob(t *testing.T, db *store.Store) (Session, dbgen.Job) {
	t.Helper()
	session := smallSession(t, db)
	transaction(t, db, func(tx *sql.Tx) error {
		if err := AttachMultipart(testContext, tx, session.ID, 1, "kept"); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE upload_sessions SET state='queued',source_sha256=?,completion_manifest='{}' WHERE id=?", strings.Repeat("a", 64), session.ID)
		if err != nil {
			return err
		}
		_, err = dbgen.New(tx).CreateJob(testContext, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobIngest, SubjectID: session.ID, DedupeKey: "ingest/" + session.ID, PayloadJson: `{"version":1,"uploadId":"` + session.ID + `","generation":1}`, NextAttemptAt: store.Timestamp(testTime), CreatedAt: store.Timestamp(testTime)})
		return err
	})
	job, err := db.ClaimJob(testContext, testTime, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return session, job
}
func primaryOutput() PrimaryOutput {
	sum := sha256.Sum256([]byte("correct"))
	return PrimaryOutput{Kind: domain.Image, Size: 7, SHA256: hex.EncodeToString(sum[:]), ContentType: "image/png", Width: 640, Height: 480}
}
func newPrimary(t *testing.T, c *Coordinator, job dbgen.Job) dbgen.Asset {
	t.Helper()
	asset, err := c.NewPrimary(testContext, job, primaryOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	return asset
}
func publicationPeer(t *testing.T, db *store.Store, mode string) (*Coordinator, *int) {
	t.Helper()
	puts := new(int)
	exists := false
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "PUT":
			*puts++
			key := strings.TrimPrefix(r.URL.Path, "/tag-files/")
			var size int64
			var sum, state string
			if err := db.Reader().QueryRow("SELECT size_bytes,sha256,state FROM assets WHERE object_key=?", key).Scan(&size, &sum, &state); err != nil || size != 7 || sum != primaryOutput().SHA256 || state != "candidate" {
				t.Error("PUT preceded immutable candidate")
			}
			if tableCount(t, db, "object_write_intents") < 1 {
				t.Error("PUT preceded independent intent")
			}
			if err := db.Write(testContext, func(tx *sql.Tx) error {
				_, err := tx.Exec("INSERT INTO tags(name) VALUES('primary IO has no SQL transaction')")
				return err
			}); err != nil {
				t.Error(err)
			}
			_, _ = io.Copy(io.Discard, r.Body)
			exists = true
			if mode == "lost-put" {
				conn, _, _ := w.(http.Hijacker).Hijack()
				conn.Close()
				return
			}
			w.Header().Set("ETag", "candidate")
		case "HEAD":
			if !exists && mode != "preexisting-corrupt" {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Content-Length", "7")
			w.Header().Set("ETag", "candidate")
		case "GET":
			w.Header().Set("Content-Length", "7")
			w.Header().Set("ETag", "candidate")
			if mode == "corrupt" || mode == "preexisting-corrupt" {
				io.WriteString(w, "corrupt")
			} else {
				io.WriteString(w, "correct")
			}
		default:
			t.Errorf("unexpected publication request %s %s", r.Method, r.URL)
		}
	}))
	return c, puts
}

func TestPrimaryCandidatePersistsExactOutputAndAlwaysUsesUniqueLeaseKeys(t *testing.T) {
	db := database(t)
	session, job := processingJob(t, db)
	c := coordinator(t, db, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("allocation performed S3 IO") }))
	first := newPrimary(t, c, job)
	second := newPrimary(t, c, job)
	if first.ID == second.ID || first.ObjectKey == second.ObjectKey || first.UploadID.String != session.ID || first.JobID.String != job.ID || first.LeaseGeneration != job.LeaseGeneration || first.State != domain.AssetCandidate || first.SizeBytes != 7 || first.Sha256 != primaryOutput().SHA256 || first.ContentType != "image/png" || first.Width.Int64 != 640 || first.Height.Int64 != 480 {
		t.Fatalf("candidate facts: %+v %+v", first, second)
	}
	var library string
	if err := db.Reader().QueryRow("SELECT library_id FROM library_meta").Scan(&library); err != nil {
		t.Fatal(err)
	}
	if first.ObjectKey != "primary/"+library+"/"+first.ID+"/media.png" {
		t.Fatal("candidate key reused client filename or attempt bytes")
	}
	if _, err := c.NewPrimary(testContext, job, primaryOutput(), testTime.Add(2*time.Hour)); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("expired lease allocated output: %v", err)
	}
	newer, err := db.ClaimJob(testContext, testTime.Add(2*time.Hour), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.NewPrimary(testContext, job, primaryOutput(), testTime); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("clock rollback revived old generation: %v", err)
	}
	third, err := c.NewPrimary(testContext, newer, primaryOutput(), testTime.Add(2*time.Hour))
	if err != nil || third.ObjectKey == first.ObjectKey {
		t.Fatal("new lease reused a candidate key", err)
	}
}

func TestPrimaryUploadVerifiesEveryByteAndLostResponseRecoveryNeverRewrites(t *testing.T) {
	for _, mode := range []string{"success", "lost-put", "corrupt", "preexisting-corrupt"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			_, job := processingJob(t, db)
			c, puts := publicationPeer(t, db, mode)
			asset := newPrimary(t, c, job)
			got, err := c.UploadPrimary(testContext, job, asset.ID, bytes.NewBufferString("correct"), testTime)
			switch mode {
			case "success":
				if err != nil || got.State != domain.AssetVerified || *puts != 1 {
					t.Fatalf("verified upload: %+v %v", got, err)
				}
			case "lost-put":
				if err == nil || tableCount(t, db, "object_write_intents") != 1 {
					t.Fatal("lost PUT was reported settled")
				}
				got, err = c.UploadPrimary(testContext, job, asset.ID, bytes.NewBufferString("correct"), testTime)
				if err != nil || got.State != domain.AssetVerified || *puts != 1 || tableCount(t, db, "object_write_intents") != 1 {
					t.Fatalf("read recovery rewrote/settled unknown PUT: %+v %v puts=%d", got, err, *puts)
				}
			default:
				if err == nil {
					t.Fatal("corrupt stored bytes marked verified")
				}
				var state string
				if err := db.Reader().QueryRow("SELECT state FROM assets WHERE id=?", asset.ID).Scan(&state); err != nil || state != "candidate" {
					t.Fatal("integrity failure published candidate")
				}
				if mode == "preexisting-corrupt" && *puts != 0 {
					t.Fatal("mismatching existing object overwritten")
				}
			}
		})
	}
}

func TestReadyPublicationAtomicallyRemovesUploadAndQueuesIndependentCleanupAndPoster(t *testing.T) {
	db := database(t)
	session, job := processingJob(t, db)
	c, _ := publicationPeer(t, db, "success")
	asset := newPrimary(t, c, job)
	if _, err := c.UploadPrimary(testContext, job, asset.ID, bytes.NewBufferString("correct"), testTime); err != nil {
		t.Fatal(err)
	}
	result, err := c.PublishPrimary(testContext, job, asset.ID, testTime)
	if err != nil || result.State != "ready" || result.ItemID < 1 || result.Session != nil {
		t.Fatalf("ready: %+v %v", result, err)
	}
	if tableCount(t, db, "upload_sessions") != 0 || tableCount(t, db, "upload_parts") != 0 || tableCount(t, db, "cleanup_receipts") != 1 || tableCount(t, db, "jobs") != 2 {
		t.Fatal("ready commit did not atomically separate visible and background work")
	}
	var filenameCount, ingestCount int
	if err := db.Reader().QueryRow("SELECT COUNT(*) FROM jobs WHERE type='ingest'").Scan(&ingestCount); err != nil || ingestCount != 0 {
		t.Fatal("finished ingestion retained as history")
	}
	if err := db.Reader().QueryRow("SELECT COUNT(*) FROM jobs WHERE payload_json LIKE '%photo.jpg%'").Scan(&filenameCount); err != nil || filenameCount != 0 {
		t.Fatal("ready retained client filename")
	}
	again, err := c.PublishPrimary(testContext, job, asset.ID, testTime)
	if err != nil || again.ItemID != result.ItemID || tableCount(t, db, "items") != 1 || tableCount(t, db, "jobs") != 2 {
		t.Fatal("ready replay re-created work", err)
	}
	outcome, err := c.Complete(testContext, session.ID)
	if err != nil || outcome.ItemID != result.ItemID || outcome.State != "ready" {
		t.Fatal("ready lookup lost origin UUID", err)
	}
	if cancelled := cancelled(t, db, session.ID); cancelled.State != "ready" || cancelled.ItemID != result.ItemID {
		t.Fatal("cancel removed published item")
	}
}

func TestReadyFailureAndCancelCannotLeavePartialItemsOrResurrectWork(t *testing.T) {
	for _, mode := range []string{"cleanup-fails", "poster-fails", "cancel-wins", "lease-changes"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session, job := processingJob(t, db)
			c, _ := publicationPeer(t, db, "success")
			asset := newPrimary(t, c, job)
			if _, err := c.UploadPrimary(testContext, job, asset.ID, bytes.NewBufferString("correct"), testTime); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "cleanup-fails":
				transaction(t, db, func(tx *sql.Tx) error {
					_, err := tx.Exec(`CREATE TRIGGER fail_ready_cleanup BEFORE INSERT ON cleanup_receipts BEGIN SELECT RAISE(ABORT,'cleanup failure'); END`)
					return err
				})
			case "poster-fails":
				transaction(t, db, func(tx *sql.Tx) error {
					_, err := tx.Exec(`CREATE TRIGGER fail_ready_poster BEFORE INSERT ON jobs WHEN NEW.type='poster' BEGIN SELECT RAISE(ABORT,'poster failure'); END`)
					return err
				})
			case "cancel-wins":
				cancelled(t, db, session.ID)
			case "lease-changes":
				if _, err := db.ClaimJob(testContext, testTime.Add(2*time.Hour), time.Hour); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.PublishPrimary(testContext, job, asset.ID, testTime); err == nil {
				t.Fatal("failed/fenced ready commit reported success")
			}
			if tableCount(t, db, "items") != 0 {
				t.Fatal("partial or cancelled ready item escaped rollback")
			}
			if mode == "cleanup-fails" || mode == "poster-fails" {
				if tableCount(t, db, "upload_sessions") != 1 || tableCount(t, db, "cleanup_receipts") != 0 || tableCount(t, db, "jobs") != 1 {
					t.Fatal("failed ready commit removed input or left partial jobs")
				}
			}
		})
	}
}

func TestPublishedPrimaryKeepsUnknownPutUntilIndependentReconciliation(t *testing.T) {
	db := database(t)
	_, job := processingJob(t, db)
	c, _ := publicationPeer(t, db, "lost-put")
	asset := newPrimary(t, c, job)
	if _, err := c.UploadPrimary(testContext, job, asset.ID, bytes.NewBufferString("correct"), testTime); err == nil {
		t.Fatal("lost reply did not inject uncertainty")
	}
	if _, err := c.UploadPrimary(testContext, job, asset.ID, nil, testTime); err != nil {
		t.Fatal(err)
	}
	if _, err := c.PublishPrimary(testContext, job, asset.ID, testTime); err != nil {
		t.Fatal(err)
	}
	if err := c.ReconcilePrimaryWrites(testContext, asset.ID); !errors.Is(err, objectio.ErrUnsettled) || tableCount(t, db, "object_write_intents") != 1 {
		t.Fatalf("ready silently settled a same-epoch writer: %v", err)
	}
}

func TestReadyAndCancelRaceHasOneAuthoritativeOutcome(t *testing.T) {
	for range 12 {
		db := database(t)
		session, job := processingJob(t, db)
		c, _ := publicationPeer(t, db, "success")
		asset := newPrimary(t, c, job)
		if _, err := c.UploadPrimary(testContext, job, asset.ID, bytes.NewBufferString("correct"), testTime); err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		published := make(chan struct {
			result Outcome
			err    error
		}, 1)
		cancelledResult := make(chan struct {
			result Cancellation
			err    error
		}, 1)
		go func() {
			<-start
			result, err := c.PublishPrimary(testContext, job, asset.ID, testTime)
			published <- struct {
				result Outcome
				err    error
			}{result, err}
		}()
		go func() {
			<-start
			var result Cancellation
			err := db.Write(testContext, func(tx *sql.Tx) error {
				var err error
				result, err = Cancel(testContext, tx, session.ID, testTime)
				return err
			})
			cancelledResult <- struct {
				result Cancellation
				err    error
			}{result, err}
		}()
		close(start)
		p, cancel := <-published, <-cancelledResult
		if cancel.err != nil || tableCount(t, db, "upload_sessions") != 0 {
			t.Fatal("race left visible work", cancel.err)
		}
		if cancel.result.State == "ready" {
			if p.err != nil || p.result.State != "ready" || p.result.ItemID != cancel.result.ItemID || tableCount(t, db, "items") != 1 {
				t.Fatalf("ready winner lost: %+v %+v", p, cancel)
			}
		} else if cancel.result.State == "cancelled" {
			if p.err == nil || tableCount(t, db, "items") != 0 {
				t.Fatalf("cancel winner resurrected: %+v %+v", p, cancel)
			}
		} else {
			t.Fatalf("indeterminate race outcome: %+v", cancel)
		}
	}
}

func TestNewVideoCandidatePublishesExactInt64TicksFromDurableDescriptor(t *testing.T) {
	db := database(t)
	_, job := processingJob(t, db)
	c, _ := publicationPeer(t, db, "success")
	ticks := int64(9223372036854775807)
	output := primaryOutput()
	output.Kind = domain.Video
	output.ContentType = "video/mp4"
	output.DurationTicks = &ticks
	asset, err := c.NewPrimary(testContext, job, output, testTime)
	if err != nil {
		t.Fatal(err)
	}
	ticks = 1 // Caller memory cannot alter the already committed descriptor.
	if _, err := c.UploadPrimary(testContext, job, asset.ID, bytes.NewBufferString("correct"), testTime); err != nil {
		t.Fatal(err)
	}
	result, err := c.PublishPrimary(testContext, job, asset.ID, testTime)
	if err != nil {
		t.Fatal(err)
	}
	var got int64
	if err := db.Reader().QueryRow("SELECT duration_ticks FROM items WHERE id=?", result.ItemID).Scan(&got); err != nil || got != 9223372036854775807 {
		t.Fatalf("exact durable candidate ticks lost: %d %v", got, err)
	}
}

func TestLateOldLeasePutCannotReplaceReadyBytesAndIsCleanedSeparately(t *testing.T) {
	db := database(t)
	session, oldJob := processingJob(t, db)
	started, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	objects := map[string][]byte{}
	oldKey := ""
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/tag-files/")
		if r.Method == "GET" && r.URL.Query().Has("uploads") {
			io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
			return
		}
		if r.Method == "PUT" {
			body, _ := io.ReadAll(r.Body)
			if key == oldKey {
				close(started)
				<-release
			}
			mu.Lock()
			objects[key] = body
			mu.Unlock()
			w.Header().Set("ETag", "output")
			return
		}
		mu.Lock()
		defer mu.Unlock()
		body, exists := objects[key]
		switch r.Method {
		case "HEAD":
			if !exists {
				w.WriteHeader(404)
			} else {
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				w.Header().Set("ETag", "output")
			}
		case "GET":
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.Header().Set("ETag", "output")
			w.Write(body)
		case "DELETE":
			delete(objects, key)
			w.WriteHeader(204)
		}
	}))
	old := newPrimary(t, c, oldJob)
	oldKey = old.ObjectKey
	oldDone := make(chan error, 1)
	go func() {
		_, err := c.UploadPrimary(testContext, oldJob, old.ID, bytes.NewBufferString("correct"), testTime)
		oldDone <- err
	}()
	<-started
	newerNow := testTime.Add(2 * time.Hour)
	newer, err := db.ClaimJob(testContext, newerNow, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("newdata")
	sum := sha256.Sum256(data)
	output := primaryOutput()
	output.SHA256 = hex.EncodeToString(sum[:])
	fresh, err := c.NewPrimary(testContext, newer, output, newerNow)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.ObjectKey == old.ObjectKey {
		t.Fatal("new lease reused old output key")
	}
	if _, err := c.UploadPrimary(testContext, newer, fresh.ID, bytes.NewBuffer(data), newerNow); err != nil {
		t.Fatal(err)
	}
	ready, err := c.PublishPrimary(testContext, newer, fresh.ID, newerNow)
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-oldDone; !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("late old writer committed metadata: %v", err)
	}
	mu.Lock()
	correct := bytes.Equal(objects[fresh.ObjectKey], data)
	mu.Unlock()
	if !correct {
		t.Fatal("late old output replaced new ready bytes")
	}
	cleanup, err := db.ClaimJob(testContext, newerNow.Add(time.Minute), time.Hour)
	if err != nil || cleanup.Type != domain.JobCleanup {
		t.Fatalf("unused old candidate cleanup missing: %+v %v", cleanup, err)
	}
	if err := c.ProcessCleanup(testContext, cleanup, newerNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	_, oldExists := objects[old.ObjectKey]
	correct = bytes.Equal(objects[fresh.ObjectKey], data)
	mu.Unlock()
	if oldExists || !correct || tableCount(t, db, "assets") != 1 {
		t.Fatal("unused candidate cleanup touched ready primary or leaked old bytes")
	}
	outcome, err := c.Complete(testContext, session.ID)
	if err != nil || outcome.ItemID != ready.ItemID {
		t.Fatal("late cleanup changed ready origin", err)
	}
}

func TestPrimaryCommandsUseCanonicalUUIDIdentity(t *testing.T) {
	for _, command := range []string{"upload", "publish"} {
		t.Run(command, func(t *testing.T) {
			db := database(t)
			_, job := processingJob(t, db)
			c, _ := publicationPeer(t, db, "success")
			asset := newPrimary(t, c, job)
			uploadID := asset.ID
			if command == "upload" {
				uploadID = strings.ToUpper(asset.ID)
			}
			if _, err := c.UploadPrimary(testContext, job, uploadID, bytes.NewBufferString("correct"), testTime); err != nil {
				t.Fatalf("valid UUID spelling rejected: %v", err)
			}
			publishID := asset.ID
			if command == "publish" {
				publishID = strings.ToUpper(asset.ID)
			}
			if _, err := c.PublishPrimary(testContext, job, publishID, testTime); err != nil {
				t.Fatalf("canonical ready identity lost: %v", err)
			}
		})
	}
}
