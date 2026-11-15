package ingestion

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
)

const coordinatorEpoch = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

func coordinator(t *testing.T, db *store.Store, handler http.Handler) *Coordinator {
	t.Helper()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", coordinatorEpoch)
		if r.Method == "HEAD" && r.URL.Path == "/tag-files" {
			return
		}
		if r.Header.Get("X-Tag-Files-Expected-Storage-Epoch") != coordinatorEpoch {
			t.Error("unfenced storage operation")
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(peer.Close)
	client, err := objectstore.New(objectstore.Config{Endpoint: peer.URL, Region: "us-east-1", Bucket: "tag-files", AccessKey: "private", SecretKey: "private", HTTPClient: peer.Client()})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := objectio.New(db, client, 3)
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCoordinator(db, manager)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func smallSession(t *testing.T, db *store.Store) Session {
	t.Helper()
	var batch Batch
	transaction(t, db, func(tx *sql.Tx) error {
		var err error
		batch, err = Create(testContext, tx, []File{{ClientFileID: domain.NewUUID(), Filename: "photo.jpg", Size: 7}}, nil, DefaultLimits(), testTime)
		return err
	})
	return batch.Sessions[0]
}

func TestCoordinatorRecoversLostCreateFromOnlyExactOwnedKeyWithoutAnotherCreate(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	var created atomic.Int64
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			created.Add(1)
			var state string
			var intents int
			if err := db.Reader().QueryRow("SELECT state FROM upload_sessions WHERE id=?", session.ID).Scan(&state); err != nil || state != "creating" {
				t.Error("S3 create preceded session commit")
			}
			if err := db.Reader().QueryRow("SELECT COUNT(*) FROM object_write_intents WHERE target_id=? AND operation='create_mpu'", session.ID).Scan(&intents); err != nil || intents != 1 {
				t.Error("S3 create preceded intent")
			}
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
		case "GET":
			if r.URL.Query().Get("prefix") != session.IncomingKey {
				t.Error("broad MPU inventory")
			}
			type upload struct {
				Key      string
				UploadID string `xml:"UploadId"`
			}
			result := struct {
				XMLName   xml.Name `xml:"ListMultipartUploadsResult"`
				Truncated bool     `xml:"IsTruncated"`
				Uploads   []upload `xml:"Upload"`
			}{Uploads: []upload{{Key: session.IncomingKey + "-foreign", UploadID: "foreign"}}}
			if created.Load() > 0 {
				result.Uploads = append(result.Uploads, upload{Key: session.IncomingKey, UploadID: "retained-owned"})
			}
			_ = xml.NewEncoder(w).Encode(result)
		default:
			t.Errorf("unexpected request %s", r.Method)
		}
	}))
	if _, err := c.EnsureMultipart(testContext, session.ID); err == nil {
		t.Fatal("lost response reported success")
	}
	got, err := c.EnsureMultipart(testContext, strings.ToUpper(session.ID))
	if err != nil || got.MultipartUploadID.String != "retained-owned" || got.State != domain.SessionReceiving || created.Load() != 1 {
		t.Fatalf("recovery: %+v %v creates=%d", got, err, created.Load())
	}
	var intents int
	if err := db.Reader().QueryRow("SELECT COUNT(*) FROM object_write_intents WHERE target_id=? AND state='uncertain'", session.ID).Scan(&intents); err != nil || intents != 1 {
		t.Fatal("adoption silently settled the unknown writer")
	}
	if _, err := c.EnsureMultipart(testContext, session.ID); err != nil || created.Load() != 1 {
		t.Fatal("attached identity recreated")
	}
}

func TestCoordinatorDoesNotRepeatUncertainCreateWhenNoUploadIsVisibleYet(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	var creates atomic.Int64
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
			return
		}
		creates.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	_, _ = c.EnsureMultipart(testContext, session.ID)
	if _, err := c.EnsureMultipart(testContext, session.ID); !errors.Is(err, objectio.ErrUnsettled) || creates.Load() != 1 {
		t.Fatalf("empty list substituted for settlement: %v creates=%d", err, creates.Load())
	}
}

