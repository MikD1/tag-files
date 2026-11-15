package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIRefusesAmbiguousConfigurationBeforeNetwork(t *testing.T) {
	for _, args := range [][]string{nil, {"scan"}, {"serve"}, {"copy", "-config", "missing"}} {
		if e := run(context.Background(), args); e == nil {
			t.Fatal("accepted ambiguous command", args)
		}
	}
}
func TestCredentialFilesAndDisjointRoots(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(p, []byte("secret\n"), 0600)
	if v, e := readSecret(p); e != nil || v != "secret" {
		t.Fatal(v, e)
	}
	os.Chmod(p, 0644)
	if _, e := readSecret(p); e == nil {
		t.Fatal("public secret accepted")
	}
	os.Chmod(p, 0600)
	l := p + "-link"
	os.Symlink(p, l)
	if _, e := readSecret(l); e == nil {
		t.Fatal("credential symlink accepted")
	}
	root := t.TempDir()
	s := settings{Database: filepath.Join(root, "source", "legacy.sqlite"), Archive: filepath.Join(root, "source", "archive"), TargetMetadata: filepath.Join(root, "target"), Journal: filepath.Join(root, "journal", "state.sqlite")}
	if e := rootsDisjoint(s); e == nil {
		t.Fatal("source/archive overlap accepted")
	}
	s.Archive = filepath.Join(root, "archive")
	if e := rootsDisjoint(s); e != nil {
		t.Fatal(e)
	}
}
