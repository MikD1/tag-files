// tag-files-migration-host handles files and inspected Docker metadata, never Docker itself.
package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

type mount struct {
	Type, Source, Name, Destination string
	Canonical                       string `json:",omitempty"`
	RW                              bool
}
type networkAttachment struct {
	Aliases   []string
	IPAddress string
}
type container struct {
	ID          string `json:"Id"`
	Image, Name string
	Config      struct {
		Env, Cmd, Entrypoint []string
		WorkingDir, User     string
		Labels               map[string]string
	}
	Mounts          []mount
	NetworkSettings struct{ Networks map[string]networkAttachment }
	State           struct{ Running, Paused, Restarting bool }
}
type options struct {
	State, Inspect, SourceDirectory, TargetDirectory, Version, AppImage, StorageImage, MigrationImage, Project, Deployment, Source, SourceStorage, Target, Parent, Stage, Plan string
	Port                                                                                                                                                                       int
	AvailableBytes                                                                                                                                                             func(string) (int64, error)
}
type operation struct {
	Format                                                                                         int
	ID, SourceDirectory, TargetDirectory, Version, AppImage, StorageImage, MigrationImage, Project string
	Port                                                                                           int
	AppID, StorageID, SourceStorageImage, DatabaseRelative, InspectionHash, DeploymentHash         string
	DatabaseMount, StorageMount                                                                    mount
	InstallationHash                                                                               string `json:",omitempty"`
	StorageRelative                                                                                string `json:",omitempty"`
	PhysicalValidation                                                                             bool   `json:",omitempty"`
}
type fileRecord struct {
	Path      string
	Size      int64
	SHA256    string
	Mode      uint32
	Directory bool `json:",omitempty"`
}
type backupManifest struct {
	Format                      int
	OperationID                 string
	Source, Storage, Deployment []fileRecord
	Evidence                    []fileRecord
}

