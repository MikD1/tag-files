package ingestion

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/operations"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func completableSession(t *testing.T, db *store.Store) (Session, []CompletePart) {
	t.Helper()
	session := smallSession(t, db)
	sum := sha256.Sum256([]byte("correct"))
	transaction(t, db, func(tx *sql.Tx) error {
		if err := AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu"); err != nil {
			return err
		}
		if _, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: base64.StdEncoding.EncodeToString(sum[:])}); err != nil {
			return err
		}
		return dbgen.New(tx).ConfirmUploadPart(testContext, dbgen.ConfirmUploadPartParams{SessionID: session.ID, PartNumber: 1, Etag: sql.NullString{String: "part", Valid: true}})
	})
	return session, []CompletePart{{Number: 1, ETag: "part"}}
}

func TestDurableCompleteBindsClientSelectorsAndClosesPartAdmission(t *testing.T) {
	db := database(t)
	session, parts := completableSession(t, db)
	now := time.Now()
	for _, invalid := range []struct {
		generation int64
		parts      []CompletePart
	}{
		{2, parts}, {1, []CompletePart{{1, "other"}}}, {1, []CompletePart{{2, "part"}}},
	} {
		if err := db.Write(testContext, func(tx *sql.Tx) error {
			_, err := ScheduleComplete(testContext, tx, session.ID, invalid.generation, invalid.parts, now)
			return err
		}); !errors.Is(err, ErrConflict) {
			t.Fatal("accepted changed selector", err)
		}
	}
	var accepted Outcome
	transaction(t, db, func(tx *sql.Tx) error {
		var err error
		accepted, err = ScheduleComplete(testContext, tx, session.ID, 1, parts, now)
		return err
	})
	if accepted.JobID == "" || accepted.Session.State != domain.SessionCompleting {
		t.Fatal(accepted)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		replay, err := ScheduleComplete(testContext, tx, session.ID, 1, parts, now)
		if err == nil && replay.JobID != accepted.JobID {
			t.Error("duplicate complete job")
		}
		return err
	})
	if tableCount(t, db, "jobs") != 1 {
		t.Fatal("duplicate jobs")
	}
	if err := db.Write(testContext, func(tx *sql.Tx) error {
		_, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: strings.Repeat("A", 43) + "="})
		return err
	}); !errors.Is(err, ErrConflict) {
		t.Fatal("part admission remained open", err)
	}
}

func TestDurableCompleteVerifiesSourceAndAtomicallyTransfersTheLease(t *testing.T) {
	for _, mode := range []string{"verified", "stale-lease", "changed-listing", "bad-source"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session, parts := completableSession(t, db)
			transaction(t, db, func(tx *sql.Tx) error {
				_, err := ScheduleComplete(testContext, tx, session.ID, 1, parts, time.Now())
				return err
			})
			job, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobComplete}, time.Now(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Query().Has("uploads"):
					io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>`+session.IncomingKey+`</Key><UploadId>owned-mpu</UploadId></Upload></ListMultipartUploadsResult>`)
				case r.Method == "GET" && r.URL.Query().Has("uploadId"):
					etag := "part"
					if mode == "changed-listing" {
						etag = "changed"
					}
					digest := sha256.Sum256([]byte("correct"))
					io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>`+etag+`</ETag><Size>7</Size><ChecksumSHA256>`+base64.StdEncoding.EncodeToString(digest[:])+`</ChecksumSHA256></Part></ListPartsResult>`)
				case r.Method == "POST":
					if mode == "changed-listing" {
						t.Error("changed client selectors reached Complete")
					}
					io.WriteString(w, `<CompleteMultipartUploadResult><ETag>source</ETag></CompleteMultipartUploadResult>`)
				case r.Method == "HEAD":
					w.Header().Set("Content-Length", "7")
					w.Header().Set("ETag", "source")
				case r.Method == "GET":
					if mode == "stale-lease" {
						transaction(t, db, func(tx *sql.Tx) error {
							_, err := tx.Exec("UPDATE jobs SET lease_generation=lease_generation+1 WHERE id=?", job.ID)
							return err
						})
					}
					data := []byte("correct")
					if mode == "bad-source" {
						data = []byte("corrupt")
					}
					w.Header().Set("Content-Length", "7")
					w.Header().Set("ETag", "source")
					io.Copy(w, bytes.NewReader(data))
				default:
					t.Errorf("unexpected storage call %s %s", r.Method, r.URL)
				}
			}))
			err = c.ProcessComplete(testContext, job, time.Now())
			outcome, lookupErr := Lookup(testContext, db.Reader(), session.ID)
			if lookupErr != nil {
				t.Fatal(lookupErr)
			}
			if mode == "verified" {
				if err != nil || outcome.Session.State != domain.SessionQueued || !outcome.Session.SourceSha256.Valid || outcome.JobID == job.ID {
					t.Fatal("source/job transfer failed", outcome, err)
				}
				if _, err := dbgen.New(db.Reader()).GetJob(testContext, job.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("complete lease retained", err)
				}
			} else {
				if err == nil || outcome.Session.SourceSha256.Valid {
					t.Fatal("unverified/fenced source queued", outcome, err)
				}
				var n int
				db.Reader().QueryRow("SELECT count(*) FROM jobs WHERE type='ingest'").Scan(&n)
				if n != 0 {
					t.Fatal("ingest escaped failed verification")
				}
			}
		})
	}
}

