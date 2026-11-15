package storageprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
)

func TestStorageLibraryBindingIsDurableImmutableAndResumesAfterRestart(t *testing.T) {
	root := t.TempDir()
	p := prepare(t, root)
	id := p.State.ID
	library := domain.NewUUID()
	if _, err := p.BindLibrary(id, library); err == nil {
		t.Fatal("binding admitted before storage ready")
	}
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	bound, err := p.BindLibrary(id, library)
	if err != nil || bound.LibraryID != library {
		t.Fatalf("binding: %+v %v", bound, err)
	}
	if _, err := p.BindLibrary(id, library); err != nil {
		t.Fatal("lost reply retry not idempotent", err)
	}
	if _, err := p.BindLibrary(domain.NewUUID(), library); !errors.Is(err, ErrStorageBindingMismatch) {
		t.Fatalf("wrong volume adopted: %v", err)
	}
	if _, err := p.BindLibrary(id, domain.NewUUID()); !errors.Is(err, ErrStorageBindingMismatch) {
		t.Fatalf("second library adopted: %v", err)
	}
	p.Close()
	p = prepare(t, root)
	if p.State.ID != id || p.State.LibraryID != library {
		t.Fatal("pair changed after storage restart")
	}
}

func TestConcurrentLibraryBindingHasExactlyOneDurableWinner(t *testing.T) {
	p := prepare(t, t.TempDir())
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	winner := ""
	wins := 0
	storageID := p.State.ID
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := domain.NewUUID()
			_, err := p.BindLibrary(storageID, id)
			if err == nil {
				mu.Lock()
				wins++
				winner = id
				mu.Unlock()
			} else if !errors.Is(err, ErrStorageBindingMismatch) {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	state, err := LoadState(p.root)
	if err != nil || wins != 1 || state.LibraryID != winner {
		t.Fatalf("multiple bindings won: %d %+v %v", wins, state, err)
	}
}

func TestStorageFormatUpgradePreservesIdentityAndRejectsFailedBindingWrite(t *testing.T) {
	root := t.TempDir()
	p := prepare(t, root)
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	before := p.State
	p.Close()
	old := before
	old.FormatVersion = 1
	old.VolumeID = ""
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, StateFilename), data, 0600); err != nil {
		t.Fatal(err)
	}
	p = prepare(t, root)
	if p.State.FormatVersion != 2 || p.State.ID != before.ID || p.State.ConfigSHA256 != before.ConfigSHA256 {
		t.Fatalf("upgrade replaced storage identity/config: %+v", p.State)
	}
	path := filepath.Join(root, StateFilename)
	saved := path + ".saved"
	if err := os.Rename(path, saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BindLibrary(p.State.ID, domain.NewUUID()); err == nil {
		t.Fatal("failed marker write acknowledged")
	}
	if p.State.LibraryID != "" {
		t.Fatal("failed binding published in memory")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, path); err != nil {
		t.Fatal(err)
	}
	if _, err := p.BindLibrary(p.State.ID, domain.NewUUID()); err != nil {
		t.Fatal("binding could not resume", err)
	}
}

func TestCopiedStorageMarkerCannotAdoptAnotherPhysicalRoot(t *testing.T) {
	p := prepare(t, t.TempDir())
	if err := p.RecordReady(); err != nil {
		t.Fatal(err)
	}
	copyRoot := t.TempDir()
	data, err := os.ReadFile(filepath.Join(p.root, StateFilename))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyRoot, StateFilename), data, 0600); err != nil {
		t.Fatal(err)
	}
	if clone, err := Prepare(copyRoot, appCredentials(), os.Getuid(), os.Getgid()); !errors.Is(err, ErrStorageVolumeChanged) {
		if clone != nil {
			clone.Close()
		}
		t.Fatalf("copied marker claimed an independent root: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(copyRoot, StateFilename))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatal("refused clone marker changed")
	}
}
