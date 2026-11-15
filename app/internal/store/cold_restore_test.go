package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestColdRestoreMarkerRefusesStartupBeforeAnyWrite(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, ".cold-restore-incomplete")
			switch kind {
			case "file":
				if e := os.WriteFile(marker, []byte("unverified pair"), 0600); e != nil {
					t.Fatal(e)
				}
			case "directory":
				if e := os.Mkdir(marker, 0700); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				if e := os.Symlink("absent-target", marker); e != nil {
					t.Fatal(e)
				}
			}
			db, e := Open(context.Background(), root)
			if db != nil {
				db.Close()
			}
			if e == nil {
				t.Fatal("incomplete restore admitted runtime writer")
			}
			entries, e := os.ReadDir(root)
			if e != nil {
				t.Fatal(e)
			}
			if len(entries) != 1 || entries[0].Name() != ".cold-restore-incomplete" {
				t.Fatalf("refused root was mutated: %v", entries)
			}
		})
	}
}
