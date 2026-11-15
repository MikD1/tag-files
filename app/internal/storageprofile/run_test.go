package storageprofile

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestBackendStartsAsCurrentOwnerWithoutPrivilegeChanges(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an unprivileged process to detect setgroups regressions")
	}
	directory := t.TempDir()
	bin := t.TempDir()
	// Replace only the external SeaweedFS process. The real launcher must start
	// it without privileges and the resulting data must belong to this user.
	if err := os.WriteFile(filepath.Join(bin, "weed"), []byte("#!/bin/sh\n: > backend-started\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	b, err := startBackend(Options{DataDir: directory, S3Config: filepath.Join(directory, "s3.json")}, false, os.Geteuid(), os.Getegid())
	if err != nil {
		t.Fatalf("start backend as current owner: %v", err)
	}
	defer b.stop()
	select {
	case <-b.done:
		if b.err != nil {
			t.Fatalf("backend exited: %v", b.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("backend did not exit")
	}
	info, err := os.Stat(filepath.Join(directory, "backend-started"))
	if err != nil {
		t.Fatal(err)
	}
	owner := info.Sys().(*syscall.Stat_t)
	if int(owner.Uid) != os.Geteuid() || int(owner.Gid) != os.Getegid() {
		t.Fatalf("backend data owner = %d:%d, want %d:%d", owner.Uid, owner.Gid, os.Geteuid(), os.Getegid())
	}
}
