package ingestion

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func cancelled(t *testing.T, db *store.Store, id string) Cancellation {
	t.Helper()
	var result Cancellation
	transaction(t, db, func(tx *sql.Tx) error { var err error; result, err = Cancel(testContext, tx, id, testTime); return err })
	return result
}
func tableCount(t *testing.T, db *store.Store, table string) int {
	t.Helper()
	var n int
	if err := db.Reader().QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func queuedJob(t *testing.T, db *store.Store, session Session) dbgen.Job {
	t.Helper()
	var job dbgen.Job
	transaction(t, db, func(tx *sql.Tx) error {
		var err error
		job, err = dbgen.New(tx).CreateJob(testContext, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobIngest, SubjectID: session.ID, DedupeKey: "ingest/" + session.ID, PayloadJson: `{"version":1}`, NextAttemptAt: store.Timestamp(testTime), CreatedAt: store.Timestamp(testTime)})
		return err
	})
	return job
}
func candidate(t *testing.T, db *store.Store, session Session, jobID string) string {
	t.Helper()
	id := domain.NewUUID()
	transaction(t, db, func(tx *sql.Tx) error {
		var library string
		if err := tx.QueryRow("SELECT library_id FROM library_meta").Scan(&library); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO assets(id,upload_id,job_id,lease_generation,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,?,1,'primary','tag-files',?,7,?,'image/jpeg','upload','candidate')`, id, session.ID, jobID, "primary/"+library+"/"+id+"/media.jpg", strings.Repeat("a", 64))
		return err
	})
	return id
}

func TestCancelAtomicallyRemovesVisibleWorkAndPreservesIndependentCleanup(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(testContext, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	session := smallSession(t, db)
	transaction(t, db, func(tx *sql.Tx) error {
		if err := AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu"); err != nil {
			return err
		}
		_, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="})
		return err
	})
	job := queuedJob(t, db, session)
	leased, err := db.ClaimJob(testContext, testTime, time.Minute)
	if err != nil || leased.ID != job.ID {
		t.Fatal(err)
	}
	assetID := candidate(t, db, session, job.ID)
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := dbgen.New(tx).CreateObjectWriteIntent(testContext, dbgen.CreateObjectWriteIntentParams{ID: domain.NewUUID(), OwnerID: domain.NewUUID(), TargetKind: "upload", TargetID: session.ID, Generation: 1, Operation: "create_mpu", ObjectKey: session.IncomingKey, StorageEpoch: coordinatorEpoch, PayloadJson: `{"size":7,"contentType":"application/octet-stream"}`, CreatedAtUtc: store.Timestamp(testTime)})
		return err
	})
	result := cancelled(t, db, strings.ToUpper(session.ID))
	if result.State != "cancelled" || domain.ValidateUUID(result.JobID) != nil || domain.ValidateUUID(result.ReceiptID) != nil {
		t.Fatalf("cancellation: %+v", result)
	}
	for _, table := range []string{"upload_sessions", "upload_parts"} {
		if tableCount(t, db, table) != 0 {
			t.Fatalf("%s retained after cancel", table)
		}
	}
	if tableCount(t, db, "cleanup_receipts") != 1 || tableCount(t, db, "object_write_intents") != 1 || tableCount(t, db, "assets") != 1 {
		t.Fatal("working cleanup/uncertainty/candidate lost")
	}
	if err := db.CommitJob(testContext, job.ID, leased.LeaseGeneration, testTime, func(*sql.Tx) error { t.Error("stale ingest worker entered commit"); return nil }); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("cancel did not permanently fence worker: %v", err)
	}
	if _, err := Lookup(testContext, db.Reader(), session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("cancelled upload remained visible")
	}
	batches, err := ListBatches(testContext, db.Reader())
	if err != nil || len(batches) != 0 {
		t.Fatal("empty batch retained as history")
	}
	again := cancelled(t, db, session.ID)
	if again != result || tableCount(t, db, "jobs") != 1 {
		t.Fatal("repeat cancel duplicated cleanup")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(testContext, dir)
	if err != nil {
		t.Fatal(err)
	}
	if again := cancelled(t, db, session.ID); again != result {
		t.Fatal("cleanup identity lost on reopen")
	}
	var payload string
	if err := db.Reader().QueryRow("SELECT payload_json FROM cleanup_receipts WHERE id=?", result.ReceiptID).Scan(&payload); err != nil || !strings.Contains(payload, assetID) || !strings.Contains(payload, "owned-mpu") || strings.Contains(payload, "photo.jpg") {
		t.Fatalf("cleanup ownership snapshot: %s %v", payload, err)
	}
}

func TestCancelRollsBackEntireFenceIfIndependentCleanupCannotPersist(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	job := queuedJob(t, db, session)
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_cleanup BEFORE INSERT ON cleanup_receipts BEGIN SELECT RAISE(ABORT,'injected cleanup write error'); END`)
		return err
	})
	err := db.Write(testContext, func(tx *sql.Tx) error { _, err := Cancel(testContext, tx, session.ID, testTime); return err })
	if err == nil {
		t.Fatal("cancel acknowledged without durable cleanup")
	}
	if _, err := Lookup(testContext, db.Reader(), session.ID); err != nil || tableCount(t, db, "cleanup_receipts") != 0 {
		t.Fatal("failed cancel partially removed session")
	}
	var jobID string
	if err := db.Reader().QueryRow("SELECT id FROM jobs").Scan(&jobID); err != nil || jobID != job.ID {
		t.Fatal("failed cancel removed job fence")
	}
}

func TestCancelLeavesAlreadyReadyItemAndUnknownUploadUntouched(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	assetID := candidate(t, db, session, domain.NewUUID())
	transaction(t, db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(id,upload_id,media_kind,uploaded_at_utc,state) VALUES(1,?,'Image',?,'importing')`, session.ID, store.Timestamp(testTime)); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE assets SET item_id=1,state='verified' WHERE id=?", assetID); err != nil {
			return err
		}
		if _, err := tx.Exec("UPDATE items SET primary_asset_id=?,state='ready' WHERE id=1", assetID); err != nil {
			return err
		}
		_, err := tx.Exec("DELETE FROM upload_sessions WHERE id=?", session.ID)
		return err
	})
	result := cancelled(t, db, session.ID)
	if result.State != "ready" || result.ItemID != 1 || result.ReceiptID != "" || tableCount(t, db, "cleanup_receipts") != 0 || tableCount(t, db, "items") != 1 {
		t.Fatalf("cancel removed ready result: %+v", result)
	}
	result = cancelled(t, db, domain.NewUUID())
	if result.State != "absent" || result.JobID != "" || tableCount(t, db, "jobs") != 0 {
		t.Fatalf("unknown upload resurrected: %+v", result)
	}
}
