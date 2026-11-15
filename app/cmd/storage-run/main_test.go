package main

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBackendOwnerMatchesUnprivilegedSupervisor(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an unprivileged supervisor")
	}
	uid, gid, err := backendOwner()
	if err != nil {
		t.Fatalf("resolve owner without requiring a seaweed account: %v", err)
	}
	if uid != os.Geteuid() || gid != os.Getegid() {
		t.Fatalf("backend owner = %d:%d, want %d:%d", uid, gid, os.Geteuid(), os.Getegid())
	}
}

func TestRootBackendOwnerUsesSeaweedAccount(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires a root supervisor")
	}
	owner, err := user.Lookup("seaweed")
	if err != nil {
		t.Skip("requires the storage image's seaweed account")
	}
	wantUID, err := strconv.Atoi(owner.Uid)
	if err != nil {
		t.Fatal(err)
	}
	wantGID, err := strconv.Atoi(owner.Gid)
	if err != nil {
		t.Fatal(err)
	}
	uid, gid, err := backendOwner()
	if err != nil {
		t.Fatal(err)
	}
	if uid != wantUID || gid != wantGID {
		t.Fatalf("backend owner = %d:%d, want seaweed %d:%d", uid, gid, wantUID, wantGID)
	}
}

func TestSecretFilesAreBoundedAndCannotBlockOnFIFO(t *testing.T) {
	dir := t.TempDir()
	large := filepath.Join(dir, "large")
	if err := os.WriteFile(large, []byte(strings.Repeat("x", 1024)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := secret(large); err == nil {
		t.Fatal("oversized secret accepted")
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := secret(fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO secret accepted")
		}
	case <-time.After(200 * time.Millisecond):
		// Release a buggy blocking reader before reporting the regression.
		f, err := os.OpenFile(fifo, os.O_RDWR|syscall.O_NONBLOCK, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write([]byte("invalid"))
		f.Close()
		<-done
		t.Fatal("secret FIFO blocked container startup")
	}
}
