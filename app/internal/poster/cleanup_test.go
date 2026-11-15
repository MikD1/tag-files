package poster

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func claimCleanup(t *testing.T, db *store.Store) dbgen.Job {
	t.Helper()
	j, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobCleanup}, testTime.Add(time.Second), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestCleanupPreflightsFutureIntentBeyondFirstPageBeforeDeletingObject(t *testing.T) {
	db, job, _ := fixture(t, []byte("source"))
	p := &peer{}
	s := service(t, db, p)
	a, err := s.NewCandidate(testContext, job, posterOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	p.objects[a.ObjectKey] = []byte("poster")
	if err := db.Write(testContext, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		for i := 1; i <= 101; i++ {
			id := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
			if i == 101 {
				_, err := tx.Exec("INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_version,payload_json,state,created_at_utc) VALUES(?,?,'asset',?,?,'put_object',?,?,2,?,'uncertain',?)", id, domain.NewUUID(), a.ID, a.LeaseGeneration, a.ObjectKey, testEpoch, `{"size":6,"sha256":"`+posterOutput().SHA256+`","contentType":"image/jpeg"}`, store.Timestamp(testTime))
				if err != nil {
					return err
				}
				continue
			}
			_, err := q.CreateObjectWriteIntent(testContext, dbgen.CreateObjectWriteIntentParams{ID: id, OwnerID: domain.NewUUID(), TargetKind: "asset", TargetID: a.ID, Generation: a.LeaseGeneration, Operation: "put_object", ObjectKey: a.ObjectKey, StorageEpoch: testEpoch, PayloadJson: `{"size":6,"sha256":"` + posterOutput().SHA256 + `","contentType":"image/jpeg"}`, CreatedAtUtc: store.Timestamp(testTime)})
			if err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.FailJob(testContext, job.ID, job.LeaseGeneration, testTime, "fixture_failure", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessCleanup(testContext, claimCleanup(t, db), testTime); !errors.Is(err, objectio.ErrIncompatibleIntent) {
		t.Fatalf("later future intent ignored: %v", err)
	}
	if p.deletes != 0 || count(t, db, "object_write_intents") != 101 || count(t, db, "assets") != 2 || count(t, db, "cleanup_receipts") != 1 {
		t.Fatal("preflight removed data before seeing future codec")
	}
}

func TestCleanupWaitsForPermanentPublicationFenceAndKeepsPrimary(t *testing.T) {
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
	cleanupJob := claimCleanup(t, db)
	if err := s.ProcessCleanup(testContext, cleanupJob, testTime); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("live generation gave cleanup permission: %v", err)
	}
	if p.deletes != 0 || count(t, db, "assets") != 2 {
		t.Fatal("active candidate removed")
	}
	if err := s.ProcessCleanup(testContext, cleanupJob, testTime.Add(2*time.Hour)); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("elapsed lease granted removal permission: %v", err)
	}
	if err := db.FailJob(testContext, job.ID, job.LeaseGeneration, testTime, "fixture_failure", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessCleanup(testContext, cleanupJob, testTime); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "assets") != 1 || count(t, db, "cleanup_receipts") != 0 || count(t, db, "jobs") != 1 {
		t.Fatal("orphan cleanup incomplete")
	}
	if _, err := s.Publish(testContext, job, a.ID, testTime); err == nil {
		t.Fatal("retired attempt published after cleanup")
	}
	item, err := dbgen.New(db.Reader()).GetItem(testContext, primary.ItemID.Int64)
	if err != nil || item.PrimaryAssetID.String != primary.ID || item.State != domain.ItemReady {
		t.Fatal("poster failure removed primary")
	}
}

func TestCleanupRetainsUnknownWriterUntilEpochBarrierAndReconcilesLatePut(t *testing.T) {
	db, job, _ := fixture(t, []byte("source"))
	p := &peer{mode: "lost"}
	s := service(t, db, p)
	a, err := s.NewCandidate(testContext, job, posterOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(testContext, job, a.ID, bytes.NewBufferString("poster"), testTime); err == nil {
		t.Fatal("fixture PUT response not lost")
	}
	if err := db.FailJob(testContext, job.ID, job.LeaseGeneration, testTime, "fixture_failure", nil); err != nil {
		t.Fatal(err)
	}
	cleanupJob := claimCleanup(t, db)
	if err := s.ProcessCleanup(testContext, cleanupJob, testTime); !errors.Is(err, objectio.ErrUnsettled) {
		t.Fatalf("same epoch settled writer: %v", err)
	}
	if count(t, db, "cleanup_receipts") != 1 || count(t, db, "object_write_intents") != 1 || count(t, db, "assets") != 2 {
		t.Fatal("unknown writer lost attribution")
	}
	p.mu.Lock()
	p.objects[a.ObjectKey] = []byte("poster")
	p.epoch = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	p.mu.Unlock()
	if err := s.ProcessCleanup(testContext, cleanupJob, testTime); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "cleanup_receipts") != 0 || count(t, db, "object_write_intents") != 0 || count(t, db, "assets") != 1 {
		t.Fatal("barrier did not retire exact orphan")
	}
	p.mu.Lock()
	_, exists := p.objects[a.ObjectKey]
	p.mu.Unlock()
	if exists {
		t.Fatal("late PUT remains")
	}
}

func TestCleanupRefusesFutureReceiptWithoutAnyRemoval(t *testing.T) {
	db, job, _ := fixture(t, []byte("source"))
	p := &peer{}
	s := service(t, db, p)
	a, err := s.NewCandidate(testContext, job, posterOutput(), testTime)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FailJob(testContext, job.ID, job.LeaseGeneration, testTime, "fixture_failure", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(testContext, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE cleanup_receipts SET payload_json=json_set(payload_json,'$.version',2)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessCleanup(testContext, claimCleanup(t, db), testTime); !errors.Is(err, ErrIncompatibleCleanup) {
		t.Fatalf("future cleanup guessed: %v", err)
	}
	if p.deletes != 0 || count(t, db, "cleanup_receipts") != 1 || count(t, db, "assets") != 2 {
		t.Fatal("future receipt removed data")
	}
	if !strings.HasSuffix(a.ObjectKey, "/poster-v1/poster.jpg") {
		t.Fatal("unowned candidate key")
	}
}
