package ingestion

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

const oldRecoveryEpoch = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func currentCleanupJob(t *testing.T, db *store.Store) dbgen.Job {
	t.Helper()
	job, err := db.ClaimJob(testContext, time.Now(), time.Minute)
	if err != nil || job.Type != domain.JobCleanup {
		t.Fatalf("claim current cleanup: %+v %v", job, err)
	}
	return job
}

func TestEnsureMultipartAdoptsStableIdentityAndDurablyCleansOnlyExtraOwnedMPUs(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	peer := &cleanupPeer{objects: map[string]bool{}, mpus: map[string]string{"z-extra": session.IncomingKey, "a-kept": session.IncomingKey, "foreign": session.IncomingKey + "-neighbour"}}
	c := coordinator(t, db, peer)
	got, err := c.EnsureMultipart(testContext, session.ID)
	if err != nil || got.MultipartUploadID.String != "a-kept" {
		t.Fatalf("ambiguous create not durably reconciled: %+v %v", got, err)
	}
	if tableCount(t, db, "cleanup_receipts") != 1 || tableCount(t, db, "jobs") != 1 {
		t.Fatal("extra owned MPU has no independent cleanup")
	}
	if _, err := c.RecoverSession(testContext, session.ID); err != nil {
		t.Fatal(err)
	}
	if tableCount(t, db, "cleanup_receipts") != 1 {
		t.Fatal("repeated listing duplicated cleanup")
	}
	if err := c.ProcessCleanup(testContext, currentCleanupJob(t, db), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(peer.mpus) != 2 || peer.mpus["a-kept"] != session.IncomingKey || peer.mpus["foreign"] != session.IncomingKey+"-neighbour" {
		t.Fatalf("extra cleanup aborted kept/foreign MPU: %+v", peer.mpus)
	}
	if tableCount(t, db, "cleanup_receipts") != 0 || tableCount(t, db, "jobs") != 0 {
		t.Fatal("extra cleanup retained history")
	}
	outcome, err := Lookup(testContext, db.Reader(), session.ID)
	if err != nil || outcome.Session.MultipartUploadID.String != "a-kept" {
		t.Fatal("extra cleanup changed active binding")
	}
}

func TestRecoveryKeepsAttachedMultipartEvenWhenALexicallyEarlierExtraAppears(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	transaction(t, db, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, session.ID, 1, "z-kept") })
	peer := &cleanupPeer{objects: map[string]bool{}, mpus: map[string]string{"z-kept": session.IncomingKey, "a-extra": session.IncomingKey}}
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Has("uploadId") && r.Method == "GET" {
			io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
			return
		}
		peer.ServeHTTP(w, r)
	}))
	got, err := c.RecoverSession(testContext, session.ID)
	if err != nil || got.Session.MultipartUploadID.String != "z-kept" {
		t.Fatalf("recovery replaced immutable identity: %+v %v", got, err)
	}
	if err := c.ProcessCleanup(testContext, currentCleanupJob(t, db), time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(peer.mpus) != 1 || peer.mpus["z-kept"] != session.IncomingKey {
		t.Fatalf("kept identity aborted: %+v", peer.mpus)
	}
}

