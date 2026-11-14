package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var ctx = context.Background()

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(ctx, t.TempDir())
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

func mustExec(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestInstallReopenPreservesIdentityAndExactTicks(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.Reader().QueryRow(`SELECT library_id FROM library_meta`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if len(before) != 36 {
		t.Fatalf("library UUID: %q", before)
	}
	err = s.Write(ctx, func(tx *sql.Tx) error {
		for i, ticks := range []any{nil, int64(0), int64(123456789), int64(864001234567), int64(math.MaxInt64)} {
			mustExec(t, tx, `INSERT INTO items(id,media_kind,uploaded_at_utc,duration_ticks,state) VALUES(?,'Video','2026-11-14T00:00:00.0000000Z',?,'missing')`, i+1, ticks)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var after string
	if err := s.Reader().QueryRow(`SELECT library_id FROM library_meta`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("library identity changed")
	}
	for i, expected := range []sql.NullInt64{{}, {Int64: 0, Valid: true}, {Int64: 123456789, Valid: true}, {Int64: 864001234567, Valid: true}, {Int64: math.MaxInt64, Valid: true}} {
		var got sql.NullInt64
		if err := s.Reader().QueryRow(`SELECT duration_ticks FROM items WHERE id=?`, i+1).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != expected {
			t.Fatalf("ticks[%d] = %v, want %v", i, got, expected)
		}
	}
}

func TestProductionPragmasOnEveryConnection(t *testing.T) {
	s := openTest(t)
	var held []*sql.Conn
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	for i := 0; i < ReaderConnections; i++ {
		c, err := s.Reader().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
		for pragma, want := range map[string]int{"foreign_keys": 1, "synchronous": 2, "query_only": 1, "busy_timeout": 5000} {
			var got int
			if err := c.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("connection %d %s=%d want %d", i, pragma, got, want)
			}
		}
		var mode string
		if err := c.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
			t.Fatalf("mode %q: %v", mode, err)
		}
		if _, err := c.ExecContext(ctx, `INSERT INTO tags(name) VALUES('should-fail')`); err == nil {
			t.Fatal("reader allowed write")
		}
	}
	if s.writer.Stats().MaxOpenConnections != 1 {
		t.Fatal("writer is not serialized")
	}
}

func TestOwnerLockIsReleasedAndEnforcedAcrossProcesses(t *testing.T) {
	if dir := os.Getenv("TAGFILES_LOCK_HELPER"); dir != "" {
		_, err := Open(ctx, dir)
		if !errors.Is(err, ErrLibraryInUse) {
			t.Fatalf("second owner: %v", err)
		}
		return
	}
	dir := t.TempDir()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOwnerLockIsReleasedAndEnforcedAcrossProcesses$")
	cmd.Env = append(os.Environ(), "TAGFILES_LOCK_HELPER="+dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child %v: %s", err, out)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
}

func TestImmediateWriterAndRollback(t *testing.T) {
	s := openTest(t)
	other, err := sql.Open("sqlite3", "file:"+filepath.Join(s.directory, "library.sqlite")+"?_busy_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	wantErr := errors.New("abort")
	err = s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := other.Exec(`BEGIN IMMEDIATE`); err == nil {
			other.Exec(`ROLLBACK`)
			t.Fatal("writer transaction was deferred")
		}
		mustExec(t, tx, `INSERT INTO tags(name) VALUES('rolled-back')`)
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatal(err)
	}
	var count int
	if err := s.Reader().QueryRow(`SELECT count(*) FROM tags`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback: %d %v", count, err)
	}
}

func TestSchemaConstraintsAndCollectionRemoval(t *testing.T) {
	s := openTest(t)
	err := s.Write(ctx, func(tx *sql.Tx) error {
		mustExec(t, tx, `INSERT INTO collections(id,name) VALUES(1,'Travel')`)
		mustExec(t, tx, `INSERT INTO items(id,media_kind,uploaded_at_utc,collection_id,state) VALUES(1,'Text','2026-11-14T00:00:00.0000000Z',1,'missing')`)
		mustExec(t, tx, `INSERT INTO tags(id,name) VALUES(1,'cat'),(2,'cat'),(3,'Cat'),(4,'кошки-猫')`)
		for _, q := range []string{
			`INSERT INTO item_tags VALUES(1,999)`,
			`UPDATE items SET view_count=-1 WHERE id=1`,
			`UPDATE items SET media_kind='Other' WHERE id=1`,
			`UPDATE items SET id=9007199254740992 WHERE id=1`,
			`UPDATE items SET state='ready' WHERE id=1`,
			`INSERT INTO tags(id,name) VALUES(0,'bad')`,
		} {
			if _, err := tx.Exec(q); err == nil {
				t.Fatalf("accepted invalid SQL: %s", q)
			}
		}
		mustExec(t, tx, `DELETE FROM collections WHERE id=1`)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var collection sql.NullInt64
	if err := s.Reader().QueryRow(`SELECT collection_id FROM items WHERE id=1`).Scan(&collection); err != nil || collection.Valid {
		t.Fatalf("collection: %v %v", collection, err)
	}
}

func TestIncompatibleLibraryIsRejectedWithoutDatabaseWrites(t *testing.T) {
	for _, alter := range []string{`PRAGMA user_version=999`, `UPDATE library_meta SET format_version=999`, `UPDATE goose_db_version SET version_id=999 WHERE version_id=1`} {
		t.Run(alter, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(ctx, dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.writer.Exec(alter); err != nil {
				t.Fatal(err)
			}
			s.Close()
			path := filepath.Join(dir, "library.sqlite")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if opened, err := Open(ctx, dir); !errors.Is(err, ErrIncompatibleLibrary) {
				if opened != nil {
					opened.Close()
				}
				t.Fatalf("expected incompatible, got %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(before) != sha256.Sum256(after) {
				t.Fatal("incompatible DB was modified")
			}
		})
	}
	t.Run("legacy schema", func(t *testing.T) {
		dir := t.TempDir()
		db, _ := sql.Open("sqlite3", filepath.Join(dir, "library.sqlite"))
		if _, err := db.Exec(`CREATE TABLE LibraryItems(Id INTEGER PRIMARY KEY)`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		if _, err := Open(ctx, dir); !errors.Is(err, ErrIncompatibleLibrary) {
			t.Fatalf("legacy accepted: %v", err)
		}
	})
}

func TestStorePathsWithSpecialCharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "library ?#% & Юникод")
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !strings.HasSuffix(s.directory, "library ?#% & Юникод") {
		t.Fatal(s.directory)
	}
}
