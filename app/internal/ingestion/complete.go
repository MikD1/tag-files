package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type frozenPart struct {
	Number int    `json:"partNumber"`
	Size   int64  `json:"size"`
	ETag   string `json:"etag"`
	SHA256 string `json:"sha256"`
}
type frozenManifest struct {
	Version int          `json:"version"`
	Parts   []frozenPart `json:"parts"`
}

func encodeManifest(parts []objectstore.Part) (string, error) {
	if _, _, err := objectio.CompletionIdentity(parts); err != nil {
		return "", err
	}
	manifest := frozenManifest{Version: 1, Parts: make([]frozenPart, len(parts))}
	for i, p := range parts {
		manifest.Parts[i] = frozenPart{Number: p.Number, Size: p.Size, ETag: p.ETag, SHA256: p.SHA256}
	}
	encoded, err := json.Marshal(manifest)
	return string(encoded), err
}
func decodeManifest(encoded string) ([]objectstore.Part, error) {
	var manifest frozenManifest
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || manifest.Version != 1 || !json.Valid([]byte(encoded)) {
		return nil, objectio.ErrIncompatibleIntent
	}
	parts := make([]objectstore.Part, len(manifest.Parts))
	for i, p := range manifest.Parts {
		parts[i] = objectstore.Part{Number: p.Number, Size: p.Size, ETag: p.ETag, SHA256: p.SHA256}
	}
	if _, _, err := objectio.CompletionIdentity(parts); err != nil {
		return nil, objectio.ErrIncompatibleIntent
	}
	return parts, nil
}
func completedSource(result Outcome) bool {
	return result.Session == nil || (result.Session.SourceSha256.Valid && (result.Session.State == domain.SessionQueued || result.Session.State == domain.SessionProcessing || result.Session.State == domain.SessionFailed))
}

