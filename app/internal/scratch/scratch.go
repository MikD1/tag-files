// Package scratch owns bounded, isolated, disposable media attempts. S3 remains
// the durable input; a scratch file is never the only authoritative copy.
package scratch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/mikd1/tag-files/app/internal/domain"
)

var ErrInsufficientStorage = errors.New("insufficient scratch storage")
var ErrInUse = errors.New("scratch is owned or has live attempts")

type Config struct {
	Root                   string
	MaxBytes, MinFreeBytes int64
}
type Manager struct {
	mu     sync.Mutex
	config Config
	lock   *os.File
	active map[string]*Attempt
	closed bool
}
type Attempt struct {
	Directory, Source, Output string
	manager                   *Manager
	sourceLimit, outputLimit  int64
	closed                    bool
}

func Open(config Config) (*Manager, error) {
	if config.Root == "" || config.MaxBytes < 1 || config.MaxBytes > domain.MaxSafeInteger || config.MinFreeBytes < 0 {
		return nil, domain.Invalid("scratch", "Invalid scratch budget")
	}
	if err := os.MkdirAll(config.Root, 0700); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	config.Root = root
	lock, err := os.OpenFile(filepath.Join(root, ".scratch.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, ErrInUse
	}
	return &Manager{config: config, lock: lock, active: map[string]*Attempt{}}, nil
}

func (m *Manager) Begin(jobID string, generation, sourceLimit, outputLimit int64) (*Attempt, error) {
	if domain.ValidateUUID(jobID) != nil || generation < 1 || sourceLimit < 1 || outputLimit < 1 || sourceLimit > domain.MaxSafeInteger || outputLimit > domain.MaxSafeInteger {
		return nil, domain.Invalid("scratch", "Invalid media attempt")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, os.ErrClosed
	}
	if sourceLimit > m.config.MaxBytes || outputLimit > m.config.MaxBytes-sourceLimit {
		return nil, ErrInsufficientStorage
	}
	if err := m.check(sourceLimit + outputLimit); err != nil {
		return nil, err
	}
	directory := filepath.Join(m.config.Root, strings.ToLower(jobID), strconv.FormatInt(generation, 10)+"-"+domain.NewUUID())
	if err := os.MkdirAll(filepath.Dir(directory), 0700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, err
	}
	a := &Attempt{Directory: directory, Source: filepath.Join(directory, "source.cache"), Output: filepath.Join(directory, "output.part"), manager: m, sourceLimit: sourceLimit, outputLimit: outputLimit}
	m.active[directory] = a
	return a, nil
}

// check counts retained caches plus reservations not yet materialized in files.
// statfs already excludes written bytes; charge only the outstanding portion
// against physical free space. Unknown/stale files are retained and budgeted.
func (m *Manager) check(extra int64) error {
	var used int64
	liveUsage := make(map[string]int64, len(m.active))
	entries := 0
	err := filepath.WalkDir(m.config.Root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
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
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsafe scratch entry: %s", entry.Name())
		}
		if info.Size() > m.config.MaxBytes-used {
			return ErrInsufficientStorage
		}
		used += info.Size()
		for directory := range m.active {
			if strings.HasPrefix(path, directory+string(filepath.Separator)) {
				liveUsage[directory] += info.Size()
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	outstanding := extra
	for directory, a := range m.active {
		for _, file := range []struct {
			path  string
			limit int64
		}{{a.Source, a.sourceLimit}, {a.Output, a.outputLimit}} {
			info, err := os.Lstat(file.path)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err == nil && (!info.Mode().IsRegular() || info.Size() > file.limit) {
				return ErrInsufficientStorage
			}
		}
		remaining := a.sourceLimit + a.outputLimit - liveUsage[directory]
		if remaining > 0 {
			if remaining > m.config.MaxBytes-outstanding {
				return ErrInsufficientStorage
			}
			outstanding += remaining
		}
	}
	if outstanding > m.config.MaxBytes-used {
		return ErrInsufficientStorage
	}
	free, err := available(m.config.Root)
	if err != nil {
		return err
	}
	if free < m.config.MinFreeBytes || outstanding > free-m.config.MinFreeBytes {
		return ErrInsufficientStorage
	}
	return nil
}

func (a *Attempt) Check() error {
	m := a.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.closed || m.closed {
		return os.ErrClosed
	}
	return m.check(0)
}

// Close is called only after every local producer/reader has returned. It
// never removes another attempt or assumes an expired lease stopped a process.
func (a *Attempt) Close() error {
	m := a.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if a.closed {
		return nil
	}
	if err := os.RemoveAll(a.Directory); err != nil {
		return err
	}
	delete(m.active, a.Directory)
	a.closed = true
	_ = os.Remove(filepath.Dir(a.Directory)) // succeeds only when empty
	return nil
}
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	if len(m.active) > 0 {
		return ErrInUse
	}
	if err := syscall.Flock(int(m.lock.Fd()), syscall.LOCK_UN); err != nil {
		return err
	}
	m.closed = true
	return m.lock.Close()
}
