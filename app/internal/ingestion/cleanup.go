package ingestion

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/cleanup"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrIncompatibleCleanup = cleanup.ErrIncompatibleJob

type cleanupResource struct{ key, kind, id, mpu string }

func cleanupKey(key string) bool {
	if len(key) < 1 || len(key) > 1024 || strings.Contains(key, "\\") {
		return false
	}
	for _, r := range key {
		if r < 33 || r > 126 {
			return false
		}
	}
	for _, s := range strings.Split(key, "/") {
		if s == "" || s == "." || s == ".." {
			return false
		}
	}
	return true
}
func decodeCancellation(receipt dbgen.CleanupReceipt) (cancellationPlan, error) {
	var plan cancellationPlan
	decoder := json.NewDecoder(strings.NewReader(receipt.PayloadJson))
	decoder.DisallowUnknownFields()
	if receipt.PayloadVersion != 1 || (receipt.Reason != "cancel" && receipt.Reason != "ready") || !receipt.UploadID.Valid || decoder.Decode(&plan) != nil || !json.Valid([]byte(receipt.PayloadJson)) || plan.Version != 1 || domain.ValidateUUID(plan.LibraryID) != nil || domain.ValidateUUID(plan.UploadID) != nil || plan.UploadID != receipt.UploadID.String || plan.IncomingKey != incomingKey(plan.LibraryID, plan.UploadID) || receipt.Generation < 1 {
		return plan, ErrIncompatibleCleanup
	}
	if receipt.Reason == "ready" {
		if domain.ValidateID(plan.ReadyItemID) != nil || domain.ValidateUUID(plan.PrimaryAssetID) != nil {
			return plan, ErrIncompatibleCleanup
		}
	} else if plan.ReadyItemID != 0 || plan.PrimaryAssetID != "" {
		return plan, ErrIncompatibleCleanup
	}
	seen := map[string]bool{}
	for _, asset := range plan.Candidates {
		if domain.ValidateUUID(asset.ID) != nil || asset.ID == plan.PrimaryAssetID || seen[asset.ID] || !cleanupKey(asset.Key) || (!strings.HasPrefix(asset.Key, "primary/"+plan.LibraryID+"/"+asset.ID+"/") && !strings.HasPrefix(asset.Key, "previews/"+plan.LibraryID+"/"+asset.ID+"/")) {
			return plan, ErrIncompatibleCleanup
		}
		seen[asset.ID] = true
	}
	return plan, nil
}
func cleanupReceiptID(job dbgen.Job) (string, error) {
	return cleanup.DecodeJob(job)
}

// checkCancellationFence is sampled before removal and again in the final
// transaction. An expired lease is not this proof: the session and ingestion
// job were permanently removed in the cancellation transaction.
func checkCancellationFence(ctx context.Context, q *dbgen.Queries, receipt dbgen.CleanupReceipt, plan cancellationPlan) error {
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return err
	}
	if library.LibraryID != plan.LibraryID {
		return ErrConflict
	}
	if _, err := q.GetUploadSession(ctx, plan.UploadID); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	item, itemErr := q.FindReadyItemByUpload(ctx, receipt.UploadID)
	if itemErr == nil {
		if receipt.Reason != "ready" || item.ID != plan.ReadyItemID || item.PrimaryAssetID.String != plan.PrimaryAssetID {
			return ErrConflict
		}
	} else if !errors.Is(itemErr, sql.ErrNoRows) {
		return itemErr
	}
	if _, err := q.GetIngestionJob(ctx, "ingest/"+plan.UploadID); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	for _, ref := range plan.Candidates {
		asset, err := q.GetCleanupAsset(ctx, ref.ID)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if !ownedCandidate(plan.LibraryID, asset) || asset.UploadID != receipt.UploadID || asset.ObjectKey != ref.Key {
			return ErrConflict
		}
		if asset.JobID.Valid {
			count, err := q.CountJobIdentity(ctx, asset.JobID.String)
			if err != nil {
				return err
			}
			if count != 0 {
				return ErrConflict
			}
		}
	}
	return nil
}
func missingObject(err error) bool {
	var wire *objectstore.Error
	return errors.As(err, &wire) && wire.Status == 404
}

