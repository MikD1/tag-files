package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// Coordinator joins short durable state transitions with the owned S3 write
// dispatcher. It never keeps a SQL transaction while doing storage I/O.
type Coordinator struct {
	db *store.Store
	io *objectio.Manager
}

func NewCoordinator(db *store.Store, manager *objectio.Manager) (*Coordinator, error) {
	if db == nil || manager == nil {
		return nil, domain.Invalid("storage", "A library and owned write manager are required")
	}
	return &Coordinator{db: db, io: manager}, nil
}

func (c *Coordinator) session(ctx context.Context, id string) (Session, error) {
	result, err := Lookup(ctx, c.db.Reader(), id)
	if err != nil {
		return Session{}, err
	}
	if result.Session == nil {
		return Session{}, ErrConflict
	}
	return *result.Session, nil
}

// EnsureMultipart first inventories this session's exact key. An empty list
// cannot justify repeating a create whose response was lost. Adopting its MPU
// does not retire the independent uncertain write intent.
func (c *Coordinator) EnsureMultipart(ctx context.Context, id string) (Session, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Session{}, err
	}
	session, err := c.session(ctx, id)
	if err != nil {
		return Session{}, err
	}
	if session.State != domain.SessionCreating {
		return session, nil
	}
	var existing []objectstore.Multipart
	err = c.io.Read(ctx, func(reader objectio.Reader) error {
		var err error
		existing, err = reader.ListMultipart(ctx, session.IncomingKey)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	var owned []objectstore.Multipart
	for _, mpu := range existing {
		if mpu.Key == session.IncomingKey {
			owned = append(owned, mpu)
		}
	}
	if len(owned) > 1 {
		// Preserve every owned identity for cleanup reconciliation; never silently
		// choose a different MPU and orphan the others.
		return Session{}, fmt.Errorf("%w: multiple owned multipart identities require reconciliation", ErrConflict)
	}
	if len(owned) == 1 {
		err = c.db.Write(ctx, func(tx *sql.Tx) error { return AttachMultipart(ctx, tx, id, session.Generation, owned[0].UploadID) })
		if err != nil {
			return Session{}, err
		}
		return c.session(ctx, id)
	}
	_, err = c.io.CreateMultipart(ctx, func(tx *sql.Tx) (objectio.Request, error) {
		q := dbgen.New(tx)
		current, err := q.GetUploadSession(ctx, id)
		if err != nil {
			return objectio.Request{}, missing(err)
		}
		if current.State != domain.SessionCreating || current.Generation != session.Generation || current.MultipartUploadID.Valid {
			return objectio.Request{}, ErrConflict
		}
		pending, err := q.CountUploadCreateIntents(ctx, id)
		if err != nil {
			return objectio.Request{}, err
		}
		if pending != 0 {
			return objectio.Request{}, objectio.ErrUnsettled
		}
		return objectio.Request{TargetKind: "upload", TargetID: id, Generation: current.Generation, Operation: objectio.CreateMPU, Key: current.IncomingKey, Arguments: objectio.Arguments{Size: current.ExpectedSize, ContentType: "application/octet-stream"}}, nil
	}, func(mpu string) error {
		ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return c.db.Write(ackCtx, func(tx *sql.Tx) error { return AttachMultipart(ackCtx, tx, id, session.Generation, mpu) })
	})
	if err != nil {
		return Session{}, err
	}
	return c.session(ctx, id)
}

// UploadPart binds the exact geometry/digest and writes its intent in one
// transaction. The request body is read only by the streaming SDK call after
// that commit, with bounded global admission in the manager.
func (c *Coordinator) UploadPart(ctx context.Context, id string, generation int64, binding PartBinding, body io.Reader) (Part, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Part{}, err
	}
	var confirmed Part
	_, err = c.io.UploadPart(ctx, func(tx *sql.Tx) (objectio.Request, error) {
		if _, err := BindPart(ctx, tx, id, generation, binding); err != nil {
			return objectio.Request{}, err
		}
		current, err := dbgen.New(tx).GetUploadSession(ctx, id)
		if err != nil {
			return objectio.Request{}, missing(err)
		}
		return objectio.Request{TargetKind: "upload", TargetID: id, Generation: generation, Operation: objectio.UploadPart, Key: current.IncomingKey, Arguments: objectio.Arguments{MultipartID: current.MultipartUploadID.String, PartNumber: binding.Number, Size: binding.Size, SHA256: binding.SHA256}}, nil
	}, body, func(ack objectstore.Part) error {
		ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		return c.db.Write(ackCtx, func(tx *sql.Tx) error {
			var err error
			confirmed, err = confirmPart(ackCtx, tx, id, generation, ack)
			return err
		})
	})
	return confirmed, err
}

