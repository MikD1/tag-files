// This program is an external, offline native-volume tool. It is never linked
// into the application executable or exposed through an application endpoint.
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/mattn/go-sqlite3"
	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/storageprofile"
	"github.com/mikd1/tag-files/app/internal/store"
)

const formatVersion = 1
const maxManifestBytes = 256 << 20
const maxEntries = 1_000_000

var archiveNames = map[string]string{"metadata": "metadata-volume.tar", "storage": "storage-volume.tar", "deployment": "deployment.tar"}

type Paths struct{ Metadata, Storage, Deployment string }
type Options struct {
	Paths
	Destination, Generation string
	Images                  map[string]string
	OwnerUID, OwnerGID      int
	SetOwner                bool
	MinFreeBytes            int64
}
type Report struct {
	Generation         string `json:"generation"`
	LibraryID          string `json:"libraryId"`
	StorageID          string `json:"storageId"`
	OriginalVolumeID   string `json:"originalVolumeId"`
	RestoredVolumeID   string `json:"restoredVolumeId,omitempty"`
	NativeDataRequired bool   `json:"nativeDataRequired"`
	FileCount          int    `json:"fileCount"`
	Bytes              int64  `json:"bytes"`
}
type Entry struct {
	Role    string    `json:"role"`
	Path    string    `json:"path"`
	Type    string    `json:"type"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256,omitempty"`
	Mode    int64     `json:"mode"`
	UID     int       `json:"uid"`
	GID     int       `json:"gid"`
	ModTime time.Time `json:"modTime"`
}
type Artifact struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type Metadata struct {
	Format        int               `json:"format"`
	Tool          string            `json:"tool"`
	Created       time.Time         `json:"created"`
	Schema        int               `json:"schema"`
	LibraryFormat int               `json:"libraryFormat"`
	StorageFormat int               `json:"storageFormat"`
	Engine        string            `json:"engine"`
	Report        Report            `json:"report"`
	Images        map[string]string `json:"images"`
	Artifacts     []Artifact        `json:"artifacts"`
}

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && s == strings.ToLower(s)
}
func privateRead(p string, limit int64) ([]byte, error) {
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Size() > limit {
		return nil, fmt.Errorf("unsafe or oversized file: %s", p)
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("oversized file")
	}
	return b, nil
}
func strictJSON(b []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if !json.Valid(b) {
		return fmt.Errorf("invalid JSON")
	}
	return d.Decode(v)
}
func syncDir(p string) error {
	f, e := os.Open(p)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func atomicFile(p string, b []byte, mode fs.FileMode) error {
	f, e := os.CreateTemp(filepath.Dir(p), ".cold-backup-tmp-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if e = f.Chmod(mode); e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), p); e != nil {
		return e
	}
	return syncDir(filepath.Dir(p))
}
func writeJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicFile(p, append(b, '\n'), 0600)
}
func realDir(p string) (string, error) {
	a, e := filepath.Abs(p)
	if e != nil {
		return "", e
	}
	actual, e := filepath.EvalSymlinks(a)
	if e != nil {
		return "", e
	}
	i, e := os.Lstat(a)
	if e != nil {
		return "", e
	}
	if actual != a || !i.IsDir() {
		return "", fmt.Errorf("root must be a real directory without symlinks: %s", p)
	}
	return a, nil
}
func overlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}
func roots(p Paths) (map[string]string, error) {
	out := map[string]string{}
	for role, v := range map[string]string{"metadata": p.Metadata, "storage": p.Storage, "deployment": p.Deployment} {
		a, e := realDir(v)
		if e != nil {
			return nil, e
		}
		for _, prev := range out {
			if overlap(prev, a) {
				return nil, fmt.Errorf("roots overlap")
			}
		}
		out[role] = a
	}
	return out, nil
}
func lockFile(root, name string, create bool) (*os.File, error) {
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if create {
		flags = os.O_CREATE | os.O_RDWR | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	}
	f, e := os.OpenFile(filepath.Join(root, name), flags, 0600)
	if e != nil {
		return nil, e
	}
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("unsafe ownership lock")
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		f.Close()
		return nil, fmt.Errorf("running writer owns %s: %w", name, e)
	}
	return f, nil
}
func lockPair(p Paths, create bool) (func(), error) {
	a, e := lockFile(p.Metadata, ".library.lock", create)
	if e != nil {
		return nil, e
	}
	s, e := lockFile(p.Storage, ".tag-files-storage.lock", create)
	if e != nil {
		a.Close()
		return nil, e
	}
	return func() { a.Close(); s.Close() }, nil
}
func safeName(s string) bool {
	return s != "" && utf8.ValidString(s) && !strings.ContainsAny(s, "\\\x00") && !path.IsAbs(s) && path.Clean(s) == s && s != ".." && !strings.HasPrefix(s, "../")
}
func entryFor(role, rel string, i fs.FileInfo) (Entry, error) {
	if !safeName(rel) {
		return Entry{}, fmt.Errorf("unsafe archive path %q", rel)
	}
	if !i.IsDir() && !i.Mode().IsRegular() {
		return Entry{}, fmt.Errorf("only regular files/directories are supported: %q", rel)
	}
	h, e := tar.FileInfoHeader(i, "")
	if e != nil {
		return Entry{}, e
	}
	typ := "file"
	if i.IsDir() {
		typ = "directory"
	}
	if i.Mode().IsRegular() {
		st, ok := i.Sys().(*syscall.Stat_t)
		if !ok || st.Nlink != 1 {
			return Entry{}, fmt.Errorf("hard links are unsupported: %q", rel)
		}
	}
	return Entry{Role: role, Path: rel, Type: typ, Size: h.Size, Mode: h.Mode, UID: h.Uid, GID: h.Gid, ModTime: h.ModTime.UTC()}, nil
}
func archive(root, role, dest string) ([]Entry, error) {
	f, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	entries := []Entry{}
	buf := make([]byte, 128<<10)
	e = filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		i, e := os.Lstat(p)
		if e != nil {
			return e
		}
		v, e := entryFor(role, rel, i)
		if e != nil {
			return e
		}
		if len(entries) >= maxEntries {
			return fmt.Errorf("too many archive entries")
		}
		h, e := tar.FileInfoHeader(i, "")
		if e != nil {
			return e
		}
		h.Name = rel
		h.Format = tar.FormatPAX
		if e = tw.WriteHeader(h); e != nil {
			return e
		}
		if v.Type == "file" {
			src, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if e != nil {
				return e
			}
			defer src.Close()
			now, e := src.Stat()
			if e != nil {
				return e
			}
			if !os.SameFile(i, now) || !now.Mode().IsRegular() {
				return fmt.Errorf("source changed during capture")
			}
			hash := sha256.New()
			n, e := io.CopyBuffer(io.MultiWriter(tw, hash), src, buf)
			if e != nil {
				return e
			}
			after, e := src.Stat()
			src.Close()
			if e != nil {
				return e
			}
			if n != v.Size || i.ModTime() != after.ModTime() || i.Size() != after.Size() || i.Mode() != after.Mode() {
				return fmt.Errorf("source changed during capture: %s", rel)
			}
			v.SHA256 = hex.EncodeToString(hash.Sum(nil))
		}
		entries = append(entries, v)
		return nil
	})
	if e != nil {
		return nil, e
	}
	if e = tw.Close(); e != nil {
		return nil, e
	}
	if e = f.Sync(); e != nil {
		return nil, e
	}
	if e = f.Close(); e != nil {
		return nil, e
	}
	return entries, nil
}
func fileArtifact(p string) (Artifact, error) {
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return Artifact{}, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil || !i.Mode().IsRegular() {
		return Artifact{}, fmt.Errorf("unsafe artifact")
	}
	h := sha256.New()
	n, e := io.CopyBuffer(h, f, make([]byte, 128<<10))
	if e != nil {
		return Artifact{}, e
	}
	if n != i.Size() {
		return Artifact{}, fmt.Errorf("artifact changed")
	}
	return Artifact{filepath.Base(p), n, hex.EncodeToString(h.Sum(nil))}, nil
}
func preservedMode(mode int64) fs.FileMode {
	m := fs.FileMode(mode & 0777)
	if mode&04000 != 0 {
		m |= fs.ModeSetuid
	}
	if mode&02000 != 0 {
		m |= fs.ModeSetgid
	}
	if mode&01000 != 0 {
		m |= fs.ModeSticky
	}
	return m
}