func TestCoordinatorStreamsOnlyCommittedImmutablePartAndConfirmsVerifiedAck(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	transaction(t, db, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu") })
	data := []byte("correct")
	digest := sha256.Sum256(data)
	sum := base64.StdEncoding.EncodeToString(digest[:])
	binding := PartBinding{Number: 1, Size: 7, SHA256: sum}
	var uploads atomic.Int64
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uploads.Add(1)
		if r.Method != "PUT" || r.URL.Query().Get("uploadId") != "owned-mpu" || r.URL.Query().Get("partNumber") != "1" || r.ContentLength != 7 || r.Header.Get("X-Amz-Checksum-Sha256") != sum {
			t.Error("wire differs from binding")
		}
		got, err := Lookup(testContext, db.Reader(), session.ID)
		if err != nil || len(got.Session.Parts) != 1 || got.Session.Parts[0].SHA256 != sum {
			t.Error("binding not committed before streaming")
		}
		// The serialized metadata writer remains available while the body is read.
		if err := db.Write(testContext, func(tx *sql.Tx) error {
			_, err := tx.Exec("INSERT INTO tags(name) VALUES('during upload')")
			return err
		}); err != nil {
			t.Error(err)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, data) {
			t.Error("body changed")
		}
		w.Header().Set("ETag", `"part-etag"`)
		w.Header().Set("X-Amz-Checksum-Sha256", sum)
	}))
	part, err := c.UploadPart(testContext, session.ID, 1, binding, bytes.NewBuffer(data))
	if err != nil || part.State != "confirmed" || part.ETag != `"part-etag"` {
		t.Fatalf("part: %+v %v", part, err)
	}
	changed := binding
	changed.SHA256 = base64.StdEncoding.EncodeToString(make([]byte, 32))
	if _, err := c.UploadPart(testContext, session.ID, 1, changed, strings.NewReader("corrupt")); !errors.Is(err, ErrConflict) || uploads.Load() != 1 {
		t.Fatalf("different bytes forwarded: %v", err)
	}
	transaction(t, db, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE upload_sessions SET state='completing' WHERE id=?", session.ID)
		return err
	})
	if _, err := c.UploadPart(testContext, session.ID, 1, binding, bytes.NewReader(data)); !errors.Is(err, ErrConflict) || uploads.Load() != 1 {
		t.Fatal("admission remained open during Complete")
	}
}

