package ingestion

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var testContext = context.Background()
var testTime = time.Date(2026, 11, 14, 16, 0, 0, 123456700, time.UTC)

func database(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(testContext, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func transaction(t *testing.T, s *store.Store, fn func(*sql.Tx) error) {
	t.Helper()
	if err := s.Write(testContext, fn); err != nil {
		t.Fatal(err)
	}
}
func files() []File {
	return []File{
		{ClientFileID: "11111111-1111-4111-8111-111111111111", Filename: "same.heic", Size: 32*1024*1024 + 1, Fingerprint: strings.Repeat("a", 64)},
		{ClientFileID: "22222222-2222-4222-8222-222222222222", Filename: "same.heic", Size: 1024},
	}
}

func TestCreatePersistsWholeBatchIdentityAndCollectionBeforeAnyStorage(t *testing.T) {
	s := database(t)
	var collectionID int64
	var batch Batch
	transaction(t, s, func(tx *sql.Tx) error {
		collection, err := dbgen.New(tx).CreateCollection(testContext, "new uploads")
		if err != nil {
			return err
		}
		collectionID = collection.ID
		batch, err = Create(testContext, tx, files(), &collectionID, DefaultLimits(), testTime)
		return err
	})
	if domain.ValidateUUID(batch.ID) != nil || len(batch.Sessions) != 2 || batch.Sessions[0].ID == batch.Sessions[1].ID {
		t.Fatalf("batch identities: %+v", batch)
	}
	var libraryID string
	if err := s.Reader().QueryRow("SELECT library_id FROM library_meta").Scan(&libraryID); err != nil {
		t.Fatal(err)
	}
	for i, session := range batch.Sessions {
		if session.BatchID != batch.ID || session.ClientFileID != files()[i].ClientFileID || session.ClientFilename != "same.heic" || session.State != domain.SessionCreating || session.Generation != 1 || session.MultipartUploadID.Valid || session.CreatedAtUtc != "2026-11-14T16:00:00.1234567Z" || session.CollectionID.Int64 != collectionID || !session.CollectionID.Valid {
			t.Fatalf("durable creation intent: %+v", session)
		}
		if session.IncomingKey != "incoming/"+libraryID+"/"+session.ID+"/source" {
			t.Fatalf("unowned/filename-derived key: %q", session.IncomingKey)
		}
		wantCount := int64(1)
		if i == 0 {
			wantCount = 2
		}
		if session.PartSize != 32*1024*1024 || session.PartCount != wantCount {
			t.Fatalf("part geometry: %+v", session)
		}
		got, err := Lookup(testContext, s.Reader(), session.ID)
		if err != nil || got.State != "pending" || got.Session == nil || len(got.Session.Parts) != 0 {
			t.Fatalf("lookup intent: %+v %v", got, err)
		}
	}
	var count int
	if err := s.Reader().QueryRow("SELECT COUNT(*) FROM jobs").Scan(&count); err != nil || count != 0 {
		t.Fatal("processing queued before verified input")
	}
}

func TestInvalidBatchRollsBackAndUUIDIdentityIsCaseInsensitive(t *testing.T) {
	for _, mutate := range []func([]File){
		func(f []File) { f[1].Size = 0 },
		func(f []File) { f[1].Size = 20*1024*1024*1024 + 1 },
		func(f []File) { f[1].Fingerprint = "bad" },
		func(f []File) { f[1].Filename = strings.Repeat("x", 1025) },
		func(f []File) { f[1].ClientFileID = "bad" },
		func(f []File) {
			f[0].ClientFileID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
			f[1].ClientFileID = strings.ToUpper(f[0].ClientFileID)
		},
	} {
		s := database(t)
		request := files()
		mutate(request)
		err := s.Write(testContext, func(tx *sql.Tx) error {
			_, err := Create(testContext, tx, request, nil, DefaultLimits(), testTime)
			return err
		})
		var validation *domain.ValidationError
		if !errors.As(err, &validation) {
			t.Fatalf("invalid batch accepted: %v", err)
		}
		var count int
		if err := s.Reader().QueryRow("SELECT COUNT(*) FROM upload_sessions").Scan(&count); err != nil || count != 0 {
			t.Fatal("partial batch persisted")
		}
	}
}

func createSession(t *testing.T, s *store.Store) Session {
	t.Helper()
	var batch Batch
	transaction(t, s, func(tx *sql.Tx) error {
		var err error
		batch, err = Create(testContext, tx, files()[:1], nil, DefaultLimits(), testTime)
		return err
	})
	return batch.Sessions[0]
}

func TestMultipartIdentityAndPartBindingsAreImmutableAndSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := store.Open(testContext, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	session := createSession(t, s)
	digest := base64.StdEncoding.EncodeToString(make([]byte, 32))
	command := PartBinding{Number: 1, Size: 32 * 1024 * 1024, SHA256: digest}
	err = s.Write(testContext, func(tx *sql.Tx) error { _, err := BindPart(testContext, tx, session.ID, 1, command); return err })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("part accepted before MPU: %v", err)
	}
	transaction(t, s, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, session.ID, 1, "opaque-s3-id") })
	transaction(t, s, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, session.ID, 1, "opaque-s3-id") })
	for _, mpu := range []string{"another-mpu"} {
		err = s.Write(testContext, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, session.ID, 1, mpu) })
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("MPU identity changed: %v", err)
		}
	}
	transaction(t, s, func(tx *sql.Tx) error { _, err := BindPart(testContext, tx, session.ID, 1, command); return err })
	transaction(t, s, func(tx *sql.Tx) error { _, err := BindPart(testContext, tx, session.ID, 1, command); return err })
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(testContext, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Lookup(testContext, s.Reader(), strings.ToUpper(session.ID))
	if err != nil || got.Session == nil || len(got.Session.Parts) != 1 || got.Session.Parts[0].SHA256 != digest || got.Session.Parts[0].State != "bound" || got.Session.MultipartUploadID.String != "opaque-s3-id" {
		t.Fatalf("lost binding on reopen: %+v %v", got, err)
	}
	for _, change := range []PartBinding{{Number: 1, Size: 32*1024*1024 - 1, SHA256: digest}, {Number: 1, Size: 32 * 1024 * 1024, SHA256: base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))}, {Number: 2, Size: 2, SHA256: digest}, {Number: 3, Size: 1, SHA256: digest}} {
		err = s.Write(testContext, func(tx *sql.Tx) error { _, err := BindPart(testContext, tx, session.ID, 1, change); return err })
		if err == nil {
			t.Fatalf("changed/invalid binding accepted: %+v", change)
		}
	}
	transaction(t, s, func(tx *sql.Tx) error {
		_, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 2, Size: 1, SHA256: digest})
		return err
	})
	err = s.Write(testContext, func(tx *sql.Tx) error { _, err := BindPart(testContext, tx, session.ID, 2, command); return err })
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale generation wrote part: %v", err)
	}
}