func TestCompleteFailureRetryAndCancelRetainInputAndFinalizeOldReceipt(t *testing.T) {
	db := database(t)
	session, parts := completableSession(t, db)
	key := domain.NewUUID()
	_, err := operations.Execute(testContext, db, operations.Request{Key: key, Method: "POST", Route: "/complete"}, time.Now(), func(tx *sql.Tx) (operations.Result, error) {
		if _, err := ScheduleComplete(testContext, tx, session.ID, 1, parts, time.Now()); err != nil {
			return operations.Result{}, err
		}
		if err := operations.Link(testContext, tx, key, "upload_complete", session.ID); err != nil {
			return operations.Result{}, err
		}
		return operations.Result{Status: 202}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	job, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobComplete}, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c := coordinator(t, db, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("SQL command performed storage IO") }))
	if err := c.FailComplete(testContext, job, time.Now(), "insufficient_storage", nil); err != nil {
		t.Fatal(err)
	}
	failed, err := Lookup(testContext, db.Reader(), session.ID)
	if err != nil || failed.State != "failed" || failed.Session.SourceSha256.Valid || len(failed.Session.Parts) != 1 {
		t.Fatal("failure discarded input", failed, err)
	}
	op, err := operations.Get(testContext, db.Reader(), key, time.Now())
	if err != nil || op.State != "failed" || op.Result.Status != 507 {
		t.Fatal(op, err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		retried, err := Retry(testContext, tx, session.ID, time.Now())
		if err == nil && (retried.Session.State != domain.SessionCompleting || retried.JobID != job.ID) {
			t.Error("retry replaced session/job", retried)
		}
		return err
	})
	transaction(t, db, func(tx *sql.Tx) error { _, err := Cancel(testContext, tx, session.ID, time.Now()); return err })
	if _, err := dbgen.New(db.Reader()).GetJob(testContext, job.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cancel retained complete job", err)
	}
	if tableCount(t, db, "object_write_intents") != 0 || tableCount(t, db, "cleanup_receipts") != 1 {
		t.Fatal("cancel cleanup missing")
	}
	op, err = operations.Get(testContext, db.Reader(), key, time.Now())
	if err != nil || op.Result.Status != 507 {
		t.Fatal("retry/cancel rewrote old failure", op, err)
	}
}

func TestStartupFencesCompleteJobsAndRefusesUnknownPayloadWithoutResettingAnything(t *testing.T) {
	for _, future := range []bool{false, true} {
		t.Run(fmt.Sprint(future), func(t *testing.T) {
			root := t.TempDir()
			db, err := store.Open(testContext, root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { db.Close() }()
			session, parts := completableSession(t, db)
			transaction(t, db, func(tx *sql.Tx) error {
				q := dbgen.New(tx)
				meta, err := q.GetLibraryMeta(testContext)
				if err != nil {
					return err
				}
				if _, err := q.BindLibraryStorage(testContext, dbgen.BindLibraryStorageParams{LibraryID: meta.LibraryID, StorageID: sql.NullString{String: domain.NewUUID(), Valid: true}, StorageVolumeID: sql.NullString{String: strings.Repeat("b", 64), Valid: true}}); err != nil {
					return err
				}
				_, err = ScheduleComplete(testContext, tx, session.ID, 1, parts, time.Now())
				return err
			})
			job, err := db.ClaimJobKinds(testContext, []domain.JobKind{domain.JobComplete}, time.Now(), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if future {
				transaction(t, db, func(tx *sql.Tx) error {
					_, err := tx.Exec("UPDATE jobs SET payload_json=replace(payload_json,'\"version\":1','\"version\":99') WHERE id=?", job.ID)
					return err
				})
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.Open(testContext, root)
			if err != nil {
				t.Fatal(err)
			}
			count, err := RecoverInterrupted(testContext, db, time.Now().Add(-time.Hour))
			current, readErr := dbgen.New(db.Reader()).GetJob(testContext, job.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if future {
				if err == nil || current.State != domain.JobRunning || current.LeaseGeneration != job.LeaseGeneration {
					t.Fatal("unknown work was reset", count, current, err)
				}
			} else if err != nil || count != 1 || current.State != domain.JobQueued || current.Attempt != 0 || current.LeaseGeneration <= job.LeaseGeneration {
				t.Fatal("Complete not recovered", count, current, err)
			}
		})
	}
}
