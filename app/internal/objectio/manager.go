package objectio

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrBusy = errors.New("external write capacity is busy")
var ErrUnsettled = errors.New("the earlier storage lifetime has not ended")

type liveWrite struct {
	key       string
	operation Operation
	done      chan struct{}
}
type Manager struct {
	db    *store.Store
	base  *objectstore.Client
	owner string
	slots chan struct{}
	mu    sync.Mutex
	live  map[string]liveWrite
}

func New(db *store.Store, base *objectstore.Client, maxParallel int) (*Manager, error) {
	if db == nil || base == nil || maxParallel < 1 || maxParallel > 32 {
		return nil, domain.Invalid("limits", "Invalid external write capacity")
	}
	return &Manager{db: db, base: base, owner: domain.NewUUID(), slots: make(chan struct{}, maxParallel), live: map[string]liveWrite{}}, nil
}
func (m *Manager) acquire(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case m.slots <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}
func (m *Manager) add(id string, r Request) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.live[id] = liveWrite{key: r.Key, operation: r.Operation, done: make(chan struct{})}
}
func (m *Manager) finish(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if write, ok := m.live[id]; ok {
		delete(m.live, id)
		close(write.done)
	}
}

// run admits one synchronous write plus verification reads. prepare does only
// domain SQL: it checks ownership/generation and binds bytes in the same commit
// as the intent. work must use the provided epoch-bound client/request outside
// SQL. work must propagate the write RPC's error: matching bytes do not prove
// an earlier accepted writer has stopped. Ambiguous-result recovery is separate
// and leaves the uncertain intent intact until settlement proof. commit owns
// the fenced metadata transaction after verified success.
func run[T any](ctx context.Context, m *Manager, prepare func(*sql.Tx) (Request, error), work func(*objectstore.Client, Request) (T, error), commit func(T) error) (result T, err error) {
	if err = m.acquire(ctx); err != nil {
		return result, err
	}
	defer func() { <-m.slots }()
	epoch, err := m.base.StorageEpoch(ctx)
	if err != nil {
		return result, err
	}
	bound, err := m.base.AtEpoch(epoch)
	if err != nil {
		return result, err
	}
	id := domain.NewUUID()
	defer m.finish(id)
	var request Request
	err = m.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		request, err = prepare(tx)
		if err != nil {
			return err
		}
		if err := validate(request); err != nil {
			return err
		}
		request.TargetID = strings.ToLower(request.TargetID)
		payload, err := json.Marshal(request.Arguments)
		if err != nil {
			return err
		}
		_, err = dbgen.New(tx).CreateObjectWriteIntent(ctx, dbgen.CreateObjectWriteIntentParams{ID: id, OwnerID: m.owner, TargetKind: request.TargetKind, TargetID: request.TargetID, Generation: request.Generation, Operation: string(request.Operation), ObjectKey: request.Key, StorageEpoch: epoch, PayloadJson: string(payload), CreatedAtUtc: store.Timestamp(time.Now())})
		if err == nil {
			m.add(id, request)
		}
		return err
	})
	if err != nil {
		return result, err
	}
	result, err = work(bound, request)
	if err != nil {
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var wire *objectstore.Error
		if errors.As(err, &wire) && wire.Status == 409 && wire.Code == storageprotocol.EpochMismatchCode {
			return result, errors.Join(err, m.delete(settleCtx, id, m.owner, epoch))
		}
		markErr := m.db.Write(settleCtx, func(tx *sql.Tx) error {
			_, err := dbgen.New(tx).MarkObjectWriteUncertain(settleCtx, dbgen.MarkObjectWriteUncertainParams{ID: id, OwnerID: m.owner})
			return err
		})
		return result, errors.Join(err, markErr)
	}
	// Keep the live registration through metadata commit, so drain includes
	// its acknowledgement. Even a fenced/failed metadata commit cannot make
	// a successfully finished network call become a future writer again.
	metadataErr := commit(result)
	settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return result, errors.Join(metadataErr, m.delete(settleCtx, id, m.owner, epoch))
}

