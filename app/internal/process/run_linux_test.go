package process

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessFixture(t *testing.T) {
	if os.Getenv("TAG_FILES_PROCESS_FIXTURE") != "1" {
		return
	}
	separator := 0
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i + 1
			break
		}
	}
	args := os.Args[separator:]
	switch args[0] {
	case "args":
		fmt.Fprint(os.Stdout, strings.Join(args[1:], "\n"))
		os.Exit(0)
	case "flood":
		for range 400 {
			fmt.Fprint(os.Stdout, strings.Repeat("o", 1024))
			fmt.Fprint(os.Stderr, strings.Repeat("e", 1024))
		}
		os.Exit(7)
	case "ignore":
		signal.Ignore(syscall.SIGTERM)
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			os.Exit(9)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "orphan", "orphan-pipes", "cancel":
		child := exec.Command(os.Args[0], "-test.run=^TestProcessFixture$", "--", "ignore", args[1])
		child.Env = os.Environ()
		if args[0] == "orphan-pipes" || args[0] == "cancel" {
			child.Stdout = os.Stdout
			child.Stderr = os.Stderr
		}
		if err := child.Start(); err != nil {
			os.Exit(8)
		}
		child.Process.Release()
		deadline := time.Now().Add(2 * time.Second)
		for {
			if _, err := os.Stat(args[1]); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(10)
			}
			time.Sleep(time.Millisecond)
		}
		if args[0] == "cancel" {
			for {
				time.Sleep(time.Hour)
			}
		}
		os.Exit(0)
	}
	os.Exit(11)
}
func fixtureCommand(t *testing.T, mode string, args ...string) Command {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Command{Path: binary, Args: append([]string{"-test.run=^TestProcessFixture$", "--", mode}, args...), Env: []string{"TAG_FILES_PROCESS_FIXTURE=1", "GORACE=atexit_sleep_ms=0"}}
}
func shortLimits() Limits {
	return Limits{StdoutBytes: 4096, StderrBytes: 2048, GracePeriod: 40 * time.Millisecond, DrainTimeout: 100 * time.Millisecond}
}
func TestRunUsesLiteralArgumentsWithoutShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	argument := "$(touch " + marker + ") ; & quoted ' value"
	result, err := Run(context.Background(), fixtureCommand(t, "args", argument, "next"), shortLimits())
	if err != nil || result.ExitCode != 0 || string(result.Stdout) != argument+"\nnext" {
		t.Fatalf("literal arguments changed: %+v %v", result, err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("argument reached a shell")
	}
}
func TestRunBoundsBothOutputsAndPreservesFailureStatus(t *testing.T) {
	result, err := Run(context.Background(), fixtureCommand(t, "flood"), shortLimits())
	if err == nil || result.ExitCode != 7 || len(result.Stdout) != 4096 || len(result.Stderr) != 2048 || !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("unbounded/lost diagnostics: sizes=%d/%d exit=%d error=%v", len(result.Stdout), len(result.Stderr), result.ExitCode, err)
	}
}
func TestRunDrainsDescendantAfterLeaderExits(t *testing.T) {
	for _, mode := range []string{"orphan", "orphan-pipes"} {
		t.Run(mode, func(t *testing.T) {
			pidfile := filepath.Join(t.TempDir(), "child.pid")
			started := time.Now()
			_, err := Run(context.Background(), fixtureCommand(t, mode, pidfile), shortLimits())
			if !errors.Is(err, ErrDescendants) || time.Since(started) > 2*time.Second {
				t.Fatalf("parent exit orphaned its child: %v", err)
			}
			assertChildReaped(t, pidfile)
		})
	}
}
func assertChildReaped(t *testing.T, pidfile string) {
	t.Helper()
	raw, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Fatalf("owned child still exists or is a zombie: pid=%d error=%v", pid, err)
	}
}
func TestRunCancellationKillsIgnoringDescendantAndWaits(t *testing.T) {
	pidfile := filepath.Join(t.TempDir(), "child.pid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Run(ctx, fixtureCommand(t, "cancel", pidfile), shortLimits()); done <- err }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(pidfile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture did not start child")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TERM-ignoring child prevented shutdown")
	}
	assertChildReaped(t, pidfile)
}