func TestCoordinatorReconcilesListPartsOnlyAgainstExistingBindings(t *testing.T) {
	for _, mode := range []string{"missing-checksum", "matching", "wrong-size", "wrong-checksum", "unbound"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session := smallSession(t, db)
			digest := sha256.Sum256([]byte("correct"))
			sum := base64.StdEncoding.EncodeToString(digest[:])
			transaction(t, db, func(tx *sql.Tx) error {
				if err := AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu"); err != nil {
					return err
				}
				if mode != "unbound" {
					_, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: sum})
					return err
				}
				return nil
			})
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Query().Get("uploadId") != "owned-mpu" {
					t.Error("wrong ListParts")
				}
				size := 7
				checksum := ""
				if mode == "wrong-size" {
					size = 6
				}
				if mode == "matching" {
					checksum = sum
				}
				if mode == "wrong-checksum" {
					checksum = base64.StdEncoding.EncodeToString(make([]byte, 32))
				}
				fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>listed</ETag><Size>%d</Size><ChecksumSHA256>%s</ChecksumSHA256></Part></ListPartsResult>`, size, checksum)
			}))
			got, err := c.Refresh(testContext, session.ID)
			if mode == "matching" {
				if err != nil || len(got.Session.Parts) != 1 || got.Session.Parts[0].State != "confirmed" || got.Session.Parts[0].SHA256 != sum || got.Session.Parts[0].ETag != "listed" {
					t.Fatalf("valid bound recovery: %+v %v", got, err)
				}
			} else {
				if !errors.Is(err, objectstore.ErrIntegrity) {
					t.Fatalf("untrusted list accepted: %v", err)
				}
				outcome, err := Lookup(testContext, db.Reader(), session.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, part := range outcome.Session.Parts {
					if part.State != "bound" {
						t.Fatal("partial reconciliation committed on integrity failure")
					}
				}
			}
		})
	}
}

func TestCompleteClosesAdmissionDrainsKnownWriterAndQueuesOnlyVerifiedSource(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	transaction(t, db, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu") })
	data := []byte("correct")
	digest := sha256.Sum256(data)
	sum := base64.StdEncoding.EncodeToString(digest[:])
	started, release := make(chan struct{}), make(chan struct{})
	var completes, gets atomic.Int64
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "PUT":
			_, _ = io.Copy(io.Discard, r.Body)
			close(started)
			<-release
			w.Header().Set("ETag", "part-etag")
			w.Header().Set("X-Amz-Checksum-Sha256", sum)
		case r.Method == "GET" && r.URL.Query().Has("uploadId"):
			fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>part-etag</ETag><Size>7</Size><ChecksumSHA256>%s</ChecksumSHA256></Part></ListPartsResult>`, sum)
		case r.Method == "POST":
			completes.Add(1)
			var state, manifest string
			if err := db.Reader().QueryRow("SELECT state,completion_manifest FROM upload_sessions WHERE id=?", session.ID).Scan(&state, &manifest); err != nil || state != "completing" || !strings.Contains(manifest, "part-etag") {
				t.Error("Complete preceded frozen manifest")
			}
			var jobs int
			if err := db.Reader().QueryRow("SELECT COUNT(*) FROM jobs").Scan(&jobs); err != nil || jobs != 0 {
				t.Error("job queued before full source verification")
			}
			io.WriteString(w, `<CompleteMultipartUploadResult><ETag>source-etag</ETag></CompleteMultipartUploadResult>`)
		case r.Method == "HEAD":
			w.Header().Set("Content-Length", "7")
			w.Header().Set("ETag", "source-etag")
		case r.Method == "GET":
			gets.Add(1)
			w.Header().Set("Content-Length", "7")
			w.Header().Set("ETag", "source-etag")
			w.Write(data)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
	}))
	partDone := make(chan error, 1)
	go func() {
		_, err := c.UploadPart(testContext, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: sum}, bytes.NewBuffer(data))
		partDone <- err
	}()
	<-started
	type completion struct {
		result Outcome
		err    error
	}
	done := make(chan completion, 1)
	go func() { got, err := c.Complete(testContext, session.ID); done <- completion{got, err} }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := Lookup(testContext, db.Reader(), session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Session.State == domain.SessionCompleting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Complete did not close admission before drain")
		}
		time.Sleep(time.Millisecond)
	}
	if completes.Load() != 0 {
		t.Error("Complete forwarded while part writer was live")
	}
	if _, err := c.UploadPart(testContext, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: sum}, bytes.NewBuffer(data)); !errors.Is(err, ErrConflict) {
		t.Errorf("closed admission: %v", err)
	}
	close(release)
	if err := <-partDone; err != nil {
		t.Fatal(err)
	}
	completed := <-done
	if completed.err != nil || completed.result.Session == nil || completed.result.Session.State != domain.SessionQueued || completed.result.JobID == "" || completed.result.Session.SourceSha256.String != hex.EncodeToString(digest[:]) || gets.Load() != 1 {
		t.Fatalf("completion: %+v %v gets=%d", completed.result, completed.err, gets.Load())
	}
	again, err := c.Complete(testContext, session.ID)
	if err != nil || again.JobID != completed.result.JobID || completes.Load() != 1 || gets.Load() != 1 {
		t.Fatalf("replay re-created work: %+v %v", again, err)
	}
}

