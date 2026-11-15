package diskspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMeasureUsesFilesystemIdentityAcrossRootsAndReportsRealCapacity(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "other-root")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := Measure(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Measure(child)
	if err != nil || !Valid(a) || !Valid(b) || a.FileSystemID != b.FileSystemID || a.AvailableBytes <= 0 || a.AvailableInodes != nil && *a.AvailableInodes <= 0 {
		t.Fatalf("invalid or root-dependent capacity: %+v %+v %v", a, b, err)
	}
	if a.RootIdentity == "" || a.RootIdentity == b.RootIdentity {
		t.Fatal("capacity observation did not retain its exact opened root")
	}
	if _, err := Measure(filepath.Join(root, "absent")); err == nil {
		t.Fatal("missing filesystem treated as available")
	}
}
