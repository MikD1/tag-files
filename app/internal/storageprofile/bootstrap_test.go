package storageprofile

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func appCredentials() aws.Credentials {
	return aws.Credentials{AccessKeyID: "test-app-access-key-123456789", SecretAccessKey: "test-app-secret-key-123456789-abcdef"}
}

func prepare(t *testing.T, dir string) *Installation {
	t.Helper()
	p, err := Prepare(dir, appCredentials(), os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

func TestOwnedStorageInitializationResumesAndKeepsCredentialsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	// The official base image may contribute empty directories to a new volume.
	if err := os.Mkdir(filepath.Join(dir, "filerldb2"), 0755); err != nil {
		t.Fatal(err)
	}
	p := prepare(t, dir)
	if p.State.Phase != "initializing" || p.State.ID == "" {
		t.Fatalf("missing durable init intent: %+v", p.State)
	}
	config, err := os.ReadFile(filepath.Join(dir, "config", "s3.json"))
	if err != nil {
		t.Fatal(err)
	}
	control, err := PrivateCredentials(config)
	if err != nil || control.AccessKeyID == appCredentials().AccessKeyID {
		t.Fatalf("control identity must be separate: %v", err)
	}
	first := p.State
	p.Close()
	p = prepare(t, dir)
	if p.State != first {
		t.Fatal("interrupted init acquired a different identity")
	}
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	p.Close()
	p = prepare(t, dir)
	if p.State.ID != first.ID || p.State.Phase != "ready" {
		t.Fatal("ready marker did not survive restart")
	}
	after, err := os.ReadFile(filepath.Join(dir, "config", "s3.json"))
	if err != nil || !bytes.Equal(config, after) {
		t.Fatalf("credentials changed on reopen: %v", err)
	}
}

func TestStorageOwnershipRejectsSecondWriterAndMismatchedSecrets(t *testing.T) {
	dir := t.TempDir()
	p := prepare(t, dir)
	if other, err := Prepare(dir, appCredentials(), os.Getuid(), os.Getgid()); !errors.Is(err, ErrStorageInUse) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("second writer: %v", err)
	}
	p.Close()
	changed := appCredentials()
	changed.SecretAccessKey += "different"
	if other, err := Prepare(dir, changed, os.Getuid(), os.Getgid()); !errors.Is(err, ErrCredentialMismatch) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("changed credentials: %v", err)
	}
}

func TestStorageRefusesUnownedDataAndUnknownStateWithoutModifyingFiles(t *testing.T) {
	for _, state := range []string{"", `{"formatVersion":99,"engine":"future","phase":"ready"}`} {
		t.Run(state, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "unrelated.data")
			original := []byte("keep every byte")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if state != "" {
				if err := os.WriteFile(filepath.Join(dir, StateFilename), []byte(state), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := os.ReadDir(dir)
			if p, err := Prepare(dir, appCredentials(), os.Getuid(), os.Getgid()); err == nil {
				p.Close()
				t.Fatal("unowned/unknown storage was adopted")
			}
			after, _ := os.ReadDir(dir)
			got, err := os.ReadFile(path)
			if err != nil || len(before) != len(after) || !bytes.Equal(original, got) {
				t.Fatalf("refusal modified source files: %v", err)
			}
		})
	}
}

func TestStorageInitRecoversConfigWriteBoundaryAndRefusesTampering(t *testing.T) {
	dir := t.TempDir()
	p := prepare(t, dir)
	state := p.State
	// Simulate the durable intent preceding the first config write.
	state.ConfigSHA256 = ""
	if err := writeState(dir, state); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "config", "s3.json")); err != nil {
		t.Fatal(err)
	}
	p.Close()
	p = prepare(t, dir)
	if p.State.ID != state.ID || p.State.ConfigSHA256 == "" {
		t.Fatal("initialization checkpoint was not resumed")
	}
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	p.Close()
	config := filepath.Join(dir, "config", "s3.json")
	if err := os.WriteFile(config, []byte(`{"identities":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if q, err := Prepare(dir, appCredentials(), os.Getuid(), os.Getgid()); err == nil {
		q.Close()
		t.Fatal("tampered ready configuration was healed")
	}
}

func TestClosedStorageOwnerCannotPublishReadiness(t *testing.T) {
	dir := t.TempDir()
	p := prepare(t, dir)
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, StateFilename))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RecordReady(); err == nil {
		t.Fatal("closed owner published ready state")
	}
	after, err := os.ReadFile(filepath.Join(dir, StateFilename))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("closed owner modified durable state")
	}
}

func TestStorageRejectsExternalOwnershipLock(t *testing.T) {
	dir := t.TempDir()
	p := prepare(t, dir)
	p.Close()
	outside := filepath.Join(t.TempDir(), "unrelated.lock")
	if err := os.WriteFile(outside, []byte("other service"), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, lockFilename)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if p, err := Prepare(dir, appCredentials(), os.Getuid(), os.Getgid()); err == nil {
		p.Close()
		t.Fatal("storage acquired a lock outside its complete volume")
	}
}
