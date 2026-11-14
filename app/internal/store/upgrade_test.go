package store

import (
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

func TestSchemaOneUpgradePreservesLibraryAndLiveWriteIntentsOutliveSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "library.sqlite")
	legacy, err := sql.Open("sqlite3", dsn(path, false))
	if err != nil {
		t.Fatal(err)
	}
	versionOne, err := migrationFiles.ReadFile("migrations/00001_library.sql")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, legacy, fstest.MapFS{"00001_library.sql": &fstest.MapFile{Data: versionOne}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var identity string
	if err := legacy.QueryRow("SELECT library_id FROM library_meta").Scan(&identity); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO tags(id,name) VALUES(11,'猫'),(12,'猫');
INSERT INTO items(id,media_kind,uploaded_at_utc,duration_ticks,state) VALUES(23,'Video','2001-02-03T04:05:06.1234567Z',9223372036854775807,'missing');
INSERT INTO upload_sessions(id,batch_id,client_file_id,client_filename,expected_size,part_size,part_count,incoming_key,state,created_at_utc)
VALUES('11111111-1111-4111-8111-111111111111','22222222-2222-4222-8222-222222222222','33333333-3333-4333-8333-333333333333','retain.heic',7,5242880,1,'incoming/owned/source','failed','2001-01-01T00:00:00.0000000Z');`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if s != nil {
			s.Close()
		}
	}()
	var got string
	var schema, version, tags int
	var ticks int64
	if err := s.Reader().QueryRow("SELECT library_id,schema_version FROM library_meta").Scan(&got, &schema); err != nil {
		t.Fatal(err)
	}
	if got != identity || schema != SchemaVersion {
		t.Fatalf("upgrade identity/version: %s %d", got, schema)
	}
	if err := s.Reader().QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatalf("PRAGMA version: %d %v", version, err)
	}
	if err := s.Reader().QueryRow("SELECT COUNT(*) FROM tags WHERE name='猫'").Scan(&tags); err != nil || tags != 2 {
		t.Fatal("upgrade merged duplicate tags")
	}
	var tagIDs, filename, sessionState string
	var sessionSize int64
	if err := s.Reader().QueryRow("SELECT group_concat(id,',') FROM (SELECT id FROM tags ORDER BY id)").Scan(&tagIDs); err != nil || tagIDs != "11,12" {
		t.Fatal("upgrade changed tag identities")
	}
	if err := s.Reader().QueryRow("SELECT client_filename,state,expected_size FROM upload_sessions WHERE id='11111111-1111-4111-8111-111111111111'").Scan(&filename, &sessionState, &sessionSize); err != nil || filename != "retain.heic" || sessionState != "failed" || sessionSize != 7 {
		t.Fatal("upgrade changed old failed input")
	}
	if err := s.Reader().QueryRow("SELECT duration_ticks FROM items WHERE id=23").Scan(&ticks); err != nil || ticks != 9223372036854775807 {
		t.Fatal("upgrade changed historical ticks")
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_json,state,created_at_utc)
VALUES('44444444-4444-4444-8444-444444444444','55555555-5555-4555-8555-555555555555','upload','11111111-1111-4111-8111-111111111111',1,'create_mpu','incoming/owned/source','66666666-6666-4666-8666-666666666666','{"size":7}','uncertain','2001-01-01T00:00:00.0000000Z')`)
		if err != nil {
			return err
		}
		_, err = tx.Exec("DELETE FROM upload_sessions")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var state, payload string
	if err := s.Reader().QueryRow("SELECT state,payload_json FROM object_write_intents").Scan(&state, &payload); err != nil || state != "uncertain" || payload != `{"size":7}` {
		t.Fatalf("independent intent was lost: %q %q %v", state, payload, err)
	}
	// The intent is live work, never a terminal outcome/history archive.
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE object_write_intents SET state='succeeded'")
		return err
	}); err == nil {
		t.Fatal("journal accepted terminal history")
	}
}

func TestObjectWriteIntentCannotChangeOwnerEpochOrRequestAfterRegistration(t *testing.T) {
	s := openTest(t)
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO object_write_intents(id,owner_id,target_kind,target_id,generation,operation,object_key,storage_epoch,payload_json,state,created_at_utc)
VALUES('44444444-4444-4444-8444-444444444444','55555555-5555-4555-8555-555555555555','asset','11111111-1111-4111-8111-111111111111',1,'put_object','primary/owned/asset','66666666-6666-4666-8666-666666666666','{"size":7}','inflight','2001-01-01T00:00:00.0000000Z')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{
		"UPDATE object_write_intents SET owner_id='77777777-7777-4777-8777-777777777777'",
		"UPDATE object_write_intents SET storage_epoch='77777777-7777-4777-8777-777777777777'",
		"UPDATE object_write_intents SET generation=2",
		"UPDATE object_write_intents SET object_key='another/key'",
		`UPDATE object_write_intents SET payload_json='{"size":8}'`,
	} {
		if err := s.Write(ctx, func(tx *sql.Tx) error { _, err := tx.Exec(mutation); return err }); err == nil {
			t.Fatalf("live write was rebound: %s", mutation)
		}
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE object_write_intents SET state='uncertain'")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("UPDATE object_write_intents SET state='inflight'")
		return err
	}); err == nil {
		t.Fatal("uncertain request was reused as a new attempt")
	}
}

func TestSchemaTwoUpgradePreservesOldCandidatesAndFreezesNewOutputDescriptor(t *testing.T) {
	directory := t.TempDir()
	legacy, err := sql.Open("sqlite3", dsn(filepath.Join(directory, "library.sqlite"), false))
	if err != nil {
		t.Fatal(err)
	}
	files := fstest.MapFS{}
	for _, name := range []string{"00001_library.sql", "00002_object_write_intents.sql"} {
		data, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, legacy, files)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(`INSERT INTO assets(id,role,bucket,object_key,size_bytes,sha256,content_type,width,height,provenance,state) VALUES('legacy-candidate','primary','tag-files','known-old-key',7,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','image/jpeg',640,480,'upload','candidate')`); err != nil {
		t.Fatal(err)
	}
	legacy.Close()
	db, err := Open(ctx, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var kind sql.NullString
	var ticks sql.NullInt64
	var key string
	if err := db.Reader().QueryRow("SELECT object_key,media_kind,duration_ticks FROM assets WHERE id='legacy-candidate'").Scan(&key, &kind, &ticks); err != nil {
		t.Fatal(err)
	}
	if key != "known-old-key" || kind.Valid || ticks.Valid {
		t.Fatal("upgrade invented old candidate media metadata")
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO assets(id,role,bucket,object_key,size_bytes,sha256,content_type,width,height,rotation,provenance,state,media_kind,duration_ticks) VALUES('new-candidate','primary','tag-files','new-key',7,'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','video/mp4',1920,1080,0,'upload','candidate','Video',9223372036854775807)`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"duration_ticks=1", "media_kind='Image'", "width=1280", "height=720", "rotation=90", "content_type='image/jpeg'"} {
		err := db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.Exec("UPDATE assets SET " + change + " WHERE id='new-candidate'")
			return err
		})
		if err == nil {
			t.Fatalf("immutable output descriptor changed: %s", change)
		}
	}
	if err := db.Reader().QueryRow("SELECT duration_ticks FROM assets WHERE id='new-candidate'").Scan(&ticks); err != nil || ticks.Int64 != 9223372036854775807 {
		t.Fatal("candidate duration lost exact int64 ticks")
	}
}
