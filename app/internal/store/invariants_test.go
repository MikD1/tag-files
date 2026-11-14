package store

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiedPrimaryOwnershipAndImmutableBytes(t *testing.T) {
	s := openTest(t)
	err := s.Write(ctx, func(tx *sql.Tx) error {
		mustExec(t, tx, `INSERT INTO items(id,media_kind,uploaded_at_utc,state) VALUES(1,'Image','2026-11-14T00:00:00.0000000Z','importing'),(2,'Image','2026-11-14T00:00:00.0000000Z','importing')`)
		mustExec(t, tx, `INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES('asset',1,'primary','tag-files','primary/library/asset/media.jpg',3,?,'image/jpeg','upload','verified')`, strings.Repeat("a", 64))
		mustExec(t, tx, `UPDATE items SET primary_asset_id='asset',state='ready' WHERE id=1`)
		for _, q := range []string{
			`UPDATE items SET primary_asset_id='asset',state='ready' WHERE id=2`,
			`UPDATE assets SET object_key='primary/other' WHERE id='asset'`,
			`UPDATE assets SET size_bytes=9 WHERE id='asset'`,
			`UPDATE assets SET item_id=2 WHERE id='asset'`,
			`UPDATE assets SET role='poster' WHERE id='asset'`,
			`UPDATE assets SET state='candidate' WHERE id='asset'`,
		} {
			if _, err := tx.Exec(q); err == nil {
				t.Fatalf("invalid primary change accepted: %s", q)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestInitialInstallRecoversIfInterruptedAfterGooseVersionTable(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite3", filepath.Join(dir, "library.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE goose_db_version(id INTEGER PRIMARY KEY AUTOINCREMENT,version_id INTEGER NOT NULL,is_applied INTEGER NOT NULL,tstamp TIMESTAMP DEFAULT CURRENT_TIMESTAMP);INSERT INTO goose_db_version(version_id,is_applied) VALUES(0,1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var n int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM library_meta`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("install not recovered: %d %v", n, err)
	}
}

func TestUnknownVersionCannotMasqueradeAsEmptyInstall(t *testing.T) {
	for _, gooseTable := range []bool{false, true} {
		t.Run(fmt.Sprint(gooseTable), func(t *testing.T) {
			dir := t.TempDir()
			db, err := sql.Open("sqlite3", filepath.Join(dir, "library.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`PRAGMA user_version=999`); err != nil {
				t.Fatal(err)
			}
			if gooseTable {
				if _, err := db.Exec(`CREATE TABLE goose_db_version(id INTEGER PRIMARY KEY,version_id INTEGER,is_applied INTEGER);INSERT INTO goose_db_version VALUES(1,0,1)`); err != nil {
					t.Fatal(err)
				}
			}
			db.Close()
			opened, err := Open(ctx, dir)
			if opened != nil {
				opened.Close()
			}
			if !errors.Is(err, ErrIncompatibleLibrary) {
				t.Fatalf("unknown schema initialized: %v", err)
			}
		})
	}
}

func TestPartBindingsCannotChangeAndCleanupSurvivesSessionRemoval(t *testing.T) {
	s := openTest(t)
	err := s.Write(ctx, func(tx *sql.Tx) error {
		mustExec(t, tx, `INSERT INTO upload_sessions(id,batch_id,client_file_id,client_filename,expected_size,part_size,part_count,incoming_key,state,created_at_utc) VALUES('upload','batch','file','photo.jpg',3,5242880,1,'incoming/library/upload/source','receiving','2000-01-01T00:00:00.0000000Z')`)
		mustExec(t, tx, `INSERT INTO upload_parts(session_id,part_number,expected_size,sha256,state) VALUES('upload',1,3,'original-digest','bound')`)
		if _, err := tx.Exec(`UPDATE upload_parts SET sha256='different-digest' WHERE session_id='upload'`); err == nil {
			t.Fatal("changed part bytes allowed")
		}
		mustExec(t, tx, `INSERT INTO cleanup_receipts(id,reason,upload_id,payload_json,generation,state,created_at_utc) VALUES('cleanup','cancel','upload','{"version":1,"keys":["incoming/library/upload/source"]}',2,'pending','2026-11-14T00:00:00.0000000Z')`)
		mustExec(t, tx, `DELETE FROM upload_sessions WHERE id='upload'`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"upload_sessions": 0, "upload_parts": 0, "cleanup_receipts": 1} {
		var n int
		if err := s.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: %d %v", table, n, err)
		}
	}
}
