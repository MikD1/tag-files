// Package capacity owns cross-volume admission. SQLite working state supplies
// future demand; authenticated storage and local statfs supply observations.
package capacity

import (
	"github.com/mikd1/tag-files/app/internal/diskspace"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/scratch"
)

var ErrInsufficientStorage = scratch.ErrInsufficientStorage
var ErrUnavailable = diskspace.ErrUnavailable

type plan struct{ Incoming, Working, Scratch int64 }
type Extra struct{ Incoming, Working, Scratch, PartBytes int64 }

func add(a, b int64) (int64, error) {
	if a < 0 || b < 0 || a > domain.MaxSafeInteger || b > domain.MaxSafeInteger-a {
		return 0, ErrInsufficientStorage
	}
	return a + b, nil
}

func evaluate(p plan, s scratch.Usage, data, storage diskspace.Snapshot, reserve, inodes int64) error {
	if p.Incoming < 0 || p.Working < 0 || p.Scratch < 0 || s.UsedBytes < s.ActiveBytes || s.ActiveBytes < 0 || s.AllocatedActiveBytes < 0 || s.AllocatedActiveBytes > s.ActiveBytes || s.ReservedBytes < 0 || reserve < 0 || inodes < 0 {
		return ErrUnavailable
	}
	active, err := add(s.ActiveBytes, s.ReservedBytes)
	if err != nil {
		return err
	}
	peak := max(p.Scratch, active)
	quota, err := add(s.UsedBytes-s.ActiveBytes, peak)
	if err != nil || quota > s.QuotaBytes {
		return ErrInsufficientStorage
	}
	storageNeed, err := add(p.Incoming, p.Working)
	if err != nil {
		return err
	}
	spaces := []diskspace.Snapshot{data, storage, s.Space}
	needs := []int64{0, storageNeed, max(int64(0), peak-s.AllocatedActiveBytes)}
	reserves := []int64{reserve, reserve, max(reserve, s.MinFreeBytes)}
	shared := false
	for _, item := range spaces {
		if !diskspace.Valid(item) || item.AvailableInodes == nil {
			return ErrUnavailable
		}
		shared = shared || !item.Independent
	}
	type group struct{ free, inodes, need, reserve int64 }
	groups := map[string]group{}
	for i, space := range spaces {
		key := space.FileSystemID
		if shared {
			key = "unknown-shared-backing"
		}
		g, exists := groups[key]
		if !exists {
			g.free, g.inodes = space.AvailableBytes, *space.AvailableInodes
		}
		g.free, g.inodes = min(g.free, space.AvailableBytes), min(g.inodes, *space.AvailableInodes)
		g.need, err = add(g.need, needs[i])
		if err != nil {
			return err
		}
		g.reserve = max(g.reserve, reserves[i])
		groups[key] = g
	}
	for _, g := range groups {
		if g.inodes < inodes || g.free < g.reserve || g.need > g.free-g.reserve {
			return ErrInsufficientStorage
		}
	}
	return nil
}
