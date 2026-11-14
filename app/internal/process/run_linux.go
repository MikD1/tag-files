package process

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

var subreaper struct {
	sync.Once
	err error
}

func prepareSubreaper() error {
	// A container PID 1 already adopts orphans. Explicit subreaping also makes
	// the same lifecycle work under a development host or a test parent. Reap
	// only this runner's process group; never consume another exec.Cmd's child.
	subreaper.Do(func() {
		_, _, errno := syscall.RawSyscall6(syscall.SYS_PRCTL, 36, 1, 0, 0, 0, 0)
		if errno != 0 {
			subreaper.err = errno
		}
	})
	return subreaper.err
}
func reapGroup(pgid int) {
	for {
		var status syscall.WaitStatus
		pid, err := syscall.Wait4(-pgid, &status, syscall.WNOHANG, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || pid <= 0 {
			return
		}
	}
}
func groupExists(pgid int) bool { return syscall.Kill(-pgid, 0) != syscall.ESRCH }
func waitGroup(pgid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		reapGroup(pgid)
		if !groupExists(pgid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(2 * time.Millisecond)
	}
}
func drainGroup(pgid int, limits Limits) error {
	if !groupExists(pgid) {
		return nil
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	if !waitGroup(pgid, limits.GracePeriod) {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		if !waitGroup(pgid, limits.DrainTimeout) {
			return errors.Join(ErrDescendants, errors.New("process group did not drain after KILL"))
		}
	}
	return ErrDescendants
}
func run(ctx context.Context, command Command, limits Limits) (Result, error) {
	if err := prepareSubreaper(); err != nil {
		return Result{ExitCode: -1}, err
	}
	stdout := &capture{limit: limits.StdoutBytes}
	stderr := &capture{limit: limits.StderrBytes, tail: true}
	cmd := exec.Command(command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = append(os.Environ(), command.Env...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = limits.DrainTimeout
	if err := cmd.Start(); err != nil {
		return Result{ExitCode: -1}, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var waitErr, cancelErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		cancelErr = ctx.Err()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		timer := time.NewTimer(limits.GracePeriod)
		select {
		case waitErr = <-done:
			if !timer.Stop() {
				<-timer.C
			}
		case <-timer.C:
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			waitErr = <-done
		}
	}
	groupErr := drainGroup(cmd.Process.Pid, limits)
	result := Result{Stdout: stdout.data, Stderr: stderr.data, StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated, ExitCode: cmd.ProcessState.ExitCode()}
	if result.StdoutTruncated {
		waitErr = errors.Join(waitErr, ErrOutputLimit)
	}
	return result, errors.Join(cancelErr, waitErr, groupErr)
}
