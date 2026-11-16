package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	migrate "github.com/mikd1/tag-files/app/tools/migrate"
)

func autoSettings(t *testing.T) (settings, string) {
	t.Helper()
	root := t.TempDir()
	s := settings{Database: filepath.Join(root, "source", "legacy.sqlite"), Archive: filepath.Join(root, "archive", "run-001"), TargetMetadata: filepath.Join(root, "target"), Journal: filepath.Join(root, "journal", "migration.sqlite"), SnapshotID: "frozen", RollbackReference: "verified receipt", RollbackVerified: true}
	for _, role := range []string{"source", "target"} {
		p := filepath.Join(root, role+"-secret")
		if err := os.WriteFile(p, []byte(role+"-credential\n"), 0600); err != nil {
			t.Fatal(err)
		}
		e := endpoint{Endpoint: "http://" + role + ":9000", Region: "us-east-1", AccessKeyFile: p, SecretKeyFile: p}
		if role == "source" {
			s.Source = e
		} else {
			s.Target = e
		}
	}
	return s, filepath.Join(root, "config.json")
}

func TestAutomaticCLIRequiresVerifiedRollbackBeforeNetwork(t *testing.T) {
	s, path := autoSettings(t)
	s.RollbackVerified = false
	b, _ := json.Marshal(s)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{"auto", "-config", path})
	if err == nil || !strings.Contains(err.Error(), "verified rollback") {
		t.Fatalf("automatic command did not enforce rollback: %v", err)
	}
}

func TestAutomaticReceiptFreezesSettingsAndOnlyAllowsLossDecisionsBeforeCopy(t *testing.T) {
	s, _ := autoSettings(t)
	state, err := openAutomatic(s)
	if err != nil {
		t.Fatal(err)
	}
	if err = state.save(); err != nil {
		t.Fatal(err)
	}
	state.close()
	s.Exceptions = []migrate.Exception{{IssueID: "missing", Decision: "missing_media", Owner: "owner", Reason: "retained loss"}}
	state, err = openAutomatic(s)
	if err != nil {
		t.Fatal("pre-copy exception edit refused", err)
	}
	state.CopyStarted = true
	if err = state.save(); err != nil {
		t.Fatal(err)
	}
	state.close()
	s.Exceptions = nil
	if state, err = openAutomatic(s); err == nil {
		state.close()
		t.Fatal("copy exception identity changed")
	}
	s.Exceptions = []migrate.Exception{{IssueID: "missing", Decision: "missing_media", Owner: "owner", Reason: "retained loss"}}
	s.SnapshotID = "different-source"
	if state, err = openAutomatic(s); err == nil {
		state.close()
		t.Fatal("source identity changed")
	}
}

func TestAutomaticArchiveRetryRetainsPartialAttemptsAndRejectsDamagedComplete(t *testing.T) {
	s, _ := autoSettings(t)
	state, err := openAutomatic(s)
	if err != nil {
		t.Fatal(err)
	}
	defer state.close()
	archive, err := state.archive(context.Background())
	if err != nil || archive != s.Archive {
		t.Fatalf("first archive: %s %v", archive, err)
	}
	if err = os.MkdirAll(archive, 0700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(archive, "bytes.part")
	if err = os.WriteFile(partial, []byte("partial media"), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := state.archive(context.Background())
	if err != nil || second == archive {
		t.Fatalf("interrupted archive was reused: %s %v", second, err)
	}
	if b, err := os.ReadFile(partial); err != nil || string(b) != "partial media" {
		t.Fatal("partial evidence lost", err)
	}
	if err = os.MkdirAll(second, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(second, "complete"), []byte("damaged completion"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = state.archive(context.Background()); err == nil {
		t.Fatal("damaged complete archive silently replaced")
	}
}

func TestAutomaticReceiptRefusesChangedCredentialAndConcurrentDriver(t *testing.T) {
	s, _ := autoSettings(t)
	state, err := openAutomatic(s)
	if err != nil {
		t.Fatal(err)
	}
	if competing, err := openAutomatic(s); err == nil {
		competing.close()
		t.Fatal("two automatic drivers acquired one journal")
	}
	state.close()
	if err := os.WriteFile(s.Source.AccessKeyFile, []byte("changed credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if changed, err := openAutomatic(s); err == nil {
		changed.close()
		t.Fatal("credential change was accepted on retry")
	}
}

func TestAutomaticArchiveCannotRestartAfterCopyWhenDirectoryDisappears(t *testing.T) {
	s, _ := autoSettings(t)
	state, err := openAutomatic(s)
	if err != nil {
		t.Fatal(err)
	}
	defer state.close()
	state.CopyStarted = true
	if _, err := state.archive(context.Background()); err == nil {
		t.Fatal("missing archive restarted after copy began")
	}
}

func TestAutomaticRetryAcceptsLastPersistedConfigAfterArchiveCheckpoint(t *testing.T) {
	s, _ := autoSettings(t)
	state, err := openAutomatic(s)
	if err != nil {
		t.Fatal(err)
	}
	state.CurrentArchive = s.Archive + ".attempt-002"
	state.Attempts = append(state.Attempts, state.CurrentArchive)
	s.Archive = state.CurrentArchive
	state.CurrentArchive = state.BaseArchive + ".attempt-003"
	state.Attempts = append(state.Attempts, state.CurrentArchive)
	if err := state.save(); err != nil {
		t.Fatal(err)
	}
	state.close()
	resumed, err := openAutomatic(s)
	if err != nil {
		t.Fatal("configuration write interruption made receipt unusable", err)
	}
	resumed.close()
}

func TestAutomaticOutputsKeepHostDirectoryOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires container-root ownership privileges")
	}
	s, path := autoSettings(t)
	root := filepath.Dir(path)
	if err := os.Chown(root, 12345, 23456); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateJSON(path, s); err != nil {
		t.Fatal(err)
	}
	state, err := openAutomatic(s)
	if err != nil {
		t.Fatal(err)
	}
	state.close()
	for _, p := range []string{path, filepath.Dir(s.Journal), filepath.Join(filepath.Dir(s.Journal), "automatic.json")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		owner := info.Sys().(*syscall.Stat_t)
		if owner.Uid != 12345 || owner.Gid != 23456 {
			t.Errorf("automatic output %s owned by %d:%d", p, owner.Uid, owner.Gid)
		}
	}
}
