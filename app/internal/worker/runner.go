// Package worker runs one durable queue consumer. SQLite owns scheduling;
// channels only wake the consumer and no handler outlives Run's return.
package worker

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrRunning = errors.New("the queue already has a running consumer")

type Config struct {
	Lease, Heartbeat, Poll time.Duration
	Now                    func() time.Time
}
type Handler struct {
	Execute      func(context.Context, dbgen.Job, time.Time) error
	Fail         func(context.Context, dbgen.Job, time.Time, string, *time.Time) error
	Interrupt    func(context.Context, dbgen.Job, time.Time) error
	RetryForever bool
}
type Failure struct {
	Code      string
	Temporary bool
	Err       error
}

func (f *Failure) Error() string {
	if f.Err == nil {
		return f.Code
	}
	return f.Code + ": " + f.Err.Error()
}
func (f *Failure) Unwrap() error { return f.Err }

type Runner struct {
	db       *store.Store
	handlers map[domain.JobKind]Handler
	kinds    []domain.JobKind
	config   Config
	running  atomic.Bool
	wake     chan struct{}
}

func New(db *store.Store, handlers map[domain.JobKind]Handler, config Config) (*Runner, error) {
	if config.Lease == 0 {
		config.Lease = time.Minute
	}
	if config.Heartbeat == 0 {
		config.Heartbeat = 10 * time.Second
	}
	if config.Poll == 0 {
		config.Poll = time.Second
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if db == nil || len(handlers) == 0 || config.Lease <= 0 || config.Lease > 24*time.Hour || config.Heartbeat <= 0 || config.Heartbeat >= config.Lease/2 || config.Poll <= 0 || config.Poll > time.Minute {
		return nil, domain.Invalid("worker", "Invalid worker profile")
	}
	r := &Runner{db: db, config: config, handlers: map[domain.JobKind]Handler{}, wake: make(chan struct{}, 1)}
	for kind, h := range handlers {
		if kind != domain.JobIngest && kind != domain.JobPoster && kind != domain.JobCleanup && kind != domain.JobDelete || h.Execute == nil || h.Fail == nil || h.Interrupt == nil {
			return nil, domain.Invalid("worker", "Invalid worker handler")
		}
		r.handlers[kind] = h
		r.kinds = append(r.kinds, kind)
	}
	return r, nil
}
func (r *Runner) Wake() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
func (r *Runner) Run(ctx context.Context) error {
	if !r.running.CompareAndSwap(false, true) {
		return ErrRunning
	}
	defer r.running.Store(false)
	for ctx.Err() == nil {
		ran, err := r.once(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if ran {
			continue
		}
		timer := time.NewTimer(r.config.Poll)
		select {
		case <-ctx.Done():
		case <-r.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
	return nil
}
func (r *Runner) RunOne(ctx context.Context) (bool, error) {
	if !r.running.CompareAndSwap(false, true) {
		return false, ErrRunning
	}
	defer r.running.Store(false)
	return r.once(ctx)
}
func (r *Runner) once(ctx context.Context) (bool, error) {
	job, err := r.db.ClaimJobKinds(ctx, r.kinds, r.config.Now(), r.config.Lease)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	h := r.handlers[job.Type]
	work, cancel := context.WithCancelCause(ctx)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(r.config.Heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-work.Done():
				return
			case <-ticker.C:
				if err := r.db.HeartbeatJob(work, job.ID, job.LeaseGeneration, r.config.Now(), r.config.Lease); err != nil {
					cancel(err)
					return
				}
			}
		}
	}()
	runErr := h.Execute(work, job, r.config.Now())
	cancel(nil)
	<-stopped
	finalize, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if runErr == nil {
		_, err := dbgen.New(r.db.Reader()).GetJob(finalize, job.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		if err != nil {
			return true, err
		}
		runErr = &Failure{Code: "unfinished_job", Err: errors.New("handler returned before committing job completion")}
	}
	if ctx.Err() != nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, store.ErrLeaseLost) || errors.Is(context.Cause(work), store.ErrLeaseLost) {
		err = h.Interrupt(finalize, job, r.config.Now())
	} else {
		failure := &Failure{Code: "processing_failed", Temporary: true, Err: runErr}
		var typed *Failure
		if errors.As(runErr, &typed) {
			failure = typed
		}
		var next *time.Time
		if failure.Temporary && (job.Attempt <= 3 || h.RetryForever) {
			delays := [3]time.Duration{5 * time.Second, 30 * time.Second, 120 * time.Second}
			index := job.Attempt - 1
			if index > 2 {
				index = 2
			}
			at := r.config.Now().Add(delays[index])
			next = &at
		}
		err = h.Fail(finalize, job, r.config.Now(), failure.Code, next)
	}
	if errors.Is(err, store.ErrLeaseLost) {
		return true, nil
	}
	return true, err
}
