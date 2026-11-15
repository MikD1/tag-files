package main

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/mikd1/tag-files/app/internal/storageprofile"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func write(t *testing.T, p string, b []byte) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	p := Paths{filepath.Join(root, "metadata"), filepath.Join(root, "storage"), filepath.Join(root, "deployment")}
	db, e := store.Open(context.Background(), p.Metadata)
	if e != nil {
		t.Fatal(e)
	}
	st, e := storageprofile.Prepare(p.Storage, aws.Credentials{AccessKeyID: strings.Repeat("a", 24), SecretAccessKey: strings.Repeat("s", 36)}, -1, -1)
	if e != nil {
		t.Fatal(e)
	}
	if e = st.RecordReady(); e != nil {
		t.Fatal(e)
	}
	m, e := dbgen.New(db.Reader()).GetLibraryMeta(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	s, e := st.BindLibrary(st.State.ID, m.LibraryID)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Write(context.Background(), func(tx *sql.Tx) error {
		_, e := dbgen.New(tx).BindLibraryStorage(context.Background(), dbgen.BindLibraryStorageParams{StorageID: sql.NullString{String: s.ID, Valid: true}, StorageVolumeID: sql.NullString{String: s.VolumeID, Valid: true}, LibraryID: m.LibraryID})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if e = db.Close(); e != nil {
		t.Fatal(e)
	}
	if e = st.Close(); e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"master/raft.dat", "filerldb2/CURRENT", "1.dat", "1.idx", ".hidden/mpu/part"} {
		write(t, filepath.Join(p.Storage, v), []byte("retained "+v))
	}
	write(t, filepath.Join(p.Metadata, ".hidden"), []byte("application private state"))
	write(t, filepath.Join(p.Deployment, "compose.yml"), []byte("services: {}\n"))
	write(t, filepath.Join(p.Deployment, "secrets/s3_access_key"), []byte(strings.Repeat("a", 24)))
	write(t, filepath.Join(p.Deployment, "secrets/s3_secret_key"), []byte(strings.Repeat("s", 36)))
	write(t, filepath.Join(p.Deployment, "filer.toml"), []byte("[leveldb2]\nenabled = true\ndir = \"/data/filerldb2\"\n"))
	return Options{Paths: p, Destination: filepath.Join(root, "backups"), Generation: "fixture-01", Images: map[string]string{"app": "sha256:" + strings.Repeat("1", 64), "storage": "sha256:" + strings.Repeat("2", 64), "tool": "sha256:" + strings.Repeat("3", 64)}}
}
func TestCompleteGenerationPreservesHiddenFilesAndRejectsMutation(t *testing.T) {
	o := fixture(t)
	r, e := Backup(o)
	if e != nil {
		t.Fatal(e)
	}
	if r.LibraryID == "" || r.StorageID == "" || r.FileCount < 8 {
		t.Fatalf("missing evidence: %+v", r)
	}
	gen := filepath.Join(o.Destination, o.Generation)
	if _, e = Verify(gen); e != nil {
		t.Fatal(e)
	}
	f, e := os.OpenFile(filepath.Join(gen, "storage-volume.tar"), os.O_RDWR, 0)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.WriteAt([]byte{99}, 1024); e != nil {
		t.Fatal(e)
	}
	f.Close()
	if _, e = Verify(gen); e == nil {
		t.Fatal("modified native archive accepted")
	}
}
func TestIncompleteGenerationAndUnsafeSourcesCannotPublish(t *testing.T) {
	for _, kind := range []string{"symlink", "fifo", "missing-native"} {
		t.Run(kind, func(t *testing.T) {
			o := fixture(t)
			switch kind {
			case "symlink":
				os.Symlink("/etc/passwd", filepath.Join(o.Storage, "unsafe"))
			case "fifo":
				syscall.Mkfifo(filepath.Join(o.Storage, "pipe"), 0600)
			case "missing-native":
				os.RemoveAll(filepath.Join(o.Storage, "filerldb2"))
			}
			if _, e := Backup(o); e == nil {
				t.Fatal("unsafe source accepted")
			}
			if _, e := os.Stat(filepath.Join(o.Destination, o.Generation, "complete")); !os.IsNotExist(e) {
				t.Fatal("unsafe generation published")
			}
		})
	}
	o := fixture(t)
	os.MkdirAll(filepath.Join(o.Destination, o.Generation+".part"), 0700)
	if _, e := Verify(filepath.Join(o.Destination, o.Generation+".part")); e == nil {
		t.Fatal("incomplete accepted")
	}
}
func TestColdLocksRefuseRunningWriters(t *testing.T) {
	for _, role := range []string{"metadata", "storage"} {
		t.Run(role, func(t *testing.T) {
			o := fixture(t)
			p := filepath.Join(o.Metadata, ".library.lock")
			if role == "storage" {
				p = filepath.Join(o.Storage, ".tag-files-storage.lock")
			}
			f, e := os.Open(p)
			if e != nil {
				t.Fatal(e)
			}
			defer f.Close()
			if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
				t.Fatal(e)
			}
			if _, e = Backup(o); e == nil {
				t.Fatal("writer lock ignored")
			}
		})
	}
}
func TestRestoreRequiresEmptyTargetsAndRebasesOnlyPhysicalPair(t *testing.T) {
	o := fixture(t)
	before, e := Backup(o)
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	p := Paths{filepath.Join(root, "metadata"), filepath.Join(root, "storage"), filepath.Join(root, "deployment")}
	for _, x := range []string{p.Metadata, p.Storage, p.Deployment} {
		os.Mkdir(x, 0700)
	}
	r, e := Restore(filepath.Join(o.Destination, o.Generation), p)
	if e != nil {
		t.Fatal(e)
	}
	if r.LibraryID != before.LibraryID || r.StorageID != before.StorageID || r.OriginalVolumeID == r.RestoredVolumeID {
		t.Fatalf("invalid restore identities: %+v", r)
	}
	state, e := storageprofile.LoadState(p.Storage)
	if e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(context.Background(), p.Metadata)
	if e != nil {
		t.Fatal(e)
	}
	m, e := dbgen.New(db.Reader()).GetLibraryMeta(context.Background())
	db.Close()
	if e != nil {
		t.Fatal(e)
	}
	if state.VolumeID != m.StorageVolumeID.String {
		t.Fatal("restored pair disagrees")
	}
	for _, v := range []struct{ root, path, want string }{{p.Metadata, ".hidden", "application private state"}, {p.Storage, ".hidden/mpu/part", "retained .hidden/mpu/part"}, {p.Deployment, "secrets/s3_secret_key", strings.Repeat("s", 36)}} {
		got, e := os.ReadFile(filepath.Join(v.root, v.path))
		if e != nil || string(got) != v.want {
			t.Fatalf("lost full-volume bytes %s: %s %v", v.path, got, e)
		}
	}
	if _, e = Restore(filepath.Join(o.Destination, o.Generation), p); e == nil {
		t.Fatal("nonempty restore accepted")
	}
	if _, e = Verify(filepath.Join(o.Destination, o.Generation)); e != nil {
		t.Fatalf("restore changed archive: %v", e)
	}
}

