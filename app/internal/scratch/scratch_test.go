package scratch

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
)

func TestAttemptsAreIsolatedAndReservationsIncludeExistingBytes(t *testing.T) {
	root := t.TempDir()
	m, err := Open(Config{Root: root, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	id := domain.NewUUID()
	old, err := m.Begin(id, 1, 100, 300)
	if err != nil {
		t.Fatal(err)
	}
	newer, err := m.Begin(id, 2, 100, 300)
	if err != nil {
		t.Fatal(err)
	}
	if old.Directory == newer.Directory || filepath.Dir(old.Source) != old.Directory || filepath.Dir(newer.Output) != newer.Directory {
		t.Fatal("attempts share scratch files")
	}
	if err := os.WriteFile(newer.Output, []byte("new generation"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Begin(domain.NewUUID(), 1, 100, 201); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("unwritten reservations ignored: %v", err)
	}
	if err := os.WriteFile(old.Output, []byte("old writer"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(newer.Output); string(got) != "new generation" {
		t.Fatal("old attempt overwrote or cleaned newer output")
	}
	if err := m.Close(); !errors.Is(err, ErrInUse) {
		t.Fatalf("manager released live ownership: %v", err)
	}
	if _, err := Open(Config{Root: root, MaxBytes: 1000}); !errors.Is(err, ErrInUse) {
		t.Fatalf("second process admitted to owned scratch: %v", err)
	}
	if err := newer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	// A prior-process cache is counted and retained; age does not permit removal.
	if err := os.MkdirAll(filepath.Join(root, "old-attempt"), 0700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "old-attempt", "source.cache")
	if err := os.WriteFile(stale, make([]byte, 700), 0600); err != nil {
		t.Fatal(err)
	}
	m, err = Open(Config{Root: root, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Begin(id, 3, 200, 101); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("prior cache not budgeted: %v", err)
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatal("prior cache deleted without quiescence proof")
	}
}

func TestCapacityAndOutputChecksRefuseWithoutDiscardingFiles(t *testing.T) {
	root := t.TempDir()
	// Free space can rise between snapshots when other tests/containers clean
	// files. A reserve beyond any representable availability stays impossible.
	m, err := Open(Config{Root: root, MaxBytes: 100000, MinFreeBytes: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Begin(domain.NewUUID(), 1, 10, 10); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("physical free-space reserve ignored: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = Open(Config{Root: root, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Begin(domain.NewUUID(), 1, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(a.Output, make([]byte, 21), 0600); err != nil {
		t.Fatal(err)
	}
	if err := a.Check(); !errors.Is(err, ErrInsufficientStorage) {
		t.Fatalf("output exceeded its reservation: %v", err)
	}
	if _, err := os.Stat(a.Output); err != nil {
		t.Fatal("monitor removed file before producer stopped")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stopped attempt not removed")
	}
}

func TestScratchRefusesSymlinkAndTraversal(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	if err := os.Symlink(other, filepath.Join(root, "foreign")); err != nil {
		t.Fatal(err)
	}
	m, err := Open(Config{Root: root, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Begin(domain.NewUUID(), 1, 10, 10); err == nil {
		t.Fatal("symlink counted as safe owned scratch")
	}
	if _, err := m.Begin("../../foreign", 1, 10, 10); err == nil {
		t.Fatal("job path traversal accepted")
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal("foreign directory touched")
	}
}

func TestScratchOwnershipLockCannotCreateFileThroughSymlink(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	foreign := filepath.Join(other, "foreign-file")
	if err := os.Symlink(foreign, filepath.Join(root, ".scratch.lock")); err != nil {
		t.Fatal(err)
	}
	m, err := Open(Config{Root: root, MaxBytes: 1000})
	if m != nil {
		m.Close()
	}
	if err == nil {
		t.Fatal("ownership lock followed symlink outside scratch")
	}
	if _, err := os.Stat(foreign); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("opening scratch created a foreign file")
	}
}
