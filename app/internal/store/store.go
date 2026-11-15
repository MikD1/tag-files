// Package store owns the single application writer, bounded readers, on-disk
// ownership lock and embedded history of the new library format.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	_ "github.com/mattn/go-sqlite3"
	"github.com/pressly/goose/v3"
)

const (
	SchemaVersion     = 7
	FormatVersion     = 1
	ReaderConnections = 4
)

var (
	ErrLibraryInUse        = errors.New("library is already owned by another process")
	ErrIncompatibleLibrary = errors.New("incompatible library schema or format; use an external migration or matching application version")
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	startup       sync.Mutex
	startupSealed bool
	lifecycle     sync.RWMutex
	closed        bool
	directory     string
	writer        *sql.DB
	reader        *sql.DB
	lock          *os.File
	closeOnce     sync.Once
	closeErr      error
}

var ErrStartupSealed = errors.New("startup recovery is sealed after recovery or job claims begin")

// RecoverStartup serializes recovery against the first public job claim. The
// store's process ownership lock is already held; callbacks contain SQL only.
func (s *Store) RecoverStartup(ctx context.Context, fn func(*sql.Tx) error) error {
	s.startup.Lock()
	defer s.startup.Unlock()
	if s.startupSealed {
		return ErrStartupSealed
	}
	if err := s.Write(ctx, fn); err != nil {
		return err
	}
	s.startupSealed = true
	return nil
}
func (s *Store) sealClaims() { s.startup.Lock(); s.startupSealed = true; s.startup.Unlock() }

func Open(ctx context.Context, directory string) (_ *Store, err error) {
	if _, markerErr := os.Lstat(filepath.Join(directory, ".cold-restore-incomplete")); !errors.Is(markerErr, os.ErrNotExist) {
		return nil, fmt.Errorf("incomplete external cold restore; restore into new empty volumes before starting the application")
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	s := &Store{directory: directory}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	s.lock, err = os.OpenFile(filepath.Join(directory, ".library.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrLibraryInUse
		}
		return nil, err
	}
	path := filepath.Join(directory, "library.sqlite")
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("library database must be a regular file")
		}
		// Probe read-only BEFORE journal mode, migrations, or any other writes.
		if err = preflight(ctx, path); err != nil {
			return nil, err
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return nil, statErr
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	s.writer, err = sql.Open("sqlite3", dsn(path, false))
	if err != nil {
		return nil, err
	}
	s.writer.SetMaxOpenConns(1)
	s.writer.SetMaxIdleConns(1)
	if err = s.writer.PingContext(ctx); err != nil {
		return nil, err
	}
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, s.writer, migrations)
	if err != nil {
		return nil, err
	}
	if _, err = provider.Up(ctx); err != nil {
		return nil, fmt.Errorf("migrate library: %w", err)
	}
	s.reader, err = sql.Open("sqlite3", dsn(path, true))
	if err != nil {
		return nil, err
	}
	s.reader.SetMaxOpenConns(ReaderConnections)
	s.reader.SetMaxIdleConns(ReaderConnections)
	if err = s.reader.PingContext(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

func dsn(path string, reader bool) string {
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"_foreign_keys": {"on"}, "_synchronous": {"FULL"}, "_busy_timeout": {"5000"}}
	if reader {
		q.Set("mode", "ro")
		q.Set("_query_only", "true")
	} else {
		q.Set("_journal_mode", "WAL")
		q.Set("_txlock", "immediate")
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func preflight(ctx context.Context, path string) error {
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&_query_only=true"}
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var version, format, schema, history int
	if err = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version < 0 || version > SchemaVersion {
		return ErrIncompatibleLibrary
	}
	var tables int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name NOT LIKE 'sqlite_%'`).Scan(&tables); err != nil {
		return err
	}
	if tables == 0 {
		if version == 0 {
			return nil
		}
		return ErrIncompatibleLibrary
	}
	// Goose may have committed its version table before a crash during the
	// very first migration. Only an otherwise empty version-zero DB is safe
	// to resume as an install; it contains no user library to reinterpret.
	if tables == 1 && version == 0 {
		var table string
		if err = db.QueryRowContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%'`).Scan(&table); err != nil {
			return err
		}
		if table == "goose_db_version" {
			var nonzero int
			if err = db.QueryRowContext(ctx, `SELECT count(*) FROM goose_db_version WHERE version_id<>0`).Scan(&nonzero); err != nil {
				return err
			}
			if nonzero == 0 {
				return nil
			}
		}
	}
	if version < 1 || version > SchemaVersion {
		return ErrIncompatibleLibrary
	}
	if err = db.QueryRowContext(ctx, `SELECT format_version,schema_version FROM library_meta WHERE singleton=1`).Scan(&format, &schema); err != nil {
		return fmt.Errorf("%w: %v", ErrIncompatibleLibrary, err)
	}
	if err = db.QueryRowContext(ctx, `SELECT version_id FROM goose_db_version WHERE is_applied=1 ORDER BY id DESC LIMIT 1`).Scan(&history); err != nil {
		return fmt.Errorf("%w: %v", ErrIncompatibleLibrary, err)
	}
	var unknownHistory int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM goose_db_version WHERE version_id<0 OR version_id>?`, SchemaVersion).Scan(&unknownHistory); err != nil {
		return fmt.Errorf("%w: %v", ErrIncompatibleLibrary, err)
	}
	if unknownHistory != 0 {
		return ErrIncompatibleLibrary
	}
	if format != FormatVersion || schema != version || history != version {
		return ErrIncompatibleLibrary
	}
	return nil
}

// Reader returns a read-only, query-only pool. Use Write for atomic commands.
func (s *Store) Reader() *sql.DB { return s.reader }

// Write always begins IMMEDIATE on the sole writer connection. The callback
// must do only SQLite work: network IO and subprocesses belong outside it.
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	s.lifecycle.RLock()
	defer s.lifecycle.RUnlock()
	if s.closed {
		return sql.ErrConnDone
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.lifecycle.Lock()
		defer s.lifecycle.Unlock()
		s.closed = true
		if s.reader != nil {
			s.closeErr = errors.Join(s.closeErr, s.reader.Close())
		}
		if s.writer != nil {
			s.closeErr = errors.Join(s.closeErr, s.writer.Close())
		}
		if s.lock != nil {
			s.closeErr = errors.Join(s.closeErr, s.lock.Close())
		}
	})
	return s.closeErr
}