var exactImage = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func randomID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func safeValue(s string) bool { return s != "" && !strings.ContainsAny(s, "\x00\r\n,") }
func boundedDirectory(path string) error {
	i, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return errors.New("expected a real directory without symlinks")
	}
	return nil
}
func overlap(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	r, e := filepath.Rel(a, b)
	if e == nil && (r == "." || r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator))) {
		return true
	}
	r, e = filepath.Rel(b, a)
	return e == nil && (r == "." || r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)))
}
func regular(path string, limit int64) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	i, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if !i.Mode().IsRegular() || i.Size() > limit {
		return nil, errors.New("expected bounded regular file")
	}
	b, e := io.ReadAll(io.LimitReader(f, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("file exceeds limit")
	}
	return b, nil
}
func syncDirectory(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func owner(path string) (int, int, error) {
	i, e := os.Stat(path)
	if e != nil {
		return 0, 0, e
	}
	s := i.Sys().(*syscall.Stat_t)
	return int(s.Uid), int(s.Gid), nil
}
func atomicFile(path string, data []byte, mode os.FileMode) error {
	parent := filepath.Dir(path)
	if e := boundedDirectory(parent); e != nil {
		return e
	}
	f, e := os.CreateTemp(parent, ".migration-write-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	uid, gid, e := owner(parent)
	if e == nil {
		e = f.Chown(uid, gid)
	}
	if e == nil {
		e = f.Chmod(mode)
	}
	if e == nil {
		_, e = f.Write(data)
	}
	if e == nil {
		e = f.Sync()
	}
	e = errors.Join(e, f.Close())
	if e != nil {
		return e
	}
	if e = os.Rename(name, path); e != nil {
		return e
	}
	return syncDirectory(parent)
}
func saveJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return atomicFile(path, append(b, '\n'), 0600)
}
func privateDir(root, name string) error {
	p := filepath.Join(root, name)
	if e := os.Mkdir(p, 0700); e != nil && !os.IsExist(e) {
		return e
	}
	if e := boundedDirectory(p); e != nil {
		return e
	}
	uid, gid, e := owner(root)
	if e != nil {
		return e
	}
	if e = os.Chown(p, uid, gid); e != nil {
		return e
	}
	return os.Chmod(p, 0700)
}
func loadOperation(root string) (operation, error) {
	var s operation
	b, e := regular(filepath.Join(root, "state.json"), 1<<20)
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(b, &s)
	if e == nil && (s.Format != 1 || s.ID == "") {
		e = errors.New("unsupported migration state")
	}
	return s, e
}
func environment(row container) (map[string]string, error) {
	m := map[string]string{}
	for _, s := range row.Config.Env {
		k, v, ok := strings.Cut(s, "=")
		if !ok || k == "" {
			return nil, errors.New("invalid inspected environment")
		}
		if _, exists := m[k]; exists {
			return nil, errors.New("ambiguous duplicate environment setting")
		}
		m[k] = v
	}
	return m, nil
}
func readRows(path string) ([]container, error) {
	b, e := regular(path, 128<<20)
	if e != nil {
		return nil, e
	}
	var rows []container
	e = json.Unmarshal(b, &rows)
	if e != nil {
		return rows, e
	}
	mapping, e := loadCanonical(path)
	if os.IsNotExist(e) {
		return rows, nil
	}
	if e != nil {
		return nil, e
	}
	keys := map[string]string{}
	for _, entry := range mapping.Mounts {
		keys[mountKey(mount{Type: entry.Type, Source: entry.Source, Name: entry.Name})] = entry.Identity
	}
	for i := range rows {
		for j := range rows[i].Mounts {
			m := &rows[i].Mounts[j]
			if m.Type == "bind" || m.Type == "volume" {
				m.Canonical = keys[mountKey(*m)]
			}
		}
	}
	return rows, nil
}
func sourceRows(rows []container, source string) (container, container, error) {
	var apps, stores []container
	for _, r := range rows {
		if filepath.Clean(r.Config.Labels["com.docker.compose.project.working_dir"]) != source {
			continue
		}
		env, e := environment(r)
		if e != nil {
			return container{}, container{}, e
		}
		if env["ConnectionStrings__SQLite"] != "" {
			apps = append(apps, r)
		}
		if env["MINIO_ROOT_USER"] != "" || env["MINIO_ACCESS_KEY"] != "" {
			stores = append(stores, r)
		}
		if r.State.Running || r.State.Paused || r.State.Restarting {
			return container{}, container{}, errors.New("stop every source Compose container before migration")
		}
	}
	if len(apps) != 1 || len(stores) != 1 || apps[0].ID == stores[0].ID {
		return container{}, container{}, errors.New("cannot discover exactly one stopped legacy application and MinIO container; use compose stop, not down")
	}
	return apps[0], stores[0], nil
}
func databasePath(value string) (string, error) {
	parts := strings.Split(value, ";")
	path := ""
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			continue
		}
		k, v, ok := strings.Cut(p, "=")
		k = strings.ToLower(strings.TrimSpace(k))
		if !ok || k != "data source" && k != "datasource" && k != "filename" || path != "" {
			return "", errors.New("unsupported SQLite connection string; expected one absolute Data Source")
		}
		path = strings.Trim(strings.TrimSpace(v), "\"'")
	}
	if !filepath.IsAbs(path) || !safeValue(path) {
		return "", errors.New("source SQLite must use an absolute mounted path")
	}
	return filepath.Clean(path), nil
}
func nativeMount(m mount) error {
	if m.Type != "bind" && m.Type != "volume" {
		return errors.New("source requires bind mounts or plain local named volumes")
	}
	if !safeValue(m.Source) || !filepath.IsAbs(m.Source) || !safeValue(m.Destination) || m.Source == "/" {
		return errors.New("unsafe source native mount")
	}
	if m.Type == "volume" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`).MatchString(m.Name) {
		return errors.New("unsafe source named volume")
	}
	return nil
}
func storageDataMount(row container) (mount, error) {
	if len(row.Mounts) != 1 || row.Mounts[0].Destination != "/data" {
		return mount{}, errors.New("legacy MinIO must have exactly one /data mount; additional persistent configuration is unsupported")
	}
	m := row.Mounts[0]
	if e := nativeMount(m); e != nil {
		return mount{}, e
	}
	command := strings.Join(row.Config.Cmd, " ")
	if !strings.Contains(command, "server") || !strings.HasSuffix(command, " /data") {
		return mount{}, errors.New("unsupported MinIO server command")
	}
	return m, nil
}
func appDataMount(row container, db string) (mount, string, error) {
	var selected []mount
	for _, m := range row.Mounts {
		if e := nativeMount(m); e != nil {
			return mount{}, "", e
		}
		r, e := filepath.Rel(m.Destination, db)
		if e == nil && r != "." && r != ".." && !strings.HasPrefix(r, "../") {
			selected = append(selected, m)
		}
	}
	if len(selected) != 1 || len(row.Mounts) != 1 {
		return mount{}, "", errors.New("legacy application must have one unambiguous database directory mount")
	}
	m := selected[0]
	rel, _ := filepath.Rel(m.Destination, db)
	return m, rel, nil
}
func inspectionHash(app, storage container) string {
	app.State = container{}.State
	storage.State = container{}.State
	app.Mounts = append([]mount(nil), app.Mounts...)
	storage.Mounts = append([]mount(nil), storage.Mounts...)
	for i := range app.Mounts {
		app.Mounts[i].Canonical = ""
	}
	for i := range storage.Mounts {
		storage.Mounts[i].Canonical = ""
	}
	b, _ := json.Marshal([]container{app, storage})
	return digest(b)
}
func checkWriters(rows []container, db, storage mount, operations ...operation) error {
	for _, r := range rows {
		if !r.State.Running && !r.State.Paused && !r.State.Restarting {
			continue
		}
		for _, m := range r.Mounts {
			if m.Type != "bind" && m.Type != "volume" {
				continue
			}
			if len(operations) == 1 && knownTemporary(r, operations[0]) && !m.RW {
				continue
			}
			if m.Source != "" && (overlap(mountIdentity(m), mountIdentity(db)) || overlap(mountIdentity(m), mountIdentity(storage))) || m.Type == "volume" && (db.Type == "volume" && m.Name == db.Name || storage.Type == "volume" && m.Name == storage.Name) {
				return errors.New("a running, paused or restarting container holds protected source data")
			}
		}
	}
	return nil
}
func deploymentFiles(root string) ([]fileRecord, error) {
	var records []fileRecord
	// Only configuration known to the legacy Docker deployment is included. Native
	// data is copied independently, never recursively through the deployment root.
	for _, name := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml", ".env", "appsettings.json", "appsettings.Production.json"} {
		p := filepath.Join(root, name)
		if _, e := os.Lstat(p); os.IsNotExist(e) {
			continue
		}
		b, e := regular(p, 4<<20)
		if e != nil {
			return nil, e
		}
		i, _ := os.Lstat(p)
		records = append(records, fileRecord{Path: name, Size: int64(len(b)), SHA256: digest(b), Mode: uint32(i.Mode().Perm())})
	}
	if len(records) == 0 {
		return nil, errors.New("source deployment has no supported Compose/configuration files")
	}
	return records, nil
}
func discover(o options) error {
	if o.Project == "" {
		o.Project = "tag-files-" + digest([]byte(o.TargetDirectory))[:12]
	}
	if e := boundedDirectory(o.State); e != nil {
		return e
	}
	if e := os.Chmod(o.State, 0700); e != nil {
		return e
	}
	if !filepath.IsAbs(o.SourceDirectory) || !filepath.IsAbs(o.TargetDirectory) || !safeValue(o.SourceDirectory) || !safeValue(o.TargetDirectory) || overlap(o.SourceDirectory, o.TargetDirectory) {
		return errors.New("source and target must be separate absolute directories")
	}
	if !regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(o.Version) || !exactImage.MatchString(o.AppImage) || !exactImage.MatchString(o.StorageImage) || !exactImage.MatchString(o.MigrationImage) || !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`).MatchString(o.Project) || o.Port < 1 || o.Port > 65535 {
		return errors.New("explicit stable version, exact images, valid project and port required")
	}
	rows, e := readRows(o.Inspect)
	if e != nil {
		return e
	}
	app, storage, e := sourceRows(rows, filepath.Clean(o.SourceDirectory))
	if e != nil {
		return e
	}
	if !exactImage.MatchString(storage.Image) {
		return errors.New("cannot establish exact installed MinIO image")
	}
	env, e := environment(app)
	if e != nil {
		return e
	}
	db, e := databasePath(env["ConnectionStrings__SQLite"])
	if e != nil {
		return e
	}
	dm, rel, e := appDataMount(app, db)
	if e != nil {
		return e
	}
	sm, e := storageDataMount(storage)
	if e != nil {
		return e
	}
	if _, err := loadCanonical(o.Inspect); err == nil && (dm.Canonical == "" || sm.Canonical == "") {
		return errors.New("protected source physical mount identity unavailable")
	}
	targetKey, e := targetIdentity(o.Inspect, o.TargetDirectory)
	if e != nil {
		return e
	}
	databaseHost := filepath.Join(mountIdentity(dm), rel)
	if overlap(databaseHost, mountIdentity(sm)) || overlap(targetKey, mountIdentity(dm)) || overlap(targetKey, mountIdentity(sm)) || overlap(targetKey+".migration", mountIdentity(dm)) || overlap(targetKey+".migration", mountIdentity(sm)) {
		return errors.New("source database, storage, target and migration state overlap")
	}
	if e = correlateSource(app, storage, env, rows); e != nil {
		return e
	}
	se, e := environment(storage)
	if e != nil {
		return e
	}
	access := se["MINIO_ROOT_USER"]
	secret := se["MINIO_ROOT_PASSWORD"]
	if access == "" {
		access = se["MINIO_ACCESS_KEY"]
		secret = se["MINIO_SECRET_KEY"]
	}
	if !credential(access) || !credential(secret) {
		return errors.New("source MinIO must supply bounded root credentials in its inspected environment")
	}
	if env["S3__Username"] == "" || env["S3__Password"] == "" {
		return errors.New("source S3 credentials are not discoverable")
	}
	cfg, e := deploymentFiles(o.Deployment)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(cfg)
	state := operation{Format: 1, ID: randomID(), SourceDirectory: o.SourceDirectory, TargetDirectory: o.TargetDirectory, Version: o.Version, AppImage: o.AppImage, StorageImage: o.StorageImage, MigrationImage: o.MigrationImage, Project: o.Project, Port: o.Port, AppID: app.ID, StorageID: storage.ID, SourceStorageImage: storage.Image, DatabaseRelative: rel, DatabaseMount: rawMount(dm), StorageMount: rawMount(sm), InspectionHash: inspectionHash(app, storage), DeploymentHash: digest(b)}
	state.PhysicalValidation = dm.Canonical != "" && sm.Canonical != ""
	if relative, err := filepath.Rel(mountIdentity(dm), mountIdentity(sm)); err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, "../") {
		state.StorageRelative = relative
	}
	old, e := loadOperation(o.State)
	resumed := e == nil
	if e == nil {
		state.ID = old.ID
		state.InstallationHash = old.InstallationHash
		if state != old {
			return errors.New("migration source, configuration, images or target changed; preserve operation state and inspect before retrying")
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = checkWriters(rows, dm, sm, state); e != nil {
		return e
	}
	if e = checkTargetRows(rows, state, resumed, o.State, targetKey); e != nil {
		return e
	}
	if e = saveJSON(filepath.Join(o.State, "state.json"), state); e != nil {
		return e
	}
	if e = privateDir(o.State, "source-secrets"); e != nil {
		return e
	}
	for name, value := range map[string]string{"source-secrets/source_access": access + "\n", "source-secrets/source_secret": secret + "\n", "source-minio.env": "MINIO_ROOT_USER=" + access + "\nMINIO_ROOT_PASSWORD=" + secret + "\nMINIO_ACCESS_KEY=" + access + "\nMINIO_SECRET_KEY=" + secret + "\n"} {
		if e = atomicFile(filepath.Join(o.State, name), []byte(value), 0600); e != nil {
			return e
		}
	}
	kindSource := func(m mount) (string, string) {
		if m.Type == "volume" {
			return m.Type, m.Name
		}
		return m.Type, m.Source
	}
	dk, ds := kindSource(dm)
	sk, ss := kindSource(sm)
	protocol := fmt.Sprintf("DB_MOUNT_KIND=%s\nDB_MOUNT_SOURCE=%s\nSTORAGE_MOUNT_KIND=%s\nSTORAGE_MOUNT_SOURCE=%s\nSOURCE_DB_RELATIVE=%s\nSOURCE_STORAGE_IMAGE=%s\nSOURCE_APP_ID=%s\nSOURCE_STORAGE_ID=%s\nPROJECT=%s\nOPERATION_ID=%s\n", dk, ds, sk, ss, rel, storage.Image, app.ID, storage.ID, o.Project, state.ID)
	return atomicFile(filepath.Join(o.State, "host.env"), []byte(protocol), 0600)
}
func credential(value string) bool {
	return len(value) > 0 && len(value) <= 4095 && !strings.ContainsAny(value, "\x00\r\n")
}
func tree(root, exclude string) ([]fileRecord, error) {
	if e := boundedDirectory(root); e != nil {
		return nil, e
	}
	opened, e := os.OpenRoot(root)
	if e != nil {
		return nil, e
	}
	defer opened.Close()
	var result []fileRecord
	e = fs.WalkDir(opened.FS(), ".", func(rel string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if rel == "." {
			return nil
		}
		if exclude != "" && (rel == exclude || strings.HasPrefix(rel, exclude+"/")) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		i, e := opened.Lstat(rel)
		if e != nil {
			return e
		}
		if i.IsDir() {
			result = append(result, fileRecord{Path: rel, Mode: uint32(i.Mode().Perm()), Directory: true})
			return nil
		}
		if !i.Mode().IsRegular() {
			return errors.New("native data contains a symlink or nonregular entry")
		}
		f, e := opened.OpenFile(rel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		actual, e := f.Stat()
		if e != nil || !actual.Mode().IsRegular() {
			f.Close()
			return errors.New("native data file changed while opening")
		}
		h := sha256.New()
		n, e := io.CopyBuffer(h, f, make([]byte, 128<<10))
		e = errors.Join(e, f.Close())
		if e != nil {
			return e
		}
		if n != i.Size() {
			return errors.New("source changed while hashing")
		}
		result = append(result, fileRecord{Path: rel, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), Mode: uint32(i.Mode().Perm())})
		return nil
	})
	return result, e
}
func excludeStorage(s operation) string {
	if s.StorageRelative != "" {
		return s.StorageRelative
	}
	r, e := filepath.Rel(s.DatabaseMount.Source, s.StorageMount.Source)
	if e == nil && r != "." && r != ".." && !strings.HasPrefix(r, "../") {
		return r
	}
	return ""
}
func equalFiles(a, b []fileRecord) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func available(path string) (int64, error) {
	var s syscall.Statfs_t
	if e := syscall.Statfs(path, &s); e != nil {
		return 0, e
	}
	if s.Bavail > uint64(math.MaxInt64)/uint64(s.Bsize) {
		return math.MaxInt64, nil
	}
	return int64(s.Bavail) * s.Bsize, nil
}
func totalBytes(groups ...[]fileRecord) (int64, error) {
	var sum int64
	for _, g := range groups {
		for _, f := range g {
			if f.Size < 0 || sum > math.MaxInt64-f.Size {
				return 0, errors.New("disk estimate overflow")
			}
			sum += f.Size
		}
	}
	return sum, nil
}
func copyFiles(source, dest string, files []fileRecord) error {
	if e := os.MkdirAll(dest, 0700); e != nil {
		return e
	}
	if e := boundedDirectory(dest); e != nil {
		return e
	}
	input, e := os.OpenRoot(source)
	if e != nil {
		return e
	}
	defer input.Close()
	output, e := os.OpenRoot(dest)
	if e != nil {
		return e
	}
	defer output.Close()
	for _, r := range files {
		if r.Directory {
			if e = output.MkdirAll(r.Path, 0700); e != nil {
				return e
			}
			dir, err := output.Open(r.Path)
			if err != nil {
				return err
			}
			err = dir.Chmod(os.FileMode(r.Mode))
			if err == nil {
				err = dir.Sync()
			}
			err = errors.Join(err, dir.Close())
			if err != nil {
				return err
			}
			continue
		}
		if e = output.MkdirAll(filepath.Dir(r.Path), 0700); e != nil {
			return e
		}
		in, e := input.OpenFile(r.Path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if e != nil {
			return e
		}
		i, e := in.Stat()
		if e != nil || !i.Mode().IsRegular() {
			in.Close()
			return errors.New("source changed while opening backup file")
		}
		out, e := output.OpenFile(r.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			in.Close()
			return e
		}
		h := sha256.New()
		n, e := io.CopyBuffer(io.MultiWriter(out, h), io.LimitReader(in, r.Size+1), make([]byte, 128<<10))
		if e == nil && (n != r.Size || hex.EncodeToString(h.Sum(nil)) != r.SHA256) {
			e = errors.New("source changed while backing up")
		}
		if e == nil {
			e = out.Chmod(os.FileMode(r.Mode))
		}
		if e == nil {
			e = out.Sync()
		}
		e = errors.Join(e, in.Close(), out.Close())
		if e != nil {
			return e
		}
	}
	observed, e := tree(dest, "")
	if e != nil {
		return e
	}
	if !equalFiles(files, observed) {
		return errors.New("rollback copy full-byte verification failed")
	}
	return syncDirectory(dest)
}
func readBackup(root string) (backupManifest, error) {
	var m backupManifest
	b, e := regular(filepath.Join(root, "manifest.json"), 128<<20)
	if e != nil {
		return m, e
	}
	marker, e := regular(filepath.Join(root, "complete"), 128)
	if e != nil {
		return m, e
	}
	if strings.TrimSpace(string(marker)) != digest(b) {
		return m, errors.New("rollback manifest checksum mismatch")
	}
	e = json.Unmarshal(b, &m)
	if e == nil && m.Format != 1 {
		e = errors.New("unsupported rollback format")
	}
	return m, e
}
func verifyBackup(root string, m backupManifest) error {
	for _, r := range m.Evidence {
		b, e := regular(filepath.Join(root, r.Path), 128<<20)
		if e != nil {
			return e
		}
		if int64(len(b)) != r.Size || digest(b) != r.SHA256 {
			return errors.New("rollback effective configuration metadata changed")
		}
	}
	if len(m.Evidence) != 2 {
		return errors.New("rollback effective configuration evidence is incomplete")
	}

	for _, entry := range []struct {
		name  string
		files []fileRecord
	}{{"source", m.Source}, {"storage", m.Storage}, {"deployment", m.Deployment}} {
		actual, e := tree(filepath.Join(root, entry.name), "")
		if e != nil {
			return e
		}
		if !equalFiles(actual, entry.files) {
			return fmt.Errorf("rollback %s bytes changed", entry.name)
		}
	}
	return nil
}
func sourceSnapshot(o options, s operation) (backupManifest, error) {
	m := backupManifest{Format: 1, OperationID: s.ID}
	var e error
	m.Source, e = tree(o.Source, excludeStorage(s))
	if e != nil {
		return m, e
	}
	found := false
	for _, f := range m.Source {
		if f.Path == s.DatabaseRelative {
			found = true
		}
	}
	if !found {
		return m, errors.New("discovered source SQLite file is missing")
	}
	m.Storage, e = tree(o.SourceStorage, "")
	if e != nil {
		return m, e
	}
	m.Deployment, e = deploymentFiles(o.Deployment)
	if e == nil {
		b, _ := json.Marshal(m.Deployment)
		if digest(b) != s.DeploymentHash {
			return m, errors.New("source deployment configuration changed since discovery")
		}
	}
	return m, e
}
func sameSnapshot(a, b backupManifest) bool {
	return a.OperationID == b.OperationID && equalFiles(a.Source, b.Source) && equalFiles(a.Storage, b.Storage) && equalFiles(a.Deployment, b.Deployment)
}
func prepare(o options) error {
	s, e := loadOperation(o.State)
	if e != nil {
		return e
	}
	m, e := sourceSnapshot(o, s)
	if e != nil {
		return e
	}
	rollback := filepath.Join(o.State, "rollback")
	saved, e := readBackup(rollback)
	if e == nil {
		if !sameSnapshot(saved, m) {
			return errors.New("source changed since the verified cold rollback backup")
		}
		if e = verifyBackup(rollback, saved); e != nil {
			return e
		}
	} else if !os.IsNotExist(e) {
		return e
	} else {
		if _, e = os.Lstat(rollback); !os.IsNotExist(e) {
			return errors.New("existing unverified rollback directory requires inspection")
		}
		// Budget cold rollback, separate mutable MinIO copy, logical archive/target
		// estimate, automatic 10 GB archive reserve and working allowance. Native
		// compression can hide logical bytes; automatic inventory checks again.
		// Complete source/archive/target charges are aggregated on their shared disk.
		native, e := totalBytes(m.Source, m.Storage, m.Deployment)
		if e != nil {
			return e
		}
		if native > (math.MaxInt64-10_000_000_000-(1<<30))/5 {
			return errors.New("disk estimate overflow")
		}
		need := native*5 + 10_000_000_000 + (1 << 30)
		capacity := o.AvailableBytes
		if capacity == nil {
			capacity = available
		}
		free, e := capacity(o.State)
		if e != nil {
			return e
		}
		if free < need {
			return fmt.Errorf("insufficient disk space before rollback: need at least %d bytes, available %d", need, free)
		}
		stage, e := os.MkdirTemp(o.State, "rollback-incomplete-")
		if e != nil {
			return e
		}
		for _, entry := range []struct {
			source, name string
			files        []fileRecord
		}{{o.Source, "source", m.Source}, {o.SourceStorage, "storage", m.Storage}, {o.Deployment, "deployment", m.Deployment}} {
			if e = copyFiles(entry.source, filepath.Join(stage, entry.name), entry.files); e != nil {
				return fmt.Errorf("rollback attempt retained at %s: %w", stage, e)
			}
		}
		// Include effective environment and mount/image metadata in the private
		// rollback material; source Compose files alone may omit those values.
		inspected, e := rawSourceContainers(filepath.Join(o.State, "docker-inspect.json"), s.SourceDirectory)
		if e != nil {
			return e
		}
		if e = atomicFile(filepath.Join(stage, "containers.json"), inspected, 0600); e != nil {
			return e
		}
		if e = saveJSON(filepath.Join(stage, "operation.json"), s); e != nil {
			return e
		}
		for _, name := range []string{"containers.json", "operation.json"} {
			data, err := regular(filepath.Join(stage, name), 128<<20)
			if err != nil {
				return err
			}
			m.Evidence = append(m.Evidence, fileRecord{Path: name, Size: int64(len(data)), SHA256: digest(data), Mode: 0600})
		}
		if e = inheritTreeOwner(stage, o.State); e != nil {
			return e
		}
		if e = saveJSON(filepath.Join(stage, "manifest.json"), m); e != nil {
			return e
		}
		b, e := regular(filepath.Join(stage, "manifest.json"), 128<<20)
		if e != nil {
			return e
		}
		if e = atomicFile(filepath.Join(stage, "complete"), []byte(digest(b)+"\n"), 0600); e != nil {
			return e
		}
		after, e := sourceSnapshot(o, s)
		if e != nil {
			return e
		}
		if !sameSnapshot(m, after) {
			return errors.New("source changed during cold rollback backup")
		}
		if e = os.Rename(stage, rollback); e != nil {
			return e
		}
		if e = syncDirectory(o.State); e != nil {
			return e
		}
	}
	clone := filepath.Join(o.State, "source-minio")
	if _, e = os.Lstat(clone); e == nil {
		return boundedDirectory(clone)
	} else if !os.IsNotExist(e) {
		return e
	}
	stage, e := os.MkdirTemp(o.State, "source-minio-incomplete-")
	if e != nil {
		return e
	}
	if e = copyFiles(filepath.Join(rollback, "storage"), stage, m.Storage); e != nil {
		return e
	}
	if e = inheritTreeOwner(stage, o.State); e != nil {
		return e
	}
	if e = os.Rename(stage, clone); e != nil {
		return e
	}
	return syncDirectory(o.State)
}
func verifySource(o options) error {
	s, e := loadOperation(o.State)
	if e != nil {
		return e
	}
	rows, e := readRows(o.Inspect)
	if e != nil {
		return e
	}
	app, storage, e := sourceRows(rows, s.SourceDirectory)
	if e != nil {
		return e
	}
	targetKey, e := targetIdentity(o.Inspect, s.TargetDirectory)
	if e != nil {
		return e
	}
	if s.PhysicalValidation && (app.Mounts[0].Canonical == "" || storage.Mounts[0].Canonical == "") {
		return errors.New("physical source mount mapping missing")
	}
	if e = checkTargetRows(rows, s, true, o.State, targetKey); e != nil {
		return e
	}
	if inspectionHash(app, storage) != s.InspectionHash {
		return errors.New("source containers or effective configuration changed")
	}
	if e = checkWriters(rows, app.Mounts[0], storage.Mounts[0], s); e != nil {
		return e
	}
	m, e := readBackup(filepath.Join(o.State, "rollback"))
	if e != nil {
		return e
	}
	current, e := sourceSnapshot(o, s)
	if e != nil {
		return e
	}
	if !sameSnapshot(m, current) {
		return errors.New("source bytes changed since cold rollback")
	}
	return verifyBackup(filepath.Join(o.State, "rollback"), m)
}
func installationHash(target string) (string, error) {
	var hashes []string
	for _, name := range []string{".env", "compose.yml", "installation.json", "secrets/s3_access_key", "secrets/s3_secret_key"} {
		b, e := regular(filepath.Join(target, name), 4<<20)
		if e != nil {
			return "", e
		}
		hashes = append(hashes, name+":"+digest(b))
	}
	return digest([]byte(strings.Join(hashes, "\n"))), nil
}
func configureTarget(o options) error {
	s, e := loadOperation(o.State)
	if e != nil {
		return e
	}
	h, e := installationHash(o.Target)
	if e != nil {
		return e
	}
	if e = validateInstallation(o.Target, s); e != nil {
		return e
	}
	if s.InstallationHash == "" {
		for _, name := range []string{"data/metadata", "data/media"} {
			entries, err := os.ReadDir(filepath.Join(o.Target, name))
			if err != nil {
				return err
			}
			if len(entries) != 0 {
				return errors.New("initial target library directories must be empty")
			}
		}
	}
	if s.InstallationHash != "" && s.InstallationHash != h {
		return errors.New("target deployment configuration or credentials changed")
	}
	s.InstallationHash = h
	if e = saveJSON(filepath.Join(o.State, "state.json"), s); e != nil {
		return e
	}
	if e = privateDir(o.State, "target-secrets"); e != nil {
		return e
	}
	for _, name := range []string{"s3_access_key", "s3_secret_key"} {
		b, e := regular(filepath.Join(o.Target, "secrets", name), 4096)
		if e != nil {
			return e
		}
		if !credential(strings.TrimSuffix(string(b), "\n")) {
			return errors.New("invalid target credential")
		}
		if e = atomicFile(filepath.Join(o.State, "target-secrets", name), b, 0600); e != nil {
			return e
		}
	}
	config := filepath.Join(o.State, "config.json")
	if _, e = os.Lstat(config); e == nil {
		_, e = regular(config, 1<<20)
		return e
	} else if !os.IsNotExist(e) {
		return e
	}
	return saveJSON(config, map[string]any{"FFprobe": "ffprobe", "Source": map[string]any{"Endpoint": "http://frozen-minio:9000", "Region": "us-east-1", "Buckets": []string{}, "AccessKeyFile": "/source-secrets/source_access", "SecretKeyFile": "/source-secrets/source_secret"}, "Database": filepath.Join("/source", s.DatabaseRelative), "Archive": "/work/archive/run-001", "SnapshotID": s.ID, "RollbackReference": "verified cold rollback at " + s.TargetDirectory + ".migration/rollback", "RollbackVerified": true, "Target": map[string]any{"Endpoint": "http://storage:8333", "Region": "us-east-1", "AccessKeyFile": "/target-secrets/s3_access_key", "SecretKeyFile": "/target-secrets/s3_secret_key"}, "TargetMetadata": "/target", "Journal": "/work/journal/migration.sqlite", "Budget": map[string]int64{}, "Exceptions": []any{}})
}
func finalize(o options) error {
	s, e := loadOperation(o.State)
	if e != nil {
		return e
	}
	h, e := installationHash(o.Target)
	if e != nil {
		return e
	}
	if s.InstallationHash != h {
		return errors.New("target deployment changed before finalization")
	}
	b, e := regular(filepath.Join(o.State, "journal", "automatic-result.json"), 128<<20)
	if e != nil {
		return e
	}
	var result struct{ Complete bool }
	if e = json.Unmarshal(b, &result); e != nil || !result.Complete {
		return errors.New("complete independent migration verification is required before startup")
	}
	uid, gid, e := owner(o.Target)
	if e != nil {
		return e
	}
	for _, name := range []string{"data/metadata", "data/media", "scratch"} {
		root := filepath.Join(o.Target, name)
		if e = boundedDirectory(root); e != nil {
			return e
		}
		e = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("unexpected symlink in target data")
			}
			return os.Chown(path, uid, gid)
		})
		if e != nil {
			return e
		}
	}
	return saveJSON(filepath.Join(o.State, "verified.json"), map[string]any{"format": 1, "operationID": s.ID, "complete": true, "reportSHA256": digest(b), "target": s.TargetDirectory})
}
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("expected discover, prepare, verify-source, configure-target or finalize")
	}
	fs := flag.NewFlagSet("tag-files-migration-host", flag.ContinueOnError)
	var o options
	fs.StringVar(&o.State, "state", "/work", "private migration state")
	fs.StringVar(&o.Inspect, "inspect", "/work/docker-inspect.json", "private Docker inspection JSON")
	fs.StringVar(&o.SourceDirectory, "source-directory", "", "original host deployment directory")
	fs.StringVar(&o.TargetDirectory, "target-directory", "", "new host deployment directory")
	fs.StringVar(&o.Version, "version", "", "stable version")
	fs.StringVar(&o.AppImage, "app-image", "", "exact app image")
	fs.StringVar(&o.StorageImage, "storage-image", "", "exact storage image")
	fs.StringVar(&o.MigrationImage, "migration-image", "", "exact migration image")
	fs.StringVar(&o.Project, "project", "tag-files", "new Compose project")
	fs.IntVar(&o.Port, "port", 8080, "loopback app port")
	fs.StringVar(&o.Deployment, "deployment", "/deployment", "source configuration mount")
	fs.StringVar(&o.Source, "source", "/source", "source database mount")
	fs.StringVar(&o.SourceStorage, "source-storage", "/source-storage", "source native MinIO mount")
	fs.StringVar(&o.Target, "target", "/install", "target deployment mount")
	fs.StringVar(&o.Parent, "parent", "/parent", "target and state parent mount")
	fs.StringVar(&o.Stage, "stage", "", "owned setup stage name")
	fs.StringVar(&o.Plan, "plan", "/plan.json", "non-sensitive physical inspection plan")
	if e := fs.Parse(args[1:]); e != nil {
		return e
	}
	if fs.NArg() != 0 {
		return errors.New("unexpected helper arguments")
	}
	switch args[0] {
	case "discover":
		return discover(o)
	case "prepare":
		return prepare(o)
	case "verify-source":
		return verifySource(o)
	case "configure-target":
		return configureTarget(o)
	case "finalize":
		return finalize(o)
	case "completed":
		return completed(o)
	case "mount-sources":
		return mountSources(o)
	case "canonical-mounts":
		return canonicalMounts(o)
	case "setup-stage":
		return setupStage(o)
	case "publish-target":
		return publishTarget(o)
	case "wait-source":
		return waitSource("http://frozen-minio:9000/minio/health/ready", 90*time.Second)
	default:
		return errors.New("unknown host migration command")
	}
}
func main() {
	if e := run(os.Args[1:]); e != nil {
		fmt.Fprintln(os.Stderr, "Tag Files migration:", e)
		os.Exit(1)
	}
}

