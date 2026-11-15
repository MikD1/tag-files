package capacity

import (
	"context"
	"time"

	"github.com/mikd1/tag-files/app/internal/diskspace"
)

func diagnostic(space diskspace.Snapshot, err error) map[string]any {
	if err != nil || !diskspace.Valid(space) {
		return map[string]any{"status": "unknown", "availableBytes": nil, "availableInodes": nil}
	}
	return map[string]any{"status": "measured", "availableBytes": space.AvailableBytes, "availableInodes": space.AvailableInodes, "filesystemId": space.FileSystemID, "independent": space.Independent}
}

// Diagnostics publishes observations, never an admission promise. A transient
// remote failure leaves its capacity explicitly unknown while SQL is usable.
func (m *Manager) Diagnostics(ctx context.Context) (map[string]any, error) {
	data, err := diskspace.Measure(m.config.DataDir)
	metadata := diagnostic(data, err)
	metadata["reserveBytes"] = m.config.MinFreeBytes
	usage, err := m.scratch.Stats()
	cache := diagnostic(usage.Space, err)
	if err == nil {
		cache["usedBytes"], cache["reservedBytes"], cache["quotaBytes"] = usage.UsedBytes, usage.ReservedBytes+usage.ActiveBytes-usage.AllocatedActiveBytes, usage.QuotaBytes
	}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	remote, err := m.remote.Capacity(check)
	cancel()
	return map[string]any{"metadata": metadata, "scratch": cache, "storage": diagnostic(remote.Snapshot, err)}, nil
}
