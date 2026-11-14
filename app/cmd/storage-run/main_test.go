package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

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