// Complete has three durable boundaries: close part admission, freeze the
// drained/reconciled manifest, then queue only a fully verified incoming file.
// An ambiguous previous Complete is recovered by full verification of that
// same manifest. Neither InvalidPart nor a later listing replaces it.
func (c *Coordinator) Complete(ctx context.Context, id string) (Outcome, error) {
	return c.complete(ctx, id, nil)
}
func (c *Coordinator) complete(ctx context.Context, id string, w *completionWork) (Outcome, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Outcome{}, err
	}
	result, err := Lookup(ctx, c.db.Reader(), id)
	if err != nil {
		return Outcome{}, err
	}
	if completedSource(result) {
		return result, nil
	}
	if err := c.checkCapacity(ctx); err != nil {
		return c.completedOrError(ctx, id, err)
	}
	var session Session
	err = c.db.Write(ctx, func(tx *sql.Tx) error {
		if err := checkCompleteOwner(ctx, dbgen.New(tx), id, w); err != nil {
			return err
		}
		current, err := readSession(ctx, dbgen.New(tx), id)
		if err != nil {
			return err
		}
		if current.State != domain.SessionReceiving && current.State != domain.SessionCompleting {
			return ErrConflict
		}
		if !current.MultipartUploadID.Valid {
			return ErrConflict
		}
		if w != nil {
			if err := matchComplete(current, w.payload); err != nil {
				return err
			}
		}
		if current.State == domain.SessionReceiving {
			if int64(len(current.Parts)) != current.PartCount {
				return ErrConflict
			}
			if err := dbgen.New(tx).CloseUploadAdmission(ctx, id); err != nil {
				return err
			}
			current.State = domain.SessionCompleting
		}
		session = current
		return nil
	})
	if err != nil {
		return c.completedOrError(ctx, id, err)
	}
	if err := c.io.Drain(ctx, session.IncomingKey, objectio.UploadPart); err != nil {
		return Outcome{}, err
	}
	wasFrozen := session.CompletionManifest.Valid
	if !wasFrozen {
		if _, err := c.Refresh(ctx, id); err != nil {
			return Outcome{}, err
		}
		var incomplete bool
		err = c.db.Write(ctx, func(tx *sql.Tx) error {
			q := dbgen.New(tx)
			if err := checkCompleteOwner(ctx, q, id, w); err != nil {
				return err
			}
			current, err := readSession(ctx, q, id)
			if err != nil {
				return err
			}
			if current.Generation != session.Generation || current.State != domain.SessionCompleting {
				return ErrConflict
			}
			if w != nil {
				if err := matchComplete(current, w.payload); err != nil {
					return err
				}
			}
			if current.CompletionManifest.Valid {
				session = current
				wasFrozen = true
				return nil
			}
			parts := make([]objectstore.Part, 0, len(current.Parts))
			for _, p := range current.Parts {
				if p.State != "confirmed" {
					incomplete = true
					break
				}
				parts = append(parts, objectstore.Part{Number: p.Number, Size: p.Size, ETag: p.ETag, SHA256: p.SHA256})
			}
			if int64(len(parts)) != current.PartCount {
				incomplete = true
			}
			if incomplete {
				return q.ReopenUploadAdmission(ctx, id)
			}
			encoded, err := encodeManifest(parts)
			if err != nil {
				return err
			}
			if err := q.FreezeUploadManifest(ctx, dbgen.FreezeUploadManifestParams{ID: id, CompletionManifest: sql.NullString{String: encoded, Valid: true}}); err != nil {
				return err
			}
			current.CompletionManifest = sql.NullString{String: encoded, Valid: true}
			session = current
			return nil
		})
		if err != nil {
			return c.completedOrError(ctx, id, err)
		}
		if incomplete {
			return Outcome{}, ErrConflict
		}
	}
	parts, err := decodeManifest(session.CompletionManifest.String)
	if err != nil {
		return Outcome{}, err
	}
	if wasFrozen {
		var verified objectstore.Verified
		err = c.io.Read(ctx, func(reader objectio.Reader) error {
			var err error
			verified, err = reader.VerifyParts(ctx, session.IncomingKey, parts)
			return err
		})
		if err == nil {
			if err := c.queueVerified(ctx, session, verified, w); err != nil {
				return c.completedOrError(ctx, id, err)
			}
			return Lookup(ctx, c.db.Reader(), id)
		}
		var wire *objectstore.Error
		if !errors.As(err, &wire) || wire.Status != 404 {
			return Outcome{}, err
		}
	}
	size, identity, err := objectio.CompletionIdentity(parts)
	if err != nil {
		return Outcome{}, err
	}
	_, err = c.io.Complete(ctx, func(tx *sql.Tx) (objectio.Request, error) {
		q := dbgen.New(tx)
		if err := checkCompleteOwner(ctx, q, id, w); err != nil {
			return objectio.Request{}, err
		}
		current, err := q.GetUploadSession(ctx, id)
		if err != nil {
			return objectio.Request{}, missing(err)
		}
		if current.Generation != session.Generation || current.State != domain.SessionCompleting || current.CompletionManifest != session.CompletionManifest || current.MultipartUploadID != session.MultipartUploadID {
			return objectio.Request{}, ErrConflict
		}
		pending, err := q.CountUploadCompleteIntents(ctx, id)
		if err != nil {
			return objectio.Request{}, err
		}
		if pending != 0 {
			return objectio.Request{}, objectio.ErrUnsettled
		}
		return objectio.Request{TargetKind: "upload", TargetID: id, Generation: current.Generation, Operation: objectio.CompleteMPU, Key: current.IncomingKey, Arguments: objectio.Arguments{Size: size, MultipartID: current.MultipartUploadID.String, ManifestSHA256: identity}}, nil
	}, parts, func(verified objectstore.Verified) error {
		ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return c.queueVerified(ackCtx, session, verified, w)
	})
	if err != nil {
		return c.completedOrError(ctx, id, err)
	}
	return Lookup(ctx, c.db.Reader(), id)
}
func (c *Coordinator) completedOrError(ctx context.Context, id string, original error) (Outcome, error) {
	result, err := Lookup(ctx, c.db.Reader(), id)
	if err == nil && completedSource(result) {
		return result, nil
	}
	return Outcome{}, original
}
func (c *Coordinator) queueVerified(ctx context.Context, session Session, verified objectstore.Verified, work ...*completionWork) error {
	if verified.Key != session.IncomingKey || verified.Size != session.ExpectedSize || !validHash(verified.SHA256) {
		return objectstore.ErrIntegrity
	}
	var w *completionWork
	if len(work) > 0 {
		w = work[0]
	}
	commit := c.db.Write
	if w != nil {
		commit = func(ctx context.Context, fn func(*sql.Tx) error) error {
			return c.db.CommitJob(ctx, w.job.ID, w.job.LeaseGeneration, w.current(), fn)
		}
	}
	return commit(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		if err := checkCompleteOwner(ctx, q, session.ID, w); err != nil {
			return err
		}
		current, err := q.GetUploadSession(ctx, session.ID)
		if err != nil {
			return missing(err)
		}
		if current.Generation != session.Generation || current.IncomingKey != session.IncomingKey || current.CompletionManifest != session.CompletionManifest {
			return ErrConflict
		}
		if current.State == domain.SessionQueued || current.State == domain.SessionProcessing {
			if current.SourceSha256.String != verified.SHA256 {
				return objectstore.ErrIntegrity
			}
			return nil
		}
		if current.State != domain.SessionCompleting {
			return ErrConflict
		}
		payload, err := json.Marshal(struct {
			Version    int    `json:"version"`
			UploadID   string `json:"uploadId"`
			Generation int64  `json:"generation"`
		}{Version: 1, UploadID: current.ID, Generation: current.Generation})
		if err != nil {
			return err
		}
		now := store.Timestamp(time.Now())
		if _, err := q.CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobIngest, SubjectID: current.ID, DedupeKey: "ingest/" + current.ID, PayloadJson: string(payload), CreatedAt: now, NextAttemptAt: now}); err != nil {
			return err
		}
		return q.QueueVerifiedUpload(ctx, dbgen.QueueVerifiedUploadParams{ID: current.ID, SourceSha256: sql.NullString{String: verified.SHA256, Valid: true}})
	})
}