func scanArchive(p, role string, want []Entry, dest string, existingLocks bool) error {
	f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	tr := tar.NewReader(f)
	idx := 0
	seen := map[string]bool{}
	dirs := []Entry{}
	buf := make([]byte, 128<<10)
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if idx >= len(want) {
			return fmt.Errorf("unlisted archive entry")
		}
		v := want[idx]
		idx++
		if !safeName(h.Name) || h.Linkname != "" || (h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeReg) || seen[h.Name] {
			return fmt.Errorf("unsafe archive entry: %q", h.Name)
		}
		seen[h.Name] = true
		typ := "file"
		if h.Typeflag == tar.TypeDir {
			typ = "directory"
		}
		if v.Role != role || v.Path != h.Name || v.Type != typ || v.Size != h.Size || v.Mode != h.Mode || v.UID != h.Uid || v.GID != h.Gid || !v.ModTime.Equal(h.ModTime) {
			return fmt.Errorf("archive header differs from manifest: %s", h.Name)
		}
		if h.Name != "." && !seen[path.Dir(h.Name)] {
			return fmt.Errorf("parent missing from archive")
		}
		var out *os.File
		if dest != "" {
			target := filepath.Join(dest, filepath.FromSlash(h.Name))
			if typ == "directory" {
				if h.Name != "." {
					if e = os.Mkdir(target, 0700); e != nil {
						return e
					}
				}
				dirs = append(dirs, v)
			} else {
				flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL | syscall.O_NOFOLLOW
				if existingLocks && (h.Name == ".library.lock" || h.Name == ".tag-files-storage.lock") {
					flags = os.O_WRONLY | os.O_TRUNC | syscall.O_NOFOLLOW
				}
				out, e = os.OpenFile(target, flags, 0600)
				if e != nil {
					return e
				}
			}
		}
		if typ == "file" {
			hash := sha256.New()
			var w io.Writer = hash
			if out != nil {
				w = io.MultiWriter(hash, out)
			}
			n, e := io.CopyBuffer(w, tr, buf)
			if e != nil {
				if out != nil {
					out.Close()
				}
				return e
			}
			if n != v.Size || hex.EncodeToString(hash.Sum(nil)) != v.SHA256 {
				if out != nil {
					out.Close()
				}
				return fmt.Errorf("archive file hash differs: %s", h.Name)
			}
			if out != nil {
				if e = out.Chown(v.UID, v.GID); e == nil {
					e = out.Chmod(preservedMode(v.Mode))
				}
				if e == nil {
					e = out.Sync()
				}
				ce := out.Close()
				if e != nil {
					return e
				}
				if ce != nil {
					return ce
				}
				if e = os.Chtimes(filepath.Join(dest, filepath.FromSlash(h.Name)), v.ModTime, v.ModTime); e != nil {
					return e
				}
			}
		}
	}
	if idx != len(want) {
		return fmt.Errorf("archive is incomplete")
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		v := dirs[i]
		p := filepath.Join(dest, filepath.FromSlash(v.Path))
		if e = os.Chown(p, v.UID, v.GID); e != nil {
			return e
		}
		if e = os.Chmod(p, preservedMode(v.Mode)); e != nil {
			return e
		}
		if e = os.Chtimes(p, v.ModTime, v.ModTime); e != nil {
			return e
		}
		if e = syncDir(p); e != nil {
			return e
		}
	}
	return nil
}
func readEntries(p string) ([]Entry, error) {
	b, e := privateRead(p, maxManifestBytes)
	if e != nil {
		return nil, e
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	out := []Entry{}
	seen := map[string]bool{}
	for {
		var v Entry
		if e = d.Decode(&v); e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		key := v.Role + "/" + v.Path
		if _, ok := archiveNames[v.Role]; !ok || !safeName(v.Path) || seen[key] || v.Size < 0 || v.Mode < 0 || v.Mode > 07777 || v.UID < 0 || v.GID < 0 || v.Type != "file" && v.Type != "directory" || v.Type == "file" && !validHash(v.SHA256) || v.Type == "directory" && (v.Size != 0 || v.SHA256 != "") {
			return nil, fmt.Errorf("invalid manifest entry")
		}
		seen[key] = true
		out = append(out, v)
		if len(out) > maxEntries {
			return nil, fmt.Errorf("manifest entry limit")
		}
	}
	return out, nil
}
func byRole(entries []Entry, role string) []Entry {
	var out []Entry
	for _, v := range entries {
		if v.Role == role {
			out = append(out, v)
		}
	}
	return out
}
func writeManifest(p string, v []Entry) error {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	for _, v := range v {
		if err := e.Encode(v); err != nil {
			return err
		}
	}
	if b.Len() > maxManifestBytes {
		return fmt.Errorf("manifest too large")
	}
	return atomicFile(p, b.Bytes(), 0600)
}

// sqliteSnapshot uses the production SQLite driver, including its WAL recovery,
// only on a disposable extraction. The source named volume remains read-only.
func sqliteOpen(p string, ro bool) (*sql.DB, error) {
	u := url.URL{Scheme: "file", Path: p}
	q := url.Values{"mode": {"rw"}, "_foreign_keys": {"on"}, "_synchronous": {"FULL"}, "_txlock": {"immediate"}, "_busy_timeout": {"5000"}}
	if ro {
		q.Set("mode", "ro")
		q.Set("_query_only", "true")
	}
	u.RawQuery = q.Encode()
	db, e := sql.Open("sqlite3", u.String())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	if e = db.Ping(); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
func integrity(db *sql.DB) error {
	rows, e := db.Query("PRAGMA integrity_check")
	if e != nil {
		return e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var result string
		if e = rows.Scan(&result); e != nil {
			return e
		}
		if result != "ok" {
			return fmt.Errorf("SQLite integrity_check: %s", result)
		}
		count++
	}
	if e = rows.Err(); e != nil {
		return e
	}
	rows.Close()
	if count != 1 {
		return fmt.Errorf("missing SQLite integrity result")
	}
	fk, e := db.Query("PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	defer fk.Close()
	if fk.Next() {
		return fmt.Errorf("SQLite foreign key violation")
	}
	return fk.Err()
}
func inspectDB(db *sql.DB) (Report, error) {
	var r Report
	var schema, format, user int
	var storage, volume sql.NullString
	if e := db.QueryRow("SELECT library_id,schema_version,format_version,storage_id,storage_volume_id FROM library_meta WHERE singleton=1").Scan(&r.LibraryID, &schema, &format, &storage, &volume); e != nil {
		return r, e
	}
	if e := db.QueryRow("PRAGMA user_version").Scan(&user); e != nil {
		return r, e
	}
	if schema != store.SchemaVersion || format != store.FormatVersion || user != schema || !storage.Valid || !volume.Valid || domain.ValidateUUID(r.LibraryID) != nil || domain.ValidateUUID(storage.String) != nil || !validHash(volume.String) {
		return r, fmt.Errorf("unsupported or unpaired library")
	}
	var bad int
	if e := db.QueryRow("SELECT count(*) FROM goose_db_version WHERE version_id>?", schema).Scan(&bad); e != nil {
		return r, e
	}
	if bad > 0 {
		return r, fmt.Errorf("unknown migration history")
	}
	if e := db.QueryRow("SELECT EXISTS(SELECT 1 FROM assets WHERE size_bytes>0 AND state IN ('verified','published')) OR EXISTS(SELECT 1 FROM upload_parts WHERE state='confirmed') OR EXISTS(SELECT 1 FROM upload_sessions WHERE source_sha256 IS NOT NULL)").Scan(&r.NativeDataRequired); e != nil {
		return r, e
	}
	r.StorageID = storage.String
	r.OriginalVolumeID = volume.String
	return r, nil
}
func sqliteSnapshot(root, dest string) (Report, error) {
	db, e := sqliteOpen(filepath.Join(root, "library.sqlite"), false)
	if e != nil {
		return Report{}, e
	}
	defer db.Close()
	if e = integrity(db); e != nil {
		return Report{}, e
	}
	r, e := inspectDB(db)
	if e != nil {
		return r, e
	}
	file, e := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if e != nil {
		return r, e
	}
	file.Close()
	to, e := sqliteOpen(dest, false)
	if e != nil {
		return r, e
	}
	defer to.Close()
	ctx := context.Background()
	src, e := db.Conn(ctx)
	if e != nil {
		return r, e
	}
	defer src.Close()
	dst, e := to.Conn(ctx)
	if e != nil {
		return r, e
	}
	defer dst.Close()
	e = src.Raw(func(sc any) error {
		return dst.Raw(func(dc any) error {
			b, e := dc.(*sqlite3.SQLiteConn).Backup("main", sc.(*sqlite3.SQLiteConn), "main")
			if e != nil {
				return e
			}
			for {
				done, e := b.Step(128)
				if e != nil {
					b.Finish()
					return e
				}
				if done {
					break
				}
			}
			return b.Finish()
		})
	})
	dst.Close()
	src.Close()
	if e != nil {
		return r, e
	}
	if _, e = to.Exec("PRAGMA journal_mode=DELETE"); e != nil {
		return r, e
	}
	if e = integrity(to); e != nil {
		return r, e
	}
	if e = to.Close(); e != nil {
		return r, e
	}
	return r, nil
}
func stateFrom(root string, checkRoot bool) (storageprofile.StorageState, error) {
	if checkRoot {
		return storageprofile.LoadState(root)
	}
	var s storageprofile.StorageState
	b, e := privateRead(filepath.Join(root, storageprofile.StateFilename), 1<<20)
	if e != nil {
		return s, e
	}
	if e = strictJSON(b, &s); e != nil {
		return s, e
	}
	if s.FormatVersion != storageprofile.StorageFormatVersion || s.Engine != storageprofile.StorageEngine || s.Phase != "ready" || domain.ValidateUUID(s.ID) != nil || domain.ValidateUUID(s.LibraryID) != nil || !validHash(s.VolumeID) || !validHash(s.ConfigSHA256) || !validHash(s.AppCredentialSHA256) {
		return s, fmt.Errorf("unsupported or unpaired storage")
	}
	return s, nil
}
func validateNative(entries []Entry, s storageprofile.StorageState, r Report) error {
	if s.ID != r.StorageID || s.LibraryID != r.LibraryID || s.VolumeID != r.OriginalVolumeID {
		return fmt.Errorf("SQLite and storage pair differ")
	}
	master, filer, config := false, false, false
	dat := map[string]bool{}
	idx := map[string]bool{}
	for _, v := range entries {
		if v.Role != "storage" || v.Type != "file" {
			continue
		}
		if strings.HasPrefix(v.Path, "master/") {
			master = true
		}
		if strings.HasPrefix(v.Path, "filerldb2/") {
			filer = true
		}
		if v.Path == "config/s3.json" {
			config = v.SHA256 == s.ConfigSHA256
		}
		if path.Dir(v.Path) == "." && strings.HasSuffix(v.Path, ".dat") {
			dat[strings.TrimSuffix(v.Path, ".dat")] = true
		}
		if path.Dir(v.Path) == "." && strings.HasSuffix(v.Path, ".idx") {
			idx[strings.TrimSuffix(v.Path, ".idx")] = true
		}
	}
	if !master || !filer || !config {
		return fmt.Errorf("missing native master/filer/config state")
	}
	if r.NativeDataRequired && len(dat) == 0 {
		return fmt.Errorf("SQLite references durable media/parts but native chunk data is absent")
	}
	for name := range dat {
		if !idx[name] {
			return fmt.Errorf("missing native volume index: %s", name)
		}
	}
	return nil
}
func verifyDeployment(root string) error {
	b, e := privateRead(filepath.Join(root, "filer.toml"), 1<<20)
	if e != nil {
		return e
	}
	if e = storageprofile.ValidateBackend(b); e != nil {
		return e
	}
	for _, name := range []string{"secrets/s3_access_key", "secrets/s3_secret_key"} {
		b, e := privateRead(filepath.Join(root, name), 257)
		if e != nil {
			return e
		}
		b = bytes.TrimSuffix(b, []byte("\n"))
		if len(b) < 20 || len(b) > 256 || bytes.ContainsAny(b, "\x00\r\n") {
			return fmt.Errorf("invalid captured secret")
		}
	}
	return nil
}
func deploymentPair(root string, s storageprofile.StorageState) error {
	values := []string{}
	for _, name := range []string{"s3_access_key", "s3_secret_key"} {
		b, e := privateRead(filepath.Join(root, "secrets", name), 257)
		if e != nil {
			return e
		}
		values = append(values, strings.TrimSuffix(string(b), "\n"))
	}
	b, e := json.Marshal(values)
	if e != nil {
		return e
	}
	if digest(b) != s.AppCredentialSHA256 {
		return fmt.Errorf("captured secrets differ from owned storage credentials")
	}
	return nil
}

func checkpoint(p, stage string) error {
	return writeJSON(filepath.Join(p, "checkpoint.json"), map[string]any{"stage": stage, "at": time.Now().UTC()})
}

// Estimates charge complete logical bytes, tar/PAX headers, SQLite copies and
// reserve. Multiple destinations sharing one filesystem are charged together.
func requireSpace(demand map[string]int64, reserve int64) error {
	if reserve == 0 {
		reserve = 64 << 20
	}
	if reserve < 0 {
		return fmt.Errorf("negative free-space reserve")
	}
	type budget struct{ need, available int64 }
	groups := map[string]budget{}
	for root, need := range demand {
		if need < 0 {
			return fmt.Errorf("invalid space estimate")
		}
		var st syscall.Statfs_t
		if e := syscall.Statfs(root, &st); e != nil {
			return e
		}
		snapshot, e := diskspace.Measure(root)
		if e != nil {
			return e
		}
		key := fmt.Sprintf("%d:%v", st.Type, st.Fsid)
		g, ok := groups[key]
		if !ok {
			g.available = snapshot.AvailableBytes
		}
		if need > domain.MaxSafeInteger-g.need {
			return fmt.Errorf("space estimate overflow")
		}
		g.need += need
		groups[key] = g
	}
	for _, g := range groups {
		if g.available < reserve || g.need > g.available-reserve {
			return fmt.Errorf("insufficient free bytes for complete archive/restore and SQLite verification workspace")
		}
	}
	return nil
}
func estimateSources(source map[string]string) (map[string]int64, int64, error) {
	sizes := map[string]int64{}
	var count int64
	for role, root := range source {
		e := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
			if e != nil {
				return e
			}
			i, e := os.Lstat(p)
			if e != nil {
				return e
			}
			rel, e := filepath.Rel(root, p)
			if e != nil {
				return e
			}
			if _, e = entryFor(role, filepath.ToSlash(rel), i); e != nil {
				return e
			}
			count++
			if count > maxEntries {
				return fmt.Errorf("source entry limit exceeded")
			}
			if i.Mode().IsRegular() {
				if i.Size() > domain.MaxSafeInteger-sizes[role] {
					return fmt.Errorf("source size overflow")
				}
				sizes[role] += i.Size()
			}
			return nil
		})
		if e != nil {
			return nil, 0, e
		}
	}
	return sizes, count, nil
}
func entryBytes(entries []Entry) map[string]int64 {
	out := map[string]int64{}
	for _, v := range entries {
		if v.Type == "file" {
			out[v.Role] += v.Size
		}
	}
	return out
}

func Backup(o Options) (Report, error) {
	started := time.Now().UTC()
	for _, root := range []string{o.Metadata, o.Storage} {
		if _, e := os.Lstat(filepath.Join(root, ".cold-restore-incomplete")); !errors.Is(e, os.ErrNotExist) {
			return Report{}, fmt.Errorf("incomplete restore root cannot be archived")
		}
	}
	if !safeName(o.Generation) || strings.Contains(o.Generation, "/") || o.Generation == "." || strings.HasSuffix(o.Generation, ".part") {
		return Report{}, fmt.Errorf("invalid generation")
	}
	source, e := roots(o.Paths)
	if e != nil {
		return Report{}, e
	}
	for _, role := range []string{"app", "storage", "tool"} {
		if !strings.HasPrefix(o.Images[role], "sha256:") || !validHash(strings.TrimPrefix(o.Images[role], "sha256:")) {
			return Report{}, fmt.Errorf("exact %s image content digest required", role)
		}
	}
	if e = os.MkdirAll(o.Destination, 0700); e != nil {
		return Report{}, e
	}
	dest, e := realDir(o.Destination)
	if e != nil {
		return Report{}, e
	}
	for _, v := range source {
		if overlap(dest, v) {
			return Report{}, fmt.Errorf("backup destination overlaps source")
		}
	}
	unlock, e := lockPair(o.Paths, false)
	if e != nil {
		return Report{}, e
	}
	defer unlock()
	if e = verifyDeployment(o.Deployment); e != nil {
		return Report{}, e
	}
	s, e := stateFrom(o.Storage, true)
	if e != nil {
		return Report{}, e
	}
	if e = deploymentPair(o.Deployment, s); e != nil {
		return Report{}, e
	}
	sizes, count, e := estimateSources(source)
	if e != nil {
		return Report{}, e
	}
	archiveNeed := count*4096 + sizes["metadata"] + sizes["storage"] + sizes["deployment"] + sizes["metadata"]
	workNeed := sizes["metadata"]*4 + sizes["deployment"]
	demand := map[string]int64{dest: archiveNeed}
	demand[os.TempDir()] += workNeed
	if e = requireSpace(demand, o.MinFreeBytes); e != nil {
		return Report{}, e
	}
	target := filepath.Join(dest, o.Generation)
	if _, e = os.Lstat(target); !errors.Is(e, os.ErrNotExist) {
		return Report{}, fmt.Errorf("generation already exists")
	}
	part := target + ".part"
	if e = os.Mkdir(part, 0700); e != nil {
		return Report{}, e
	}
	if o.SetOwner {
		if e = os.Chown(part, o.OwnerUID, o.OwnerGID); e != nil {
			return Report{}, e
		}
		defer func() {
			_ = filepath.WalkDir(part, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				return os.Lchown(p, o.OwnerUID, o.OwnerGID)
			})
		}()
	}
	if e = checkpoint(part, "archiving"); e != nil {
		return Report{}, e
	}
	entries := []Entry{}
	for _, role := range []string{"metadata", "storage", "deployment"} {
		v, e := archive(source[role], role, filepath.Join(part, archiveNames[role]))
		if e != nil {
			return Report{}, e
		}
		entries = append(entries, v...)
		if e = checkpoint(part, "archived-"+role); e != nil {
			return Report{}, e
		}
	}
	tmp, e := os.MkdirTemp("", "tag-files-backup-verify-")
	if e != nil {
		return Report{}, e
	}
	defer os.RemoveAll(tmp)
	if e = scanArchive(filepath.Join(part, archiveNames["metadata"]), "metadata", byRole(entries, "metadata"), tmp, false); e != nil {
		return Report{}, e
	}
	r, e := sqliteSnapshot(tmp, filepath.Join(part, "sqlite-snapshot.sqlite"))
	if e != nil {
		return r, e
	}
	r.Generation = o.Generation
	if e = validateNative(entries, s, r); e != nil {
		return r, e
	}
	for _, v := range entries {
		r.FileCount++
		if v.Type == "file" {
			r.Bytes += v.Size
		}
	}
	if e = writeManifest(filepath.Join(part, "manifest.jsonl"), entries); e != nil {
		return r, e
	}
	if e = checkpoint(part, "verifying"); e != nil {
		return r, e
	}
	m := Metadata{Format: formatVersion, Tool: "tag-files-cold-backup/v1", Created: started, Schema: store.SchemaVersion, LibraryFormat: store.FormatVersion, StorageFormat: storageprofile.StorageFormatVersion, Engine: storageprofile.StorageEngine, Report: r, Images: o.Images}
	for _, name := range []string{"metadata-volume.tar", "storage-volume.tar", "deployment.tar", "sqlite-snapshot.sqlite", "manifest.jsonl", "checkpoint.json"} {
		a, e := fileArtifact(filepath.Join(part, name))
		if e != nil {
			return r, e
		}
		m.Artifacts = append(m.Artifacts, a)
	}
	if e = writeJSON(filepath.Join(part, "backup.json"), m); e != nil {
		return r, e
	}
	if _, e = verifyGeneration(part, false); e != nil {
		return r, e
	}
	for _, role := range []string{"metadata", "storage", "deployment"} {
		if e = verifySource(source[role], role, byRole(entries, role)); e != nil {
			return r, e
		}
	}
	if o.SetOwner {
		if e = filepath.WalkDir(part, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Lchown(p, o.OwnerUID, o.OwnerGID)
		}); e != nil {
			return r, e
		}
	}
	if e = checkpoint(part, "verified"); e != nil {
		return r, e
	}
	for i := range m.Artifacts {
		if m.Artifacts[i].Name == "checkpoint.json" {
			m.Artifacts[i], e = fileArtifact(filepath.Join(part, "checkpoint.json"))
			if e != nil {
				return r, e
			}
		}
	}
	if e = writeJSON(filepath.Join(part, "backup.json"), m); e != nil {
		return r, e
	}
	b, e := privateRead(filepath.Join(part, "backup.json"), 1<<20)
	if e != nil {
		return r, e
	}
	if e = atomicFile(filepath.Join(part, "complete"), []byte(digest(b)+"\n"), 0600); e != nil {
		return r, e
	}
	if e = syncDir(part); e != nil {
		return r, e
	}
	if o.SetOwner {
		for _, name := range []string{"backup.json", "checkpoint.json", "complete"} {
			if e = os.Chown(filepath.Join(part, name), o.OwnerUID, o.OwnerGID); e != nil {
				return r, e
			}
		}
	}
	if e = os.Rename(part, target); e != nil {
		return r, e
	}
	if e = syncDir(dest); e != nil {
		return r, e
	}
	return r, nil
}
func verifySource(root, role string, want []Entry) error {
	idx := 0
	buf := make([]byte, 128<<10)
	e := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if idx >= len(want) {
			return fmt.Errorf("source gained files")
		}
		v := want[idx]
		idx++
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		i, e := os.Lstat(p)
		if e != nil {
			return e
		}
		now, e := entryFor(role, filepath.ToSlash(rel), i)
		if e != nil {
			return e
		}
		if now.Path != v.Path || now.Type != v.Type || now.Size != v.Size || now.Mode != v.Mode || now.UID != v.UID || now.GID != v.GID || !now.ModTime.Equal(v.ModTime) {
			return fmt.Errorf("source metadata changed: %s", rel)
		}
		if v.Type == "file" {
			f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
			if e != nil {
				return e
			}
			h := sha256.New()
			_, e = io.CopyBuffer(h, f, buf)
			f.Close()
			if e != nil {
				return e
			}
			if hex.EncodeToString(h.Sum(nil)) != v.SHA256 {
				return fmt.Errorf("source bytes changed: %s", rel)
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	if idx != len(want) {
		return fmt.Errorf("source lost files")
	}
	return nil
}
func verifyGeneration(root string, complete bool) (Report, error) {
	root, e := realDir(root)
	if e != nil {
		return Report{}, e
	}
	b, e := privateRead(filepath.Join(root, "backup.json"), 1<<20)
	if e != nil {
		return Report{}, e
	}
	var m Metadata
	if e = strictJSON(b, &m); e != nil {
		return Report{}, e
	}
	r := m.Report
	if m.Format != formatVersion || m.Schema != store.SchemaVersion || m.LibraryFormat != store.FormatVersion || m.StorageFormat != storageprofile.StorageFormatVersion || m.Engine != storageprofile.StorageEngine || m.Tool != "tag-files-cold-backup/v1" || r.Generation == "" || r.RestoredVolumeID != "" {
		return r, fmt.Errorf("unsupported backup format")
	}
	if len(m.Images) != 3 {
		return r, fmt.Errorf("missing exact image IDs")
	}
	for _, role := range []string{"app", "storage", "tool"} {
		if !strings.HasPrefix(m.Images[role], "sha256:") || !validHash(strings.TrimPrefix(m.Images[role], "sha256:")) {
			return r, fmt.Errorf("invalid exact image ID")
		}
	}
	if complete {
		marker, e := privateRead(filepath.Join(root, "complete"), 128)
		if e != nil {
			return r, e
		}
		if string(marker) != digest(b)+"\n" || strings.HasSuffix(root, ".part") || filepath.Base(root) != r.Generation {
			return r, fmt.Errorf("incomplete or mixed generation")
		}
	}
	names := map[string]bool{}
	for _, a := range m.Artifacts {
		if !safeName(a.Name) || strings.Contains(a.Name, "/") || names[a.Name] || !validHash(a.SHA256) || a.Size < 0 {
			return r, fmt.Errorf("invalid artifact")
		}
		names[a.Name] = true
		got, e := fileArtifact(filepath.Join(root, a.Name))
		if e != nil {
			return r, e
		}
		if got != a {
			return r, fmt.Errorf("artifact hash differs: %s", a.Name)
		}
	}
	for _, n := range []string{"metadata-volume.tar", "storage-volume.tar", "deployment.tar", "sqlite-snapshot.sqlite", "manifest.jsonl", "checkpoint.json"} {
		if !names[n] {
			return r, fmt.Errorf("artifact absent: %s", n)
		}
	}
	if len(names) != 6 {
		return r, fmt.Errorf("unrecognized artifact")
	}
	listing, e := os.ReadDir(root)
	if e != nil {
		return r, e
	}
	for _, f := range listing {
		if !names[f.Name()] && f.Name() != "backup.json" && !(f.Name() == "complete" && complete) {
			return r, fmt.Errorf("unrecognized generation file: %s", f.Name())
		}
	}
	entries, e := readEntries(filepath.Join(root, "manifest.jsonl"))
	if e != nil {
		return r, e
	}
	if len(entries) != r.FileCount {
		return r, fmt.Errorf("manifest count mismatch")
	}
	var total int64
	for _, v := range entries {
		if v.Type == "file" {
			if v.Size > int64(^uint64(0)>>1)-total {
				return r, fmt.Errorf("byte total overflow")
			}
			total += v.Size
		}
	}
	if total != r.Bytes {
		return r, fmt.Errorf("byte total mismatch")
	}
	sizes := entryBytes(entries)
	if e = requireSpace(map[string]int64{os.TempDir(): sizes["metadata"]*2 + sizes["deployment"]}, 0); e != nil {
		return r, e
	}
	tmp, e := os.MkdirTemp("", "tag-files-cold-check-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(tmp)
	for _, role := range []string{"metadata", "storage", "deployment"} {
		dest := ""
		if role == "metadata" || role == "deployment" {
			dest = filepath.Join(tmp, role)
			if e = os.Mkdir(dest, 0700); e != nil {
				return r, e
			}
		}
		if e = scanArchive(filepath.Join(root, archiveNames[role]), role, byRole(entries, role), dest, false); e != nil {
			return r, e
		}
	}
	if e = verifyDeployment(filepath.Join(tmp, "deployment")); e != nil {
		return r, e
	}
	derived, e := sqliteSnapshot(filepath.Join(tmp, "metadata"), filepath.Join(tmp, "derived.sqlite"))
	if e != nil {
		return r, e
	}
	if derived.LibraryID != r.LibraryID || derived.StorageID != r.StorageID || derived.OriginalVolumeID != r.OriginalVolumeID || derived.NativeDataRequired != r.NativeDataRequired {
		return r, fmt.Errorf("archived SQLite pair differs")
	}
	derivedArtifact, e := fileArtifact(filepath.Join(tmp, "derived.sqlite"))
	if e != nil {
		return r, e
	}
	savedArtifact, e := fileArtifact(filepath.Join(root, "sqlite-snapshot.sqlite"))
	if e != nil {
		return r, e
	}
	if derivedArtifact.Size != savedArtifact.Size || derivedArtifact.SHA256 != savedArtifact.SHA256 {
		return r, fmt.Errorf("SQLite snapshot differs from archived database and WAL")
	}
	snapshot, e := sqliteOpen(filepath.Join(root, "sqlite-snapshot.sqlite"), true)
	if e != nil {
		return r, e
	}
	if e = integrity(snapshot); e == nil {
		var sr Report
		sr, e = inspectDB(snapshot)
		if e == nil && (sr.LibraryID != r.LibraryID || sr.StorageID != r.StorageID || sr.OriginalVolumeID != r.OriginalVolumeID || sr.NativeDataRequired != r.NativeDataRequired) {
			e = fmt.Errorf("snapshot pair differs")
		}
	}
	snapshot.Close()
	if e != nil {
		return r, e
	}
	// Read the bounded storage marker without unpacking the potentially huge volume.
	f, e := os.Open(filepath.Join(root, archiveNames["storage"]))
	if e != nil {
		return r, e
	}
	defer f.Close()
	tr := tar.NewReader(f)
	var s storageprofile.StorageState
	found := false
	for {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return r, e
		}
		if h.Name == storageprofile.StateFilename {
			b, e := io.ReadAll(io.LimitReader(tr, 1<<20))
			if e != nil {
				return r, e
			}
			if e = strictJSON(b, &s); e != nil {
				return r, e
			}
			found = true
			break
		}
	}
	if !found || s.FormatVersion != storageprofile.StorageFormatVersion || s.Engine != storageprofile.StorageEngine || s.Phase != "ready" {
		return r, fmt.Errorf("storage marker missing/unsupported")
	}
	if e = validateNative(entries, s, r); e != nil {
		return r, e
	}
	if e = deploymentPair(filepath.Join(tmp, "deployment"), s); e != nil {
		return r, e
	}
	return r, nil
}
func Verify(root string) (Report, error) { return verifyGeneration(root, true) }

// rebase touches only verified restored copies. DDL and the SQLite identity
// update commit together under BEGIN IMMEDIATE; the storage marker is published
// afterwards. Both roots retain an incomplete marker until the complete pair is
// revalidated. No runtime process may be started before host success.
func rebase(p Paths, r Report) (Report, error) {
	s, e := stateFrom(p.Storage, false)
	if e != nil {
		return r, e
	}
	if s.ID != r.StorageID || s.LibraryID != r.LibraryID || s.VolumeID != r.OriginalVolumeID {
		return r, fmt.Errorf("restore pairing changed")
	}
	newID, e := storageprofile.VolumeIdentity(p.Storage)
	if e != nil {
		return r, e
	}
	if newID == r.OriginalVolumeID {
		return r, fmt.Errorf("restore must use a new physical root")
	}
	db, e := sqliteOpen(filepath.Join(p.Metadata, "library.sqlite"), false)
	if e != nil {
		return r, e
	}
	defer db.Close()
	if e = integrity(db); e != nil {
		return r, e
	}
	before, e := inspectDB(db)
	if e != nil {
		return r, e
	}
	if before.LibraryID != r.LibraryID || before.StorageID != r.StorageID || before.OriginalVolumeID != r.OriginalVolumeID {
		return r, fmt.Errorf("restore metadata pairing changed")
	}
	tx, e := db.BeginTx(context.Background(), nil)
	if e != nil {
		return r, e
	}
	defer tx.Rollback()
	var ddl string
	if e = tx.QueryRow("SELECT sql FROM sqlite_schema WHERE type='trigger' AND name='immutable_storage_pair'").Scan(&ddl); e != nil {
		return r, e
	}
	if _, e = tx.Exec("DROP TRIGGER immutable_storage_pair"); e != nil {
		return r, e
	}
	result, e := tx.Exec("UPDATE library_meta SET storage_volume_id=? WHERE singleton=1 AND library_id=? AND storage_id=? AND storage_volume_id=?", newID, r.LibraryID, r.StorageID, r.OriginalVolumeID)
	if e != nil {
		return r, e
	}
	n, e := result.RowsAffected()
	if e != nil || n != 1 {
		return r, fmt.Errorf("restore pairing changed")
	}
	if _, e = tx.Exec(ddl); e != nil {
		return r, e
	}
	if e = tx.Commit(); e != nil {
		return r, e
	}
	if e = db.Close(); e != nil {
		return r, e
	}
	s.VolumeID = newID
	if e = writeJSON(filepath.Join(p.Storage, storageprofile.StateFilename), s); e != nil {
		return r, e
	}
	if _, e = storageprofile.LoadState(p.Storage); e != nil {
		return r, e
	}
	r.RestoredVolumeID = newID
	return r, nil
}

// Restore's identity update replaces the storage marker, and SQLite may create
// sidecars while validating the restored database. Repair their ownership only
// after all database handles have closed and before publishing a complete pair.
// Every archived entry retains its individual owner and permission bits.
func restoreNativePermissions(target map[string]string, entries []Entry) error {
	apply := func(root string, v Entry, optional bool) error {
		p := filepath.Join(root, filepath.FromSlash(v.Path))
		info, e := os.Lstat(p)
		if optional && errors.Is(e, os.ErrNotExist) {
			return nil
		}
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() && !info.IsDir() || info.IsDir() != (v.Type == "directory") {
			return fmt.Errorf("restored permission target changed: %s", p)
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return fmt.Errorf("missing native permission metadata: %s", p)
		}
		mode := preservedMode(v.Mode)
		if int(st.Uid) == v.UID && int(st.Gid) == v.GID && info.Mode()&(fs.ModePerm|fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) == mode {
			return nil
		}
		f, e := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		defer f.Close()
		current, e := f.Stat()
		if e != nil {
			return e
		}
		if !os.SameFile(info, current) {
			return fmt.Errorf("restored permission target changed: %s", p)
		}
		if e = f.Chown(v.UID, v.GID); e != nil {
			return e
		}
		if e = f.Chmod(mode); e != nil {
			return e
		}
		return f.Sync()
	}
	var database Entry
	sidecars := map[string]bool{}
	for _, v := range entries {
		if v.Role != "metadata" && v.Role != "storage" {
			continue
		}
		isSidecar := v.Role == "metadata" && (v.Path == "library.sqlite-wal" || v.Path == "library.sqlite-shm")
		if isSidecar {
			sidecars[v.Path] = true
		}
		if v.Role == "metadata" && v.Path == "library.sqlite" {
			database = v
		}
		if e := apply(target[v.Role], v, isSidecar); e != nil {
			return e
		}
	}
	for _, name := range []string{"library.sqlite-wal", "library.sqlite-shm"} {
		if sidecars[name] {
			continue
		}
		info, e := os.Lstat(filepath.Join(target["metadata"], name))
		if errors.Is(e, os.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		v := database
		v.Path, v.Type = name, "file"
		v.Mode = int64(info.Mode().Perm())
		if e = apply(target["metadata"], v, false); e != nil {
			return e
		}
	}
	return nil
}

func Restore(generation string, p Paths) (Report, error) {
	r, e := Verify(generation)
	if e != nil {
		return r, e
	}
	target, e := roots(p)
	if e != nil {
		return r, e
	}
	for _, v := range target {
		if overlap(v, generation) {
			return r, fmt.Errorf("restore target overlaps archive")
		}
		files, e := os.ReadDir(v)
		if e != nil {
			return r, e
		}
		if len(files) != 0 {
			return r, fmt.Errorf("restore requires new empty roots: %s", v)
		}
	}
	entries, e := readEntries(filepath.Join(generation, "manifest.jsonl"))
	if e != nil {
		return r, e
	}
	sizes := entryBytes(entries)
	demand := map[string]int64{}
	for role, root := range target {
		demand[root] += sizes[role]
	}
	demand[os.TempDir()] += sizes["metadata"]*2 + sizes["deployment"]
	if e = requireSpace(demand, 0); e != nil {
		return r, e
	}
	unlock, e := lockPair(p, true)
	if e != nil {
		return r, e
	}
	defer unlock()
	for _, role := range []string{"metadata", "storage"} {
		if e = writeJSON(filepath.Join(target[role], ".cold-restore-incomplete"), map[string]any{"generation": r.Generation, "libraryId": r.LibraryID, "storageId": r.StorageID, "stage": "extracting"}); e != nil {
			return r, e
		}
	}
	entries, e = readEntries(filepath.Join(generation, "manifest.jsonl"))
	if e != nil {
		return r, e
	}
	for _, role := range []string{"metadata", "storage", "deployment"} {
		if e = scanArchive(filepath.Join(generation, archiveNames[role]), role, byRole(entries, role), target[role], true); e != nil {
			return r, e
		}
	}
	// Verify all artifact hashes again after extraction, before controlled rebasing.
	if _, e = Verify(generation); e != nil {
		return r, e
	}
	r, e = rebase(p, r)
	if e != nil {
		return r, e
	}
	db, e := sqliteOpen(filepath.Join(p.Metadata, "library.sqlite"), true)
	if e != nil {
		return r, e
	}
	e = integrity(db)
	if e == nil {
		m, err := inspectDB(db)
		e = err
		if e == nil && (m.LibraryID != r.LibraryID || m.StorageID != r.StorageID || m.OriginalVolumeID != r.RestoredVolumeID) {
			e = fmt.Errorf("rebased pair differs")
		}
	}
	db.Close()
	if e != nil {
		return r, e
	}
	if e = restoreNativePermissions(target, entries); e != nil {
		return r, e
	}
	for _, role := range []string{"metadata", "storage"} {
		if e = os.Remove(filepath.Join(target[role], ".cold-restore-incomplete")); e != nil {
			return r, e
		}
		if e = syncDir(target[role]); e != nil {
			return r, e
		}
	}
	return r, nil
}