func TestIncompleteRestoreAndMismatchedSecretsCannotBecomeBackup(t *testing.T) {
	for _, kind := range []string{"incomplete", "credentials"} {
		t.Run(kind, func(t *testing.T) {
			o := fixture(t)
			if kind == "incomplete" {
				write(t, filepath.Join(o.Metadata, ".cold-restore-incomplete"), []byte("interrupted"))
			} else {
				write(t, filepath.Join(o.Deployment, "secrets/s3_secret_key"), []byte(strings.Repeat("x", 36)))
			}
			if _, e := Backup(o); e == nil {
				t.Fatal("unsafe restoration configuration accepted")
			}
		})
	}
}

func TestPermissionMetadataSurvivesRestore(t *testing.T) {
	o := fixture(t)
	p := filepath.Join(o.Deployment, "sticky")
	if e := os.Mkdir(p, os.ModeSticky|0770); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(p, os.ModeSticky|0770); e != nil {
		t.Fatal(e)
	}
	if _, e := Backup(o); e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	target := Paths{filepath.Join(root, "metadata"), filepath.Join(root, "storage"), filepath.Join(root, "deployment")}
	for _, p := range []string{target.Metadata, target.Storage, target.Deployment} {
		os.Mkdir(p, 0700)
	}
	if _, e := Restore(filepath.Join(o.Destination, o.Generation), target); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(filepath.Join(target.Deployment, "sticky"))
	if e != nil {
		t.Fatal(e)
	}
	if info.Mode()&os.ModeSticky == 0 || info.Mode().Perm() != 0770 {
		t.Fatalf("filesystem permissions lost: %s", info.Mode())
	}
}

