package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestSettingsReadBoundedSecretFilesAndExplicitRuntimePaths(t *testing.T) {
	root := t.TempDir()
	access, secret := "app-private-access-key", "app-private-secret-key"
	for name, value := range map[string]string{"access": access, "secret": secret} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	env := map[string]string{"TAG_FILES_DATA_DIR": filepath.Join(root, "metadata"), "TAG_FILES_SCRATCH_DIR": filepath.Join(root, "cache"), "TAG_FILES_S3_ENDPOINT": "http://storage:8333", "TAG_FILES_ACCESS_KEY_FILE": filepath.Join(root, "access"), "TAG_FILES_SECRET_KEY_FILE": filepath.Join(root, "secret"), "TAG_FILES_LISTEN": "127.0.0.1:8123", "TAG_FILES_SCRATCH_MAX_BYTES": "8388608"}
	settings, err := settingsFrom(func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if settings.listen != "127.0.0.1:8123" || settings.app.Storage.AccessKey != access || settings.app.Storage.SecretKey != secret || settings.app.DataDir != env["TAG_FILES_DATA_DIR"] || settings.app.Scratch.Root != env["TAG_FILES_SCRATCH_DIR"] || settings.app.Scratch.MaxBytes != 8388608 || settings.app.Storage.Bucket != "tag-files" {
		t.Fatal("runtime settings were not applied")
	}
	env["TAG_FILES_SCRATCH_MAX_BYTES"] = "-1"
	if _, err := settingsFrom(func(key string) string { return env[key] }); err == nil {
		t.Fatal("negative capacity admitted")
	}
}

func TestSecretsRefuseNonregularOversizedAndControlBytesWithoutLoggingContents(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{"short": "tiny", "long": strings.Repeat("private", 100), "multiline": "private-access-key\nprivate-key", "nul": "private-secret\x00private-key"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSecret(path); err == nil || strings.Contains(err.Error(), value) {
			t.Fatal("invalid secret accepted or exposed", name)
		}
	}
	fifo := filepath.Join(root, "pipe")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readSecret(fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO admitted as secret")
		}
	case <-time.After(time.Second):
		t.Fatal("secret read blocked opening a FIFO")
	}
}
