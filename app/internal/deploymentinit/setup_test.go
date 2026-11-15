package deploymentinit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

const appID = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const storageID = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func setupArgs(t *testing.T, directory string) []string {
	t.Helper()
	compose := filepath.Join(t.TempDir(), "compose.yml")
	if err := os.WriteFile(compose, []byte("services:\n  app:\n    image: ${TAG_FILES_APP_IMAGE}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"--directory", directory, "--version", "v0.1.3", "--app-image", appID,
		"--storage-image", storageID, "--project", "my-library", "--port", "8091", "--compose-file", compose}
}

func snapshot(t *testing.T, directory string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			name, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			files[name] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestSetupPreparesPrivatePinnedDeployment(t *testing.T) {
	directory := t.TempDir()
	args := setupArgs(t, directory)
	owner, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(args); err != nil {
		t.Fatal(err)
	}
	files := snapshot(t, directory)
	if len(files) != 5 {
		t.Fatalf("deployment contains %d files, want 5", len(files))
	}
	wantEnv := fmt.Sprintf("COMPOSE_PROJECT_NAME=my-library\nTAG_FILES_APP_IMAGE=%s\nTAG_FILES_STORAGE_IMAGE=%s\nTAG_FILES_PORT=8091\nTAG_FILES_MIN_FREE_BYTES=1073741824\n", appID, storageID)
	if files[".env"] != wantEnv {
		t.Fatalf("unexpected environment: %q", files[".env"])
	}
	template, err := os.ReadFile(args[len(args)-1])
	if err != nil || files["compose.yml"] != string(template) {
		t.Fatalf("compose template differs: %v", err)
	}
	for name, length := range map[string]int{"secrets/s3_access_key": 32, "secrets/s3_secret_key": 64} {
		if !regexp.MustCompile(fmt.Sprintf(`^[0-9a-f]{%d}\n$`, length)).MatchString(files[name]) {
			t.Fatalf("%s is not a generated hexadecimal credential", name)
		}
	}
	for name, mode := range map[string]os.FileMode{".": 0700, "secrets": 0700, ".env": 0600,
		"compose.yml": 0600, "installation.json": 0600, "secrets/s3_access_key": 0444, "secrets/s3_secret_key": 0444} {
		info, err := os.Stat(filepath.Join(directory, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Errorf("%s mode = %o, want %o", name, info.Mode().Perm(), mode)
		}
		got, want := info.Sys().(*syscall.Stat_t), owner.Sys().(*syscall.Stat_t)
		if got.Uid != want.Uid || got.Gid != want.Gid {
			t.Errorf("%s owner = %d:%d, want %d:%d", name, got.Uid, got.Gid, want.Uid, want.Gid)
		}
	}
	var receipt struct {
		Format  int
		State   string
		Version string
		Project string
		Port    int
		Created string
		Images  map[string]map[string]any
		Files   map[string]string
	}
	if err := json.Unmarshal([]byte(files["installation.json"]), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Format != 1 || receipt.State != "prepared" || receipt.Version != "v0.1.3" || receipt.Project != "my-library" || receipt.Port != 8091 {
		t.Fatalf("incorrect installation receipt: %+v", receipt)
	}
	if _, err := time.Parse(time.RFC3339Nano, receipt.Created); err != nil {
		t.Fatalf("invalid creation time: %v", err)
	}
	for role, id := range map[string]string{"app": appID, "storage": storageID} {
		want := map[string]any{"requested": id, "Id": id, "RepoDigests": []any{}, "Architecture": runtime.GOARCH, "Os": "linux"}
		if !reflect.DeepEqual(receipt.Images[role], want) {
			t.Errorf("%s image receipt = %#v, want %#v", role, receipt.Images[role], want)
		}
	}
	for _, name := range []string{".env", "compose.yml"} {
		sum := sha256.Sum256([]byte(files[name]))
		if receipt.Files[name] != hex.EncodeToString(sum[:]) {
			t.Errorf("%s receipt hash differs", name)
		}
	}
	other := t.TempDir()
	if err := Run(setupArgs(t, other)); err != nil {
		t.Fatal(err)
	}
	otherFiles := snapshot(t, other)
	for _, name := range []string{"secrets/s3_access_key", "secrets/s3_secret_key"} {
		if otherFiles[name] == files[name] {
			t.Errorf("independent installations share %s", name)
		}
	}
	if err := Run(args); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("rerun error = %v, want empty-directory refusal", err)
	}
	if after := snapshot(t, directory); !reflect.DeepEqual(files, after) {
		t.Fatal("rerun changed existing deployment files")
	}
}

func TestSetupValidatesArgumentsBeforeWriting(t *testing.T) {
	for _, flag := range []string{"--directory", "--version", "--app-image", "--storage-image"} {
		t.Run("missing "+flag, func(t *testing.T) {
			directory := t.TempDir()
			args := setupArgs(t, directory)
			for index := 0; index < len(args); index += 2 {
				if args[index] == flag {
					args = append(args[:index], args[index+2:]...)
					break
				}
			}
			if err := Run(args); err == nil {
				t.Fatal("missing required argument accepted")
			}
			if len(snapshot(t, directory)) != 0 {
				t.Fatal("validation failure wrote deployment files")
			}
		})
	}
	for _, invalid := range []struct{ flag, value string }{
		{"--version", "latest"}, {"--version", "v01.2.3"}, {"--version", "1.2.3-rc1"},
		{"--app-image", "app:v0.1.3"}, {"--storage-image", "sha256:abcd"},
		{"--project", "Invalid"}, {"--project", "../../outside"}, {"--port", "0"},
		{"--port", "65536"}, {"--port", "not-a-port"}, {"--compose-file", "/does/not/exist"},
	} {
		t.Run(invalid.flag+"="+invalid.value, func(t *testing.T) {
			directory := t.TempDir()
			args := setupArgs(t, directory)
			for index := 0; index < len(args); index += 2 {
				if args[index] == invalid.flag {
					args[index+1] = invalid.value
				}
			}
			if err := Run(args); err == nil {
				t.Fatal("invalid argument accepted")
			}
			if len(snapshot(t, directory)) != 0 {
				t.Fatal("validation failure wrote deployment files")
			}
		})
	}
}

func TestSetupRefusesExistingFilesAndSymlinks(t *testing.T) {
	for _, name := range []string{".env", "compose.yml", "installation.json", ".setup-incomplete", "data"} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, name), []byte("existing bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, directory)
			if err := Run(setupArgs(t, directory)); err == nil {
				t.Fatal("existing file accepted")
			}
			if !reflect.DeepEqual(before, snapshot(t, directory)) {
				t.Fatal("existing file changed")
			}
		})
	}
	t.Run("directory symlink", func(t *testing.T) {
		directory := t.TempDir()
		link := filepath.Join(t.TempDir(), "deployment")
		if err := os.Symlink(directory, link); err != nil {
			t.Fatal(err)
		}
		if err := Run(setupArgs(t, link)); err == nil {
			t.Fatal("symlink deployment accepted")
		}
		if len(snapshot(t, directory)) != 0 {
			t.Fatal("symlink target changed")
		}
	})
	t.Run("contained symlink", func(t *testing.T) {
		directory := t.TempDir()
		target := filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(target, []byte("existing secret"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(directory, ".env")); err != nil {
			t.Fatal(err)
		}
		if err := Run(setupArgs(t, directory)); err == nil {
			t.Fatal("existing symlink accepted")
		}
		data, err := os.ReadFile(target)
		if err != nil || string(data) != "existing secret" {
			t.Fatalf("symlink target changed: %v", err)
		}
	})
}

func TestPartialSetupRetainsEvidenceAndCannotBeRetried(t *testing.T) {
	directory := t.TempDir()
	marker := []byte("incomplete setup evidence\n")
	if err := os.WriteFile(filepath.Join(directory, incomplete), marker, 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	dir, err := root.Open(".")
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	injected := errors.New("injected filesystem write failure")
	write := func(string, []byte, os.FileMode) error { return injected }
	err = prepare(root, dir, write, os.Getuid(), os.Getgid(), "v0.1.3", "tag-files", 8080, appID, storageID, []byte("services: {}\n"))
	if !errors.Is(err, injected) {
		t.Fatalf("partial setup error = %v, want injected failure", err)
	}
	if info, err := os.Stat(filepath.Join(directory, "secrets")); err != nil || !info.IsDir() {
		t.Fatalf("partial setup directory was removed: %v", err)
	}
	before := snapshot(t, directory)
	if before[incomplete] != string(marker) {
		t.Fatal("incomplete setup marker changed")
	}
	if err := Run(setupArgs(t, directory)); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("partial setup retry error = %v, want inspection refusal", err)
	}
	if !reflect.DeepEqual(before, snapshot(t, directory)) {
		t.Fatal("retry changed partial setup evidence")
	}
}

func TestSetupDoesNotEmitCredentials(t *testing.T) {
	directory := t.TempDir()
	args := setupArgs(t, directory)
	output, err := os.CreateTemp(t.TempDir(), "setup-output")
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	stdout, stderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = output, output
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	if err := Run(args); err != nil {
		t.Fatal(err)
	}
	retryErr := Run(args)
	if retryErr == nil {
		t.Fatal("setup retry unexpectedly succeeded")
	}
	info, err := output.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatalf("setup emitted output: %v", err)
	}
	files := snapshot(t, directory)
	for _, name := range []string{"secrets/s3_access_key", "secrets/s3_secret_key"} {
		if strings.Contains(retryErr.Error(), strings.TrimSpace(files[name])) {
			t.Fatal("setup error contains credentials")
		}
	}
}

func TestSetupPreservesBindMountOwnerWhenRunningAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires container-root ownership privileges")
	}
	directory := t.TempDir()
	const uid, gid = 12345, 12346
	if err := os.Chown(directory, uid, gid); err != nil {
		t.Fatal(err)
	}
	if err := Run(setupArgs(t, directory)); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		owner := info.Sys().(*syscall.Stat_t)
		if owner.Uid != uid || owner.Gid != gid {
			t.Errorf("%s owner = %d:%d, want %d:%d", path, owner.Uid, owner.Gid, uid, gid)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