func TestActiveCreateRecoveryRequiresEpochBarrierBeforeReplacingAnEmptyUnknownResult(t *testing.T) {
	for _, sameEpoch := range []bool{true, false} {
		t.Run(fmt.Sprint(sameEpoch), func(t *testing.T) {
			db := database(t)
			session := smallSession(t, db)
			epoch := oldRecoveryEpoch
			if sameEpoch {
				epoch = coordinatorEpoch
			}
			addIntent(t, db, session, epoch, "1")
			creates := 0
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" {
					io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
					return
				}
				creates++
				io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>new-after-barrier</UploadId></InitiateMultipartUploadResult>`)
			}))
			got, err := c.RecoverSession(testContext, session.ID)
			if sameEpoch {
				if !errors.Is(err, objectio.ErrUnsettled) || creates != 0 || tableCount(t, db, "object_write_intents") != 1 || got.Session.State != domain.SessionCreating {
					t.Fatalf("empty same-epoch result replaced unknown Create: %+v %v", got, err)
				}
			} else if err != nil || creates != 1 || tableCount(t, db, "object_write_intents") != 0 || got.Session.MultipartUploadID.String != "new-after-barrier" {
				t.Fatalf("old empty Create did not recover: %+v %v creates=%d", got, err, creates)
			}
		})
	}
}

func TestRecoveryReconcilesUnknownPartAgainstImmutableTupleBeforeRetirement(t *testing.T) {
	for _, mode := range []string{"bound-match", "absent", "wrong-size", "unbound", "future-version"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session := smallSession(t, db)
			sum := base64.StdEncoding.EncodeToString(make([]byte, 32))
			transaction(t, db, func(tx *sql.Tx) error {
				if err := AttachMultipart(testContext, tx, session.ID, 1, "kept"); err != nil {
					return err
				}
				if mode != "unbound" {
					if _, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: sum}); err != nil {
						return err
					}
				}
				version := 1
				if mode == "future-version" {
					version = 99
				}
				_, err := tx.Exec(`INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_version,payload_json,state,created_at_utc) VALUES(?,?,'upload',?,1,'upload_part',?,?,?,?,'uncertain',?)`, domain.NewUUID(), domain.NewUUID(), session.ID, session.IncomingKey, oldRecoveryEpoch, version, `{"size":7,"multipartId":"kept","partNumber":1,"sha256":"`+sum+`"}`, session.CreatedAtUtc)
				return err
			})
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("uploads") {
					fmt.Fprintf(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>%s</Key><UploadId>kept</UploadId></Upload></ListMultipartUploadsResult>`, session.IncomingKey)
					return
				}
				if mode == "absent" {
					io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
					return
				}
				size := 7
				if mode == "wrong-size" {
					size = 8
				}
				fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>part</ETag><Size>%d</Size></Part></ListPartsResult>`, size)
			}))
			got, err := c.RecoverSession(testContext, session.ID)
			if mode == "bound-match" || mode == "absent" {
				if err != nil || tableCount(t, db, "object_write_intents") != 0 {
					t.Fatalf("valid old part did not settle: %+v %v", got, err)
				}
				state := "confirmed"
				if mode == "absent" {
					state = "bound"
				}
				if got.Session.Parts[0].State != state || got.Session.Parts[0].SHA256 != sum {
					t.Fatal("recovery changed byte binding")
				}
			} else if err == nil || tableCount(t, db, "object_write_intents") != 1 {
				t.Fatalf("unbound/corrupt/future write retired: %v", err)
			}
		})
	}
}

func TestDuplicateCleanupPayloadCannotNameTheSelectedMultipart(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	peer := &cleanupPeer{objects: map[string]bool{}, mpus: map[string]string{"a-kept": session.IncomingKey, "z-extra": session.IncomingKey}}
	c := coordinator(t, db, peer)
	if _, err := c.EnsureMultipart(testContext, session.ID); err != nil {
		t.Fatal(err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE cleanup_receipts SET payload_json=json_set(payload_json,'$.extraMultipartId','a-kept')")
		return err
	})
	job := currentCleanupJob(t, db)
	before := len(peer.actions)
	if err := c.ProcessCleanup(testContext, job, time.Now()); err == nil || len(peer.actions) != before || peer.mpus["a-kept"] != session.IncomingKey {
		t.Fatalf("kept MPU accepted as cleanup target: %v", err)
	}
}

func TestActiveCompleteRecoveryPreservesFrozenManifestAndRequiresFullSourceVerification(t *testing.T) {
	for _, mode := range []string{"verified-source", "missing-source", "old-part-before-complete", "changed-etag", "corrupt-source", "wrong-identity", "same-epoch"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session := smallSession(t, db)
			content := []byte("correct")
			digest := sha256.Sum256(content)
			checksum := base64.StdEncoding.EncodeToString(digest[:])
			parts := []objectstore.Part{{Number: 1, Size: 7, ETag: "frozen-etag", SHA256: checksum}}
			frozen, err := encodeManifest(parts)
			if err != nil {
				t.Fatal(err)
			}
			_, identity, err := objectio.CompletionIdentity(parts)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "wrong-identity" {
				identity = strings.Repeat("0", 64)
			}
			epoch := oldRecoveryEpoch
			if mode == "same-epoch" {
				epoch = coordinatorEpoch
			}
			transaction(t, db, func(tx *sql.Tx) error {
				if err := AttachMultipart(testContext, tx, session.ID, 1, "kept"); err != nil {
					return err
				}
				if _, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: checksum}); err != nil {
					return err
				}
				if _, err := tx.Exec("UPDATE upload_sessions SET state='completing',completion_manifest=? WHERE id=?", frozen, session.ID); err != nil {
					return err
				}
				operation := "complete_mpu"
				args := objectio.Arguments{Size: 7, MultipartID: "kept", ManifestSHA256: identity}
				if mode == "old-part-before-complete" {
					operation = "upload_part"
					args = objectio.Arguments{Size: 7, MultipartID: "kept", PartNumber: 1, SHA256: checksum}
				}
				payload, _ := json.Marshal(args)
				_, err := dbgen.New(tx).CreateObjectWriteIntent(testContext, dbgen.CreateObjectWriteIntentParams{ID: domain.NewUUID(), OwnerID: domain.NewUUID(), TargetKind: "upload", TargetID: session.ID, Generation: 1, Operation: operation, ObjectKey: session.IncomingKey, StorageEpoch: epoch, PayloadJson: string(payload), CreatedAtUtc: session.CreatedAtUtc})
				return err
			})
			sourceExists := mode != "missing-source" && mode != "old-part-before-complete" && mode != "changed-etag"
			completes, reads := 0, 0
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Query().Has("uploads"):
					fmt.Fprintf(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated><Upload><Key>%s</Key><UploadId>kept</UploadId></Upload></ListMultipartUploadsResult>`, session.IncomingKey)
				case r.Method == "HEAD":
					if !sourceExists {
						w.WriteHeader(404)
						return
					}
					w.Header().Set("Content-Length", "7")
					w.Header().Set("ETag", "source")
				case r.Method == "GET" && r.URL.Query().Has("uploadId"):
					etag := "frozen-etag"
					if mode == "changed-etag" {
						etag = "replacement-etag"
					}
					fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>%s</ETag><Size>7</Size></Part></ListPartsResult>`, etag)
				case r.Method == "POST":
					completes++
					sourceExists = true
					body, _ := io.ReadAll(r.Body)
					if !bytes.Contains(body, []byte("frozen-etag")) || bytes.Contains(body, []byte("replacement-etag")) {
						t.Error("recovery changed frozen request")
					}
					io.WriteString(w, `<CompleteMultipartUploadResult><ETag>source</ETag></CompleteMultipartUploadResult>`)
				case r.Method == "GET":
					reads++
					w.Header().Set("Content-Length", "7")
					w.Header().Set("ETag", "source")
					if mode == "corrupt-source" {
						io.WriteString(w, "corrupt")
					} else {
						w.Write(content)
					}
				default:
					t.Errorf("unexpected recovery request %s %s", r.Method, r.URL)
				}
			}))
			got, err := c.RecoverSession(testContext, session.ID)
			if mode == "changed-etag" || mode == "corrupt-source" || mode == "wrong-identity" {
				if !errors.Is(err, objectstore.ErrIntegrity) || tableCount(t, db, "object_write_intents") != 1 || tableCount(t, db, "jobs") != 0 || completes != 0 {
					t.Fatalf("unsafe frozen recovery: %+v %v", got, err)
				}
				got, _ = Lookup(testContext, db.Reader(), session.ID)
			} else {
				if mode == "same-epoch" {
					if !errors.Is(err, objectio.ErrUnsettled) || tableCount(t, db, "object_write_intents") != 1 {
						t.Fatalf("same-epoch writer retired: %v", err)
					}
				} else if err != nil || tableCount(t, db, "object_write_intents") != 0 {
					t.Fatalf("valid frozen recovery: %+v %v", got, err)
				}
				if got.Session.State != domain.SessionQueued || got.Session.SourceSha256.String != hex.EncodeToString(digest[:]) || got.JobID == "" || reads != 1 {
					t.Fatalf("unverified/missing queued source: %+v reads=%d", got, reads)
				}
				want := 0
				if mode == "missing-source" || mode == "old-part-before-complete" {
					want = 1
				}
				if completes != want {
					t.Fatalf("wrong number of identical Complete attempts %d", completes)
				}
			}
			if got.Session.CompletionManifest.String != frozen {
				t.Fatal("frozen manifest was replaced")
			}
		})
	}
}

func TestMultipartChoiceAndExtraReceiptsRollbackTogether(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	peer := &cleanupPeer{objects: map[string]bool{}, mpus: map[string]string{"a-kept": session.IncomingKey, "b-extra": session.IncomingKey, "c-extra": session.IncomingKey}}
	c := coordinator(t, db, peer)
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TRIGGER fail_second_extra BEFORE INSERT ON cleanup_receipts WHEN NEW.reason='duplicate_mpu' AND (SELECT COUNT(*) FROM cleanup_receipts)>0 BEGIN SELECT RAISE(ABORT,'second receipt failure'); END`)
		return err
	})
	if _, err := c.EnsureMultipart(testContext, session.ID); err == nil {
		t.Fatal("partially durable adoption reported success")
	}
	outcome, err := Lookup(testContext, db.Reader(), session.ID)
	if err != nil || outcome.Session.MultipartUploadID.Valid || outcome.Session.State != domain.SessionCreating || tableCount(t, db, "cleanup_receipts") != 0 || tableCount(t, db, "jobs") != 0 {
		t.Fatalf("choice/receipt transaction partially committed: %+v %v", outcome, err)
	}
	transaction(t, db, func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER fail_second_extra"); return err })
	if _, err := c.EnsureMultipart(testContext, session.ID); err != nil || tableCount(t, db, "cleanup_receipts") != 2 {
		t.Fatalf("failed adoption cannot retry safely: %v", err)
	}
}

func TestUnsupportedCreateIntentCannotDriveMultipartAdoptionOrExtraCleanup(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			db := database(t)
			session := smallSession(t, db)
			addIntent(t, db, session, oldRecoveryEpoch, "99")
			peer := &cleanupPeer{objects: map[string]bool{}, mpus: map[string]string{"a": session.IncomingKey, "b": session.IncomingKey}}
			c := coordinator(t, db, peer)
			var err error
			if recover {
				_, err = c.RecoverSession(testContext, session.ID)
			} else {
				_, err = c.EnsureMultipart(testContext, session.ID)
			}
			if !errors.Is(err, objectio.ErrIncompatibleIntent) {
				t.Fatalf("unsupported write did not stop adoption: %v", err)
			}
			outcome, lookupErr := Lookup(testContext, db.Reader(), session.ID)
			if lookupErr != nil || outcome.Session.MultipartUploadID.Valid || tableCount(t, db, "cleanup_receipts") != 0 || tableCount(t, db, "object_write_intents") != 1 || len(peer.actions) != 0 {
				t.Fatal("unknown work changed identity or scheduled destructive reconciliation")
			}
		})
	}
}
