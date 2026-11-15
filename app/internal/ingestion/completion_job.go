package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/operations"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type CompletePart struct {
	Number int    `json:"partNumber"`
	ETag   string `json:"etag"`
}
type completePayload struct {
	Version    int            `json:"version"`
	UploadID   string         `json:"uploadId"`
	Generation int64          `json:"generation"`
	Parts      []CompletePart `json:"parts"`
}
type completionWork struct {
	job          dbgen.Job
	payload      completePayload
	now, started time.Time
}

func (w *completionWork) current() time.Time { return w.now.Add(time.Since(w.started)) }

func validCompleteParts(parts []CompletePart) bool {
	if len(parts) < 1 || len(parts) > 10000 {
		return false
	}
	for i, p := range parts {
		if p.Number != i+1 || len(p.ETag) == 0 || len(p.ETag) > 4096 || !utf8.ValidString(p.ETag) || strings.ContainsAny(p.ETag, "\x00\r\n") {
			return false
		}
	}
	return true
}
func decodeCompleteJob(job dbgen.Job) (completePayload, error) {
	var p completePayload
	d := json.NewDecoder(strings.NewReader(job.PayloadJson))
	d.DisallowUnknownFields()
	if job.Type != domain.JobComplete || d.Decode(&p) != nil || !json.Valid([]byte(job.PayloadJson)) || p.Version != 1 || domain.ValidateUUID(p.UploadID) != nil || p.Generation < 1 || p.Generation > domain.MaxSafeInteger || !validCompleteParts(p.Parts) || job.SubjectID != p.UploadID || job.DedupeKey != "complete/"+p.UploadID {
		return p, ErrConflict
	}
	return p, nil
}
func sameCompleteParts(a, b []CompletePart) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func matchComplete(session Session, p completePayload) error {
	if session.ID != p.UploadID || session.Generation != p.Generation || int64(len(p.Parts)) != session.PartCount || len(session.Parts) != len(p.Parts) {
		return ErrConflict
	}
	for i, part := range session.Parts {
		if part.State != "confirmed" || part.Number != p.Parts[i].Number || part.ETag != p.Parts[i].ETag {
			return ErrConflict
		}
	}
	return nil
}

// ScheduleComplete closes admission and commits the requested selectors before
// 202. All object reads, reconciliation and hashing belong to the queue worker.
func ScheduleComplete(ctx context.Context, tx *sql.Tx, id string, generation int64, parts []CompletePart, now time.Time) (Outcome, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Outcome{}, err
	}
	if generation < 1 || generation > domain.MaxSafeInteger || !validCompleteParts(parts) {
		return Outcome{}, ErrConflict
	}
	q := dbgen.New(tx)
	result, err := lookup(ctx, q, id)
	if err != nil || result.State == "ready" {
		return result, err
	}
	session := *result.Session
	p := completePayload{Version: 1, UploadID: id, Generation: generation, Parts: parts}
	if err := matchComplete(session, p); err != nil {
		return Outcome{}, err
	}
	prior, err := q.FindCompleteJob(ctx, "complete/"+id)
	if err == nil {
		stored, err := decodeCompleteJob(prior)
		if err != nil || stored.Generation != generation || !sameCompleteParts(stored.Parts, parts) {
			return Outcome{}, ErrConflict
		}
		return result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, err
	}
	if session.SourceSha256.Valid && (session.State == domain.SessionQueued || session.State == domain.SessionProcessing || session.State == domain.SessionFailed) {
		return result, nil
	}
	if (session.State != domain.SessionReceiving && session.State != domain.SessionCompleting) || !session.MultipartUploadID.Valid {
		return Outcome{}, ErrConflict
	}
	if session.CompletionManifest.Valid {
		frozen, err := decodeManifest(session.CompletionManifest.String)
		if err != nil {
			return Outcome{}, err
		}
		for i, part := range frozen {
			if i >= len(parts) || part.Number != parts[i].Number || part.ETag != parts[i].ETag {
				return Outcome{}, ErrConflict
			}
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return Outcome{}, err
	}
	if err := q.CloseUploadAdmission(ctx, id); err != nil {
		return Outcome{}, err
	}
	if _, err := q.CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobComplete, SubjectID: id, DedupeKey: "complete/" + id, PayloadJson: string(raw), NextAttemptAt: store.Timestamp(now), CreatedAt: store.Timestamp(now)}); err != nil {
		return Outcome{}, err
	}
	return lookup(ctx, q, id)
}

