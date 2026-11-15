package capacity

import (
	"context"
	"errors"
	"time"

	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
)

type Observer interface {
	Capacity(context.Context) (storageprotocol.Capacity, error)
}
type Config struct {
	DataDir                            string
	OutputLimit, PosterLimit, PartSize int64
	Parallel                           int
	MinFreeBytes, MinFreeInodes        int64
}
type Manager struct {
	db      *store.Store
	scratch *scratch.Manager
	remote  Observer
	config  Config
	gate    chan struct{}
}

func New(db *store.Store, sandbox *scratch.Manager, remote Observer, c Config) (*Manager, error) {
	if db == nil || sandbox == nil || remote == nil || c.DataDir == "" || c.OutputLimit < 1 || c.OutputLimit > domain.MaxSafeInteger || c.PosterLimit < 1 || c.PosterLimit > domain.MaxSafeInteger || c.PartSize < 1 || c.PartSize > 5*1024*1024*1024 || c.Parallel < 1 || c.Parallel > 32 || c.MinFreeBytes < 0 || c.MinFreeBytes > domain.MaxSafeInteger || c.MinFreeInodes < 4 || c.MinFreeInodes > domain.MaxSafeInteger {
		return nil, domain.Invalid("capacity", "Invalid physical disk budget")
	}
	return &Manager{db: db, scratch: sandbox, remote: remote, config: c, gate: make(chan struct{}, 1)}, nil
}

// Files reserves all future incoming bytes but only one serial conversion peak.
// MPU plus the configured concurrent Complete requests conservatively allow
// that many source extents. Confirmed parts credit already allocated bytes.
func (m *Manager) Files(sizes []int64) (Extra, error) {
	var extra Extra
	for _, size := range sizes {
		if size < 1 {
			return extra, domain.Invalid("size", "File must be nonempty")
		}
		incoming := size
		var err error
		for range m.config.Parallel {
			incoming, err = add(incoming, size)
			if err != nil {
				break
			}
		}
		if err != nil {
			return Extra{}, err
		}
		extra.Incoming, err = add(extra.Incoming, incoming)
		if err != nil {
			return Extra{}, err
		}
		work, err := m.Retry(size)
		if err != nil {
			return Extra{}, err
		}
		extra.Working = max(extra.Working, work.Working)
		extra.Scratch = max(extra.Scratch, work.Scratch)
		extra.PartBytes = max(extra.PartBytes, min(size, m.config.PartSize))
	}
	return extra, nil
}

func (m *Manager) Retry(sourceSize int64) (Extra, error) {
	working, err := add(m.config.OutputLimit, m.config.PosterLimit)
	if err != nil {
		return Extra{}, err
	}
	peak, err := add(sourceSize, m.config.OutputLimit)
	if err != nil {
		return Extra{}, err
	}
	return Extra{Working: working, Scratch: peak}, nil
}

// Admit serializes observation plus the caller's SQL-only creation/retry commit.
// The logical snapshot is read and committed BEFORE filesystem observations:
// concurrently acknowledged writes may reduce demand but cannot create a stale
// free-space credit. It holds no SQL transaction across an external observation.
func (m *Manager) Admit(ctx context.Context, extra Extra, commit func() error) error {
	select {
	case m.gate <- struct{}{}:
		defer func() { <-m.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	p, err := m.load(ctx, extra)
	if err != nil {
		return err
	}
	usage, err := m.scratch.Stats()
	if err != nil {
		return err
	}
	data, err := diskspace.Measure(m.config.DataDir)
	if err != nil {
		return errors.Join(ErrUnavailable, err)
	}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	observed, err := m.remote.Capacity(check)
	cancel()
	if err != nil {
		return errors.Join(ErrUnavailable, err)
	}
	if err := evaluate(p, usage, data, observed.Snapshot, m.config.MinFreeBytes, m.config.MinFreeInodes); err != nil {
		return err
	}
	if commit != nil {
		return commit()
	}
	return nil
}

func (m *Manager) Check(ctx context.Context) error { return m.Admit(ctx, Extra{}, nil) }

func (m *Manager) Metadata(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s, err := diskspace.Measure(m.config.DataDir)
	if err != nil || !diskspace.Valid(s) || s.AvailableInodes == nil {
		return errors.Join(ErrUnavailable, err)
	}
	if s.AvailableBytes < m.config.MinFreeBytes || *s.AvailableInodes < m.config.MinFreeInodes {
		return ErrInsufficientStorage
	}
	return nil
}
