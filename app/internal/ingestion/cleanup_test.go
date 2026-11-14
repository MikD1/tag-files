package ingestion

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type cleanupPeer struct {
	mu         sync.Mutex
	objects    map[string]bool
	mpus       map[string]string
	actions    []string
	failDelete bool
}

func (p *cleanupPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/tag-files/")
	p.actions = append(p.actions, r.Method+" "+key+" "+r.URL.RawQuery)
	switch {
	case r.Method == "GET" && r.URL.Query().Has("uploads"):
		type upload struct {
			Key      string
			UploadID string `xml:"UploadId"`
		}
		result := struct {
			XMLName   xml.Name `xml:"ListMultipartUploadsResult"`
			Uploads   []upload `xml:"Upload"`
			Truncated bool     `xml:"IsTruncated"`
		}{Uploads: []upload{}}
		for id, key := range p.mpus {
			if strings.HasPrefix(key, r.URL.Query().Get("prefix")) {
				result.Uploads = append(result.Uploads, upload{Key: key, UploadID: id})
			}
		}
		_ = xml.NewEncoder(w).Encode(result)
	case r.Method == "DELETE" && r.URL.Query().Has("uploadId"):
		if p.mpus[r.URL.Query().Get("uploadId")] == key {
			delete(p.mpus, r.URL.Query().Get("uploadId"))
		}
		w.WriteHeader(204)
	case r.Method == "HEAD":
		if !p.objects[key] {
			w.WriteHeader(404)
		} else {
			w.Header().Set("Content-Length", "7")
			w.Header().Set("ETag", "opaque")
		}
	case r.Method == "DELETE":
		if p.failDelete {
			w.WriteHeader(403)
			return
		}
		delete(p.objects, key)
		w.WriteHeader(204)
	default:
		w.WriteHeader(400)
	}
}
func cleanupJob(t *testing.T, db *store.Store) dbgen.Job {
	t.Helper()
	job, err := db.ClaimJob(testContext, testTime, time.Minute)
	if err != nil || job.Type != domain.JobCleanup {
		t.Fatalf("claim cleanup: %+v %v", job, err)
	}
	return job
}
func addIntent(t *testing.T, db *store.Store, session Session, epoch, version string) string {
	t.Helper()
	id := domain.NewUUID()
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_version,payload_json,state,created_at_utc) VALUES(?,?,'upload',?,1,'create_mpu',?,?,?,?,'uncertain',?)`, id, domain.NewUUID(), session.ID, session.IncomingKey, epoch, version, `{"size":7,"contentType":"application/octet-stream"}`, store.Timestamp(testTime))
		return err
	})
	return id
}
func TestCancellationCleanupRemovesOnlyFencedOwnedResourcesAndWorkingRecords(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	transaction(t, db, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, session.ID, 1, "known-mpu") })
	ingest := queuedJob(t, db, session)
	assetID := candidate(t, db, session, ingest.ID)
	var key string
	if err := db.Reader().QueryRow("SELECT object_key FROM assets WHERE id=?", assetID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	result := cancelled(t, db, session.ID)
	peer := &cleanupPeer{objects: map[string]bool{session.IncomingKey: true, key: true, session.IncomingKey + "-foreign": true}, mpus: map[string]string{"known-mpu": session.IncomingKey, "late-owned": session.IncomingKey, "foreign": session.IncomingKey + "-foreign"}}
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tableCount(t, db, "upload_sessions") != 0 || tableCount(t, db, "cleanup_receipts") != 1 {
			t.Error("cleanup preceded durable cancel fence")
		}
		if err := db.Write(testContext, func(tx *sql.Tx) error { _, err := tx.Exec("INSERT INTO tags(name) VALUES('cleanup wire')"); return err }); err != nil {
			t.Error(err)
		}
		peer.ServeHTTP(w, r)
	}))
	if err := c.ProcessCleanup(testContext, cleanupJob(t, db), testTime); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"jobs", "cleanup_receipts", "assets"} {
		if tableCount(t, db, table) != 0 {
			t.Fatalf("%s became completed history", table)
		}
	}
	if len(peer.mpus) != 1 || peer.mpus["foreign"] != session.IncomingKey+"-foreign" || len(peer.objects) != 1 || !peer.objects[session.IncomingKey+"-foreign"] {
		t.Fatalf("foreign resources changed: %+v %+v", peer.mpus, peer.objects)
	}
	if got := cancelled(t, db, session.ID); got.State != "absent" || got.ReceiptID == result.ReceiptID {
		t.Fatal("settled cancellation retained history")
	}
}
func TestCleanupKeepsReceiptForSameEpochEvenAfterRepeatedEmptyListingsAndLateEffects(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	addIntent(t, db, session, coordinatorEpoch, "1")
	cancelled(t, db, session.ID)
	peer := &cleanupPeer{objects: map[string]bool{}, mpus: map[string]string{}}
	c := coordinator(t, db, peer)
	job := cleanupJob(t, db)
	for range 2 {
		if err := c.ProcessCleanup(testContext, job, testTime); !errors.Is(err, objectio.ErrUnsettled) {
			t.Fatalf("empty list settled unknown writer: %v", err)
		}
		if tableCount(t, db, "cleanup_receipts") != 1 || tableCount(t, db, "object_write_intents") != 1 {
			t.Fatal("quiet cleanup discarded independent work")
		}
	}
	peer.objects[session.IncomingKey] = true
	peer.mpus["late-create"] = session.IncomingKey
	if err := c.ProcessCleanup(testContext, job, testTime); !errors.Is(err, objectio.ErrUnsettled) || len(peer.objects) != 0 || len(peer.mpus) != 0 {
		t.Fatalf("late effect not repeatedly removed: %v %+v %+v", err, peer.objects, peer.mpus)
	}
}
func TestCleanupNewEpochReconcilesOwnedResultsBeforeRetiringUnknownWrites(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	addIntent(t, db, session, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "1")
	cancelled(t, db, session.ID)
	peer := &cleanupPeer{objects: map[string]bool{session.IncomingKey: true}, mpus: map[string]string{"recovered-late": session.IncomingKey}}
	c := coordinator(t, db, peer)
	if err := c.ProcessCleanup(testContext, cleanupJob(t, db), testTime); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"jobs", "cleanup_receipts", "object_write_intents"} {
		if tableCount(t, db, table) != 0 {
			t.Fatalf("%s not retired after reconciled barrier", table)
		}
	}
	if len(peer.objects) != 0 || len(peer.mpus) != 0 {
		t.Fatal("epoch change substituted for owned result reconciliation")
	}
}
func TestCleanupFailureOrFuturePayloadNeverLosesWorkingRecords(t *testing.T) {
	for _, mode := range []string{"delete-fails", "unknown-intent", "unknown-receipt", "changed-owned-key"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session := smallSession(t, db)
			if mode == "unknown-intent" {
				addIntent(t, db, session, "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "99")
			}
			result := cancelled(t, db, session.ID)
			if mode == "unknown-receipt" {
				transaction(t, db, func(tx *sql.Tx) error {
					_, err := tx.Exec("UPDATE cleanup_receipts SET payload_version=99 WHERE id=?", result.ReceiptID)
					return err
				})
			}
			if mode == "changed-owned-key" {
				transaction(t, db, func(tx *sql.Tx) error {
					_, err := tx.Exec("UPDATE cleanup_receipts SET payload_json=json_set(payload_json,'$.incomingKey','incoming/foreign/source') WHERE id=?", result.ReceiptID)
					return err
				})
			}
			peer := &cleanupPeer{objects: map[string]bool{session.IncomingKey: true}, mpus: map[string]string{}, failDelete: mode == "delete-fails"}
			c := coordinator(t, db, peer)
			if err := c.ProcessCleanup(testContext, cleanupJob(t, db), testTime); err == nil {
				t.Fatal("unverified cleanup reported success")
			}
			if tableCount(t, db, "cleanup_receipts") != 1 || tableCount(t, db, "jobs") != 1 {
				t.Fatal("failed cleanup discarded working records")
			}
			if mode == "unknown-intent" && tableCount(t, db, "object_write_intents") != 1 {
				t.Fatal("unknown intent discarded")
			}
			if mode == "unknown-receipt" || mode == "changed-owned-key" {
				if len(peer.actions) != 0 {
					t.Fatal("uninterpretable/unowned cleanup performed storage I/O")
				}
			}
		})
	}
}

func TestCancelDuringKnownCompleteWaitsForWriterAndCannotQueueLateResult(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	sum := sha256.Sum256([]byte("correct"))
	transaction(t, db, func(tx *sql.Tx) error {
		if err := AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu"); err != nil {
			return err
		}
		_, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: base64.StdEncoding.EncodeToString(sum[:])})
		return err
	})
	started, release := make(chan struct{}), make(chan struct{})
	peer := &cleanupPeer{objects: map[string]bool{}, mpus: map[string]string{"owned-mpu": session.IncomingKey}}
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Query().Has("uploadId"):
			io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>part</ETag><Size>7</Size></Part></ListPartsResult>`)
		case r.Method == "POST":
			close(started)
			<-release
			peer.mu.Lock()
			peer.objects[session.IncomingKey] = true
			delete(peer.mpus, "owned-mpu")
			peer.mu.Unlock()
			io.WriteString(w, `<CompleteMultipartUploadResult><ETag>source</ETag></CompleteMultipartUploadResult>`)
		case r.Method == "GET" && !r.URL.Query().Has("uploads"):
			w.Header().Set("Content-Length", "7")
			w.Header().Set("ETag", "opaque")
			io.WriteString(w, "correct")
		default:
			peer.ServeHTTP(w, r)
		}
	}))
	completed := make(chan error, 1)
	go func() { _, err := c.Complete(testContext, session.ID); completed <- err }()
	<-started
	cancelled(t, db, session.ID)
	job := cleanupJob(t, db)
	ctx, cancel := context.WithTimeout(testContext, 30*time.Millisecond)
	err := c.ProcessCleanup(ctx, job, testTime)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || len(peer.actions) != 0 {
		t.Fatalf("cleanup raced known Complete: %v %+v", err, peer.actions)
	}
	close(release)
	if err := <-completed; !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("late verified Complete resurrected cancelled work: %v", err)
	}
	if tableCount(t, db, "object_write_intents") != 0 || tableCount(t, db, "upload_sessions") != 0 {
		t.Fatal("known finished writer failed to settle or resurrected upload")
	}
	if err := c.ProcessCleanup(testContext, job, testTime); err != nil {
		t.Fatal(err)
	}
	if len(peer.objects) != 0 || tableCount(t, db, "cleanup_receipts") != 0 || tableCount(t, db, "jobs") != 0 {
		t.Fatal("late source was not independently cleaned")
	}
}

