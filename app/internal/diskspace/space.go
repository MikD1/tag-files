// Package diskspace measures the filesystem holding an owned root. Measurements
// are observations, not reservations or evidence that remote writes stopped.
package diskspace

import (
	"encoding/hex"
	"errors"
	"strings"

	"github.com/mikd1/tag-files/app/internal/domain"
)

var ErrUnavailable = errors.New("filesystem capacity is unavailable")

type Snapshot struct {
	FileSystemID    string `json:"filesystemId"`
	AvailableBytes  int64  `json:"availableBytes"`
	AvailableInodes *int64 `json:"availableInodes"`
	// Unknown/stacked filesystems may share backing space despite different
	// fsids. Their budgets must conservatively coalesce instead of adding free.
	Independent bool `json:"independent"`
	// RootIdentity is local evidence from the same opened descriptor as statfs.
	// It is never accepted from a remote capacity response.
	RootIdentity string `json:"-"`
}

func Valid(s Snapshot) bool {
	id, err := hex.DecodeString(s.FileSystemID)
	return err == nil && len(id) == 32 && strings.ToLower(s.FileSystemID) == s.FileSystemID && s.AvailableBytes >= 0 && s.AvailableBytes <= domain.MaxSafeInteger && (s.AvailableInodes == nil || *s.AvailableInodes >= 0 && *s.AvailableInodes <= domain.MaxSafeInteger)
}
