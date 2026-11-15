package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/domain"
)

func regular(path string) (os.FileInfo, error) {
	i, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() {
		return nil, fmt.Errorf("expected regular file: %s", path)
	}
	return i, nil
}
func hashFile(path string) (int64, string, error) {
	if _, e := regular(path); e != nil {
		return 0, "", e
	}
	f, e := os.Open(path)
	if e != nil {
		return 0, "", e
	}
	defer f.Close()
	return digestReader(f)
}
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func writeAtomic(path string, b []byte) error {
	f, e := os.OpenFile(path+".part", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(path+".part", path); e != nil {
		return e
	}
	return syncDirectory(filepath.Dir(path))
}
func saveJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return writeAtomic(path, append(b, '\n'))
}
func safePath(root, path string) (string, error) {
	if filepath.IsAbs(path) || path == "" || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, "../") {
		return "", fmt.Errorf("unsafe archive path %q", path)
	}
	p := root
	for _, part := range strings.Split(path, string(os.PathSeparator)) {
		p = filepath.Join(p, part)
		i, e := os.Lstat(p)
		if e != nil {
			return "", e
		}
		if i.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("archive symlink refused")
		}
	}
	return p, nil
}
func canonicalInventory(v Inventory) Inventory {
	v.Objects = append([]SourceObject(nil), v.Objects...)
	v.BucketConfiguration = maps.Clone(v.BucketConfiguration)
	sort.Slice(v.Objects, func(i, j int) bool { return v.Objects[i].Identity() < v.Objects[j].Identity() })
	for i := range v.Objects {
		v.Objects[i].Metadata = maps.Clone(v.Objects[i].Metadata)
		v.Objects[i].Tags = maps.Clone(v.Objects[i].Tags)
		v.Objects[i].SHA256 = ""
		v.Objects[i].ArchivePath = ""
	}
	v.Multipart = append([]Multipart(nil), v.Multipart...)
	sort.Slice(v.Multipart, func(i, j int) bool {
		a, b := v.Multipart[i], v.Multipart[j]
		return a.Bucket+a.Key+a.UploadID < b.Bucket+b.Key+b.UploadID
	})
	for i := range v.Multipart {
		v.Multipart[i].Parts = append([]Part(nil), v.Multipart[i].Parts...)
		sort.Slice(v.Multipart[i].Parts, func(a, b int) bool { return v.Multipart[i].Parts[a].Number < v.Multipart[i].Parts[b].Number })
	}
	return v
}
func inventoryHash(v Inventory) string { b, _ := json.Marshal(canonicalInventory(v)); return digest(b) }
func fingerprint(m *Manifest) string {
	b, _ := json.Marshal(struct {
		SnapshotID                    string
		NativeFiles                   []NativeFile
		Inventory                     string
		Objects                       []SourceObject
		SnapshotSHA256, RawRowsSHA256 string
	}{m.SnapshotID, m.NativeFiles, inventoryHash(m.Inventory), m.Objects, m.SnapshotSHA256, m.RawRowsSHA256})
	return digest(b)
}
func nativeFiles(path string) ([]NativeFile, error) {
	out := []NativeFile{}
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		p := path + suffix
		_, e := os.Lstat(p)
		if errors.Is(e, os.ErrNotExist) && suffix != "" {
			continue
		}
		if e != nil {
			return nil, e
		}
		n, h, e := hashFile(p)
		if e != nil {
			return nil, e
		}
		out = append(out, NativeFile{Path: p, ArchivePath: "native/legacy.sqlite" + suffix, Size: n, SHA256: h})
	}
	return out, nil
}
func archiveStream(path string, r io.Reader, expected int64) (int64, string, error) {
	f, e := os.OpenFile(path+".part", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return 0, "", e
	}
	h := newHash()
	n, e := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(r, expected+1), make([]byte, 128<<10))
	if e == nil && n != expected {
		e = fmt.Errorf("source byte length mismatch: got %d want %d", n, expected)
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return n, "", e
	}
	if e = os.Rename(path+".part", path); e != nil {
		return n, "", e
	}
	return n, hexHash(h), syncDirectory(filepath.Dir(path))
}

