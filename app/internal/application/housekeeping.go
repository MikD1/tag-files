package application

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/mikd1/tag-files/app/internal/operations"
)

// Only operational receipts and navigation snapshots have retention. This
// loop never expires upload sessions, inputs, jobs or independent cleanup.
func (a *App) housekeep(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for ctx.Err() == nil {
		prune, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := a.db.Write(prune, func(tx *sql.Tx) error { return operations.Prune(prune, tx, time.Now()) })
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Operational metadata pruning failed; will retry", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
