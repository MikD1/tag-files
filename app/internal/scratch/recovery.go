package scratch

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/process"
)

const guardMarker = ".guard-v1"

// Context must be obtained before starting producers and propagated to every
// media command. The marker identifies attempts created under the inherited
// root-lock protocol; older/unmarked caches never acquire this proof by age.
func (a *Attempt) Context(ctx context.Context) (context.Context, error) {
	m := a.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.closed || m.closed {
		return nil, os.ErrClosed
	}
	if a.stopping {
		return nil, ErrInUse
	}
	if a.guard == nil {
		marker, err := os.OpenFile(filepath.Join(a.Directory, guardMarker), os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
		if err != nil {
			return nil, err
		}
		if err := syscall.Flock(int(marker.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			marker.Close()
			return nil, err
		}
		a.guard = marker
		a.lifetime = process.NewLifetime(m.lock, marker)
	}
	return a.lifetime.Context(ctx), nil
}

func lockAttempt(directory string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(directory, guardMarker), os.O_RDWR|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && (!info.Mode().IsRegular() || info.Size() != 0) {
		err = fmt.Errorf("invalid scratch guard: %s", directory)
	}
	if err == nil {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrInUse
		}
		return nil, err
	}
	return file, nil
}

// Reclaim runs before admission while the root lock proves all prior guarded
// producers have closed their inherited descriptors. It preflights the whole
// tree before removing any known disposable attempts. Unknown caches remain
// counted by admission; no job lease, timestamp or quiet file authorizes GC.
func (m *Manager) Reclaim() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return 0, os.ErrClosed
	}
	if len(m.active) != 0 {
		return 0, ErrInUse
	}
	var directories []string
	entries := 0
	err := filepath.WalkDir(m.config.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		entries++
		if entries > 100000 {
			return ErrInsufficientStorage
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe scratch entry: %s", path)
		}
		if entry.Name() != guardMarker {
			return nil
		}
		relative, err := filepath.Rel(m.config.Root, filepath.Dir(path))
		if err != nil {
			return err
		}
		parts := strings.Split(relative, string(filepath.Separator))
		if len(parts) != 2 || domain.ValidateUUID(parts[0]) != nil || strings.ToLower(parts[0]) != parts[0] || info.Size() != 0 {
			return fmt.Errorf("invalid scratch guard: %s", path)
		}
		generation, attemptID, found := strings.Cut(parts[1], "-")
		n, err := strconv.ParseInt(generation, 10, 64)
		if !found || err != nil || n < 1 || strconv.FormatInt(n, 10) != generation || domain.ValidateUUID(attemptID) != nil || strings.ToLower(attemptID) != attemptID {
			return fmt.Errorf("invalid scratch attempt: %s", path)
		}
		directories = append(directories, filepath.Dir(path))
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, directory := range directories {
		files, err := os.ReadDir(directory)
		if err != nil {
			return 0, err
		}
		for _, file := range files {
			if file.IsDir() || (file.Name() != guardMarker && file.Name() != "source.cache" && file.Name() != "output.part") {
				return 0, fmt.Errorf("unknown guarded scratch entry: %s", filepath.Join(directory, file.Name()))
			}
		}
		guard, err := lockAttempt(directory)
		if err != nil {
			return 0, err
		}
		guard.Close()
	}
	removed := 0
	for _, directory := range directories {
		guard, err := lockAttempt(directory)
		if err != nil {
			return removed, err
		}
		err = os.RemoveAll(directory)
		guard.Close()
		if err != nil {
			return removed, err
		}
		removed++
		_ = os.Remove(filepath.Dir(directory)) // only removes an empty job parent
	}
	return removed, nil
}
