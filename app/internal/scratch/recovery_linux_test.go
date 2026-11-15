package scratch

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/process"
)

func TestScratchCrashFixture(t *testing.T) {
	if os.Getenv("TAG_FILES_SCRATCH_FIXTURE") != "1" {
		return
	}
	var args []string
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	switch args[0] {
	case "owner", "abandon", "native-owner":
		m, err := Open(Config{Root: args[1], MaxBytes: 100000})
		if err != nil {
			t.Fatal(err)
		}
		a, err := m.Begin(domain.NewUUID(), 1, 1000, 1000)
		if err != nil {
			t.Fatal(err)
		}
		ctx, err := a.Context(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(a.Source, []byte("durable input remains in S3"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(args[2], []byte(a.Directory), 0600); err != nil {
			t.Fatal(err)
		}
		if args[0] == "abandon" {
			os.Exit(0)
		}
		command := process.Command{Path: "ffmpeg", Args: []string{"-hide_banner", "-v", "error", "-nostdin", "-re", "-f", "lavfi", "-i", "testsrc2=size=64x48:rate=1", "-threads", "1", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency", "-g", "1", "-flush_packets", "1", "-pix_fmt", "yuv420p", "-f", "mp4", "-movflags", "frag_keyframe+empty_moov", a.Output}}
		if args[0] != "native-owner" {
			command = scratchFixtureCommand("fork", args[3])
		}
		_, err = process.Run(ctx, command, process.DefaultLimits())
		if err != nil {
			t.Fatal(err)
		}
	case "fork":
		guard := os.NewFile(3, "scratch lifetime")
		if _, err := guard.Stat(); err != nil {
			t.Fatal(err)
		}
		command := scratchFixtureCommand("descendant", args[1])
		child := exec.Command(command.Path, command.Args...)
		child.Env = append(os.Environ(), command.Env...)
		child.ExtraFiles = []*os.File{guard, os.NewFile(4, "attempt lifetime")}
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "descendant":
		guard := os.NewFile(3, "scratch lifetime")
		if _, err := guard.Stat(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(args[1], []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
			t.Fatal(err)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "noop":
	default:
		t.Fatal("unknown fixture")
	}
	os.Exit(0)
}

func TestAttemptCloseKeepsFilesUntilProducerReleasesInheritedLock(t *testing.T) {
	root := t.TempDir()
	m, err := Open(Config{Root: root, MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	a, err := m.Begin(domain.NewUUID(), 1, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := a.Context(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pidfile := filepath.Join(t.TempDir(), "producer.pid")
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, err := process.Run(ctx, scratchFixtureCommand("descendant", pidfile), process.DefaultLimits())
		done <- err
	}()
	defer func() { cancel(); <-stopped; _ = a.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(pidfile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("producer did not start")
		}
		time.Sleep(time.Millisecond)
	}
	if err := a.Close(); !errors.Is(err, ErrInUse) {
		t.Fatalf("live producer's files removed: %v", err)
	}
	if _, err := os.Stat(a.Directory); err != nil {
		t.Fatal("live attempt removed")
	}
	if _, err := a.Context(context.Background()); !errors.Is(err, ErrInUse) {
		t.Fatalf("closing attempt admitted a new producer: %v", err)
	}
	if _, err := process.Run(ctx, scratchFixtureCommand("noop"), process.DefaultLimits()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("retired lifetime admitted a new command: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("producer stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("producer did not stop")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stopped attempt retained")
	}
}

func TestReclaimRefusesAnAttemptStillLockedWithoutTheRootDescriptor(t *testing.T) {
	root := t.TempDir()
	old := abandonedAttempt(t, root)
	guard, err := os.OpenFile(filepath.Join(old, ".guard-v1"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if err := syscall.Flock(int(guard.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	m, err := Open(Config{Root: root, MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Reclaim(); !errors.Is(err, ErrInUse) {
		t.Fatalf("locked attempt reclaimed: %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("still locked files removed")
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := m.Reclaim(); err != nil || n != 1 {
		t.Fatalf("unlocked attempt retained: %d %v", n, err)
	}
}

func TestNativeFFmpegRetainsScratchLockAndDiesWithApplication(t *testing.T) {
	if os.Getenv("TAG_FILES_MEDIA_INTEGRATION") != "1" {
		t.Skip("requires the pinned FFmpeg runtime")
	}
	if _, err := process.Run(context.Background(), scratchFixtureCommand("noop"), process.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	directoryFile := filepath.Join(t.TempDir(), "attempt")
	command := scratchFixtureCommand("native-owner", root, directoryFile)
	parent := exec.Command(command.Path, command.Args...)
	parent.Env = append(os.Environ(), command.Env...)
	parent.Stderr = os.Stderr
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for pid == 0 {
		children, _ := filepath.Glob("/proc/" + strconv.Itoa(parent.Process.Pid) + "/task/*/children")
		for _, path := range children {
			raw, _ := os.ReadFile(path)
			for _, child := range strings.Fields(string(raw)) {
				executable, _ := os.Readlink("/proc/" + child + "/exe")
				guard, _ := os.Readlink("/proc/" + child + "/fd/3")
				attemptGuard, _ := os.Readlink("/proc/" + child + "/fd/4")
				directory, _ := os.ReadFile(directoryFile)
				output, outputErr := os.Stat(filepath.Join(string(directory), "output.part"))
				if filepath.Base(executable) == "ffmpeg" && guard == filepath.Join(root, ".scratch.lock") && attemptGuard == filepath.Join(string(directory), ".guard-v1") && outputErr == nil && output.Size() > 0 {
					pid, _ = strconv.Atoi(child)
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("actual FFmpeg did not retain lifetime descriptor")
		}
		time.Sleep(time.Millisecond)
	}
	defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	deadline = time.Now().Add(2 * time.Second)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		var status syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
		if time.Now().After(deadline) {
			t.Fatal("actual FFmpeg survived application death")
		}
		time.Sleep(time.Millisecond)
	}
	m, err := Open(Config{Root: root, MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if n, err := m.Reclaim(); err != nil || n != 1 {
		t.Fatalf("native crash scratch retained: %d %v", n, err)
	}
}

func scratchFixtureCommand(args ...string) process.Command {
	return process.Command{Path: os.Args[0], Args: append([]string{"-test.run=^TestScratchCrashFixture$", "--"}, args...), Env: []string{"TAG_FILES_SCRATCH_FIXTURE=1", "GORACE=atexit_sleep_ms=0"}}
}

func abandonedAttempt(t *testing.T, root string) string {
	t.Helper()
	directoryFile := filepath.Join(t.TempDir(), "attempt")
	_, err := process.Run(context.Background(), scratchFixtureCommand("abandon", root, directoryFile), process.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.ReadFile(directoryFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(directory)
}

// Replacing the inherited fd with an ordinary app-only lock permits another
// owner to delete files while a surviving descendant still has them open.
func TestCrashLockSurvivesApplicationAndDirectProducer(t *testing.T) {
	// Become a subreaper through the real supervisor so this test also reaps
	// its fixture's orphan after terminating it.
	if _, err := process.Run(context.Background(), scratchFixtureCommand("noop"), process.DefaultLimits()); err != nil {
		t.Fatal(err)
	}
	root, state := t.TempDir(), t.TempDir()
	directoryFile, pidfile := filepath.Join(state, "attempt"), filepath.Join(state, "descendant.pid")
	command := scratchFixtureCommand("owner", root, directoryFile, pidfile)
	parent := exec.Command(command.Path, command.Args...)
	parent.Env = append(os.Environ(), command.Env...)
	parent.Stderr = os.Stderr
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
	var pid int
	deadline := time.Now().Add(5 * time.Second)
	for pid == 0 {
		raw, _ := os.ReadFile(pidfile)
		pid, _ = strconv.Atoi(string(raw))
		if time.Now().After(deadline) {
			t.Fatal("descendant did not start")
		}
		time.Sleep(time.Millisecond)
	}
	stop := func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			var status syscall.WaitStatus
			_, _ = syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
			if errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Error("fixture descendant did not stop/reap")
	}
	defer stop()
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = parent.Wait()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("descendant did not survive: %v", err)
	}
	if m, err := Open(Config{Root: root, MaxBytes: 100000}); !errors.Is(err, ErrInUse) {
		if m != nil {
			_ = m.Close()
		}
		t.Fatalf("scratch admitted while orphan producer holds files: %v", err)
	}
	directory, err := os.ReadFile(directoryFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(string(directory), "source.cache")); err != nil {
		t.Fatal("input removed before orphan stopped")
	}
	stop()
	m, err := Open(Config{Root: root, MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if n, err := m.Reclaim(); err != nil || n != 1 {
		t.Fatalf("quiescent cache not reclaimed: %d %v", n, err)
	}
	if _, err := os.Stat(string(directory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("abandoned attempt remains")
	}
}

func TestReclaimKeepsUnprovenCachesAndRefusesLiveAttempts(t *testing.T) {
	root := t.TempDir()
	old := abandonedAttempt(t, root)
	legacy := filepath.Join(root, "unproven", "source.cache")
	if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("unknown producer"), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := Open(Config{Root: root, MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	live, err := m.Begin(domain.NewUUID(), 2, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := live.Context(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Reclaim(); !errors.Is(err, ErrInUse) {
		t.Fatalf("reclaimed with live attempts: %v", err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("startup precondition bypassed")
	}
	if err := live.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := m.Reclaim(); err != nil || n != 1 {
		t.Fatalf("reclaim: %d %v", n, err)
	}
	if got, err := os.ReadFile(legacy); err != nil || string(got) != "unknown producer" {
		t.Fatal("unproven cache removed")
	}
	if _, err := live.Context(context.Background()); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed guard context accepted: %v", err)
	}
}

func TestReclaimPreflightsAllFilesBeforeRemovingAnything(t *testing.T) {
	root := t.TempDir()
	old := abandonedAttempt(t, root)
	unsafe := abandonedAttempt(t, root)
	foreign := filepath.Join(t.TempDir(), "foreign")
	if err := os.WriteFile(foreign, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(unsafe, "output.part")); err != nil {
		t.Fatal(err)
	}
	m, err := Open(Config{Root: root, MaxBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if _, err := m.Reclaim(); err == nil {
		t.Fatal("unsafe scratch accepted")
	}
	for _, path := range []string{old, unsafe, foreign} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("preflight failure removed %s: %v", path, err)
		}
	}
}