func checkTargetRows(rows []container, s operation, resumed bool, stateRoot string, targetKeys ...string) error {
	targetKey := s.TargetDirectory
	if len(targetKeys) > 0 {
		targetKey = targetKeys[0]
	}
	for _, r := range rows {
		if !r.State.Running && !r.State.Paused && !r.State.Restarting {
			continue
		}
		if knownTemporary(r, s) {
			continue
		}
		expected := r.Config.Labels["com.docker.compose.project"] == s.Project && (r.Config.Labels["com.docker.compose.service"] == "app" || r.Config.Labels["com.docker.compose.service"] == "storage")
		for _, m := range r.Mounts {
			if (m.Type == "bind" || m.Type == "volume") && m.Source != "" && (overlap(mountIdentity(m), targetKey) || overlap(mountIdentity(m), targetKey+".migration")) {
				if !expected {
					return errors.New("an unrelated active container holds target or migration state")
				}
			}
		}
	}

	_, doneErr := os.Lstat(filepath.Join(stateRoot, "verified.json"))
	for _, r := range rows {
		if r.Config.Labels["com.docker.compose.project"] != s.Project {
			continue
		}
		if !resumed {
			return errors.New("target Compose project already has containers; choose a fresh project")
		}
		service := r.Config.Labels["com.docker.compose.service"]
		expectedImage := s.AppImage
		if service == "storage" {
			expectedImage = s.StorageImage
		}
		if r.Image != expectedImage {
			return errors.New("target container image differs from migration installation")
		}
		role := ""
		switch service {
		case "app":
			role = "metadata"
		case "storage":
			role = "media"
		default:
			return errors.New("target project contains an unrelated service")
		}
		found := false
		for _, m := range r.Mounts {
			switch m.Destination {
			case "/data":
			case "/scratch":
				if service != "app" || m.Type != "bind" || m.Source != filepath.Join(s.TargetDirectory, "scratch") {
					return errors.New("target scratch mount changed")
				}
			case "/run/secrets/s3_access_key", "/run/secrets/s3_secret_key":
				if m.Type != "bind" || m.RW || m.Source != filepath.Join(s.TargetDirectory, "secrets", filepath.Base(m.Destination)) {
					return errors.New("target secret mount changed")
				}
			case "/tmp":
				if service != "storage" || m.Type != "tmpfs" {
					return errors.New("target temporary mount changed")
				}
			default:
				return errors.New("unexpected persistent target mount")
			}
			if m.Destination == "/data" {
				if m.Type != "bind" || filepath.Clean(m.Source) != filepath.Join(s.TargetDirectory, "data", role) {
					return errors.New("target project container uses unrelated library data")
				}
				found = true
			}
		}
		if !found {
			return errors.New("target project has no matching data mount")
		}
		if service == "app" && (r.State.Running || r.State.Paused || r.State.Restarting) && doneErr != nil {
			return errors.New("target application must remain stopped until independent verification completes")
		}
	}
	return nil
}
func validateInstallation(target string, s operation) error {
	if _, e := os.Lstat(filepath.Join(target, ".setup-incomplete")); !os.IsNotExist(e) {
		return errors.New("target setup is incomplete")
	}
	b, e := regular(filepath.Join(target, "installation.json"), 4<<20)
	if e != nil {
		return e
	}
	var receipt struct {
		Format                  int
		State, Version, Project string
		Port                    int
		Images                  map[string]struct {
			ID string `json:"Id"`
		}
	}
	if e = json.Unmarshal(b, &receipt); e != nil {
		return e
	}
	if receipt.Format != 1 || receipt.State != "prepared" || receipt.Version != s.Version || receipt.Project != s.Project || receipt.Port != s.Port || receipt.Images["app"].ID != s.AppImage || receipt.Images["storage"].ID != s.StorageImage {
		return errors.New("target installation does not belong to this migration")
	}
	return nil
}
func completed(o options) error {
	s, e := loadOperation(o.State)
	if e != nil {
		return e
	}
	h, e := installationHash(o.Target)
	if e != nil {
		return e
	}
	if h != s.InstallationHash || h == "" {
		return errors.New("completed target deployment changed")
	}
	b, e := regular(filepath.Join(o.State, "verified.json"), 4<<20)
	if e != nil {
		return e
	}
	var receipt struct {
		Format               int
		OperationID          string
		Complete             bool
		ReportSHA256, Target string
	}
	if e = json.Unmarshal(b, &receipt); e != nil {
		return e
	}
	if receipt.Format != 1 || !receipt.Complete || receipt.OperationID != s.ID || receipt.Target != s.TargetDirectory {
		return errors.New("invalid completed migration receipt")
	}
	report, e := regular(filepath.Join(o.State, "journal", "automatic-result.json"), 128<<20)
	if e != nil {
		return e
	}
	if receipt.ReportSHA256 != digest(report) {
		return errors.New("completed verification report changed")
	}
	return nil
}
func waitSource(endpoint string, timeout time.Duration) error {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := http.Client{Timeout: 2 * time.Second, Transport: transport}
	defer transport.CloseIdleConnections()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r, e := client.Get(endpoint)
		if e == nil {
			r.Body.Close()
			if r.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("temporary source MinIO did not become ready")
}

func inheritTreeOwner(root, parent string) error {
	uid, gid, e := owner(parent)
	if e != nil {
		return e
	}
	r, e := os.OpenRoot(root)
	if e != nil {
		return e
	}
	defer r.Close()
	return fs.WalkDir(r.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("unexpected symlink in migration state")
		}
		return r.Lchown(path, uid, gid)
	})
}
func setupStage(o options) error {
	if _, e := loadOperation(o.State); e != nil {
		return e
	}
	name := "setup-attempt-" + randomID()
	if e := privateDir(o.State, name); e != nil {
		return e
	}
	return atomicFile(filepath.Join(o.State, "setup-stage.env"), []byte("SETUP_STAGE="+name+"\n"), 0600)
}
func publishTarget(o options) error {
	s, e := loadOperation(o.State)
	if e != nil {
		return e
	}
	if !regexp.MustCompile(`^setup-attempt-[0-9a-f]{32}$`).MatchString(o.Stage) {
		return errors.New("invalid target setup stage")
	}
	stagePath := filepath.Join(o.State, o.Stage)
	if e = validateInstallation(stagePath, s); e != nil {
		return e
	}
	if e = boundedDirectory(o.Parent); e != nil {
		return e
	}
	parent, e := os.OpenRoot(o.Parent)
	if e != nil {
		return e
	}
	defer parent.Close()
	leaf := filepath.Base(s.TargetDirectory)
	stateLeaf := leaf + ".migration"
	actual, e := parent.Stat(stateLeaf)
	if e != nil {
		return e
	}
	mounted, e := os.Stat(o.State)
	if e != nil || !os.SameFile(actual, mounted) {
		return errors.New("target publication parent does not contain this operation state")
	}
	// Remove only an empty directory, never a file or symlink. Root.Rename
	// refuses any directory created concurrently after this removal.
	if info, err := parent.Lstat(leaf); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("target publication refuses files and symlinks")
		}
		if err = syscall.Rmdir(filepath.Join(o.Parent, leaf)); err != nil {
			return fmt.Errorf("target must remain empty before publication: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if e = parent.Rename(filepath.Join(stateLeaf, o.Stage), leaf); e != nil {
		return fmt.Errorf("target publication refused; setup stage retained: %w", e)
	}
	return syncDirectory(o.Parent)
}
func knownTemporary(r container, s operation) bool {
	if r.Config.Labels["tag-files.migration.operation"] != s.ID {
		return false
	}
	name := strings.TrimPrefix(r.Name, "/")
	return name == s.Project+"-migrate-run" && r.Image == s.MigrationImage || name == s.Project+"-source-minio" && r.Image == s.SourceStorageImage
}

