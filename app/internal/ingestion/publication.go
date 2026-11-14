package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// PrimaryOutput describes a media worker's already validated output. The media
// module must probe/decode it; these facts never come from browser MIME claims.
type PrimaryOutput struct {
	Kind                          domain.MediaKind
	ContentType, SHA256           string
	Size, Width, Height, Rotation int64
	DurationTicks                 *int64
}

func primaryExtension(kind domain.MediaKind, contentType string) string {
	if kind == domain.Video && contentType == "video/mp4" {
		return "mp4"
	}
	if kind != domain.Image {
		return ""
	}
	switch contentType {
	case "image/jpeg":
		return "jpg"
	case "image/png":
		return "png"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/avif":
		return "avif"
	}
	return ""
}
func (out PrimaryOutput) validate() error {
	if primaryExtension(out.Kind, out.ContentType) == "" || out.Size < 1 || out.Size > domain.MaxSafeInteger || !validHash(out.SHA256) || out.Width < 1 || out.Width > domain.MaxSafeInteger || out.Height < 1 || out.Height > domain.MaxSafeInteger || out.Rotation < -domain.MaxSafeInteger || out.Rotation > domain.MaxSafeInteger || (out.DurationTicks != nil && (*out.DurationTicks < 0 || out.Kind != domain.Video)) {
		return domain.Invalid("output", "Invalid validated primary output descriptor")
	}
	return nil
}
func nullableInt(value *int64) sql.NullInt64 {
	if value == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *value, Valid: true}
}
func intValue(value int64) sql.NullInt64 { return sql.NullInt64{Int64: value, Valid: true} }
func leasedUpload(ctx context.Context, q *dbgen.Queries, job dbgen.Job, now time.Time) (dbgen.UploadSession, error) {
	current, err := q.GetLeasedJob(ctx, dbgen.GetLeasedJobParams{ID: job.ID, Generation: job.LeaseGeneration, Now: sql.NullString{String: store.Timestamp(now), Valid: true}})
	if errors.Is(err, sql.ErrNoRows) {
		return dbgen.UploadSession{}, store.ErrLeaseLost
	}
	if err != nil {
		return dbgen.UploadSession{}, err
	}
	var payload struct {
		Version    int    `json:"version"`
		UploadID   string `json:"uploadId"`
		Generation int64  `json:"generation"`
	}
	decoder := json.NewDecoder(strings.NewReader(current.PayloadJson))
	decoder.DisallowUnknownFields()
	if current.Type != domain.JobIngest || decoder.Decode(&payload) != nil || !json.Valid([]byte(current.PayloadJson)) || payload.Version != 1 || domain.ValidateUUID(payload.UploadID) != nil || payload.Generation < 1 || current.SubjectID != payload.UploadID || current.DedupeKey != "ingest/"+payload.UploadID {
		return dbgen.UploadSession{}, ErrConflict
	}
	session, err := q.GetUploadSession(ctx, payload.UploadID)
	if err != nil {
		return session, missing(err)
	}
	if session.Generation != payload.Generation || (session.State != domain.SessionQueued && session.State != domain.SessionProcessing) || !session.SourceSha256.Valid || !validHash(session.SourceSha256.String) {
		return session, ErrConflict
	}
	return session, nil
}
func primaryForLease(ctx context.Context, q *dbgen.Queries, job dbgen.Job, assetID string, now time.Time) (dbgen.Asset, dbgen.UploadSession, error) {
	session, err := leasedUpload(ctx, q, job, now)
	if err != nil {
		return dbgen.Asset{}, session, err
	}
	asset, err := q.GetCleanupAsset(ctx, assetID)
	if err != nil {
		return asset, session, missing(err)
	}
	if asset.UploadID.String != session.ID || !asset.UploadID.Valid || !asset.JobID.Valid || asset.JobID.String != job.ID || asset.LeaseGeneration != job.LeaseGeneration || asset.Role != "primary" || asset.ItemID.Valid || (asset.State != domain.AssetCandidate && asset.State != domain.AssetVerified) || !asset.MediaKind.Valid {
		return asset, session, ErrConflict
	}
	output := PrimaryOutput{Kind: domain.MediaKind(asset.MediaKind.String), ContentType: asset.ContentType, Size: asset.SizeBytes, SHA256: asset.Sha256, Width: asset.Width.Int64, Height: asset.Height.Int64, Rotation: asset.Rotation.Int64}
	if asset.DurationTicks.Valid {
		output.DurationTicks = &asset.DurationTicks.Int64
	}
	if err := output.validate(); err != nil {
		return asset, session, err
	}
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return asset, session, err
	}
	if !ownedCandidate(library.LibraryID, asset) || asset.ObjectKey != "primary/"+library.LibraryID+"/"+asset.ID+"/media."+primaryExtension(output.Kind, output.ContentType) {
		return asset, session, ErrConflict
	}
	return asset, session, nil
}

