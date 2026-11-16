package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
)

func TestMigrationPublishedPlansUseRuntimeOwnedObjectKeys(t *testing.T) {
	m, _, _ := scanned(t)
	library := "be69309a-e888-4435-ad61-9fc8aa9fabcf"
	var primary, poster int
	for _, plan := range makePlans(m, library) {
		if plan.Role == "quarantine" {
			continue
		}
		if !domain.OwnedObjectKey(library, plan.AssetID, plan.Role, plan.Key) {
			t.Errorf("published %s is inaccessible to media serving: %s", plan.Role, plan.Key)
		}
		if plan.Role == "primary" {
			primary++
		} else if plan.Role == "poster" {
			poster++
		}
	}
	if primary != 5 || poster != 2 {
		t.Fatalf("missing plan coverage: %d primaries / %d posters", primary, poster)
	}
}

func TestLegacyInaccessiblePublishedMappingsCannotVerifyOrResume(t *testing.T) {
	m, source, archive := scanned(t)
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "target"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	destination := newDestination()
	config := ApplyConfig{Archive: archive, Journal: filepath.Join(t.TempDir(), "journal.sqlite"), TargetID: "fixture-target", Budget: Budget{WritePeakBytes: 1, MetadataBytes: 1, ReserveBytes: 1}, Capacity: func(context.Context) (int64, int64, error) { return 1 << 40, 1 << 20, nil }}
	if err = Apply(ctx, config, m, source, db, destination); err != nil {
		t.Fatal(err)
	}
	var library string
	if err = db.Reader().QueryRow(`SELECT library_id FROM library_meta WHERE singleton=1`).Scan(&library); err != nil {
		t.Fatal(err)
	}

	// Reproduce the descriptors emitted by the previous tool. Trigger removal is
	// fixture preparation only; the migration itself must never rewrite bytes.
	plans := makePlans(m, library)
	legacy := map[string]string{}
	for _, plan := range plans {
		if plan.Role == "quarantine" {
			continue
		}
		key := "primary/" + library + "/" + plan.AssetID + "/media"
		if plan.Role == "poster" {
			key = "previews/" + library + "/" + plan.AssetID + "/poster.jpg"
		}
		legacy[plan.ID] = key
		destination.data[key] = destination.data[plan.Key]
		object := destination.info[plan.Key]
		object.Key = key
		destination.info[key] = object
		if key != plan.Key {
			delete(destination.data, plan.Key)
			delete(destination.info, plan.Key)
		}
	}
	if err = db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DROP TRIGGER immutable_asset_bytes`); err != nil {
			return err
		}
		for id, key := range legacy {
			if _, err := tx.Exec(`UPDATE assets SET object_key=? WHERE id=?`, key, id); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	journal, err := sql.Open("sqlite3", config.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = journal.Exec(`DROP TRIGGER immutable_mapping`); err != nil {
		t.Fatal(err)
	}
	for id, key := range legacy {
		if _, err = journal.Exec(`UPDATE objects SET object_key=? WHERE id=?`, key, id); err != nil {
			t.Fatal(err)
		}
	}
	if err = journal.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(config.Journal)
	if err != nil {
		t.Fatal(err)
	}

	report, verifyErr := Verify(ctx, config, m, source, db, destination)
	if verifyErr == nil || report.Complete {
		t.Fatal("Verify certified published objects that runtime ownership rejects")
	}
	puts := destination.puts
	if err = Apply(ctx, config, m, source, db, destination); err == nil {
		t.Fatal("incompatible journal resumed")
	}
	if destination.puts != puts {
		t.Fatal("incompatible replay wrote replacement object bytes")
	}
	after, err := os.ReadFile(config.Journal)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("incompatible journal was modified while refusing resume", err)
	}
	for id, key := range legacy {
		var got string
		if err = db.Reader().QueryRow(`SELECT object_key FROM assets WHERE id=?`, id).Scan(&got); err != nil || got != key {
			t.Fatalf("published descriptor was rewritten: %s %v", got, err)
		}
	}
}
