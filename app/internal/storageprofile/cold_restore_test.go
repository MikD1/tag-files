package storageprofile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
)

func TestColdRestoreMarkerRefusesPrepareBeforeAnyWrite(t *testing.T) {
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
			install, e := Prepare(root, aws.Credentials{AccessKeyID: strings.Repeat("a", 24), SecretAccessKey: strings.Repeat("s", 36)}, -1, -1)
			if install != nil {
				install.Close()
			}
			if e == nil {
				t.Fatal("incomplete restore admitted storage writer")
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