// removeResource never follows a prefix to a different key. Unknown objects or
// MPU keys outside this exact durable ownership record remain untouched.
func removeResource(ctx context.Context, client objectio.ReconciliationClient, resource cleanupResource) error {
	if resource.kind == "upload" {
		mpus, err := client.ListMultipart(ctx, resource.key)
		if err != nil {
			return err
		}
		ids := map[string]bool{}
		if resource.mpu != "" {
			ids[resource.mpu] = true
		}
		for _, mpu := range mpus {
			if mpu.Key == resource.key {
				ids[mpu.UploadID] = true
			}
		}
		for id := range ids {
			if err := client.Abort(ctx, resource.key, id); err != nil {
				return err
			}
		}
		remaining, err := client.ListMultipart(ctx, resource.key)
		if err != nil {
			return err
		}
		for _, mpu := range remaining {
			if mpu.Key == resource.key {
				return objectio.ErrUnsettled
			}
		}
	}
	_, err := client.Head(ctx, resource.key)
	if missingObject(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := client.Delete(ctx, resource.key); err != nil {
		return err
	}
	_, err = client.Head(ctx, resource.key)
	if missingObject(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return objectio.ErrUnsettled
}

// ProcessCleanup executes a claimed cleanup job. Object removal is allowed only
// by the permanent cancellation fence. Live writers drain; uncertain writers
// keep the independent receipt, and a changed epoch requires another exact
// owned-result reconciliation before their intents can retire.
func (c *Coordinator) ProcessCleanup(ctx context.Context, job dbgen.Job, now time.Time) error {
	started := time.Now()
	receiptID, err := cleanupReceiptID(job)
	if err != nil {
		return err
	}
	q := dbgen.New(c.db.Reader())
	receipt, err := q.GetCleanupReceipt(ctx, receiptID)
	if err != nil {
		return missing(err)
	}
	if receipt.Reason == "duplicate_mpu" {
		return c.processExtraMultipart(ctx, job, receipt, now.Add(time.Since(started)))
	}
	plan, err := decodeCancellation(receipt)
	if err != nil {
		return err
	}
	if err := checkCancellationFence(ctx, q, receipt, plan); err != nil {
		return err
	}
	resources := make([]cleanupResource, 0, len(plan.Candidates)+1)
	resources = append(resources, cleanupResource{key: plan.IncomingKey, kind: "upload", id: plan.UploadID, mpu: plan.MultipartID})
	for _, ref := range plan.Candidates {
		resources = append(resources, cleanupResource{key: ref.Key, kind: "asset", id: ref.ID})
	}
	var unsettled bool
	for _, resource := range resources {
		if err := c.io.Drain(ctx, resource.key, ""); err != nil {
			return err
		}
		// Decode before removing anything: an unsupported intent must remain
		// attributable to this resource and must never be treated as settled.
		if _, err := c.io.ListKey(ctx, resource.key, "", 100); err != nil {
			return err
		}
		if err := c.io.RemoveOwned(ctx, func(client objectio.ReconciliationClient) error {
			if err := checkCancellationFence(ctx, dbgen.New(c.db.Reader()), receipt, plan); err != nil {
				return err
			}
			return removeResource(ctx, client, resource)
		}); err != nil {
			return err
		}
		cursor := ""
		for {
			intents, err := c.io.ListKey(ctx, resource.key, cursor, 100)
			if err != nil {
				return err
			}
			for _, intent := range intents {
				if intent.TargetKind != resource.kind || intent.TargetID != resource.id {
					return ErrConflict
				}
				err := c.io.ReconcileAfterRestart(ctx, intent, func(client objectio.ReconciliationClient, current objectio.Intent) error {
					if current.Key != resource.key || current.TargetKind != resource.kind || current.TargetID != resource.id {
						return ErrConflict
					}
					if err := checkCancellationFence(ctx, dbgen.New(c.db.Reader()), receipt, plan); err != nil {
						return err
					}
					return removeResource(ctx, client, resource)
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
	}
	if unsettled {
		return objectio.ErrUnsettled
	}
	return c.db.CommitJob(ctx, job.ID, job.LeaseGeneration, now.Add(time.Since(started)), func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		current, err := q.GetCleanupReceipt(ctx, receipt.ID)
		if err != nil {
			return missing(err)
		}
		if current.Generation != receipt.Generation || current.PayloadVersion != receipt.PayloadVersion || current.PayloadJson != receipt.PayloadJson {
			return ErrConflict
		}
		if err := checkCancellationFence(ctx, q, current, plan); err != nil {
			return err
		}
		for _, resource := range resources {
			count, err := q.CountWritesForKey(ctx, resource.key)
			if err != nil {
				return err
			}
			if count != 0 {
				return objectio.ErrUnsettled
			}
		}
		for _, ref := range plan.Candidates {
			if err := q.DeleteCancelledAsset(ctx, dbgen.DeleteCancelledAssetParams{ID: ref.ID, UploadID: receipt.UploadID}); err != nil {
				return err
			}
		}
		return q.DeleteCleanupReceipt(ctx, receipt.ID)
	})
}