func TestRootRestorePreservesNonrootOwnershipAndPermissions(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to archive and restore another user's ownership")
	}
	o := fixture(t)
	const uid, gid = 12345, 23456
	for _, root := range []string{o.Metadata, o.Storage, o.Deployment} {
		if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Chown(path, uid, gid)
		}); err != nil {
			t.Fatal(err)
		}
	}
	state := filepath.Join(o.Storage, storageprofile.StateFilename)
	if err := os.Chmod(state, 0640); err != nil {
		t.Fatal(err)
	}
	// Preserve individual archived owners, rather than assigning every file the
	// root directory's owner or making private state readable to other users.
	if err := os.Chown(filepath.Join(o.Metadata, ".hidden"), uid+1, gid+1); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(o); err != nil {
		t.Fatal(err)
	}
	generation := filepath.Join(o.Destination, o.Generation)
	entries, err := readEntries(filepath.Join(generation, "manifest.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// The root backup process must leave the nonroot source tree untouched.
	for _, role := range []string{"metadata", "storage", "deployment"} {
		root := map[string]string{"metadata": o.Metadata, "storage": o.Storage, "deployment": o.Deployment}[role]
		if err := verifySource(root, role, byRole(entries, role)); err != nil {
			t.Fatalf("backup changed %s source: %v", role, err)
		}
	}
	root := t.TempDir()
	target := Paths{filepath.Join(root, "metadata"), filepath.Join(root, "storage"), filepath.Join(root, "deployment")}
	for _, path := range []string{target.Metadata, target.Storage, target.Deployment} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Restore(generation, target); err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{"metadata": target.Metadata, "storage": target.Storage, "deployment": target.Deployment}
	for _, entry := range entries {
		path := filepath.Join(roots[entry.Role], filepath.FromSlash(entry.Path))
		info, err := os.Stat(path)
		if os.IsNotExist(err) && entry.Role == "metadata" && (entry.Path == "library.sqlite-wal" || entry.Path == "library.sqlite-shm") {
			continue // A closed SQLite connection may remove its checkpointed sidecars.
		}
		if err != nil {
			t.Fatal(err)
		}
		owner := info.Sys().(*syscall.Stat_t)
		if owner.Uid != uint32(entry.UID) || owner.Gid != uint32(entry.GID) || info.Mode()&(os.ModePerm|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != preservedMode(entry.Mode) {
			t.Errorf("restored %s/%s has owner %d:%d, mode %s; want %d:%d, %s", entry.Role, entry.Path, owner.Uid, owner.Gid, info.Mode(), entry.UID, entry.GID, preservedMode(entry.Mode))
		}
	}
	for _, root := range []string{target.Metadata, target.Storage} {
		if err := filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if info.Sys().(*syscall.Stat_t).Uid == 0 {
				t.Errorf("root restore left root-owned native entry: %s", path)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRootRestoredSQLiteSidecarsPreservePrivatePermissionsAndOwners(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to repair another user's ownership")
	}
	for _, archived := range []bool{false, true} {
		t.Run(map[bool]string{false: "generated", true: "archived"}[archived], func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "library.sqlite"), []byte("database"))
			write(t, filepath.Join(root, "library.sqlite-wal"), []byte("WAL"))
			entries := []Entry{{Role: "metadata", Path: ".", Type: "directory", UID: 12345, GID: 23456, Mode: 0700},
				{Role: "metadata", Path: "library.sqlite", Type: "file", UID: 12345, GID: 23456, Mode: 0640}}
			wantUID, wantGID, wantMode := uint32(12345), uint32(23456), fs.FileMode(0600)
			if archived {
				entries = append(entries,
					Entry{Role: "metadata", Path: "library.sqlite-wal", Type: "file", UID: 12346, GID: 23457, Mode: 0640},
					Entry{Role: "metadata", Path: "library.sqlite-shm", Type: "file", UID: 12346, GID: 23457, Mode: 0600})
				wantUID, wantGID, wantMode = 12346, 23457, 0640
			} else {
				write(t, filepath.Join(root, "library.sqlite-shm"), []byte("SHM"))
			}
			if err := restoreNativePermissions(map[string]string{"metadata": root}, entries); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(root, "library.sqlite-wal"))
			if err != nil {
				t.Fatal(err)
			}
			owner := info.Sys().(*syscall.Stat_t)
			if owner.Uid != wantUID || owner.Gid != wantGID || info.Mode().Perm() != wantMode {
				t.Fatalf("restored WAL owner/mode = %d:%d %s; want %d:%d %s", owner.Uid, owner.Gid, info.Mode(), wantUID, wantGID, wantMode)
			}
			info, err = os.Stat(filepath.Join(root, "library.sqlite-shm"))
			if archived {
				if !os.IsNotExist(err) {
					t.Fatalf("checkpointed sidecar should remain absent: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				owner = info.Sys().(*syscall.Stat_t)
				if owner.Uid != 12345 || owner.Gid != 23456 || info.Mode().Perm() != 0600 {
					t.Fatalf("generated SHM owner/mode = %d:%d %s; want 12345:23456 0600", owner.Uid, owner.Gid, info.Mode())
				}
			}
		})
	}
}

// An actual killed process leaves WAL frames without a final sqlite3_close.
func TestWALCrashProcess(t *testing.T) {
	root := os.Getenv("TAG_FILES_BACKUP_WAL_FIXTURE")
	if root == "" {
		return
	}
	db, e := store.Open(context.Background(), root)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Write(context.Background(), func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO tags(id,name) VALUES(9001,'WAL-only 雪'); INSERT INTO items(id,media_kind,uploaded_at_utc,duration_ticks,state) VALUES(9001,'Video','2001-01-01T00:00:00.0000000Z',9223372036854775807,'importing')")
		return e
	}); e != nil {
		t.Fatal(e)
	}
	os.Exit(0)
}
func TestBackupIncludesUncheckpointedWALWithoutWritingSource(t *testing.T) {
	o := fixture(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestWALCrashProcess$")
	cmd.Env = append(os.Environ(), "TAG_FILES_BACKUP_WAL_FIXTURE="+o.Metadata)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("WAL fixture: %s %v", out, e)
	}
	wal := filepath.Join(o.Metadata, "library.sqlite-wal")
	before, e := os.ReadFile(wal)
	if e != nil || len(before) < 32 {
		t.Fatal("no persistent WAL fixture", e)
	}
	if _, e = Backup(o); e != nil {
		t.Fatal(e)
	}
	after, e := os.ReadFile(wal)
	if e != nil || !bytes.Equal(before, after) {
		t.Fatal("source WAL modified", e)
	}
	snapshot, e := sqliteOpen(filepath.Join(o.Destination, o.Generation, "sqlite-snapshot.sqlite"), true)
	if e != nil {
		t.Fatal(e)
	}
	defer snapshot.Close()
	var name, ticks string
	if e = snapshot.QueryRow("SELECT t.name,CAST(i.duration_ticks AS TEXT) FROM tags t JOIN items i ON i.id=t.id WHERE t.id=9001").Scan(&name, &ticks); e != nil || name != "WAL-only 雪" || ticks != "9223372036854775807" {
		t.Fatal("WAL rows or integer precision lost", name, ticks, e)
	}
}
func TestArchiveTraversalIsRejectedBeforeDestinationWrites(t *testing.T) {
	o := fixture(t)
	if _, e := Backup(o); e != nil {
		t.Fatal(e)
	}
	gen := filepath.Join(o.Destination, o.Generation)
	p := filepath.Join(gen, "storage-volume.tar")
	var b bytes.Buffer
	tw := tar.NewWriter(&b)
	if e := tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0600, Size: 4, Typeflag: tar.TypeReg}); e != nil {
		t.Fatal(e)
	}
	tw.Write([]byte("evil"))
	tw.Close()
	if e := os.WriteFile(p, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	// Recompute the outer checksum to prove safe extraction does not rely solely
	// on the whole-archive hash. The per-entry contract still rejects traversal.
	var m Metadata
	raw, e := os.ReadFile(filepath.Join(gen, "backup.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		t.Fatal(e)
	}
	for i := range m.Artifacts {
		if m.Artifacts[i].Name == "storage-volume.tar" {
			m.Artifacts[i], e = fileArtifact(p)
			if e != nil {
				t.Fatal(e)
			}
		}
	}
	if e = writeJSON(filepath.Join(gen, "backup.json"), m); e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile(filepath.Join(gen, "backup.json"))
	if e != nil {
		t.Fatal(e)
	}
	write(t, filepath.Join(gen, "complete"), []byte(digest(raw)+"\n"))
	if _, e = Verify(gen); e == nil {
		t.Fatal("unsafe tar accepted after outer hash refresh")
	}
	if _, e = os.Stat(filepath.Join(o.Destination, "escape")); !os.IsNotExist(e) {
		t.Fatal("tar escaped destination")
	}
}

func TestInsufficientArchiveSpaceFailsBeforePartialGeneration(t *testing.T) {
	o := fixture(t)
	o.MinFreeBytes = 1 << 62
	if _, e := Backup(o); e == nil {
		t.Fatal("unavailable archive budget accepted")
	}
	if _, e := os.Stat(filepath.Join(o.Destination, o.Generation+".part")); !os.IsNotExist(e) {
		t.Fatal("archive started without available budget")
	}
}

func TestCheckpointMutationCannotPassVerify(t *testing.T) {
	o := fixture(t)
	if _, e := Backup(o); e != nil {
		t.Fatal(e)
	}
	gen := filepath.Join(o.Destination, o.Generation)
	write(t, filepath.Join(gen, "checkpoint.json"), []byte(`{"stage":"different"}`))
	if _, e := Verify(gen); e == nil {
		t.Fatal("mutated generation checkpoint accepted")
	}
}

func TestInterruptedRebaseKeepsBothRuntimeGuardsAndRefusesReuse(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("permission interruption requires an unprivileged host test process")
	}
	o := fixture(t)
	r, e := Backup(o)
	if e != nil {
		t.Fatal(e)
	}
	gen := filepath.Join(o.Destination, o.Generation)
	entries, e := readEntries(filepath.Join(gen, "manifest.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	p := Paths{filepath.Join(root, "metadata"), filepath.Join(root, "storage"), filepath.Join(root, "deployment")}
	targets := map[string]string{"metadata": p.Metadata, "storage": p.Storage, "deployment": p.Deployment}
	for role, target := range targets {
		if e = os.Mkdir(target, 0700); e != nil {
			t.Fatal(e)
		}
		if e = scanArchive(filepath.Join(gen, archiveNames[role]), role, byRole(entries, role), target, false); e != nil {
			t.Fatal(e)
		}
	}
	for _, target := range []string{p.Metadata, p.Storage} {
		write(t, filepath.Join(target, ".cold-restore-incomplete"), []byte("interrupted before marker replacement"))
	}
	if e = os.Chmod(p.Storage, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(p.Storage, 0700)
	if _, e = rebase(p, r); e == nil {
		t.Fatal("read-only storage marker replacement succeeded")
	}
	db, e := sqliteOpen(filepath.Join(p.Metadata, "library.sqlite"), true)
	if e != nil {
		t.Fatal(e)
	}
	m, e := inspectDB(db)
	if e != nil {
		t.Fatal(e)
	}
	var trigger int
	if e = db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type='trigger' AND name='immutable_storage_pair'").Scan(&trigger); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if m.OriginalVolumeID == r.OriginalVolumeID || trigger != 1 {
		t.Fatal("SQL rebase not committed atomically with restored trigger")
	}
	raw, e := os.ReadFile(filepath.Join(p.Storage, storageprofile.StateFilename))
	if e != nil {
		t.Fatal(e)
	}
	var old storageprofile.StorageState
	if e = json.Unmarshal(raw, &old); e != nil {
		t.Fatal(e)
	}
	if old.VolumeID != r.OriginalVolumeID {
		t.Fatal("failed atomic marker update modified old storage marker")
	}
	if db, e := store.Open(context.Background(), p.Metadata); e == nil {
		db.Close()
		t.Fatal("interrupted SQL half admitted application")
	}
	if install, e := storageprofile.Prepare(p.Storage, aws.Credentials{AccessKeyID: strings.Repeat("a", 24), SecretAccessKey: strings.Repeat("s", 36)}, -1, -1); e == nil {
		install.Close()
		t.Fatal("interrupted storage half admitted runtime")
	}
	if _, e = Restore(gen, p); e == nil {
		t.Fatal("partial rebase reused nonempty targets")
	}
	if _, e = Verify(gen); e != nil {
		t.Fatal("interrupted restore mutated archive", e)
	}
}

func TestReadyLibraryWithoutAnyChunkDataCannotPublish(t *testing.T) {
	o := fixture(t)
	db, e := store.Open(context.Background(), o.Metadata)
	if e != nil {
		t.Fatal(e)
	}
	asset := "74fe097b-d624-45f6-9c45-a5b72b584bb2"
	if e = db.Write(context.Background(), func(tx *sql.Tx) error {
		_, e := tx.Exec("INSERT INTO items(id,media_kind,uploaded_at_utc,state) VALUES(1,'Text','2001-01-01T00:00:00.0000000Z','importing'); INSERT INTO assets(id,item_id,role,bucket,object_key,size_bytes,sha256,content_type,provenance,state) VALUES(?,1,'primary','tag-files','missing',1,?,'text/plain','legacy_preserved','published'); UPDATE items SET state='ready',primary_asset_id=? WHERE id=1", asset, strings.Repeat("a", 64), asset)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if e = os.Remove(filepath.Join(o.Storage, "1.dat")); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(o.Storage, "1.idx")); e != nil {
		t.Fatal(e)
	}
	if _, e = Backup(o); e == nil {
		t.Fatal("ready assets without native chunk data accepted")
	}
}