func TestCleanupDrainsKnownVerificationReaderBeforeRemovingIncoming(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	cancelled(t, db, session.ID)
	started, release := make(chan struct{}), make(chan struct{})
	var paused atomic.Bool
	peer := &cleanupPeer{objects: map[string]bool{session.IncomingKey: true}, mpus: map[string]string{}}
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" && !paused.Swap(true) {
			close(started)
			<-release
		}
		peer.ServeHTTP(w, r)
	}))
	readerDone := make(chan error, 1)
	go func() {
		readerDone <- c.io.Read(testContext, func(reader objectio.Reader) error {
			_, err := reader.Head(testContext, session.IncomingKey)
			return err
		})
	}()
	<-started
	job := cleanupJob(t, db)
	ctx, cancel := context.WithTimeout(testContext, 30*time.Millisecond)
	err := c.ProcessCleanup(ctx, job, testTime)
	cancel()
	close(release)
	readErr := <-readerDone
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cleanup did not drain known reader: %v (read %v)", err, readErr)
	}
	if readErr != nil {
		t.Fatalf("known reader lost its source during cleanup: %v", readErr)
	}
	if err := c.ProcessCleanup(testContext, job, testTime); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupFinalMetadataFailureRollsBackReceiptAssetAndJobTogether(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	ingest := queuedJob(t, db, session)
	assetID := candidate(t, db, session, ingest.ID)
	cancelled(t, db, session.ID)
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_cleanup_delete BEFORE DELETE ON cleanup_receipts BEGIN SELECT RAISE(ABORT,'injected metadata completion error'); END`)
		return err
	})
	var key string
	if err := db.Reader().QueryRow("SELECT object_key FROM assets WHERE id=?", assetID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	peer := &cleanupPeer{objects: map[string]bool{session.IncomingKey: true, key: true}, mpus: map[string]string{}}
	c := coordinator(t, db, peer)
	job := cleanupJob(t, db)
	if err := c.ProcessCleanup(testContext, job, testTime); err == nil {
		t.Fatal("failed metadata cleanup reported success")
	}
	for _, table := range []string{"jobs", "cleanup_receipts", "assets"} {
		if tableCount(t, db, table) != 1 {
			t.Fatalf("%s escaped failed final transaction", table)
		}
	}
	if len(peer.objects) != 0 {
		t.Fatal("fixture did not reach post-storage metadata failure")
	}
	transaction(t, db, func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER fail_cleanup_delete"); return err })
	if err := c.ProcessCleanup(testContext, job, testTime); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"jobs", "cleanup_receipts", "assets"} {
		if tableCount(t, db, table) != 0 {
			t.Fatalf("retry retained %s as history", table)
		}
	}
}
