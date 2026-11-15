package ingestion

import (
	"context"
	"errors"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
)

func validateUploadIntent(session Session, intent objectio.Intent) error {
	if intent.TargetKind != "upload" || intent.TargetID != session.ID || intent.Key != session.IncomingKey || intent.Generation > session.Generation {
		return ErrConflict
	}
	args := intent.Arguments
	switch intent.Operation {
	case objectio.CreateMPU:
		if args.Size != session.ExpectedSize {
			return ErrConflict
		}
	case objectio.UploadPart:
		if !session.MultipartUploadID.Valid || args.MultipartID != session.MultipartUploadID.String {
			return ErrConflict
		}
		for _, part := range session.Parts {
			if part.Number == args.PartNumber {
				if part.Size == args.Size && part.SHA256 == args.SHA256 {
					return nil
				}
				return objectstore.ErrIntegrity
			}
		}
		return objectstore.ErrIntegrity
	case objectio.CompleteMPU:
		if !session.MultipartUploadID.Valid || args.MultipartID != session.MultipartUploadID.String || !session.CompletionManifest.Valid {
			return ErrConflict
		}
		parts, err := decodeManifest(session.CompletionManifest.String)
		if err != nil {
			return err
		}
		size, identity, err := objectio.CompletionIdentity(parts)
		if err != nil {
			return err
		}
		if args.Size != size || size != session.ExpectedSize || args.ManifestSHA256 != identity {
			return objectstore.ErrIntegrity
		}
	default:
		return ErrConflict
	}
	return nil
}

// recoverCompletedSource checks the same frozen bytes, never a new ListParts
// manifest. Queued/processing/failed source hashes must still match exactly.
func (c *Coordinator) recoverCompletedSource(ctx context.Context, session Session, client objectio.Reader, work ...*completionWork) error {
	parts, err := decodeManifest(session.CompletionManifest.String)
	if err != nil {
		return err
	}
	verified, err := client.VerifyParts(ctx, session.IncomingKey, parts)
	if err != nil {
		return err
	}
	if verified.Size != session.ExpectedSize || (session.SourceSha256.Valid && session.SourceSha256.String != verified.SHA256) {
		return objectstore.ErrIntegrity
	}
	if session.State == domain.SessionCompleting {
		return c.queueVerified(ctx, session, verified, work...)
	}
	if !session.SourceSha256.Valid {
		return ErrConflict
	}
	return nil
}
func (c *Coordinator) reconcileUploadIntent(ctx context.Context, client objectio.ReconciliationClient, intent objectio.Intent, work ...*completionWork) error {
	session, err := c.session(ctx, intent.TargetID)
	if err != nil {
		return err
	}
	if err := validateUploadIntent(session, intent); err != nil {
		return err
	}
	switch intent.Operation {
	case objectio.CreateMPU:
		_, err := c.inventoryMultipartWith(ctx, session, client)
		return err
	case objectio.UploadPart:
		if session.CompletionManifest.Valid {
			return c.recoverFrozenSource(ctx, session, client, work...)
		}
		parts, err := client.ListParts(ctx, session.IncomingKey, session.MultipartUploadID.String)
		if err != nil {
			return err
		}
		return c.reconcileListedParts(ctx, session, parts)
	case objectio.CompleteMPU:
		return c.recoverFrozenSource(ctx, session, client, work...)
	}
	return ErrConflict
}

func (c *Coordinator) recoverFrozenSource(ctx context.Context, session Session, client objectio.Reader, work ...*completionWork) error {
	err := c.recoverCompletedSource(ctx, session, client, work...)
	if !missingObject(err) || session.State != domain.SessionCompleting || session.SourceSha256.Valid {
		return err
	}
	// The old backend is gone and no assembled object exists. Repeating only
	// this frozen request is permitted if every stored selector still matches.
	frozen, err := decodeManifest(session.CompletionManifest.String)
	if err != nil {
		return err
	}
	listed, err := client.ListParts(ctx, session.IncomingKey, session.MultipartUploadID.String)
	if err != nil {
		return err
	}
	if len(frozen) != len(listed) {
		return objectstore.ErrIntegrity
	}
	for i, part := range listed {
		bound := frozen[i]
		if part.Number != bound.Number || part.Size != bound.Size || part.ETag != bound.ETag || (part.SHA256 != "" && part.SHA256 != bound.SHA256) {
			return objectstore.ErrIntegrity
		}
	}
	return nil
}

// RecoverSession reconciles active work without deleting its source or replacing
// byte/MPU/manifest identities. It returns the current usable outcome together
// with ErrUnsettled when same-epoch unknown writers must still be retained.
func (c *Coordinator) RecoverSession(ctx context.Context, id string) (Outcome, error) {
	return c.recoverSession(ctx, id, nil)
}
func (c *Coordinator) recoverSession(ctx context.Context, id string, w *completionWork) (Outcome, error) {
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
	if err := c.io.Drain(ctx, session.IncomingKey, ""); err != nil {
		return Outcome{}, err
	}
	session, err = c.inventoryMultipart(ctx, session)
	if err != nil {
		return Outcome{}, err
	}
	unsettled := false
	cursor := ""
	for {
		intents, err := c.io.ListKey(ctx, session.IncomingKey, cursor, 100)
		if err != nil {
			return Outcome{}, err
		}
		for _, intent := range intents {
			if intent.TargetKind != "upload" || intent.TargetID != id {
				return Outcome{}, ErrConflict
			}
			err := c.io.ReconcileAfterRestart(ctx, intent, func(client objectio.ReconciliationClient, current objectio.Intent) error {
				return c.reconcileUploadIntent(ctx, client, current, w)
			})
			if errors.Is(err, objectio.ErrUnsettled) {
				unsettled = true
			} else if err != nil {
				return Outcome{}, err
			}
			cursor = intent.ID
		}
		if len(intents) < 100 {
			break
		}
	}
	result, err = Lookup(ctx, c.db.Reader(), id)
	if err != nil {
		return Outcome{}, err
	}
	if result.Session != nil {
		switch result.Session.State {
		case domain.SessionCreating:
			if !unsettled {
				if _, err := c.EnsureMultipart(ctx, id); err != nil {
					return Outcome{}, err
				}
				result, err = Lookup(ctx, c.db.Reader(), id)
			}
		case domain.SessionReceiving:
			result, err = c.Refresh(ctx, id)
		case domain.SessionCompleting:
			result, err = c.complete(ctx, id, w)
			if errors.Is(err, objectio.ErrUnsettled) {
				unsettled = true
				result, err = Lookup(ctx, c.db.Reader(), id)
			}
		}
	}
	if err != nil {
		return Outcome{}, err
	}
	if unsettled {
		return result, objectio.ErrUnsettled
	}
	return result, nil
}
