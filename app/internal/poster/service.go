package poster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/cleanup"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/media"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrConflict = errors.New("poster publication is no longer permitted")

type Service struct {
	db *store.Store
	io *objectio.Manager
}

func New(db *store.Store, ioManager *objectio.Manager) (*Service, error) {
	if db == nil || ioManager == nil {
		return nil, domain.Invalid("poster", "Poster dependencies are required")
	}
	return &Service{db: db, io: ioManager}, nil
}
func integer(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
func text(s string) sql.NullString  { return sql.NullString{String: s, Valid: true} }
func posterKey(libraryID, assetID string) string {
	return "previews/" + libraryID + "/" + assetID + "/" + Profile + "/poster.jpg"
}
func missingObject(err error) bool {
	var wire *objectstore.Error
	return errors.As(err, &wire) && wire.Status == 404
}

func sourceForLease(ctx context.Context, q *dbgen.Queries, job dbgen.Job, now time.Time) (Payload, dbgen.Asset, error) {
	current, err := q.GetLeasedJob(ctx, dbgen.GetLeasedJobParams{ID: job.ID, Generation: job.LeaseGeneration, Now: text(store.Timestamp(now))})
	if errors.Is(err, sql.ErrNoRows) {
		return Payload{}, dbgen.Asset{}, store.ErrLeaseLost
	}
	if err != nil {
		return Payload{}, dbgen.Asset{}, err
	}
	payload, err := DecodeJob(current)
	if err != nil {
		return payload, dbgen.Asset{}, err
	}
	item, err := q.GetItem(ctx, payload.ItemID)
	if err != nil {
		return payload, dbgen.Asset{}, err
	}
	if item.State != domain.ItemReady || !item.PrimaryAssetID.Valid || item.PrimaryAssetID.String != payload.PrimaryAssetID || (domain.MediaKind(item.MediaKind) != domain.Image && domain.MediaKind(item.MediaKind) != domain.Video) {
		return payload, dbgen.Asset{}, ErrConflict
	}
	asset, err := q.GetCleanupAsset(ctx, payload.PrimaryAssetID)
	if err != nil {
		return payload, asset, err
	}
	if asset.ItemID != integer(item.ID) || asset.Role != "primary" || asset.Bucket != "tag-files" || (asset.State != domain.AssetPublished && asset.State != domain.AssetVerified) || asset.SizeBytes < 1 {
		return payload, asset, ErrConflict
	}
	return payload, asset, nil
}
func candidateForLease(ctx context.Context, q *dbgen.Queries, job dbgen.Job, id string, now time.Time) (Payload, dbgen.Asset, error) {
	payload, _, err := sourceForLease(ctx, q, job, now)
	if err != nil {
		return payload, dbgen.Asset{}, err
	}
	a, err := q.GetCleanupAsset(ctx, id)
	if err != nil {
		return payload, a, err
	}
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return payload, a, err
	}
	if a.ItemID.Valid || a.Role != "poster" || a.JobID != text(job.ID) || a.LeaseGeneration != job.LeaseGeneration || a.Bucket != "tag-files" || a.ObjectKey != posterKey(library.LibraryID, a.ID) || a.ProfileVersion != text(Profile) || (a.State != domain.AssetCandidate && a.State != domain.AssetVerified) || !validOutput(domain.MediaOutput{Kind: domain.MediaKind(a.MediaKind.String), ContentType: a.ContentType, Size: a.SizeBytes, SHA256: a.Sha256, Width: a.Width.Int64, Height: a.Height.Int64, Rotation: a.Rotation.Int64}) || a.DurationTicks.Valid {
		return payload, a, ErrConflict
	}
	return payload, a, nil
}
func validOutput(out domain.MediaOutput) bool {
	return out.ValidatePrimary() == nil && out.Kind == domain.Image && out.ContentType == "image/jpeg" && out.Size <= media.PosterMaxBytes && out.Width <= media.PosterMaxEdge && out.Height <= media.PosterMaxEdge && out.Rotation == 0 && out.DurationTicks == nil
}