// NewPrimary commits immutable expected bytes and output facts before any PUT.
// Every conversion/allocation and every newer lease gets a fresh asset/key.
func (c *Coordinator) NewPrimary(ctx context.Context, job dbgen.Job, output PrimaryOutput, now time.Time) (dbgen.Asset, error) {
	started := time.Now()
	if err := output.validate(); err != nil {
		return dbgen.Asset{}, err
	}
	var asset dbgen.Asset
	err := c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		session, err := leasedUpload(ctx, q, job, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		library, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		id := domain.NewUUID()
		asset, err = q.CreatePrimaryCandidate(ctx, dbgen.CreatePrimaryCandidateParams{ID: id, UploadID: sql.NullString{String: session.ID, Valid: true}, JobID: sql.NullString{String: job.ID, Valid: true}, LeaseGeneration: job.LeaseGeneration, ObjectKey: "primary/" + library.LibraryID + "/" + id + "/media." + primaryExtension(output.Kind, output.ContentType), SizeBytes: output.Size, Sha256: output.SHA256, ContentType: output.ContentType, Width: intValue(output.Width), Height: intValue(output.Height), Rotation: intValue(output.Rotation), MediaKind: sql.NullString{String: string(output.Kind), Valid: true}, DurationTicks: nullableInt(output.DurationTicks)})
		if err != nil {
			return err
		}
		return q.MarkUploadProcessing(ctx, session.ID)
	})
	return asset, err
}
func (c *Coordinator) markPrimaryVerified(ctx context.Context, job dbgen.Job, assetID string, verified objectstore.Verified, now time.Time) (dbgen.Asset, error) {
	started := time.Now()
	var asset dbgen.Asset
	err := c.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		var err error
		asset, _, err = primaryForLease(ctx, q, job, assetID, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		if verified.Key != asset.ObjectKey || verified.Size != asset.SizeBytes || verified.SHA256 != asset.Sha256 {
			return objectstore.ErrIntegrity
		}
		if err := q.MarkPrimaryVerified(ctx, asset.ID); err != nil {
			return err
		}
		asset.State = domain.AssetVerified
		return nil
	})
	return asset, err
}