func TestLostCompleteRecoversFromFrozenManifestAndFullReadWithoutReplacingOrReplayingIt(t *testing.T) {
	for _, mode := range []string{"lost-response", "invalid-part", "corrupt-200"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session := smallSession(t, db)
			data := []byte("correct")
			digest := sha256.Sum256(data)
			sum := base64.StdEncoding.EncodeToString(digest[:])
			transaction(t, db, func(tx *sql.Tx) error {
				if err := AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu"); err != nil {
					return err
				}
				_, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: sum})
				return err
			})
			var completes, lists, gets atomic.Int64
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Query().Has("uploadId"):
					lists.Add(1)
					fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>frozen-etag</ETag><Size>7</Size><ChecksumSHA256>%s</ChecksumSHA256></Part></ListPartsResult>`, sum)
				case r.Method == "POST":
					completes.Add(1)
					if mode == "invalid-part" {
						w.WriteHeader(400)
						io.WriteString(w, `<Error><Code>InvalidPart</Code></Error>`)
						return
					}
					if mode == "lost-response" {
						conn, _, _ := w.(http.Hijacker).Hijack()
						conn.Close()
						return
					}
					io.WriteString(w, `<CompleteMultipartUploadResult><ETag>source</ETag></CompleteMultipartUploadResult>`)
				case r.Method == "HEAD":
					if mode == "invalid-part" {
						w.WriteHeader(404)
						return
					}
					w.Header().Set("Content-Length", "7")
					w.Header().Set("ETag", "source")
				case r.Method == "GET":
					number := gets.Add(1)
					w.Header().Set("Content-Length", "7")
					w.Header().Set("ETag", "source")
					if mode == "corrupt-200" && number == 1 {
						io.WriteString(w, "corrupt")
					} else {
						w.Write(data)
					}
				}
			}))
			if _, err := c.Complete(testContext, session.ID); err == nil {
				t.Fatal("ambiguous/corrupt Complete queued work")
			}
			before, err := Lookup(testContext, db.Reader(), session.ID)
			if err != nil || !before.Session.CompletionManifest.Valid || before.Session.State != domain.SessionCompleting || before.JobID != "" {
				t.Fatalf("lost frozen state: %+v %v", before, err)
			}
			after, err := c.Complete(testContext, session.ID)
			if mode == "invalid-part" {
				if !errors.Is(err, objectio.ErrUnsettled) {
					t.Fatalf("InvalidPart blindly replayed: %v", err)
				}
				after, err = Lookup(testContext, db.Reader(), session.ID)
				if err != nil || after.Session.State != domain.SessionCompleting || after.Session.CompletionManifest != before.Session.CompletionManifest || after.JobID != "" {
					t.Fatal("InvalidPart replaced frozen manifest")
				}
			} else if err != nil || after.Session.State != domain.SessionQueued || after.JobID == "" {
				t.Fatalf("full-read recovery: %+v %v", after, err)
			}
			if completes.Load() != 1 || lists.Load() != 1 {
				t.Fatal("frozen manifest rebuilt/replayed")
			}
			var intents int
			if err := db.Reader().QueryRow("SELECT COUNT(*) FROM object_write_intents WHERE target_id=?", session.ID).Scan(&intents); err != nil || intents != 1 {
				t.Fatal("read recovery silently settled an uncertain Complete")
			}
		})
	}
}

func TestCompleteMissingStoredPartReopensAdmissionWithoutLosingBinding(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	data := []byte("correct")
	digest := sha256.Sum256(data)
	sum := base64.StdEncoding.EncodeToString(digest[:])
	binding := PartBinding{Number: 1, Size: 7, SHA256: sum}
	transaction(t, db, func(tx *sql.Tx) error {
		if err := AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu"); err != nil {
			return err
		}
		if _, err := BindPart(testContext, tx, session.ID, 1, binding); err != nil {
			return err
		}
		_, err := confirmPart(testContext, tx, session.ID, 1, objectstore.Part{Number: 1, Size: 7, SHA256: sum, ETag: "old-ack"})
		return err
	})
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
		case "PUT":
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", "new-ack")
			w.Header().Set("X-Amz-Checksum-Sha256", sum)
		default:
			t.Error("Complete forwarded a missing part")
		}
	}))
	if _, err := c.Complete(testContext, session.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing part accepted: %v", err)
	}
	outcome, err := Lookup(testContext, db.Reader(), session.ID)
	if err != nil || outcome.Session.State != domain.SessionReceiving || outcome.Session.CompletionManifest.Valid || outcome.Session.Parts[0].State != "bound" || outcome.Session.Parts[0].SHA256 != sum || outcome.Session.Parts[0].ETag != "" {
		t.Fatalf("incomplete source cannot resume: %+v %v", outcome, err)
	}
	if _, err := c.UploadPart(testContext, session.ID, 1, binding, bytes.NewBuffer(data)); err != nil {
		t.Fatalf("retry admission remained closed: %v", err)
	}
}

func TestCompleteSourceAndJobCommitAreAtomicAndGenerationFenced(t *testing.T) {
	for _, mode := range []string{"job-write-fails", "generation-changes", "session-removed"} {
		t.Run(mode, func(t *testing.T) {
			db := database(t)
			session := smallSession(t, db)
			data := []byte("correct")
			digest := sha256.Sum256(data)
			sum := base64.StdEncoding.EncodeToString(digest[:])
			transaction(t, db, func(tx *sql.Tx) error {
				if err := AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu"); err != nil {
					return err
				}
				_, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 7, SHA256: sum})
				return err
			})
			if mode == "job-write-fails" {
				transaction(t, db, func(tx *sql.Tx) error {
					_, err := tx.Exec(`CREATE TRIGGER fail_ingest_job BEFORE INSERT ON jobs WHEN NEW.type='ingest' BEGIN SELECT RAISE(ABORT,'injected late job error'); END`)
					return err
				})
			}
			var completes atomic.Int64
			c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "GET" && r.URL.Query().Has("uploadId"):
					fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>part</ETag><Size>7</Size><ChecksumSHA256>%s</ChecksumSHA256></Part></ListPartsResult>`, sum)
				case r.Method == "POST":
					completes.Add(1)
					io.WriteString(w, `<CompleteMultipartUploadResult><ETag>source</ETag></CompleteMultipartUploadResult>`)
				case r.Method == "HEAD":
					w.Header().Set("Content-Length", "7")
					w.Header().Set("ETag", "source")
				case r.Method == "GET":
					if mode == "generation-changes" {
						transaction(t, db, func(tx *sql.Tx) error {
							_, err := tx.Exec("UPDATE upload_sessions SET generation=2 WHERE id=?", session.ID)
							return err
						})
					}
					if mode == "session-removed" {
						transaction(t, db, func(tx *sql.Tx) error {
							_, err := tx.Exec("DELETE FROM upload_sessions WHERE id=?", session.ID)
							return err
						})
					}
					w.Header().Set("Content-Length", "7")
					w.Header().Set("ETag", "source")
					w.Write(data)
				}
			}))
			if _, err := c.Complete(testContext, session.ID); err == nil {
				t.Fatal("failed/fenced publication reported success")
			}
			var jobs int
			if err := db.Reader().QueryRow("SELECT COUNT(*) FROM jobs WHERE type='ingest'").Scan(&jobs); err != nil || jobs != 0 {
				t.Fatal("job escaped failed/fenced transaction")
			}
			outcome, err := Lookup(testContext, db.Reader(), session.ID)
			if mode == "session-removed" {
				if !errors.Is(err, domain.ErrNotFound) {
					t.Fatal("removed session resurrected")
				}
				return
			}
			if err != nil || outcome.Session.State != domain.SessionCompleting || outcome.Session.SourceSha256.Valid {
				t.Fatalf("partial source commit: %+v %v", outcome, err)
			}
			if mode == "job-write-fails" {
				transaction(t, db, func(tx *sql.Tx) error { _, err := tx.Exec("DROP TRIGGER fail_ingest_job"); return err })
				outcome, err = c.Complete(testContext, session.ID)
				if err != nil || outcome.JobID == "" || outcome.Session.State != domain.SessionQueued || completes.Load() != 1 {
					t.Fatalf("post-commit recovery repeated Complete: %+v %v", outcome, err)
				}
			}
		})
	}
}

func TestConcurrentMultipartInitializationCannotDispatchTwoCreates(t *testing.T) {
	db := database(t)
	session := smallSession(t, db)
	bothListed, created, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var lists, creates atomic.Int64
	c := coordinator(t, db, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if lists.Add(1) == 2 {
				close(bothListed)
			}
			<-bothListed
			io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
			return
		}
		if creates.Add(1) != 1 {
			t.Error("two creates crossed the durable fence")
		} else {
			close(created)
		}
		<-release
		io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>only-mpu</UploadId></InitiateMultipartUploadResult>`)
	}))
	results := make(chan error, 2)
	for range 2 {
		go func() { _, err := c.EnsureMultipart(testContext, session.ID); results <- err }()
	}
	<-created
	if err := <-results; !errors.Is(err, objectio.ErrUnsettled) {
		t.Errorf("concurrent Create bypassed unresolved work: %v", err)
	}
	close(release)
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if creates.Load() != 1 {
		t.Fatal("duplicate native creation dispatched")
	}
}