// NewCandidate commits both immutable bytes and independent crash cleanup
// before PUT. It never creates an upload session or changes primary metadata.
func (s *Service) NewCandidate(ctx context.Context, job dbgen.Job, out domain.MediaOutput, now time.Time) (dbgen.Asset, error) {
	started := time.Now()
	var asset dbgen.Asset
	if !validOutput(out) {
		return asset, domain.Invalid("output", "Invalid validated poster output")
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		payload, primary, err := sourceForLease(ctx, q, job, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		if _, err := q.GetPublishedPoster(ctx, dbgen.GetPublishedPosterParams{ItemID: integer(payload.ItemID), ProfileVersion: text(Profile)}); err == nil {
			return ErrConflict
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		library, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		id := domain.NewUUID()
		asset, err = q.CreatePosterCandidate(ctx, dbgen.CreatePosterCandidateParams{ID: id, UploadID: primary.UploadID, JobID: text(job.ID), LeaseGeneration: job.LeaseGeneration, ObjectKey: posterKey(library.LibraryID, id), SizeBytes: out.Size, Sha256: out.SHA256, Width: integer(out.Width), Height: integer(out.Height), ProfileVersion: text(Profile)})
		if err != nil {
			return err
		}
		plan := candidatePlan{Version: 1, LibraryID: library.LibraryID, ItemID: payload.ItemID, PrimaryAssetID: payload.PrimaryAssetID, AssetID: id, JobID: job.ID, Generation: job.LeaseGeneration, Key: asset.ObjectKey, Size: out.Size, SHA256: out.SHA256, Profile: Profile}
		body, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		receiptID := domain.NewUUID()
		timestamp := store.Timestamp(now.Add(time.Since(started)))
		if _, err := q.CreatePosterCandidateReceipt(ctx, dbgen.CreatePosterCandidateReceiptParams{ID: receiptID, ItemID: integer(payload.ItemID), PayloadJson: string(body), Generation: job.LeaseGeneration, CreatedAtUtc: timestamp}); err != nil {
			return err
		}
		body, err = json.Marshal(cleanup.Payload{Version: 1, ReceiptID: receiptID})
		if err != nil {
			return err
		}
		_, err = q.CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobCleanup, SubjectID: receiptID, DedupeKey: "cleanup/" + receiptID, PayloadJson: string(body), NextAttemptAt: timestamp, CreatedAt: timestamp})
		return err
	})
	return asset, err
}
func (s *Service) markVerified(ctx context.Context, job dbgen.Job, id string, v objectstore.Verified, now time.Time) (dbgen.Asset, error) {
	started := time.Now()
	var a dbgen.Asset
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		var err error
		_, a, err = candidateForLease(ctx, q, job, id, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		if v.Key != a.ObjectKey || v.Size != a.SizeBytes || v.SHA256 != a.Sha256 {
			return objectstore.ErrIntegrity
		}
		n, err := q.MarkPosterVerified(ctx, id)
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		a.State = domain.AssetVerified
		return nil
	})
	return a, err
}
func (s *Service) Upload(ctx context.Context, job dbgen.Job, id string, body io.Reader, now time.Time) (dbgen.Asset, error) {
	started := time.Now()
	var a dbgen.Asset
	if domain.ValidateUUID(id) != nil {
		return a, ErrConflict
	}
	id = strings.ToLower(id)
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		_, a, err = candidateForLease(ctx, dbgen.New(tx), job, id, now.Add(time.Since(started)))
		return err
	})
	if err != nil {
		return a, err
	}
	var verified objectstore.Verified
	err = s.io.Read(ctx, func(reader objectio.Reader) error {
		var err error
		verified, err = reader.Verify(ctx, a.ObjectKey, a.SizeBytes, a.Sha256)
		return err
	})
	if err == nil {
		return s.markVerified(ctx, job, id, verified, now.Add(time.Since(started)))
	}
	if !missingObject(err) {
		return a, err
	}
	_, err = s.io.Put(ctx, func(tx *sql.Tx) (objectio.Request, error) {
		_, current, err := candidateForLease(ctx, dbgen.New(tx), job, id, now.Add(time.Since(started)))
		if err != nil {
			return objectio.Request{}, err
		}
		return objectio.Request{TargetKind: "asset", TargetID: current.ID, Generation: current.LeaseGeneration, Operation: objectio.PutObject, Key: current.ObjectKey, Arguments: objectio.Arguments{Size: current.SizeBytes, SHA256: current.Sha256, ContentType: current.ContentType}}, nil
	}, body, func(v objectstore.Verified) error {
		ack, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var err error
		a, err = s.markVerified(ack, job, id, v, now.Add(time.Since(started)))
		return err
	})
	return a, err
}
func (s *Service) Publish(ctx context.Context, job dbgen.Job, id string, now time.Time) (dbgen.Asset, error) {
	started := time.Now()
	var asset dbgen.Asset
	if domain.ValidateUUID(id) != nil {
		return asset, ErrConflict
	}
	id = strings.ToLower(id)
	// Replay requires this exact already-published attempt, never an arbitrary
	// poster attached by another generation or an item whose primary changed.
	if a, err := dbgen.New(s.db.Reader()).GetCleanupAsset(ctx, id); err == nil && a.State == domain.AssetPublished && a.Role == "poster" && a.JobID == text(job.ID) && a.LeaseGeneration == job.LeaseGeneration {
		payload, err := DecodeJob(job)
		if err != nil {
			return asset, err
		}
		item, err := dbgen.New(s.db.Reader()).GetItem(ctx, payload.ItemID)
		if err == nil && item.State == domain.ItemReady && item.PrimaryAssetID == text(payload.PrimaryAssetID) && a.ItemID == integer(item.ID) && a.ProfileVersion == text(Profile) {
			return a, nil
		}
	}
	err := s.db.CommitJob(ctx, job.ID, job.LeaseGeneration, now.Add(time.Since(started)), func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		payload, a, err := candidateForLease(ctx, q, job, id, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		if a.State != domain.AssetVerified {
			return ErrConflict
		}
		if _, err := q.GetPublishedPoster(ctx, dbgen.GetPublishedPosterParams{ItemID: integer(payload.ItemID), ProfileVersion: text(Profile)}); err == nil {
			return ErrConflict
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		receipt, err := q.FindPosterCandidateReceipt(ctx, id)
		if err != nil {
			return err
		}
		if _, err := ValidateCleanup(ctx, q, receipt); err != nil {
			return err
		}
		cleanupJob, err := q.GetCleanupJob(ctx, "cleanup/"+receipt.ID)
		if err != nil {
			return err
		}
		if _, err := cleanup.DecodeJob(cleanupJob); err != nil {
			return err
		}
		n, err := q.AttachPublishedPoster(ctx, dbgen.AttachPublishedPosterParams{ID: id, ItemID: integer(payload.ItemID)})
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrConflict
		}
		// posterUrl changes the item representation, so its optimistic version
		// and HTTP ETag must change in this same publication transaction.
		if n, err := q.BumpItemVersion(ctx, payload.ItemID); err != nil {
			return err
		} else if n != 1 {
			return ErrConflict
		}
		if err := q.DeleteReceiptJob(ctx, receipt.ID); err != nil {
			return err
		}
		if err := q.DeleteCleanupReceipt(ctx, receipt.ID); err != nil {
			return err
		}
		a.State, a.ItemID = domain.AssetPublished, integer(payload.ItemID)
		asset = a
		return nil
	})
	return asset, err
}
