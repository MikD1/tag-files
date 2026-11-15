package ingestion

import (
	"bytes"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func interruptedLibrary(t *testing.T) (string, *store.Store, Session, dbgen.Job, dbgen.Job) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(testContext, root)
	if err != nil {
		t.Fatal(err)
	}
	session, job := processingJob(t, db)
	manifest, err := encodeManifest([]objectstore.Part{{Number: 1, Size: 7, ETag: "kept", SHA256: strings.Repeat("A", 43) + "="}})
	if err != nil {
		t.Fatal(err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		meta, err := q.GetLibraryMeta(testContext)
		if err != nil {
			return err
		}
		if _, err := q.BindLibraryStorage(testContext, dbgen.BindLibraryStorageParams{LibraryID: meta.LibraryID, StorageID: sql.NullString{String: domain.NewUUID(), Valid: true}, StorageVolumeID: sql.NullString{String: strings.Repeat("b", 64), Valid: true}}); err != nil {
			return err
		}
		_, err = tx.Exec("UPDATE upload_sessions SET state='processing',completion_manifest=? WHERE id=?", manifest, session.ID)
		return err
	})
	other := smallSession(t, db)
	cancelled(t, db, other.ID)
	cleanup, err := db.ClaimJob(testContext, testTime, time.Hour)
	if err != nil || cleanup.Type != domain.JobCleanup {
		t.Fatalf("cleanup fixture: %+v %v", cleanup, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(testContext, root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return root, db, session, job, cleanup
}

func TestStartupRecoversUnexpiredJobsAfterClockRollbackWithoutSettlingWrites(t *testing.T) {
	_, db, session, job, cleanup := interruptedLibrary(t)
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := dbgen.New(tx).CreateObjectWriteIntent(testContext, dbgen.CreateObjectWriteIntentParams{ID: domain.NewUUID(), OwnerID: domain.NewUUID(), TargetKind: "upload", TargetID: session.ID, Generation: 1, Operation: "create_mpu", ObjectKey: session.IncomingKey, StorageEpoch: coordinatorEpoch, PayloadJson: `{"size":7,"contentType":"application/octet-stream"}`, CreatedAtUtc: store.Timestamp(testTime)})
		return err
	})
	rollback := testTime.Add(-24 * time.Hour)
	count, err := RecoverInterrupted(testContext, db, rollback)
	if err != nil || count != 2 {
		t.Fatalf("startup reset: %d %v", count, err)
	}
	for _, old := range []dbgen.Job{job, cleanup} {
		current, err := dbgen.New(db.Reader()).GetJob(testContext, old.ID)
		if err != nil || current.State != domain.JobQueued || current.Attempt != old.Attempt-1 || current.LeaseGeneration <= old.LeaseGeneration || current.LeaseUntil.Valid {
			t.Fatalf("old lease not fenced: %+v %v", current, err)
		}
	}
	if tableCount(t, db, "object_write_intents") != 1 || tableCount(t, db, "cleanup_receipts") != 1 {
		t.Fatal("job recovery settled external uncertainty or discarded cleanup")
	}
	result, err := Lookup(testContext, db.Reader(), session.ID)
	if err != nil || result.Session.State != domain.SessionQueued || result.Session.SourceSha256.String != strings.Repeat("a", 64) {
		t.Fatalf("input changed on recovery: %+v %v", result, err)
	}
	c := coordinator(t, db, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("recovery performed S3 IO") }))
	if _, err := c.NewPrimary(testContext, job, primaryOutput(), rollback); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("old generation revived: %v", err)
	}
	claimed, err := db.ClaimJob(testContext, rollback, time.Minute)
	if err != nil || claimed.LeaseGeneration < 2 {
		t.Fatalf("recovered job stuck until old wall clock: %+v %v", claimed, err)
	}
	if _, err := RecoverInterrupted(testContext, db, rollback); !errors.Is(err, store.ErrStartupSealed) {
		t.Fatalf("live consumer reset admitted: %v", err)
	}
}

func TestStartupFutureWorkRollsBackEveryJobAndLeavesInput(t *testing.T) {
	for _, mode := range []string{"job", "journal", "manifest", "state"} {
		t.Run(mode, func(t *testing.T) {
			_, db, session, job, cleanup := interruptedLibrary(t)
			transaction(t, db, func(tx *sql.Tx) error {
				switch mode {
				case "job":
					_, err := tx.Exec("UPDATE jobs SET payload_json=json_set(payload_json,'$.version',99) WHERE id=?", cleanup.ID)
					return err
				case "manifest":
					_, err := tx.Exec("UPDATE upload_sessions SET completion_manifest=json_set(completion_manifest,'$.version',99) WHERE id=?", session.ID)
					return err
				case "state":
					_, err := tx.Exec("UPDATE upload_sessions SET state='failed',error_code='invalid_media' WHERE id=?", session.ID)
					return err
				default:
					_, err := tx.Exec(`INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_version,payload_json,state,created_at_utc) VALUES(?,?,'upload',?,1,'create_mpu',?,?,99,'{}','uncertain',?)`, domain.NewUUID(), domain.NewUUID(), session.ID, session.IncomingKey, coordinatorEpoch, store.Timestamp(testTime))
					return err
				}
			})
			count, err := RecoverInterrupted(testContext, db, testTime.Add(-time.Hour))
			if err == nil || count != 0 {
				t.Fatalf("future work rewritten: %d %v", count, err)
			}
			if mode == "journal" && !errors.Is(err, objectio.ErrIncompatibleIntent) {
				t.Fatalf("future journal was not decoded before reset: %v", err)
			}
			for _, old := range []dbgen.Job{job, cleanup} {
				current, err := dbgen.New(db.Reader()).GetJob(testContext, old.ID)
				if err != nil || current.State != domain.JobRunning || current.LeaseGeneration != old.LeaseGeneration {
					t.Fatalf("partial reset: %+v %v", current, err)
				}
			}
			if _, err := Lookup(testContext, db.Reader(), session.ID); err != nil {
				t.Fatal("future input removed", err)
			}
		})
	}
}

