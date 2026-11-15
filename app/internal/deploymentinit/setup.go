// Package deploymentinit prepares a fresh deployment without contacting Docker,
// storage, or the application database.
package deploymentinit

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"syscall"
	"time"
)

const composeFile = "/usr/share/tag-files/compose.yml"
const incomplete = ".setup-incomplete"

// Run accepts the arguments following storage-run setup. The caller must create
// an empty bind-mounted directory; its owner also owns all generated files.
func Run(args []string) error {
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	directory := flags.String("directory", "", "empty deployment directory")
	version := flags.String("version", "", "stable release version")
	app := flags.String("app-image", "", "exact application image ID")
	storage := flags.String("storage-image", "", "exact storage image ID")
	project := flags.String("project", "tag-files", "Compose project name")
	port := flags.Int("port", 8080, "loopback application port")
	templatePath := flags.String("compose-file", composeFile, "bundled Compose template")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid setup arguments")
	}
	if *directory == "" || !regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(*version) {
		return errors.New("setup requires --directory and an explicit stable major.minor.patch --version")
	}
	imageID := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	if !imageID.MatchString(*app) || !imageID.MatchString(*storage) {
		return errors.New("setup requires exact sha256 --app-image and --storage-image IDs")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`).MatchString(*project) || *port < 1 || *port > 65535 {
		return errors.New("invalid setup project name or loopback port")
	}
	template, err := readTemplate(*templatePath)
	if err != nil {
		return err
	}
	path := filepath.Clean(*directory)
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("setup requires an existing empty directory, never a symlink")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	opened, err := dir.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("setup directory changed while opening it")
	}
	names, err := dir.Readdirnames(-1)
	if err != nil {
		return err
	}
	if len(names) != 0 {
		return errors.New("setup requires a new empty directory; inspect any existing or incomplete installation before retrying")
	}
	owner := opened.Sys().(*syscall.Stat_t)
	uid, gid := int(owner.Uid), int(owner.Gid)
	write := func(name string, data []byte, mode os.FileMode) error {
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := file.Chown(uid, gid); err != nil {
			return err
		}
		if _, err := file.Write(data); err != nil {
			return err
		}
		if err := file.Chmod(mode); err != nil {
			return err
		}
		return file.Sync()
	}
	// Exclusively claim this empty directory before creating any credentials.
	if err := write(incomplete, []byte("Setup did not complete. Preserve this directory for inspection; do not reinitialize it.\n"), 0600); err != nil {
		return fmt.Errorf("cannot claim setup directory; inspect it before retrying: %w", err)
	}
	if err := prepare(root, dir, write, uid, gid, *version, *project, *port, *app, *storage, template); err != nil {
		return fmt.Errorf("setup incomplete; preserve the directory for inspection: %w", err)
	}
	return nil
}

func prepare(root *os.Root, dir *os.File, write func(string, []byte, os.FileMode) error, uid, gid int, version, project string, port int, app, storage string, template []byte) error {
	// A competing initializer or unexpected writer must never turn a nonempty
	// directory into an installation. The exclusive marker also fences retries.
	if _, err := dir.Seek(0, 0); err != nil {
		return err
	}
	names, err := dir.Readdirnames(-1)
	if err != nil || len(names) != 1 || names[0] != incomplete {
		return errors.New("setup directory is no longer empty")
	}
	if err := dir.Chmod(0700); err != nil {
		return err
	}
	var privateDirs []*os.File
	for _, name := range []string{"data", "data/metadata", "data/media", "scratch", "secrets"} {
		if err := root.Mkdir(name, 0700); err != nil {
			return err
		}
		created, err := root.Open(name)
		if err != nil {
			return err
		}
		defer created.Close()
		if err := created.Chown(uid, gid); err != nil {
			return err
		}
		if err := created.Chmod(0700); err != nil {
			return err
		}
		privateDirs = append(privateDirs, created)
	}
	for name, size := range map[string]int{"s3_access_key": 16, "s3_secret_key": 32} {
		key := make([]byte, size)
		if _, err := rand.Read(key); err != nil {
			return errors.New("cannot generate setup credentials")
		}
		if err := write("secrets/"+name, []byte(hex.EncodeToString(key)+"\n"), 0444); err != nil {
			return err
		}
	}
	for _, created := range privateDirs {
		if err := created.Sync(); err != nil {
			return err
		}
	}
	env := []byte(fmt.Sprintf("COMPOSE_PROJECT_NAME=%s\nTAG_FILES_APP_IMAGE=%s\nTAG_FILES_STORAGE_IMAGE=%s\nTAG_FILES_PORT=%d\nTAG_FILES_MIN_FREE_BYTES=1073741824\nTAG_FILES_UID=%d\nTAG_FILES_GID=%d\n", project, app, storage, port, uid, gid))
	files := map[string]string{}
	for name, data := range map[string][]byte{".env": env, "compose.yml": template} {
		if err := write(name, data, 0600); err != nil {
			return err
		}
		hash := sha256.Sum256(data)
		files[name] = hex.EncodeToString(hash[:])
	}
	images := map[string]any{}
	for role, id := range map[string]string{"app": app, "storage": storage} {
		images[role] = map[string]any{"requested": id, "Id": id, "RepoDigests": []string{}, "Architecture": runtime.GOARCH, "Os": "linux"}
	}
	receipt := map[string]any{"format": 1, "state": "prepared", "version": version, "project": project, "port": port,
		"created": time.Now().UTC().Format(time.RFC3339Nano), "images": images, "files": files}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	if err := write("installation.json", append(data, '\n'), 0600); err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	if err := root.Remove(incomplete); err != nil {
		return err
	}
	return dir.Sync()
}

func readTemplate(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("setup Compose template is unavailable")
	}
	defer file.Close()
	info, err := file.Stat()
	const limit = 4 * 1024 * 1024
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("setup Compose template must be a bounded regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || len(data) > limit {
		return nil, errors.New("cannot read setup Compose template")
	}
	return data, nil
}