func correlateSource(app, storage container, env map[string]string, rows []container) error {
	project := app.Config.Labels["com.docker.compose.project"]
	if project == "" || project != storage.Config.Labels["com.docker.compose.project"] {
		return errors.New("source application and MinIO must belong to the same Compose project")
	}
	endpoint := env["S3__Url"]
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" || u.Port() != "9000" {
		return errors.New("unsupported source S3 endpoint; expected selected MinIO on plain HTTP port 9000")
	}
	host := strings.ToLower(u.Hostname())
	service := storage.Config.Labels["com.docker.compose.service"]
	name := strings.TrimPrefix(storage.Name, "/")
	matches := host != "" && (host == strings.ToLower(service) || host == strings.ToLower(name))
	shared := map[string]bool{}
	for network, attachment := range storage.NetworkSettings.Networks {
		if _, ok := app.NetworkSettings.Networks[network]; !ok {
			continue
		}
		shared[network] = true
		for _, alias := range attachment.Aliases {
			if strings.EqualFold(host, alias) {
				matches = true
			}
		}
	}
	if len(app.NetworkSettings.Networks) > 0 || len(storage.NetworkSettings.Networks) > 0 {
		if len(shared) == 0 {
			return errors.New("source application and selected MinIO do not share a Docker network")
		}
	}
	if !matches {
		return errors.New("source S3 endpoint does not identify the selected MinIO container")
	}
	for _, other := range rows {
		if other.ID == storage.ID {
			continue
		}
		for network, attachment := range other.NetworkSettings.Networks {
			if !shared[network] {
				continue
			}
			for _, alias := range attachment.Aliases {
				if strings.EqualFold(host, alias) {
					return errors.New("source S3 hostname is ambiguous on the application network")
				}
			}
		}
	}
	return nil
}
