package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type Cancellation struct {
	State            string
	ItemID           int64
	JobID, ReceiptID string
}
type cleanupCandidate struct {
	ID  string `json:"assetId"`
	Key string `json:"key"`
}
type cancellationPlan struct {
	Version        int                `json:"version"`
	LibraryID      string             `json:"libraryId"`
	UploadID       string             `json:"uploadId"`
	IncomingKey    string             `json:"incomingKey"`
	MultipartID    string             `json:"multipartId,omitempty"`
	Candidates     []cleanupCandidate `json:"candidates"`
	ReadyItemID    int64              `json:"readyItemId,omitempty"`
	PrimaryAssetID string             `json:"primaryAssetId,omitempty"`
}

func incomingKey(libraryID, uploadID string) string {
	return "incoming/" + libraryID + "/" + uploadID + "/source"
}
func ownedCandidate(libraryID string, asset dbgen.Asset) bool {
	return domain.ValidateUUID(asset.ID) == nil && asset.Bucket == "tag-files" && !asset.ItemID.Valid && asset.State != domain.AssetPublished && (strings.HasPrefix(asset.ObjectKey, "primary/"+libraryID+"/"+asset.ID+"/") || strings.HasPrefix(asset.ObjectKey, "previews/"+libraryID+"/"+asset.ID+"/"))
}

// Cancel is a SQL command. Session absence and removal of the ingest job are
// permanent publication fences. Independent cleanup and external-write records
// survive, including a Create/Complete/PUT still accepted by storage.
func Cancel(ctx context.Context, tx *sql.Tx, id string, now time.Time) (Cancellation, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Cancellation{}, err
	}
	q := dbgen.New(tx)
	item, err := q.FindReadyItemByUpload(ctx, sql.NullString{String: id, Valid: true})
	if err == nil {
		return Cancellation{State: "ready", ItemID: item.ID}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Cancellation{}, err
	}
	session, err := q.GetUploadSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		receipt, err := q.FindCancellationReceipt(ctx, sql.NullString{String: id, Valid: true})
		if errors.Is(err, sql.ErrNoRows) {
			return Cancellation{State: "absent"}, nil
		}
		if err != nil {
			return Cancellation{}, err
		}
		job, err := q.GetCleanupJob(ctx, "cleanup/"+receipt.ID)
		if err != nil {
			return Cancellation{}, missing(err)
		}
		return Cancellation{State: "cancelled", ReceiptID: receipt.ID, JobID: job.ID}, nil
	}
	if err != nil {
		return Cancellation{}, err
	}
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return Cancellation{}, err
	}
	if session.IncomingKey != incomingKey(library.LibraryID, id) {
		return Cancellation{}, ErrConflict
	}
	assets, err := q.ListUnpublishedUploadAssets(ctx, sql.NullString{String: id, Valid: true})
	if err != nil {
		return Cancellation{}, err
	}
	plan := cancellationPlan{Version: 1, LibraryID: library.LibraryID, UploadID: id, IncomingKey: session.IncomingKey, MultipartID: session.MultipartUploadID.String, Candidates: make([]cleanupCandidate, 0, len(assets))}
	for _, asset := range assets {
		if !ownedCandidate(library.LibraryID, asset) {
			return Cancellation{}, ErrConflict
		}
		plan.Candidates = append(plan.Candidates, cleanupCandidate{ID: asset.ID, Key: asset.ObjectKey})
	}
	payload, err := json.Marshal(plan)
	if err != nil {
		return Cancellation{}, err
	}
	receiptID, jobID, err := enqueueCleanup(ctx, q, "cancel", id, session.Generation, string(payload), now)
	if err != nil {
		return Cancellation{}, err
	}
	if err := q.DeleteUploadIngestJobs(ctx, id); err != nil {
		return Cancellation{}, err
	}
	if err := q.DeleteUploadSession(ctx, id); err != nil {
		return Cancellation{}, err
	}
	return Cancellation{State: "cancelled", JobID: jobID, ReceiptID: receiptID}, nil
}
