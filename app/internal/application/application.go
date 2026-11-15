// Package application owns the one library's startup, admission, queue and
// joined shutdown. Offline migration/backup/restore are not runtime features.
package application

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mikd1/tag-files/app/internal/deletion"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/librarybinding"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/mediaserve"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/scratch"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/worker"
)

var ErrNotReady = errors.New("application is not running or its dependencies are unavailable")
var ErrLifecycle = errors.New("application cannot be served more than once or after close")

type Config struct {
	DataDir         string
	Scratch         scratch.Config
	Storage         objectstore.Config
	Media           media.Config
	Worker          worker.Config
	MaxObjectWrites int
	MaxMediaStreams int
	Version         string
	ShutdownTimeout time.Duration
}

type App struct {
	db       *store.Store
	scratch  *scratch.Manager
	storage  *objectstore.Client
	worker   *worker.Runner
	handler  http.Handler
	timeout  time.Duration
	mu       sync.Mutex
	started  bool
	closed   bool
	runCtx   context.Context
	cancel   context.CancelFunc
	requests sync.WaitGroup
	done     chan struct{}
	once     sync.Once
	closeErr error
}

func contains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func canonicalRoot(directory string) (string, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// Open completes every startup gate before exposing any handler or starting a
// worker. Failures release both owners without reclaiming unknown work.
func Open(ctx context.Context, c Config) (_ *App, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.MaxObjectWrites == 0 {
		c.MaxObjectWrites = 2
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 30 * time.Second
	}
	if c.Media == (media.Config{}) {
		c.Media = media.DefaultConfig()
	}
	if c.DataDir == "" || c.Storage.Bucket != "tag-files" || c.MaxObjectWrites < 1 || c.MaxObjectWrites > 32 || c.ShutdownTimeout <= 0 || c.ShutdownTimeout > 5*time.Minute {
		return nil, domain.Invalid("runtime", "Invalid application configuration")
	}
	processor, err := media.New(c.Media)
	if err != nil {
		return nil, err
	}
	base, err := objectstore.New(c.Storage)
	if err != nil {
		return nil, err
	}
	a := &App{done: make(chan struct{}), timeout: c.ShutdownTimeout}
	defer func() {
		if err != nil {
			err = errors.Join(err, a.finish())
		}
	}()
	a.db, err = store.Open(ctx, c.DataDir)
	if err != nil {
		return nil, err
	}
	a.scratch, err = scratch.Open(c.Scratch)
	if err != nil {
		return nil, err
	}
	dataPath, err := canonicalRoot(c.DataDir)
	if err != nil {
		return nil, err
	}
	scratchPath, err := canonicalRoot(c.Scratch.Root)
	if err != nil {
		return nil, err
	}
	if contains(dataPath, scratchPath) || contains(scratchPath, dataPath) {
		return nil, domain.Invalid("runtime", "Library data and scratch must have separate roots")
	}
	control, err := storageprotocol.NewClient(c.Storage.Endpoint, c.Storage.AccessKey, c.Storage.SecretKey, c.Storage.HTTPClient)
	if err != nil {
		return nil, err
	}
	pair, err := librarybinding.Ensure(ctx, a.db, control)
	if err != nil {
		return nil, err
	}
	a.storage, err = base.AtLibrary(pair.StorageID, pair.LibraryID, pair.VolumeID)
	if err != nil {
		return nil, err
	}
	if _, err := ingestion.RecoverInterrupted(ctx, a.db, time.Now()); err != nil {
		return nil, err
	}
	if _, err := a.scratch.Reclaim(); err != nil {
		return nil, err
	}
	manager, err := objectio.New(a.db, a.storage, c.MaxObjectWrites)
	if err != nil {
		return nil, err
	}
	uploads, err := ingestion.NewCoordinator(a.db, manager)
	if err != nil {
		return nil, err
	}
	posters, err := poster.New(a.db, manager)
	if err != nil {
		return nil, err
	}
	deletions, err := deletion.New(a.db, manager)
	if err != nil {
		return nil, err
	}
	streams, err := mediaserve.New(a.db, manager, mediaserve.Config{MaxStreams: c.MaxMediaStreams})
	if err != nil {
		return nil, err
	}
	a.worker, err = worker.New(a.db, map[domain.JobKind]worker.Handler{
		domain.JobIngest:  worker.Ingestion(uploads, processor, a.scratch),
		domain.JobPoster:  worker.Poster(posters, a.db, processor, a.scratch),
		domain.JobCleanup: worker.Cleanup(uploads, a.db, posters),
		domain.JobDelete:  worker.Deletion(deletions, a.db),
	}, c.Worker)
	if err != nil {
		return nil, err
	}
	a.handler, err = httpapi.New(a.db, httpapi.Options{Media: streams, Version: c.Version, Readiness: a.Ready, Admit: a.admit})
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) admit() (func(), bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.started || a.closed || a.runCtx.Err() != nil {
		return nil, false
	}
	a.requests.Add(1)
	return a.requests.Done, true
}

func (a *App) Ready(ctx context.Context) error {
	a.mu.Lock()
	running := a.started && !a.closed && a.runCtx.Err() == nil
	a.mu.Unlock()
	if !running {
		return ErrNotReady
	}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := a.db.Reader().PingContext(check); err != nil {
		return err
	}
	_, err := a.storage.StorageEpoch(check)
	return err
}

// Serve owns the accepted listener, starts exactly one worker, and joins both
// HTTP and worker activity before releasing library/scratch ownership.
func (a *App) Serve(ctx context.Context, listener net.Listener) error {
	a.mu.Lock()
	if a.started || a.closed {
		a.mu.Unlock()
		return ErrLifecycle
	}
	a.started = true
	a.runCtx, a.cancel = context.WithCancel(ctx)
	runCtx, cancel := a.runCtx, a.cancel
	a.mu.Unlock()
	server := &http.Server{Handler: a.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: time.Minute, MaxHeaderBytes: 32 * 1024, BaseContext: func(net.Listener) context.Context { return runCtx }}
	httpDone, workerDone := make(chan error, 1), make(chan error, 1)
	go func() { httpDone <- server.Serve(listener) }()
	go func() { workerDone <- a.worker.Run(runCtx) }()
	var httpErr, workerErr error
	var httpStopped, workerStopped bool
	select {
	case <-runCtx.Done():
	case httpErr = <-httpDone:
		httpStopped = true
	case workerErr = <-workerDone:
		workerStopped = true
	}
	a.mu.Lock()
	a.closed = true
	a.mu.Unlock()
	cancel()
	shutdown, stop := context.WithTimeout(context.Background(), a.timeout)
	shutdownErr := server.Shutdown(shutdown)
	stop()
	if shutdownErr != nil {
		_ = server.Close()
	}
	if !httpStopped {
		httpErr = <-httpDone
	}
	if !workerStopped {
		workerErr = <-workerDone
	}
	// Close stops connections but does not join handlers. Admission is already
	// closed under the same mutex as Add, so Wait cannot race a new registration.
	a.requests.Wait()
	if errors.Is(httpErr, http.ErrServerClosed) {
		httpErr = nil
	}
	return errors.Join(httpErr, workerErr, shutdownErr, a.finish())
}

func (a *App) finish() error {
	a.once.Do(func() {
		var scratchErr, dbErr error
		if a.scratch != nil {
			scratchErr = a.scratch.Close()
		}
		if a.db != nil {
			dbErr = a.db.Close()
		}
		a.mu.Lock()
		a.closed = true
		a.closeErr = errors.Join(scratchErr, dbErr)
		a.mu.Unlock()
		close(a.done)
	})
	return a.closeErr
}

func (a *App) Close() error {
	a.mu.Lock()
	started := a.started
	a.closed = true
	if a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	if !started {
		return a.finish()
	}
	<-a.done
	return a.closeErr
}
