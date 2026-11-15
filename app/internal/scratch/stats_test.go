package scratch

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mikd1/tag-files/app/internal/domain"
)

func TestStatsDistinguishWrittenActiveAndUnwrittenReservations(t *testing.T) {
	m, err := Open(Config{Root: t.TempDir(), MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Begin(domain.NewUUID(), 1, 128, 256)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := os.WriteFile(a.Source, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := m.Stats()
	if err != nil || s.UsedBytes != 10 || s.ActiveBytes != 10 || s.AllocatedActiveBytes != 10 || s.ReservedBytes != 374 || s.QuotaBytes != 1000 {
		t.Fatalf("active/written bytes counted twice: %+v %v", s, err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = m.Stats()
	if err != nil || s.UsedBytes != 0 || s.ReservedBytes != 0 || s.ActiveBytes != 0 {
		t.Fatalf("retired attempt still reserves bytes: %+v %v", s, err)
	}
}

func TestStatsDoNotCountSparseLengthAsAllocatedSpace(t *testing.T) {
	m, err := Open(Config{Root: t.TempDir(), MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Begin(domain.NewUUID(), 1, 128, 256)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	file, err := os.Create(a.Source)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(128)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Stats()
	if err != nil || s.ActiveBytes != 128 || s.AllocatedActiveBytes != 0 || s.ReservedBytes != 256 {
		t.Fatal("sparse cache credited physical space", s, err)
	}
}

func TestStatsDoNotCreditUnrelatedFilesInsideAnActiveAttempt(t *testing.T) {
	m, err := Open(Config{Root: t.TempDir(), MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Begin(domain.NewUUID(), 1, 128, 256)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := os.WriteFile(filepath.Join(a.Directory, "unmarked"), []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := m.Stats()
	if err != nil || s.UsedBytes != 10 || s.ActiveBytes != 0 || s.ReservedBytes != 384 {
		t.Fatalf("unrelated bytes reduced a producer's unwritten reservation: %+v %v", s, err)
	}
}
