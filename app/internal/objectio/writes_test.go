package objectio_test

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
)

func prepareOutput(session ingestion.Session, content []byte) func(*sql.Tx) (objectio.Request, error) {
	sum := sha256.Sum256(content)
	return func(*sql.Tx) (objectio.Request, error) {
		return objectio.Request{TargetKind: "asset", TargetID: session.ID, Generation: 1, Operation: objectio.PutObject, Key: "primary/" + session.ID + "/output", Arguments: objectio.Arguments{Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:]), ContentType: "application/octet-stream"}}, nil
	}
}

func TestPutCannotPublishFromAMatchingOlderObjectAfterItsResponseIsLost(t *testing.T) {
	db, session := setup(t)
	content := []byte("correct")
	var verification atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.URL.Path == "/tag-files" && r.Method == "HEAD" {
			return
		}
		if r.Method == "PUT" {
			_, _ = io.Copy(io.Discard, r.Body)
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		// These bytes may belong to an older identical attempt. They do not
		// establish that the just-accepted PUT stopped after losing its reply.
		verification.Add(1)
		w.Header().Set("Content-Length", "7")
		w.Header().Set("ETag", `"opaque"`)
		if r.Method == "GET" {
			_, _ = w.Write(content)
		}
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Put(ctx, prepareOutput(session, content), bytes.NewReader(content), func(objectstore.Verified) error { t.Error("ambiguous PUT applied metadata"); return nil })
	if err == nil || verification.Load() != 0 || count(t, db) != 1 {
		t.Fatalf("matching data substituted for write settlement: reads=%d error=%v", verification.Load(), err)
	}
	records, err := m.List(ctx, "", 100)
	if err != nil || records[0].State != "uncertain" {
		t.Fatal("ambiguous PUT was not retained")
	}
}

func TestPutRequiresFullObjectHashEvenAfterSuccessfulWriteResponse(t *testing.T) {
	db, session := setup(t)
	content := []byte("correct")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.URL.Path == "/tag-files" && r.Method == "HEAD" {
			return
		}
		if r.Method == "PUT" {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"opaque"`)
			return
		}
		w.Header().Set("Content-Length", "7")
		w.Header().Set("ETag", `"opaque"`)
		if r.Method == "GET" {
			_, _ = w.Write([]byte("corrupt"))
		}
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.Put(ctx, prepareOutput(session, content), bytes.NewReader(content), func(objectstore.Verified) error { t.Error("corrupt stored bytes reached publication"); return nil })
	if !errors.Is(err, objectstore.ErrIntegrity) || count(t, db) != 1 {
		t.Fatalf("PUT response replaced full verification: %v", err)
	}
}

func TestValidPutReachesMetadataOnlyAfterFullGetAndHash(t *testing.T) {
	db, session := setup(t)
	content := []byte("correct")
	sum := sha256.Sum256(content)
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.URL.Path == "/tag-files" && r.Method == "HEAD" {
			return
		}
		if r.Method == "PUT" {
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", `"opaque"`)
			return
		}
		w.Header().Set("Content-Length", "7")
		w.Header().Set("ETag", `"opaque"`)
		if r.Method == "GET" {
			reads.Add(1)
			_, _ = w.Write(content)
		}
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	result, err := m.Put(ctx, prepareOutput(session, content), bytes.NewReader(content), func(verified objectstore.Verified) error {
		called = true
		if reads.Load() != 1 || verified.Size != 7 || verified.SHA256 != hex.EncodeToString(sum[:]) || count(t, db) != 1 {
			t.Error("metadata preceded full byte verification")
		}
		return nil
	})
	if err != nil || !called || result.SHA256 != hex.EncodeToString(sum[:]) || count(t, db) != 0 {
		t.Fatalf("valid verified output was not settled: %v", err)
	}
}

func TestUploadPartUsesTheDurableBindingAndVerifiedChecksumAcknowledgement(t *testing.T) {
	db, session := setup(t)
	if err := db.Write(ctx, func(tx *sql.Tx) error { return ingestion.AttachMultipart(ctx, tx, session.ID, 1, "owned-mpu") }); err != nil {
		t.Fatal(err)
	}
	content := []byte("correct")
	digest := sha256.Sum256(content)
	checksum := base64.StdEncoding.EncodeToString(digest[:])
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.Method == "HEAD" {
			return
		}
		var bound string
		if err := db.Reader().QueryRow("SELECT sha256 FROM upload_parts WHERE session_id=? AND part_number=1", session.ID).Scan(&bound); err != nil || bound != checksum {
			t.Error("part preceded its committed binding")
		}
		if count(t, db) != 1 || r.URL.Query().Get("uploadId") != "owned-mpu" || r.URL.Query().Get("partNumber") != "1" || r.Header.Get("X-Amz-Checksum-Sha256") != checksum {
			t.Error("part differed from the registered request")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, content) {
			t.Error("streamed bytes changed")
		}
		w.Header().Set("ETag", `"opaque"`)
		w.Header().Set("X-Amz-Checksum-Sha256", checksum)
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.UploadPart(ctx, func(tx *sql.Tx) (objectio.Request, error) {
		if _, err := ingestion.BindPart(ctx, tx, session.ID, 1, ingestion.PartBinding{Number: 1, Size: 7, SHA256: checksum}); err != nil {
			return objectio.Request{}, err
		}
		return objectio.Request{TargetKind: "upload", TargetID: session.ID, Generation: 1, Operation: objectio.UploadPart, Key: session.IncomingKey, Arguments: objectio.Arguments{Size: 7, MultipartID: "owned-mpu", PartNumber: 1, SHA256: checksum}}, nil
	}, bytes.NewReader(content), func(part objectstore.Part) error {
		return db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.Exec("UPDATE upload_parts SET state='confirmed',etag=? WHERE session_id=? AND part_number=1", part.ETag, session.ID)
			return err
		})
	})
	if err != nil || count(t, db) != 0 {
		t.Fatalf("verified part was not settled: %v", err)
	}
	got, err := ingestion.Lookup(ctx, db.Reader(), session.ID)
	if err != nil || got.Session.Parts[0].State != "confirmed" || got.Session.Parts[0].ETag != `"opaque"` {
		t.Fatalf("part acknowledgement lost: %+v %v", got, err)
	}
}

func TestCompleteBindsFrozenManifestAndVerifiesEveryIncomingByte(t *testing.T) {
	db, session := setup(t)
	content := []byte("correct")
	sum := sha256.Sum256(content)
	checksum := base64.StdEncoding.EncodeToString(sum[:])
	parts := []objectstore.Part{{Number: 1, Size: 7, ETag: `"opaque"`, SHA256: checksum}}
	canonical := fmt.Sprintf(`{"version":1,"parts":[{"partNumber":1,"size":7,"etag":"\"opaque\"","sha256":"%s"}]}`, checksum)
	manifestSum := sha256.Sum256([]byte(canonical))
	manifestHash := hex.EncodeToString(manifestSum[:])
	size, identity, err := objectio.CompletionIdentity(parts)
	if err != nil || size != 7 || identity != manifestHash {
		t.Fatalf("manifest identity is not the versioned request: %d %s %v", size, identity, err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.URL.Path == "/tag-files" && r.Method == "HEAD" {
			return
		}
		if r.Method == "POST" {
			var manifest struct {
				Parts []struct {
					Number int    `xml:"PartNumber"`
					ETag   string `xml:"ETag"`
					SHA256 string `xml:"ChecksumSHA256"`
				} `xml:"Part"`
			}
			if err := xml.NewDecoder(r.Body).Decode(&manifest); err != nil || len(manifest.Parts) != 1 || manifest.Parts[0].ETag != `"opaque"` || manifest.Parts[0].SHA256 != checksum {
				t.Error("Complete sent changed/unbound parts")
			}
			var raw string
			if err := db.Reader().QueryRow("SELECT payload_json FROM object_write_intents").Scan(&raw); err != nil {
				t.Error(err)
			}
			var recorded map[string]any
			if json.Unmarshal([]byte(raw), &recorded) != nil || recorded["manifestSha256"] != manifestHash {
				t.Error("Complete preceded its frozen manifest intent")
			}
			_, _ = w.Write([]byte(`<CompleteMultipartUploadResult><Bucket>tag-files</Bucket><Key>source</Key><ETag>object-etag</ETag></CompleteMultipartUploadResult>`))
			return
		}
		w.Header().Set("Content-Length", "7")
		w.Header().Set("ETag", `"object-etag"`)
		if r.Method == "GET" {
			_, _ = w.Write(content)
		}
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := m.Complete(ctx, func(*sql.Tx) (objectio.Request, error) {
		// The caller's mutable slice cannot change the persisted request.
		parts[0].ETag = `"changed after preparation"`
		return objectio.Request{TargetKind: "upload", TargetID: session.ID, Generation: 1, Operation: objectio.CompleteMPU, Key: session.IncomingKey, Arguments: objectio.Arguments{Size: 7, MultipartID: "owned-mpu", ManifestSHA256: manifestHash}}, nil
	}, parts, func(result objectstore.Verified) error {
		if result.Size != 7 || result.SHA256 != hex.EncodeToString(sum[:]) {
			t.Error("unverified input reached metadata")
		}
		return nil
	})
	if err != nil || verified.SHA256 != hex.EncodeToString(sum[:]) || count(t, db) != 0 {
		t.Fatalf("Complete did not fully verify/settle: %+v %v", verified, err)
	}
}