// UploadPrimary first verifies any existing candidate. Lost-response recovery
// may mark those same bytes verified while its uncertain writer stays durable.
// A mismatching object is never overwritten; publication remains a separate CAS.
func (c *Coordinator) UploadPrimary(ctx context.Context, job dbgen.Job, assetID string, body io.Reader, now time.Time) (dbgen.Asset, error) {
	started := time.Now()
	assetID, err := canonicalID(assetID)
	if err != nil {
		return dbgen.Asset{}, err
	}
	var asset dbgen.Asset
	err = c.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		asset, _, err = primaryForLease(ctx, dbgen.New(tx), job, assetID, now.Add(time.Since(started)))
		return err
	})
	if err != nil {
		return asset, err
	}
	var verified objectstore.Verified
	err = c.io.Read(ctx, func(reader objectio.Reader) error {
		var err error
		verified, err = reader.Verify(ctx, asset.ObjectKey, asset.SizeBytes, asset.Sha256)
		return err
	})
	if err == nil {
		return c.markPrimaryVerified(ctx, job, asset.ID, verified, now.Add(time.Since(started)))
	}
	if !missingObject(err) {
		return asset, err
	}
	_, err = c.io.Put(ctx, func(tx *sql.Tx) (objectio.Request, error) {
		current, _, err := primaryForLease(ctx, dbgen.New(tx), job, asset.ID, now.Add(time.Since(started)))
		if err != nil {
			return objectio.Request{}, err
		}
		return objectio.Request{TargetKind: "asset", TargetID: current.ID, Generation: current.LeaseGeneration, Operation: objectio.PutObject, Key: current.ObjectKey, Arguments: objectio.Arguments{Size: current.SizeBytes, SHA256: current.Sha256, ContentType: current.ContentType}}, nil
	}, body, func(result objectstore.Verified) error {
		ackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		var err error
		asset, err = c.markPrimaryVerified(ackCtx, job, asset.ID, result, now.Add(time.Since(started)))
		return err
	})
	return asset, err
}

// PublishPrimary makes the item visible and removes ingestion work in a single
// leased transaction. Cleanup/poster jobs survive independently of the session.
func (c *Coordinator) PublishPrimary(ctx context.Context, job dbgen.Job, assetID string, now time.Time) (Outcome, error) {
	started := time.Now()
	assetID, err := canonicalID(assetID)
	if err != nil {
		return Outcome{}, err
	}
	if result, err := Lookup(ctx, c.db.Reader(), job.SubjectID); err == nil && result.State == "ready" {
		return result, nil
	}
	var result Outcome
	err = c.db.CommitJob(ctx, job.ID, job.LeaseGeneration, now.Add(time.Since(started)), func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		asset, session, err := primaryForLease(ctx, q, job, assetID, now.Add(time.Since(started)))
		if err != nil {
			return err
		}
		if asset.State != domain.AssetVerified {
			return ErrConflict
		}
		item, err := q.CreatePublishedItem(ctx, dbgen.CreatePublishedItemParams{UploadID: asset.UploadID, MediaKind: asset.MediaKind.String, UploadedAtUtc: store.Timestamp(now.Add(time.Since(started))), CollectionID: session.CollectionID, DurationTicks: asset.DurationTicks})
		if err != nil {
			return err
		}
		if err := q.AttachPublishedPrimary(ctx, dbgen.AttachPublishedPrimaryParams{ID: asset.ID, ItemID: intValue(item.ID)}); err != nil {
			return err
		}
		if err := q.MarkItemReady(ctx, dbgen.MarkItemReadyParams{ID: item.ID, PrimaryAssetID: sql.NullString{String: asset.ID, Valid: true}}); err != nil {
			return err
		}
		if err := q.AdvanceSearchGeneration(ctx); err != nil {
			return err
		}
		library, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return err
		}
		remaining, err := q.ListUnpublishedUploadAssets(ctx, asset.UploadID)
		if err != nil {
			return err
		}
		plan := cancellationPlan{Version: 1, LibraryID: library.LibraryID, UploadID: session.ID, IncomingKey: session.IncomingKey, MultipartID: session.MultipartUploadID.String, Candidates: make([]cleanupCandidate, 0, len(remaining)), ReadyItemID: item.ID, PrimaryAssetID: asset.ID}
		for _, candidate := range remaining {
			if !ownedCandidate(library.LibraryID, candidate) {
				return ErrConflict
			}
			plan.Candidates = append(plan.Candidates, cleanupCandidate{ID: candidate.ID, Key: candidate.ObjectKey})
		}
		payload, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		if _, _, err := enqueueCleanup(ctx, q, "ready", session.ID, session.Generation, string(payload), now.Add(time.Since(started))); err != nil {
			return err
		}
		poster, err := json.Marshal(struct {
			Version        int    `json:"version"`
			ItemID         int64  `json:"itemId"`
			PrimaryAssetID string `json:"primaryAssetId"`
			Profile        string `json:"profile"`
		}{Version: 1, ItemID: item.ID, PrimaryAssetID: asset.ID, Profile: "poster-v1"})
		if err != nil {
			return err
		}
		timestamp := store.Timestamp(now.Add(time.Since(started)))
		if _, err := q.CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: strconv.FormatInt(item.ID, 10), DedupeKey: "poster/" + asset.ID + "/poster-v1", PayloadJson: string(poster), NextAttemptAt: timestamp, CreatedAt: timestamp}); err != nil {
			return err
		}
		if err := q.DeleteUploadSession(ctx, session.ID); err != nil {
			return err
		}
		result = Outcome{State: "ready", ItemID: item.ID}
		return nil
	})
	if err != nil {
		// A queued/processing source is not a successful publication. Only a
		// ready item that won the race can replace this commit error.
		if existing, lookupErr := Lookup(ctx, c.db.Reader(), job.SubjectID); lookupErr == nil && existing.State == "ready" {
			return existing, nil
		}
		return Outcome{}, err
	}
	return result, nil
}

