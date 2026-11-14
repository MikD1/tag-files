package ingestion

import (
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func TestWorkerFailureAndManualRetryAreAtomicAndRetainInput(t *testing.T) {
	db := database(t)
	session, job := processingJob(t, db)
	c := coordinator(t, db, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("state command made network call") }))
	active, err := c.BeginIngest(testContext, job, testTime)
	if err != nil || active.State != domain.SessionProcessing {
		t.Fatalf("begin: %+v %v", active, err)
	}
	if err := c.FailIngest(testContext, job, testTime, "unsupported_codec", nil); err != nil {
		t.Fatal(err)
	}
	result, err := Lookup(testContext, db.Reader(), session.ID)
	if err != nil || result.State != "failed" || result.Session.State != domain.SessionFailed || result.Session.SourceSha256 != active.SourceSha256 || result.Session.MultipartUploadID != active.MultipartUploadID {
		t.Fatalf("failure lost source or visible error: %+v %v", result, err)
	}
	failed, err := dbgen.New(db.Reader()).GetIngestionJob(testContext, "ingest/"+session.ID)
	if err != nil || failed.State != domain.JobFailed || failed.ErrorCode.String != "unsupported_codec" {
		t.Fatalf("job/session disagreement: %+v %v", failed, err)
	}
	transaction(t, db, func(tx *sql.Tx) error { _, err := Retry(testContext, tx, session.ID, testTime); return err })
	retried, err := dbgen.New(db.Reader()).GetIngestionJob(testContext, "ingest/"+session.ID)
	if err != nil || retried.State != domain.JobQueued || retried.Attempt != 0 || retried.LeaseGeneration <= job.LeaseGeneration {
		t.Fatalf("manual retry failed to reset/fence: %+v %v", retried, err)
	}
	if _, err := c.NewPrimary(testContext, job, primaryOutput(), testTime.Add(-time.Second)); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("old generation revived after retry/clock rollback: %v", err)
	}
	result, err = Lookup(testContext, db.Reader(), session.ID)
	if err != nil || result.Session.State != domain.SessionQueued || result.Session.ErrorCode.Valid || result.Session.SourceSha256 != active.SourceSha256 {
		t.Fatalf("retry changed frozen input: %+v %v", result, err)
	}
}

func TestRetryableWorkerFailureSchedulesJobAndKeepsVisibleUpload(t *testing.T) {
	db := database(t)
	session, job := processingJob(t, db)
	c := coordinator(t, db, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("network call") }))
	next := testTime.Add(30 * time.Second)
	if err := c.FailIngest(testContext, job, testTime, "storage_unavailable", &next); err != nil {
		t.Fatal(err)
	}
	result, err := Lookup(testContext, db.Reader(), session.ID)
	if err != nil || result.State != "pending" || result.Session.State != domain.SessionQueued || result.Session.ErrorCode.String != "storage_unavailable" {
		t.Fatalf("retry not visible: %+v %v", result, err)
	}
	if _, err := db.ClaimJob(testContext, testTime.Add(29*time.Second), time.Minute); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("retried early: %v", err)
	}
	retry, err := db.ClaimJob(testContext, next, time.Minute)
	if err != nil || retry.ID != job.ID || retry.Attempt != 2 {
		t.Fatalf("retry: %+v %v", retry, err)
	}
}

func TestWorkerFailureRollsBackBothStatesAndCannotUndoCancellation(t *testing.T) {
	db := database(t)
	session, job := processingJob(t, db)
	c := coordinator(t, db, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("network call") }))
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_worker_status BEFORE UPDATE ON upload_sessions WHEN NEW.state='failed' BEGIN SELECT RAISE(ABORT,'injected'); END`)
		return err
	})
	if err := c.FailIngest(testContext, job, testTime, "unsupported_codec", nil); err == nil {
		t.Fatal("injected failure ignored")
	}
	current, err := dbgen.New(db.Reader()).GetIngestionJob(testContext, "ingest/"+session.ID)
	if err != nil || current.State != domain.JobRunning {
		t.Fatalf("partial failure committed: %+v %v", current, err)
	}
	cancelled(t, db, session.ID)
	if err := c.FailIngest(testContext, job, testTime, "unsupported_codec", nil); !errors.Is(err, store.ErrLeaseLost) {
		t.Fatalf("cancelled job failure resurrected state: %v", err)
	}
	if _, err := Lookup(testContext, db.Reader(), session.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("cancelled upload returned: %v", err)
	}
}

func TestManualRetryPreservesUnsupportedFutureJobPayload(t *testing.T) {
	db := database(t)
	session, job := processingJob(t, db)
	c := coordinator(t, db, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("network call") }))
	if err := c.FailIngest(testContext, job, testTime, "unsupported_codec", nil); err != nil {
		t.Fatal(err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`UPDATE jobs SET payload_json=json_set(payload_json,'$.version',99) WHERE id=?`, job.ID)
		return err
	})
	err := db.Write(testContext, func(tx *sql.Tx) error { _, err := Retry(testContext, tx, session.ID, testTime); return err })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("future work rewritten by retry: %v", err)
	}
	current, err := dbgen.New(db.Reader()).GetIngestionJob(testContext, "ingest/"+session.ID)
	if err != nil || current.State != domain.JobFailed || current.LeaseGeneration != job.LeaseGeneration {
		t.Fatalf("unsupported job changed: %+v %v", current, err)
	}
}