func checkCompleteOwner(ctx context.Context, q *dbgen.Queries, id string, w *completionWork) error {
	if w == nil {
		_, err := q.FindCompleteJob(ctx, "complete/"+id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		return ErrConflict
	}
	current, err := q.GetLeasedJob(ctx, dbgen.GetLeasedJobParams{ID: w.job.ID, Generation: w.job.LeaseGeneration, Now: sql.NullString{String: store.Timestamp(w.current()), Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrLeaseLost
	}
	if err != nil {
		return err
	}
	p, err := decodeCompleteJob(current)
	if err != nil || current.PayloadJson != w.job.PayloadJson || p.UploadID != id {
		return ErrConflict
	}
	return nil
}
func (c *Coordinator) ProcessComplete(ctx context.Context, job dbgen.Job, now time.Time) error {
	p, err := decodeCompleteJob(job)
	if err != nil {
		return err
	}
	w := &completionWork{job: job, payload: p, now: now, started: time.Now()}
	if err := checkCompleteOwner(ctx, dbgen.New(c.db.Reader()), p.UploadID, w); err != nil {
		return err
	}
	_, err = c.recoverSession(ctx, p.UploadID, w)
	return err
}
func completionStatus(code string) int {
	if code == "insufficient_storage" {
		return 507
	}
	if code == "storage_unavailable" || code == "processing_failed" {
		return 503
	}
	return 422
}
func (c *Coordinator) FailComplete(ctx context.Context, job dbgen.Job, now time.Time, code string, retryAt *time.Time) error {
	p, err := decodeCompleteJob(job)
	if err != nil {
		return err
	}
	if len(code) < 1 || len(code) > 100 {
		return domain.Invalid("errorCode", "Invalid worker error code")
	}
	w := &completionWork{job: job, payload: p, now: now, started: time.Now()}
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		if err := checkCompleteOwner(ctx, q, p.UploadID, w); err != nil {
			return err
		}
		session, err := q.GetUploadSession(ctx, p.UploadID)
		if err != nil {
			return missing(err)
		}
		if session.Generation != p.Generation || session.State != domain.SessionCompleting || session.SourceSha256.Valid {
			return ErrConflict
		}
		state, jobState, next := domain.SessionFailed, domain.JobFailed, now
		if retryAt != nil {
			state, jobState, next = domain.SessionCompleting, domain.JobRetryWait, *retryAt
		}
		n, err := q.SetUploadWorkerState(ctx, dbgen.SetUploadWorkerStateParams{ID: p.UploadID, Generation: p.Generation, State: state, ErrorCode: sql.NullString{String: code, Valid: true}})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		n, err = q.FailLeasedJob(ctx, dbgen.FailLeasedJobParams{ID: job.ID, Generation: job.LeaseGeneration, Now: sql.NullString{String: store.Timestamp(w.current()), Valid: true}, State: jobState, NextAttemptAt: store.Timestamp(next), ErrorCode: sql.NullString{String: code, Valid: true}})
		if err != nil {
			return err
		}
		if n != 1 {
			return store.ErrLeaseLost
		}
		if retryAt == nil {
			return operations.FailUpload(ctx, tx, p.UploadID, completionStatus(code), code)
		}
		return nil
	})
}
func (c *Coordinator) InterruptComplete(ctx context.Context, job dbgen.Job, now time.Time) error {
	p, err := decodeCompleteJob(job)
	if err != nil {
		return err
	}
	started := time.Now()
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		n, err := q.ReleaseLeasedJob(ctx, dbgen.ReleaseLeasedJobParams{ID: job.ID, Generation: job.LeaseGeneration, Now: sql.NullString{String: store.Timestamp(now.Add(time.Since(started))), Valid: true}, NextAttemptAt: store.Timestamp(now)})
		if err != nil {
			return err
		}
		if n != 1 {
			return store.ErrLeaseLost
		}
		session, err := q.GetUploadSession(ctx, p.UploadID)
		if err != nil {
			return err
		}
		if session.Generation != p.Generation || session.State != domain.SessionCompleting {
			return ErrConflict
		}
		return nil
	})
}
