package deletion

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/catalog"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ctx = context.Background()
var now = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

func fixture(t *testing.T) (*store.Store, int64, string) {
	t.Helper()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	var itemID int64
	primary := domain.NewUUID()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		library, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		if err := tx.QueryRow("INSERT INTO items(media_kind,uploaded_at_utc,state) VALUES('Audio',?,'importing') RETURNING id", store.Timestamp(now)).Scan(&itemID); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,?,'primary','tag-files',?,7,?,'audio/flac','legacy','published')", primary, itemID, "primary/"+library.LibraryID+"/"+primary+"/media.flac", strings.Repeat("a", 64)); err != nil {
			return err
		}
		return q.MarkItemReady(ctx, dbgen.MarkItemReadyParams{ID: itemID, PrimaryAssetID: sql.NullString{String: primary, Valid: true}})
	}); err != nil {
		t.Fatal(err)
	}
	return db, itemID, primary
}
func count(t *testing.T, db *store.Store, table string) int {
	t.Helper()
	var n int
	if err := db.Reader().QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
func request(t *testing.T, db *store.Store, id, version int64) Result {
	t.Helper()
	var result Result
	if err := db.Write(ctx, func(tx *sql.Tx) error { var err error; result, err = Request(ctx, tx, id, version, now); return err }); err != nil {
		t.Fatal(err)
	}
	return result
}

// Any split between hiding, publication cancellation and durable work could
// leave a hidden item with no cleanup or an in-flight poster able to publish.
func TestRequestAtomicallyHidesSnapshotsAndQueuesIdempotentDeletion(t *testing.T) {
	db, id, primary := fixture(t)
	var jobID string
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		// The fixture uses an historical Audio item, which still has the full
		// deletion contract even though new uploads only accept image/video.
		body := fmt.Sprintf(`{"version":1,"itemId":%d,"primaryAssetId":%q,"profile":"poster-v1"}`, id, primary)
		job, err := dbgen.New(tx).CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: fmt.Sprint(id), DedupeKey: "poster/" + primary + "/poster-v1", PayloadJson: body, NextAttemptAt: store.Timestamp(now), CreatedAt: store.Timestamp(now)})
		jobID = job.ID
		return err
	}); err != nil {
		t.Fatal(err)
	}
	result := request(t, db, id, 1)
	if result.Absent || domain.ValidateUUID(result.JobID) != nil || domain.ValidateUUID(result.ReceiptID) != nil {
		t.Fatalf("missing working identity: %+v", result)
	}
	if _, err := catalog.Get(ctx, db.Reader(), id); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tombstone remained public: %v", err)
	}
	if _, err := dbgen.New(db.Reader()).GetJob(ctx, jobID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("poster publication not cancelled")
	}
	if count(t, db, "deletion_assets") != 1 || count(t, db, "cleanup_receipts") != 1 || count(t, db, "jobs") != 1 || count(t, db, "assets") != 1 {
		t.Fatal("snapshot/cleanup not atomic or original metadata removed prematurely")
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE deletion_assets SET object_key='foreign/rebound'")
		return err
	}); err == nil {
		t.Fatal("deletion inventory ownership rebound")
	}
	if again := request(t, db, id, 1); again != result {
		t.Fatalf("repeat changed deletion identity: %+v %+v", result, again)
	}
	if absent := request(t, db, id+999, 1); !absent.Absent {
		t.Fatal("absent item queued new work")
	}
}

func TestDeletionRefusesForeignAssetBeyondFirstInventoryPage(t *testing.T) {
	db, id, _ := fixture(t)
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		meta, err := dbgen.New(tx).GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		for i := 1; i <= 101; i++ {
			asset := fmt.Sprintf("%08x-0000-4000-8000-%012x", i, i)
			library := meta.LibraryID
			if i == 101 {
				library = domain.NewUUID()
			}
			if _, err := tx.Exec("INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,profile_version,state) VALUES(?,?,'poster','tag-files',?,1,?,'image/jpeg','legacy','legacy-v1','published')", asset, id, "previews/"+library+"/"+asset+"/legacy-v1/poster.jpg", strings.Repeat("a", 64)); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error { _, err := Request(ctx, tx, id, 1, now); return err }); !errors.Is(err, ErrIncompatible) {
		t.Fatalf("later foreign key accepted: %v", err)
	}
	if _, err := catalog.Get(ctx, db.Reader(), id); err != nil {
		t.Fatal("refused snapshot hid item", err)
	}
	if count(t, db, "cleanup_receipts") != 0 || count(t, db, "deletion_assets") != 0 {
		t.Fatal("refused snapshot left working records")
	}
}

func TestStaleVersionUnknownPosterAndCommitFailureLeaveItemUntouched(t *testing.T) {
	for _, mode := range []string{"stale", "future-poster", "receipt-failure"} {
		t.Run(mode, func(t *testing.T) {
			db, id, primary := fixture(t)
			if err := db.Write(ctx, func(tx *sql.Tx) error {
				switch mode {
				case "future-poster":
					_, err := dbgen.New(tx).CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: fmt.Sprint(id), DedupeKey: "poster/" + primary + "/poster-v1", PayloadJson: fmt.Sprintf(`{"version":2,"itemId":%d,"primaryAssetId":%q,"profile":"poster-v1"}`, id, primary), NextAttemptAt: store.Timestamp(now), CreatedAt: store.Timestamp(now)})
					return err
				case "receipt-failure":
					_, err := tx.Exec("CREATE TRIGGER fail_delete_receipt BEFORE INSERT ON cleanup_receipts BEGIN SELECT RAISE(ABORT,'fixture'); END")
					return err
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			version := int64(1)
			if mode == "stale" {
				version = 2
			}
			err := db.Write(ctx, func(tx *sql.Tx) error { _, err := Request(ctx, tx, id, version, now); return err })
			if err == nil {
				t.Fatal("invalid deletion succeeded")
			}
			if mode == "stale" && !errors.Is(err, domain.ErrStaleVersion) {
				t.Fatal(err)
			}
			if mode == "future-poster" && !errors.Is(err, poster.ErrIncompatibleJob) {
				t.Fatal(err)
			}
			if _, err := catalog.Get(ctx, db.Reader(), id); err != nil {
				t.Fatal("failed command hid item", err)
			}
			if count(t, db, "cleanup_receipts") != 0 || count(t, db, "deletion_assets") != 0 {
				t.Fatal("failed command retained partial deletion")
			}
		})
	}
}