// confirmPart also admits the acknowledgement of a writer drained after the
// receiving->completing fence, until the ordered manifest becomes immutable.
func confirmPart(ctx context.Context, tx *sql.Tx, id string, generation int64, ack objectstore.Part) (Part, error) {
	q := dbgen.New(tx)
	current, err := q.GetUploadSession(ctx, id)
	if err != nil {
		return Part{}, missing(err)
	}
	if current.Generation != generation || (current.State != domain.SessionReceiving && current.State != domain.SessionCompleting) || current.CompletionManifest.Valid {
		return Part{}, ErrConflict
	}
	bound, err := q.GetUploadPart(ctx, dbgen.GetUploadPartParams{SessionID: id, PartNumber: int64(ack.Number)})
	if err != nil {
		return Part{}, missing(err)
	}
	if ack.Size != bound.ExpectedSize || ack.SHA256 != bound.Sha256 || ack.ETag == "" || !utf8.ValidString(ack.ETag) || strings.ContainsAny(ack.ETag, "\x00\r\n") {
		return Part{}, objectstore.ErrIntegrity
	}
	if bound.State != "confirmed" || bound.Etag.String != ack.ETag {
		if err := q.ConfirmUploadPart(ctx, dbgen.ConfirmUploadPartParams{SessionID: id, PartNumber: int64(ack.Number), Etag: sql.NullString{String: ack.ETag, Valid: true}}); err != nil {
			return Part{}, err
		}
		if err := q.BumpUploadVersion(ctx, id); err != nil {
			return Part{}, err
		}
	}
	return Part{PartBinding: PartBinding{Number: ack.Number, Size: ack.Size, SHA256: ack.SHA256}, ETag: ack.ETag, State: "confirmed"}, nil
}

// Refresh restores acknowledgements only for already bound part numbers and
// bytes. The pinned Seaweed profile omits SHA256 in ListParts, so an absent
// checksum is resolved from that binding; a present mismatch is never trusted.
// Full source verification remains mandatory at Complete.
func (c *Coordinator) Refresh(ctx context.Context, id string) (Outcome, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Outcome{}, err
	}
	result, err := Lookup(ctx, c.db.Reader(), id)
	if err != nil {
		return Outcome{}, err
	}
	if result.Session == nil {
		return result, nil
	}
	session := *result.Session
	if !canReconcileParts(session.UploadSession) {
		return result, nil
	}
	var parts []objectstore.Part
	err = c.io.Read(ctx, func(reader objectio.Reader) error {
		var err error
		parts, err = reader.ListParts(ctx, session.IncomingKey, session.MultipartUploadID.String)
		return err
	})
	if err != nil {
		return Outcome{}, err
	}
	err = c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		current, err := readSession(ctx, q, id)
		if err != nil {
			return err
		}
		if current.Generation != session.Generation || current.MultipartUploadID != session.MultipartUploadID || current.IncomingKey != session.IncomingKey {
			return ErrConflict
		}
		if !canReconcileParts(current.UploadSession) {
			return nil
		}
		bindings := make(map[int]Part, len(current.Parts))
		for _, bound := range current.Parts {
			bindings[bound.Number] = bound
		}
		listed := make(map[int]objectstore.Part, len(parts))
		for _, part := range parts {
			bound, ok := bindings[part.Number]
			if !ok || part.Size != bound.Size || (part.SHA256 != "" && part.SHA256 != bound.SHA256) {
				return objectstore.ErrIntegrity
			}
			part.SHA256 = bound.SHA256
			listed[part.Number] = part
		}
		for _, bound := range current.Parts {
			if part, ok := listed[bound.Number]; ok {
				if _, err := confirmPart(ctx, tx, id, current.Generation, part); err != nil {
					return err
				}
			} else if bound.State == "confirmed" {
				if err := q.ResetUploadPartConfirmation(ctx, dbgen.ResetUploadPartConfirmationParams{SessionID: id, PartNumber: int64(bound.Number)}); err != nil {
					return err
				}
				if err := q.BumpUploadVersion(ctx, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return Outcome{}, err
	}
	return Lookup(ctx, c.db.Reader(), id)
}

func canReconcileParts(session dbgen.UploadSession) bool {
	return session.MultipartUploadID.Valid && !session.CompletionManifest.Valid && (session.State == domain.SessionReceiving || session.State == domain.SessionCompleting)
}
