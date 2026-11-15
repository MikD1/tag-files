package scratch

import (
	"context"
	"errors"
	"time"
)

// Run owns the guarded context, periodic capacity monitor and final cleanup.
// The callback must synchronously stop/join all readers and media execution.
// Attempt.Close additionally refuses removal while inherited guards are live.
func (a *Attempt) Run(ctx context.Context, fn func(context.Context) error) (err error) {
	defer func() { err = errors.Join(err, a.Close()) }()
	ctx, err = a.Context(ctx)
	if err != nil {
		return err
	}
	work, cancel := context.WithCancelCause(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				if err := a.Check(); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	defer func() { cancel(nil); <-stopped }()
	err = fn(work)
	if err != nil && work.Err() != nil {
		return context.Cause(work)
	}
	return err
}
