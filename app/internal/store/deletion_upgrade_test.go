package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

func TestSchemaFourUpgradePreservesBoundPairAndHistoricalMetadata(t *testing.T) {
	root := t.TempDir()
	old, err := sql.Open("sqlite3", dsn(filepath.Join(root, "library.sqlite"), false))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"00001_library.sql", "00002_object_write_intents.sql", "00003_candidate_descriptor.sql", "00004_storage_pair.sql"} {
		data, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, old, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	storageID := "11111111-1111-4111-8111-111111111111"
	volumeID := strings.Repeat("a", 64)
	if _, err := old.Exec("UPDATE library_meta SET storage_id=?,storage_volume_id=?", storageID, volumeID); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec("INSERT INTO tags(id,name) VALUES(11,'猫'),(12,'猫');INSERT INTO items(id,media_kind,uploaded_at_utc,duration_ticks,state) VALUES(23,'Video','2001-01-01T00:00:00.0000000Z',9223372036854775807,'missing')"); err != nil {
		t.Fatal(err)
	}
	var library string
	if err := old.QueryRow("SELECT library_id FROM library_meta").Scan(&library); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var actualLibrary, actualStorage, actualVolume string
	var schema int
	if err := db.Reader().QueryRow("SELECT library_id,storage_id,storage_volume_id,schema_version FROM library_meta").Scan(&actualLibrary, &actualStorage, &actualVolume, &schema); err != nil {
		t.Fatal(err)
	}
	if actualLibrary != library || actualStorage != storageID || actualVolume != volumeID || schema != SchemaVersion {
		t.Fatal("upgrade rebound the pair")
	}
	var ticks int64
	if err := db.Reader().QueryRow("SELECT duration_ticks FROM items WHERE id=23").Scan(&ticks); err != nil || ticks != 9223372036854775807 {
		t.Fatal("historical ticks changed", err)
	}
	var n int
	if err := db.Reader().QueryRow("SELECT count(*) FROM tags WHERE name='猫'").Scan(&n); err != nil || n != 2 {
		t.Fatal("duplicate Unicode tags changed", err)
	}
	if err := db.Reader().QueryRow("SELECT count(*) FROM deletion_assets").Scan(&n); err != nil || n != 0 {
		t.Fatal("upgrade invented deletion work", err)
	}
}