func TestBatchProjectionRetainsOldFailuresAndDropsEmptyBatches(t *testing.T) {
	s := database(t)
	first := createSession(t, s)
	var second Batch
	transaction(t, s, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(testContext, "UPDATE upload_sessions SET state='failed',error_code='unsupported_media_type',created_at_utc='2001-01-01T00:00:00.0000000Z' WHERE id=?", first.ID); err != nil {
			return err
		}
		var err error
		second, err = Create(testContext, tx, files(), nil, DefaultLimits(), testTime)
		return err
	})
	batches, err := ListBatches(testContext, s.Reader())
	if err != nil || len(batches) != 2 || batches[0].ID != first.BatchID || len(batches[1].Sessions) != 2 {
		t.Fatalf("current queue projection: %+v %v", batches, err)
	}
	if batches[0].Sessions[0].State != domain.SessionFailed || batches[0].Sessions[0].ErrorCode.String != "unsupported_media_type" {
		t.Fatal("old failure expired or lost error")
	}
	transaction(t, s, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(testContext, "DELETE FROM upload_sessions WHERE batch_id=?", second.ID)
		return err
	})
	batches, err = ListBatches(testContext, s.Reader())
	if err != nil || len(batches) != 1 || batches[0].ID != first.BatchID {
		t.Fatalf("empty batch retained as history: %+v %v", batches, err)
	}
}

func TestFilenameLimitMatchesOpenAPIUnicodeCharacters(t *testing.T) {
	s := database(t)
	request := files()[:1]
	request[0].Filename = strings.Repeat("📸", 1024)
	transaction(t, s, func(tx *sql.Tx) error {
		batch, err := Create(testContext, tx, request, nil, DefaultLimits(), testTime)
		if err == nil && batch.Sessions[0].ClientFilename != request[0].Filename {
			t.Fatal("filename was normalized or truncated")
		}
		return err
	})
}

func TestConcurrentPartBindingsCannotChooseTwoDifferentDigests(t *testing.T) {
	s := database(t)
	session := createSession(t, s)
	transaction(t, s, func(tx *sql.Tx) error { return AttachMultipart(testContext, tx, session.ID, 1, "owned-mpu") })
	var workers sync.WaitGroup
	results := make(chan error, 2)
	for _, value := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32)} {
		workers.Go(func() {
			results <- s.Write(testContext, func(tx *sql.Tx) error {
				_, err := BindPart(testContext, tx, session.ID, 1, PartBinding{Number: 1, Size: 32 * 1024 * 1024, SHA256: base64.StdEncoding.EncodeToString([]byte(value))})
				return err
			})
		})
	}
	workers.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrConflict) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("two byte identities accepted for a part: success=%d conflict=%d", succeeded, conflicted)
	}
	got, err := Lookup(testContext, s.Reader(), session.ID)
	if err != nil || len(got.Session.Parts) != 1 {
		t.Fatalf("duplicate binding rows: %+v %v", got, err)
	}
}

func TestBatchRollsBackEarlierSessionsWhenALaterSQLiteWriteFails(t *testing.T) {
	s := database(t)
	transaction(t, s, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(testContext, `CREATE TRIGGER fail_second_session BEFORE INSERT ON upload_sessions
WHEN NEW.client_file_id='22222222-2222-4222-8222-222222222222'
BEGIN SELECT RAISE(ABORT,'injected SQLite write failure'); END`)
		return err
	})
	err := s.Write(testContext, func(tx *sql.Tx) error {
		_, err := Create(testContext, tx, files(), nil, DefaultLimits(), testTime)
		return err
	})
	if err == nil {
		t.Fatal("failed batch reported success")
	}
	var sessions int
	if err := s.Reader().QueryRow("SELECT COUNT(*) FROM upload_sessions").Scan(&sessions); err != nil || sessions != 0 {
		t.Fatalf("partial batch committed after SQLite failure: %d %v", sessions, err)
	}
}