func Scan(ctx context.Context, c ScanConfig, s Source) (*Manifest, error) {
	if c.SnapshotID == "" || c.RollbackReference == "" || !c.RollbackVerified {
		return nil, fmt.Errorf("explicit frozen snapshot ID and verified rollback reference required")
	}
	p, e := filepath.Abs(c.Database)
	if e != nil {
		return nil, e
	}
	c.Database = p
	if e = os.Mkdir(c.Archive, 0700); e != nil {
		return nil, e
	}
	for _, sub := range []string{"native", "objects", "work"} {
		if e = os.Mkdir(filepath.Join(c.Archive, sub), 0700); e != nil {
			return nil, e
		}
	}
	m := &Manifest{Version: ArchiveVersion, RunID: domain.NewUUID(), CreatedAt: now(), SnapshotID: c.SnapshotID, RollbackReference: c.RollbackReference, Database: c.Database}
	m.NativeFiles, e = nativeFiles(c.Database)
	if e != nil {
		return nil, e
	}
	m.Inventory, e = s.Inventory(ctx)
	if e != nil {
		return nil, e
	}
	m.Inventory = canonicalInventory(m.Inventory)
	var needed int64
	for _, f := range m.NativeFiles {
		if needed > maxInt-f.Size {
			return nil, ErrBudget
		}
		if f.Size > maxInt/3 || needed > maxInt-3*f.Size {
			return nil, ErrBudget
		}
		needed += 3 * f.Size
	}
	for _, o := range m.Inventory.Objects {
		if o.Size < 0 || needed > maxInt-o.Size {
			return nil, ErrBudget
		}
		needed += o.Size
	}
	if e = localBudget(c.Archive, needed, c.ArchiveReserveBytes); e != nil {
		return nil, e
	}
	for _, f := range m.NativeFiles {
		in, e := os.Open(f.Path)
		if e != nil {
			return nil, e
		}
		_, hash, e := archiveStream(filepath.Join(c.Archive, f.ArchivePath), in, f.Size)
		in.Close()
		if e != nil {
			return nil, e
		}
		if hash != f.SHA256 {
			return nil, ErrSourceChanged
		}
	}
	// SQLite may recover/create WAL indexes only in this private working copy.
	for _, f := range m.NativeFiles {
		if strings.HasSuffix(f.Path, "-shm") {
			continue
		}
		in, e := os.Open(filepath.Join(c.Archive, f.ArchivePath))
		if e != nil {
			return nil, e
		}
		_, _, e = archiveStream(filepath.Join(c.Archive, "work", filepath.Base(f.ArchivePath)), in, f.Size)
		in.Close()
		if e != nil {
			return nil, e
		}
	}
	work := filepath.Join(c.Archive, "work", "legacy.sqlite")
	u := url.URL{Scheme: "file", Path: work, RawQuery: "mode=rw&_synchronous=FULL"}
	db, e := sql.Open("sqlite3", u.String())
	if e != nil {
		return nil, e
	}
	defer db.Close()
	var integrity string
	if e = db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); e != nil || integrity != "ok" {
		return nil, fmt.Errorf("legacy integrity_check: %s: %w", integrity, e)
	}
	m.SQLite.Integrity = integrity
	if e = db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&m.SQLite.UserVersion); e != nil {
		return nil, e
	}
	if e = db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&m.SQLite.JournalMode); e != nil {
		return nil, e
	}
	fk, e := db.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if e != nil {
		return nil, e
	}
	for fk.Next() {
		var table, parent string
		var rowid any
		var id int64
		if e = fk.Scan(&table, &rowid, &parent, &id); e != nil {
			fk.Close()
			return nil, e
		}
		m.SQLite.ForeignKeyViolations = append(m.SQLite.ForeignKeyViolations, []string{table, fmt.Sprint(rowid), parent, fmt.Sprint(id)})
	}
	e = fk.Err()
	fk.Close()
	if e != nil {
		return nil, e
	}
	// VACUUM INTO reads all committed WAL state while only writing an independent copy.
	snapshot := filepath.Join(c.Archive, "snapshot.sqlite")
	abs, _ := filepath.Abs(snapshot)
	if _, e = db.ExecContext(ctx, `VACUUM INTO ?`, abs); e != nil {
		return nil, fmt.Errorf("create independent SQLite snapshot: %w", e)
	}
	db.Close()
	if e = os.Chmod(snapshot, 0600); e != nil {
		return nil, e
	}
	snapshotFile, e := os.OpenFile(snapshot, os.O_RDWR, 0)
	if e != nil {
		return nil, e
	}
	e = snapshotFile.Sync()
	snapshotFile.Close()
	if e != nil {
		return nil, e
	}
	u = url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&immutable=1&_query_only=true"}
	db, e = sql.Open("sqlite3", u.String())
	if e != nil {
		return nil, e
	}
	defer db.Close()
	if e = dumpRaw(ctx, db, filepath.Join(c.Archive, "rows.jsonl")); e != nil {
		return nil, e
	}
	_, m.SnapshotSHA256, e = hashFile(snapshot)
	if e != nil {
		return nil, e
	}
	_, m.RawRowsSHA256, e = hashFile(filepath.Join(c.Archive, "rows.jsonl"))
	if e != nil {
		return nil, e
	}
	m.Model, m.Issues, e = readModel(ctx, db)
	if e != nil {
		return nil, e
	}
	seen := map[string]bool{}
	for _, o := range m.Inventory.Objects {
		if seen[o.Identity()] {
			return nil, fmt.Errorf("duplicate source inventory identity")
		}
		seen[o.Identity()] = true
		if o.DeleteMarker {
			m.Objects = append(m.Objects, o)
			continue
		}
		if e = localBudget(c.Archive, o.Size, c.ArchiveReserveBytes); e != nil {
			return nil, e
		}
		in, e := s.Open(ctx, o)
		if e != nil {
			return nil, e
		}
		o.ArchivePath = "objects/" + digest([]byte(o.Identity()))
		_, o.SHA256, e = archiveStream(filepath.Join(c.Archive, o.ArchivePath), in, o.Size)
		in.Close()
		if e != nil {
			return nil, e
		}
		m.Objects = append(m.Objects, o)
	}
	classify(m)
	m.Fingerprint = fingerprint(m)
	if e = CheckFrozen(ctx, m, s); e != nil {
		return nil, e
	}
	if e = saveJSON(filepath.Join(c.Archive, "manifest.json"), m); e != nil {
		return nil, e
	}
	_, hash, e := hashFile(filepath.Join(c.Archive, "manifest.json"))
	if e != nil {
		return nil, e
	}
	if e = writeAtomic(filepath.Join(c.Archive, "complete"), []byte(hash+"\n")); e != nil {
		return nil, e
	}
	if e = os.RemoveAll(filepath.Join(c.Archive, "work")); e != nil {
		return nil, e
	}
	return m, nil
}
func CheckFrozen(ctx context.Context, m *Manifest, s Source) error {
	files, e := nativeFiles(m.Database)
	if e != nil {
		return fmt.Errorf("%w: %v", ErrSourceChanged, e)
	}
	if !reflect.DeepEqual(files, m.NativeFiles) {
		return ErrSourceChanged
	}
	v, e := s.Inventory(ctx)
	if e != nil {
		return e
	}
	if inventoryHash(v) != inventoryHash(m.Inventory) {
		return ErrSourceChanged
	}
	for _, o := range m.Objects {
		if o.DeleteMarker {
			continue
		}
		body, e := s.Open(ctx, o)
		if e != nil {
			return fmt.Errorf("%w: %v", ErrSourceChanged, e)
		}
		n, hash, e := digestReader(io.LimitReader(body, o.Size+1))
		closeErr := body.Close()
		if e != nil || closeErr != nil || n != o.Size || hash != o.SHA256 {
			return fmt.Errorf("%w: full source hash %s", ErrSourceChanged, o.Identity())
		}
	}
	return nil
}
func LoadArchive(ctx context.Context, root string) (*Manifest, error) {
	p, e := safePath(root, "complete")
	if e != nil {
		return nil, e
	}
	complete, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	p, e = safePath(root, "manifest.json")
	if e != nil {
		return nil, e
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	if strings.TrimSpace(string(complete)) != digest(b) {
		return nil, fmt.Errorf("archive manifest hash mismatch")
	}
	var m Manifest
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.DisallowUnknownFields()
	if e = d.Decode(&m); e != nil {
		return nil, e
	}
	if m.Version != ArchiveVersion || m.Fingerprint != fingerprint(&m) {
		return nil, fmt.Errorf("unsupported/changed archive fingerprint")
	}
	checks := []NativeFile{{ArchivePath: "snapshot.sqlite", SHA256: m.SnapshotSHA256}, {ArchivePath: "rows.jsonl", SHA256: m.RawRowsSHA256}}
	checks = append(checks, m.NativeFiles...)
	for _, o := range m.Objects {
		if !o.DeleteMarker {
			checks = append(checks, NativeFile{ArchivePath: o.ArchivePath, SHA256: o.SHA256, Size: o.Size})
		}
	}
	for _, f := range checks {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		p, e := safePath(root, f.ArchivePath)
		if e != nil {
			return nil, e
		}
		n, h, e := hashFile(p)
		if e != nil || h != f.SHA256 || (f.Size != 0 && n != f.Size) {
			return nil, fmt.Errorf("archive bytes mismatch %s: %w", f.ArchivePath, e)
		}
	}
	return &m, nil
}

const maxInt = int64(^uint64(0) >> 1)

func localBudget(path string, need, reserve int64) error {
	if need < 0 || reserve < 0 || need > maxInt-reserve {
		return ErrBudget
	}
	observed, e := diskspace.Measure(path)
	if e != nil {
		return e
	}
	if observed.AvailableBytes < need+reserve || observed.AvailableInodes == nil || *observed.AvailableInodes < 2 {
		return ErrBudget
	}
	return nil
}
