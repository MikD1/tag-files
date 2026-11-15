// Package operations owns durable command receipts and their bounded retention.
// Upload lifetime and cleanup lifetime are independent and never age-based.
package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

const Retention = 7 * 24 * time.Hour

var (
	ErrKeyConflict      = errors.New("idempotency key belongs to a different request")
	ErrOperationExpired = errors.New("operation key has expired; check the resource before creating a new command")
)

type Request struct {
	Key, Method, Route string
	Body               json.RawMessage
}
type Result struct {
	Status   int             `json:"status"`
	Body     json.RawMessage `json:"body"`
	ETag     string          `json:"etag,omitempty"`
	Location string          `json:"location,omitempty"`
	Problem  bool            `json:"problem,omitempty"`
}
type Operation struct {
	ID, State            string
	Result               Result
	CreatedAt, ExpiresAt time.Time
}

func decode(row dbgen.OperationResult) (Operation, error) {
	var result Result
	if err := json.Unmarshal([]byte(row.ResultJson), &result); err != nil {
		return Operation{}, err
	}
	created, err := time.Parse(time.RFC3339Nano, row.CreatedAt)
	if err != nil {
		return Operation{}, err
	}
	expires, err := time.Parse(time.RFC3339Nano, row.ExpiresAt)
	if err != nil {
		return Operation{}, err
	}
	if result.Status != int(row.HttpStatus) {
		return Operation{}, fmt.Errorf("operation status mismatch")
	}
	return Operation{ID: row.OperationID, State: row.State, Result: result, CreatedAt: created, ExpiresAt: expires}, nil
}

func Get(ctx context.Context, db *sql.DB, id string, now time.Time) (Operation, error) {
	if err := domain.ValidateUUID(id); err != nil {
		return Operation{}, domain.Invalid("id", "Expected a UUID")
	}
	row, err := dbgen.New(db).GetOperation(ctx, strings.ToLower(id))
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, domain.ErrNotFound
	}
	if err != nil {
		return Operation{}, err
	}
	op, err := decode(row)
	if err != nil {
		return Operation{}, err
	}
	if !now.Before(op.ExpiresAt) {
		return Operation{}, domain.ErrNotFound
	}
	return op, nil
}

func requestHash(req Request) (string, string, error) {
	if err := domain.ValidateUUID(req.Key); err != nil {
		return "", "", domain.Invalid("Idempotency-Key", "Expected a UUID")
	}
	req.Key = strings.ToLower(req.Key)
	if req.Method == "" || req.Route == "" {
		return "", "", fmt.Errorf("command scope is required")
	}
	if len(req.Body) == 0 {
		req.Body = json.RawMessage(`null`)
	}
	canonical, err := json.Marshal(struct {
		Method, Route string
		Body          json.RawMessage
	}{req.Method, req.Route, req.Body})
	if err != nil {
		return "", "", err
	}
	hash := sha256.Sum256(canonical)
	return req.Key, hex.EncodeToString(hash[:]), nil
}

// Inspect avoids taking the writer or doing external admission for a known
// command. Execute still checks again atomically before changing domain state.
func Inspect(ctx context.Context, db *sql.DB, req Request, now time.Time) (Operation, bool, error) {
	key, digest, err := requestHash(req)
	if err != nil {
		return Operation{}, false, err
	}
	row, err := dbgen.New(db).GetOperation(ctx, key)
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	if row.RequestHash != digest {
		return Operation{}, true, ErrKeyConflict
	}
	op, err := decode(row)
	if err == nil && !now.Before(op.ExpiresAt) {
		err = ErrOperationExpired
	}
	return op, true, err
}

// Execute commits command metadata and its replayable HTTP result together.
// The callback performs SQLite work only. An explicit 4xx/5xx Result rolls back
// callback mutations to a savepoint, then retains the failure outcome; an
// unexpected Go error aborts the whole transaction. 202 must carry a durable
// intent for later IO, never start network/process work inside this callback.
func Execute(ctx context.Context, s *store.Store, req Request, now time.Time, fn func(*sql.Tx) (Result, error)) (result Result, err error) {
	sampled := time.Now()
	key, digest, err := requestHash(req)
	if err != nil {
		return result, err
	}
	err = s.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		row, err := q.GetOperation(ctx, key)
		if err == nil {
			if row.RequestHash != digest {
				return ErrKeyConflict
			}
			op, err := decode(row)
			if err != nil {
				return err
			}
			// The request clock may have been sampled before waiting for the
			// serialized writer. Retention cannot be extended by that wait.
			if !now.Add(time.Since(sampled)).Before(op.ExpiresAt) {
				return ErrOperationExpired
			}
			result = op.Result
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SAVEPOINT command_changes`); err != nil {
			return err
		}
		result, err = fn(tx)
		if err != nil {
			return err
		}
		if result.Status < 200 || result.Status > 599 {
			return fmt.Errorf("invalid command result status")
		}
		if len(result.Body) == 0 {
			result.Body = json.RawMessage(`null`)
		}
		state := "completed"
		if result.Status == 202 {
			state = "pending"
		}
		if result.Status >= 400 {
			state = "failed"
			if _, err := tx.ExecContext(ctx, `ROLLBACK TO command_changes`); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `RELEASE command_changes`); err != nil {
			return err
		}
		raw, err := json.Marshal(result)
		if err != nil {
			return err
		}
		return q.CreateOperation(ctx, dbgen.CreateOperationParams{OperationID: key, RequestHash: digest, State: state, HttpStatus: int64(result.Status), ResultJson: string(raw), CreatedAt: store.Timestamp(now), ExpiresAt: store.Timestamp(now.Add(Retention))})
	})
	return result, err
}

// Prune runs explicitly as operational housekeeping, never on GET. No session,
// input, job or cleanup receipt is eligible for this retention policy.
func Prune(ctx context.Context, tx *sql.Tx, now time.Time) error {
	q := dbgen.New(tx)
	if _, err := q.PruneOperations(ctx, store.Timestamp(now)); err != nil {
		return err
	}
	_, err := q.PruneNavigationContexts(ctx, store.Timestamp(now))
	return err
}
