package poster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// Enqueue owns the SQL-only, versioned identity of poster work, including
// rebuilding a missing derivative. It never replaces an unknown job codec.
func Enqueue(ctx context.Context, tx *sql.Tx, itemID int64, primaryID string, now time.Time) (dbgen.Job, error) {
	q := dbgen.New(tx)
	item, err := q.GetItem(ctx, itemID)
	if err != nil {
		return dbgen.Job{}, err
	}
	if item.State != domain.ItemReady || item.PrimaryAssetID != text(primaryID) || (domain.MediaKind(item.MediaKind) != domain.Image && domain.MediaKind(item.MediaKind) != domain.Video) {
		return dbgen.Job{}, ErrConflict
	}
	primary, err := q.GetCleanupAsset(ctx, primaryID)
	if err != nil {
		return dbgen.Job{}, err
	}
	if primary.ItemID != integer(itemID) || primary.Role != "primary" || (primary.State != domain.AssetPublished && primary.State != domain.AssetVerified) {
		return dbgen.Job{}, ErrConflict
	}
	key := "poster/" + primaryID + "/" + Profile
	job, err := q.FindPosterJob(ctx, key)
	if err == nil {
		p, err := DecodeJob(job)
		if err != nil {
			return job, err
		}
		if p.ItemID != itemID || p.PrimaryAssetID != primaryID {
			return job, ErrConflict
		}
		return job, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return job, err
	}
	body, err := json.Marshal(Payload{Version: 1, ItemID: itemID, PrimaryAssetID: primaryID, Profile: Profile})
	if err != nil {
		return job, err
	}
	timestamp := store.Timestamp(now)
	return q.CreateJob(ctx, dbgen.CreateJobParams{ID: domain.NewUUID(), Type: domain.JobPoster, SubjectID: strconv.FormatInt(itemID, 10), DedupeKey: key, PayloadJson: string(body), NextAttemptAt: timestamp, CreatedAt: timestamp})
}