// ReconcilePrimaryWrites settles independent PUT records after a storage epoch
// barrier. Ready does not erase their uncertainty; the immutable asset remains
// the authority even after its original upload session/job has been removed.
func (c *Coordinator) ReconcilePrimaryWrites(ctx context.Context, assetID string) error {
	assetID, err := canonicalID(assetID)
	if err != nil {
		return err
	}
	asset, err := dbgen.New(c.db.Reader()).GetCleanupAsset(ctx, assetID)
	if err != nil {
		return missing(err)
	}
	library, err := dbgen.New(c.db.Reader()).GetLibraryMeta(ctx)
	if err != nil {
		return err
	}
	if !asset.UploadID.Valid || domain.ValidateUUID(asset.UploadID.String) != nil || asset.Role != "primary" || asset.Bucket != "tag-files" || !asset.MediaKind.Valid || asset.ObjectKey != "primary/"+library.LibraryID+"/"+asset.ID+"/media."+primaryExtension(domain.MediaKind(asset.MediaKind.String), asset.ContentType) {
		return ErrConflict
	}
	if err := c.io.Drain(ctx, asset.ObjectKey, ""); err != nil {
		return err
	}
	cursor := ""
	unsettled := false
	for {
		intents, err := c.io.ListKey(ctx, asset.ObjectKey, cursor, 100)
		if err != nil {
			return err
		}
		for _, intent := range intents {
			if intent.TargetKind != "asset" || intent.TargetID != asset.ID || intent.Generation != asset.LeaseGeneration || intent.Operation != objectio.PutObject || intent.Arguments.Size != asset.SizeBytes || intent.Arguments.SHA256 != asset.Sha256 || intent.Arguments.ContentType != asset.ContentType {
				return objectstore.ErrIntegrity
			}
			err := c.io.ReconcileAfterRestart(ctx, intent, func(client objectio.ReconciliationClient, current objectio.Intent) error {
				fresh, err := dbgen.New(c.db.Reader()).GetCleanupAsset(ctx, asset.ID)
				if err != nil {
					return missing(err)
				}
				if fresh.ObjectKey != current.Key || fresh.LeaseGeneration != current.Generation || fresh.SizeBytes != current.Arguments.Size || fresh.Sha256 != current.Arguments.SHA256 || fresh.ContentType != current.Arguments.ContentType {
					return objectstore.ErrIntegrity
				}
				_, err = client.Verify(ctx, fresh.ObjectKey, fresh.SizeBytes, fresh.Sha256)
				if missingObject(err) && !fresh.ItemID.Valid {
					return nil
				}
				return err
			})
			if errors.Is(err, objectio.ErrUnsettled) {
				unsettled = true
			} else if err != nil {
				return err
			}
			cursor = intent.ID
		}
		if len(intents) < 100 {
			break
		}
	}
	if unsettled {
		return objectio.ErrUnsettled
	}
	return nil
}