func (m *Manager) delete(ctx context.Context, id, owner, epoch string) error {
	return m.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := dbgen.New(tx).DeleteSettledObjectWrite(ctx, dbgen.DeleteSettledObjectWriteParams{ID: id, OwnerID: owner, StorageEpoch: epoch})
		return err
	})
}

// Drain waits for known live calls through their metadata acknowledgement.
// An all-operation drain also waits for streaming read callbacks.
// The caller must first fence new admission in SQL. Uncertain persisted writes
// are not settled by drain, a timeout, an expired lease or an empty listing.
func (m *Manager) Drain(ctx context.Context, key string, operation Operation) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		m.mu.Lock()
		var waiting []chan struct{}
		for _, write := range m.live {
			if (write.key == key && (operation == "" || write.operation == operation)) || (write.key == "" && operation == "") {
				waiting = append(waiting, write.done)
			}
		}
		m.mu.Unlock()
		if len(waiting) == 0 {
			return nil
		}
		for _, done := range waiting {
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (m *Manager) List(ctx context.Context, after string, limit int) ([]Intent, error) {
	if limit < 1 || limit > 1000 {
		return nil, domain.Invalid("limit", "Intent page must contain 1 to 1000 rows")
	}
	if after != "" && domain.ValidateUUID(after) != nil {
		return nil, domain.Invalid("cursor", "Invalid intent cursor")
	}
	rows, err := dbgen.New(m.db.Reader()).ListObjectWriteIntents(ctx, dbgen.ListObjectWriteIntentsParams{AfterID: strings.ToLower(after), LimitCount: int64(limit)})
	if err != nil {
		return nil, err
	}
	result := make([]Intent, 0, len(rows))
	for _, row := range rows {
		intent, err := decode(row)
		if err != nil {
			return nil, err
		}
		result = append(result, intent)
	}
	return result, nil
}

// ReconcileAfterRestart reloads authoritative immutable work, requires a newer
// live backend, and retires it only after domain-owned result reconciliation.
// An epoch change proves no future acceptance, not the earlier write's outcome.
// reconcile may read results and abort/delete fenced owned resources; new data
// writes require their own Run/intents after this callback returns.
func (m *Manager) ReconcileAfterRestart(ctx context.Context, previous Intent, reconcile func(ReconciliationClient, Intent) error) error {
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer func() { <-m.slots }()
	if domain.ValidateUUID(previous.ID) != nil {
		return domain.Invalid("id", "Invalid write intent identity")
	}
	row, err := dbgen.New(m.db.Reader()).GetObjectWriteIntent(ctx, strings.ToLower(previous.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	intent, err := decode(row)
	if err != nil {
		return err
	}
	epoch, err := m.base.StorageEpoch(ctx)
	if err != nil {
		return err
	}
	if strings.EqualFold(epoch, intent.StorageEpoch) {
		return ErrUnsettled
	}
	bound, err := m.base.AtEpoch(epoch)
	if err != nil {
		return err
	}
	if err := reconcile(bound, intent); err != nil {
		return err
	}
	return m.delete(ctx, intent.ID, intent.OwnerID, intent.StorageEpoch)
}

// ListKey pages only the work relevant to one owned resource. An incompatible
// payload is retained and blocks reconciliation of that resource, rather than
// being silently skipped or discarded.
func (m *Manager) ListKey(ctx context.Context, key, after string, limit int) ([]Intent, error) {
	if !keyValid(key) || (after != "" && domain.ValidateUUID(after) != nil) || limit < 1 || limit > 1000 {
		return nil, domain.Invalid("cursor", "Invalid bounded write-intent page")
	}
	rows, err := dbgen.New(m.db.Reader()).ListObjectWritesForKey(ctx, dbgen.ListObjectWritesForKeyParams{ObjectKey: key, AfterID: strings.ToLower(after), LimitCount: int64(limit)})
	if err != nil {
		return nil, err
	}
	result := make([]Intent, 0, len(rows))
	for _, row := range rows {
		intent, err := decode(row)
		if err != nil {
			return nil, err
		}
		result = append(result, intent)
	}
	return result, nil
}
