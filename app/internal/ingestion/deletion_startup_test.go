package ingestion

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/deletion"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func TestStartupRetainsDeletionInventoryAndRefusesFutureJobAtomically(t *testing.T) {
	root, db, session, job, _ := interruptedLibrary(t)
	c, _ := publicationPeer(t, db, "success")
	a := newPrimary(t, c, job)
	if _, err := c.UploadPrimary(testContext, job, a.ID, bytes.NewBufferString("correct"), testTime); err != nil {
		t.Fatal(err)
	}
	ready, err := c.PublishPrimary(testContext, job, a.ID, testTime)
	if err != nil {
		t.Fatal(err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := deletion.Request(testContext, tx, ready.ItemID, 1, testTime)
		return err
	})
	old, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobDelete}, testTime.Add(time.Second), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE jobs SET payload_json=json_set(payload_json,'$.version',2) WHERE id=?", old.ID)
		return err
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(testContext, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := RecoverInterrupted(testContext, db, testTime.Add(-time.Hour)); !errors.Is(err, deletion.ErrIncompatible) {
		t.Fatalf("future deletion recovery guessed: %v", err)
	}
	current, err := dbgen.New(db.Reader()).GetJob(testContext, old.ID)
	if err != nil || current.State != domain.JobRunning || current.LeaseGeneration != old.LeaseGeneration {
		t.Fatal("future codec partially reset jobs", err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE jobs SET payload_json=? WHERE id=?", old.PayloadJson, old.ID)
		return err
	})
	if _, err := RecoverInterrupted(testContext, db, testTime.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	current, err = dbgen.New(db.Reader()).GetJob(testContext, old.ID)
	if err != nil || current.State != domain.JobQueued || current.LeaseGeneration <= old.LeaseGeneration {
		t.Fatal("deletion lease not fenced", err)
	}
	if tableCount(t, db, "deletion_assets") != 2 {
		t.Fatal("startup discarded deletion keys")
	}
	if _, err := Lookup(testContext, db.Reader(), session.ID); err == nil {
		t.Fatal("startup recreated deleted item/session")
	}
}
