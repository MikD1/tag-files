package objectio_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
)

var ctx = context.Background()

const epochOne = "11111111-1111-4111-8111-111111111111"
const epochTwo = "22222222-2222-4222-8222-222222222222"

func setup(t *testing.T) (*store.Store, ingestion.Session) {
	t.Helper()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var batch ingestion.Batch
	err = db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		batch, err = ingestion.Create(ctx, tx, []ingestion.File{{ClientFileID: domain.NewUUID(), Filename: "private-name.heic", Size: 7}}, nil, ingestion.DefaultLimits(), time.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return db, batch.Sessions[0]
}
func client(t *testing.T, url string) *objectstore.Client {
	t.Helper()
	c, err := objectstore.New(objectstore.Config{Endpoint: url, Region: "us-east-1", Bucket: "tag-files", AccessKey: "explicit-access", SecretKey: "explicit-secret", HTTPClient: &http.Client{Timeout: 3 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func prepare(session ingestion.Session) func(*sql.Tx) (objectio.Request, error) {
	return func(tx *sql.Tx) (objectio.Request, error) {
		return objectio.Request{TargetKind: "upload", TargetID: session.ID, Generation: 1, Operation: objectio.CreateMPU, Key: session.IncomingKey, Arguments: objectio.Arguments{Size: 7, ContentType: "application/octet-stream"}}, nil
	}
}
func count(t *testing.T, db *store.Store) int {
	t.Helper()
	var count int
	if err := db.Reader().QueryRow("SELECT COUNT(*) FROM object_write_intents").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestIntentCommitsBeforeS3AndDrainsThroughMetadataWithoutHoldingWriter(t *testing.T) {
	db, session := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.Method == "HEAD" {
			return
		}
		var savedEpoch, key, payload string
		if err := db.Reader().QueryRow("SELECT storage_epoch,object_key,payload_json FROM object_write_intents").Scan(&savedEpoch, &key, &payload); err != nil {
			t.Error("request preceded durable intent:", err)
		}
		if savedEpoch != epochOne || r.Header.Get("X-Tag-Files-Expected-Storage-Epoch") != epochOne || key != session.IncomingKey {
			t.Error("request does not match its durable epoch/key")
		}
		var args map[string]any
		if json.Unmarshal([]byte(payload), &args) != nil || args["size"] != float64(7) || args["contentType"] != "application/octet-stream" || args["filename"] != nil {
			t.Error("intent did not preserve operational arguments")
		}
		writeCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		if err := db.Write(writeCtx, func(tx *sql.Tx) error {
			_, err := tx.Exec("INSERT INTO tags(name) VALUES('writer available during S3')")
			return err
		}); err != nil {
			t.Error("S3 held the SQLite writer:", err)
		}
		close(entered)
		<-release
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><Bucket>tag-files</Bucket><Key>source</Key><UploadId>owned-mpu</UploadId></InitiateMultipartUploadResult>`))
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := m.CreateMultipart(ctx, prepare(session), func(id string) error {
			return db.Write(ctx, func(tx *sql.Tx) error { return ingestion.AttachMultipart(ctx, tx, session.ID, 1, id) })
		})
		done <- err
	}()
	<-entered
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	if err := m.Drain(waitCtx, session.IncomingKey, objectio.CreateMPU); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("known write was not drained: %v", err)
	}
	cancel()
	_, err = m.CreateMultipart(ctx, prepare(session), func(string) error { return nil })
	if !errors.Is(err, objectio.ErrBusy) || count(t, db) != 1 {
		t.Errorf("capacity admitted an extra request: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := m.Drain(ctx, session.IncomingKey, objectio.CreateMPU); err != nil {
		t.Fatal(err)
	}
	if count(t, db) != 0 {
		t.Fatal("successful write archived as history")
	}
	got, err := ingestion.Lookup(ctx, db.Reader(), session.ID)
	if err != nil || got.Session.State != domain.SessionReceiving || got.Session.MultipartUploadID.String != "owned-mpu" {
		t.Fatalf("metadata was not applied: %+v %v", got, err)
	}
}

func TestUnknownResultSurvivesParentDeletionAndNeedsEpochBarrierBeforeReconcile(t *testing.T) {
	db, session := setup(t)
	var epoch atomic.Value
	epoch.Store(epochOne)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epoch.Load().(string))
		if r.Method == "HEAD" {
			return
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		conn.Close()
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateMultipart(ctx, prepare(session), func(string) error { t.Error("unknown result reached metadata"); return nil })
	if err == nil || count(t, db) != 1 {
		t.Fatal("unknown accepted write was forgotten")
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.Exec("DELETE FROM upload_sessions"); return err }); err != nil {
		t.Fatal(err)
	}
	records, err := m.List(ctx, "", 100)
	if err != nil || len(records) != 1 || records[0].State != "uncertain" || records[0].StorageEpoch != epochOne {
		t.Fatalf("independent unknown work: %+v %v", records, err)
	}
	called := false
	err = m.ReconcileAfterRestart(ctx, records[0], func(objectio.ReconciliationClient, objectio.Intent) error { called = true; return nil })
	if !errors.Is(err, objectio.ErrUnsettled) || called || count(t, db) != 1 {
		t.Fatal("same-epoch listing/age substituted for settlement proof")
	}
	epoch.Store(epochTwo)
	reconcileFailure := errors.New("owned result still needs reconciliation")
	err = m.ReconcileAfterRestart(ctx, records[0], func(objectio.ReconciliationClient, objectio.Intent) error { return reconcileFailure })
	if !errors.Is(err, reconcileFailure) || count(t, db) != 1 {
		t.Fatal("epoch change erased unreconciled work")
	}
	err = m.ReconcileAfterRestart(ctx, records[0], func(c objectio.ReconciliationClient, intent objectio.Intent) error {
		if intent.TargetID != session.ID || intent.Key != session.IncomingKey || count(t, db) != 1 {
			t.Error("lost reconciliation context")
		}
		return nil
	})
	if err != nil || count(t, db) != 0 {
		t.Fatalf("reconciled old work retained: %v", err)
	}
}

func TestMetadataAcknowledgementDoesNotConsumeTheSettlementDeadline(t *testing.T) {
	db, session := setup(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.Method == "HEAD" {
			return
		}
		_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>settled-mpu</UploadId></InitiateMultipartUploadResult>`))
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateMultipart(ctx, prepare(session), func(id string) error {
		// A metadata writer can be delayed after the network call has ended.
		time.Sleep(5100 * time.Millisecond)
		return db.Write(ctx, func(tx *sql.Tx) error { return ingestion.AttachMultipart(ctx, tx, session.ID, 1, id) })
	})
	if err != nil || count(t, db) != 0 {
		t.Fatalf("settled RPC retained because metadata used its deadline: %v", err)
	}
}

func TestUUIDCasingCannotMasqueradeAsANewStorageLifetime(t *testing.T) {
	db, session := setup(t)
	const epoch = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Tag-Files-Storage-Epoch", epoch) }))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewUUID()
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_json,state,created_at_utc)
VALUES(?,?,'upload',?,1,'create_mpu',?,?,'{"size":7,"contentType":"application/octet-stream"}','uncertain','2001-01-01T00:00:00.0000000Z')`, id, domain.NewUUID(), session.ID, session.IncomingKey, strings.ToUpper(epoch))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	err = m.ReconcileAfterRestart(ctx, objectio.Intent{ID: id}, func(objectio.ReconciliationClient, objectio.Intent) error { called = true; return nil })
	if !errors.Is(err, objectio.ErrUnsettled) || called || count(t, db) != 1 {
		t.Fatalf("same UUID treated as a storage restart: %v", err)
	}
}

func TestUnknownIntentPayloadVersionIsPreservedWithoutCallingReconcile(t *testing.T) {
	db, session := setup(t)
	m, err := objectio.New(db, client(t, "http://127.0.0.1:1"), 1)
	if err != nil {
		t.Fatal(err)
	}
	id := domain.NewUUID()
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_version,payload_json,state,created_at_utc)
VALUES(?,?,'upload',?,1,'create_mpu',?,?,99,'{"future":"preserve"}','uncertain','2001-01-01T00:00:00.0000000Z')`, id, domain.NewUUID(), session.ID, session.IncomingKey, epochOne)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = m.ReconcileAfterRestart(ctx, objectio.Intent{ID: id}, func(objectio.ReconciliationClient, objectio.Intent) error {
		t.Error("unknown payload reached domain cleanup")
		return nil
	})
	if !errors.Is(err, objectio.ErrIncompatibleIntent) || count(t, db) != 1 {
		t.Fatalf("unknown intent was interpreted/dropped: %v", err)
	}
}

func TestExplicitEpochRejectionIsSettledWithoutApplyingMetadata(t *testing.T) {
	db, session := setup(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "HEAD" {
			w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
			return
		}
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochTwo)
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(409)
		_, _ = w.Write([]byte(`<Error><Code>StorageEpochChanged</Code><Message>restarted</Message></Error>`))
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateMultipart(ctx, prepare(session), func(string) error { t.Error("rejected write reached metadata"); return nil })
	var wire *objectstore.Error
	if !errors.As(err, &wire) || wire.Code != "StorageEpochChanged" || count(t, db) != 0 {
		t.Fatalf("explicit rejection retained as uncertain work: %v", err)
	}
}

func TestKnownSuccessCanSettleAfterCallerCancellation(t *testing.T) {
	db, session := setup(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.Method == "HEAD" {
			return
		}
		_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>settled-mpu</UploadId></InitiateMultipartUploadResult>`))
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, err = m.CreateMultipart(requestCtx, prepare(session), func(id string) error {
		cancel()
		return db.Write(requestCtx, func(tx *sql.Tx) error { return ingestion.AttachMultipart(requestCtx, tx, session.ID, 1, id) })
	})
	if !errors.Is(err, context.Canceled) || count(t, db) != 0 {
		t.Fatalf("caller cancellation discarded known settlement: %v", err)
	}
}

func TestInvalidIntentRollsBackItsDomainPreparationBeforeForwarding(t *testing.T) {
	db, session := setup(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epochOne)
		if r.Method != "HEAD" {
			t.Error("invalid write reached S3")
		}
	}))
	defer server.Close()
	m, err := objectio.New(db, client(t, server.URL), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.CreateMultipart(ctx, func(tx *sql.Tx) (objectio.Request, error) {
		if _, err := tx.Exec("INSERT INTO tags(name) VALUES('must roll back')"); err != nil {
			return objectio.Request{}, err
		}
		r, err := prepare(session)(tx)
		r.Generation = 0
		return r, err
	}, func(string) error { t.Error("invalid write applied metadata"); return nil })
	var tags int
	if readErr := db.Reader().QueryRow("SELECT COUNT(*) FROM tags").Scan(&tags); readErr != nil {
		t.Fatal(readErr)
	}
	if err == nil || tags != 0 || count(t, db) != 0 {
		t.Fatal("domain preparation committed without a valid write intent")
	}
}

func TestEpochRecoveryWaitsForKnownWriteThroughMetadataBeforeReconciliation(t *testing.T) {
	db, session := setup(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var epoch atomic.Value
	epoch.Store(epochOne)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tag-Files-Storage-Epoch", epoch.Load().(string))
		if r.Method == "HEAD" {
			return
		}
		close(entered)
		<-release
		_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>known-result</UploadId></InitiateMultipartUploadResult>`))
	}))
	defer server.Close()
	manager, err := objectio.New(db, client(t, server.URL), 2)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.CreateMultipart(ctx, prepare(session), func(id string) error {
			return db.Write(ctx, func(tx *sql.Tx) error { return ingestion.AttachMultipart(ctx, tx, session.ID, 1, id) })
		})
		done <- err
	}()
	<-entered
	intents, err := manager.List(ctx, "", 100)
	if err != nil || len(intents) != 1 {
		t.Fatal("missing live intent")
	}
	epoch.Store(epochTwo)
	recoveryCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
	called := false
	err = manager.ReconcileAfterRestart(recoveryCtx, intents[0], func(objectio.ReconciliationClient, objectio.Intent) error { called = true; return nil })
	cancel()
	close(release)
	if writeErr := <-done; writeErr != nil {
		t.Fatal(writeErr)
	}
	if !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("epoch change bypassed known writer drain: %v callback=%v", err, called)
	}
	if err := manager.ReconcileAfterRestart(ctx, intents[0], func(objectio.ReconciliationClient, objectio.Intent) error {
		t.Error("already settled live work reconciled again")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