func TestStartupBoundsRetryWaitAfterClockRollbackAndKeepsFailedWork(t *testing.T) {
	_, db, session, job, cleanup := interruptedLibrary(t)
	c := coordinator(t, db, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("network call") }))
	next := testTime.Add(5 * time.Second)
	if err := c.FailIngest(testContext, job, testTime, "storage_unavailable", &next); err != nil {
		t.Fatal(err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE jobs SET state='failed',lease_until=NULL,error_code='retained' WHERE id=?", cleanup.ID)
		return err
	})
	rollback := testTime.Add(-24 * time.Hour)
	count, err := RecoverInterrupted(testContext, db, rollback)
	if err != nil || count != 1 {
		t.Fatalf("retry recovery: %d %v", count, err)
	}
	current, err := dbgen.New(db.Reader()).GetJob(testContext, job.ID)
	if err != nil || current.State != domain.JobRetryWait || current.Attempt != job.Attempt || current.NextAttemptAt != store.Timestamp(rollback.Add(5*time.Second)) {
		t.Fatalf("retry waits for old wall clock: %+v %v", current, err)
	}
	failed, err := dbgen.New(db.Reader()).GetJob(testContext, cleanup.ID)
	if err != nil || failed.State != domain.JobFailed || failed.ErrorCode.String != "retained" {
		t.Fatalf("failed work expired: %+v %v", failed, err)
	}
	if _, err := Lookup(testContext, db.Reader(), session.ID); err != nil {
		t.Fatal("retry lost input", err)
	}
}

func TestStartupRecoversIndependentPosterWithoutRecreatingUpload(t *testing.T) {
	root, db, session, job, _ := interruptedLibrary(t)
	c, _ := publicationPeer(t, db, "success")
	asset := newPrimary(t, c, job)
	if _, err := c.UploadPrimary(testContext, job, asset.ID, bytes.NewBufferString("correct"), testTime); err != nil {
		t.Fatal(err)
	}
	ready, err := c.PublishPrimary(testContext, job, asset.ID, testTime)
	if err != nil {
		t.Fatal(err)
	}
	posterJob, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobPoster}, testTime.Add(time.Second), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(testContext, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if count, err := RecoverInterrupted(testContext, db, testTime.Add(-time.Hour)); err != nil || count != 3 {
		t.Fatalf("independent recovery: %d %v", count, err)
	}
	current, err := dbgen.New(db.Reader()).GetJob(testContext, posterJob.ID)
	if err != nil || current.State != domain.JobQueued || current.LeaseGeneration <= posterJob.LeaseGeneration {
		t.Fatalf("poster not recoverable: %+v %v", current, err)
	}
	result, err := Lookup(testContext, db.Reader(), session.ID)
	if err != nil || result.State != "ready" || result.ItemID != ready.ItemID || tableCount(t, db, "upload_sessions") != 0 {
		t.Fatalf("poster recreated visible upload: %+v %v", result, err)
	}
}

func TestStartupQueuedWorkDoesNotWaitForPreviousWallClock(t *testing.T) {
	_, db, _, _, cleanup := interruptedLibrary(t)
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE jobs SET state='queued',lease_until=NULL,attempt=0 WHERE id=?", cleanup.ID)
		return err
	})
	rollback := testTime.Add(-24 * time.Hour)
	if count, err := RecoverInterrupted(testContext, db, rollback); err != nil || count != 2 {
		t.Fatalf("queued work still scheduled in old clock: %d %v", count, err)
	}
	current, err := dbgen.New(db.Reader()).GetJob(testContext, cleanup.ID)
	if err != nil || current.State != domain.JobQueued || current.NextAttemptAt != store.Timestamp(rollback) {
		t.Fatalf("queued cleanup delayed: %+v %v", current, err)
	}
}

func TestStartupRecoveryPagesEveryPendingJob(t *testing.T) {
	_, db, _, _, _ := interruptedLibrary(t)
	transaction(t, db, func(tx *sql.Tx) error {
		files := make([]File, 105)
		for i := range files {
			files[i] = File{ClientFileID: domain.NewUUID(), Filename: "pending.txt", Size: 7}
		}
		batch, err := Create(testContext, tx, files, nil, DefaultLimits(), testTime)
		if err != nil {
			return err
		}
		for _, session := range batch.Sessions {
			if _, err := Cancel(testContext, tx, session.ID, testTime); err != nil {
				return err
			}
		}
		return nil
	})
	if count, err := RecoverInterrupted(testContext, db, testTime.Add(-time.Hour)); err != nil || count != 107 {
		t.Fatalf("startup skipped a page: %d %v", count, err)
	}
	var queued int
	if err := db.Reader().QueryRow("SELECT count(*) FROM jobs WHERE state='queued'").Scan(&queued); err != nil || queued != 107 {
		t.Fatalf("queued=%d err=%v", queued, err)
	}
}
