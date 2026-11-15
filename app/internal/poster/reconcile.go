package poster

import (
	"context"
	"errors"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

// ReconcileWrites preserves attribution after a lost PUT is read-recovered and
// published. Matching bytes alone do not settle a writer at the same epoch.
func (s *Service) ReconcileWrites(ctx context.Context, id string) error {
	if domain.ValidateUUID(id) != nil {
		return ErrConflict
	}
	q := dbgen.New(s.db.Reader())
	a, err := q.GetCleanupAsset(ctx, id)
	if err != nil {
		return err
	}
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return err
	}
	if a.Role != "poster" || a.State != domain.AssetPublished || !a.ItemID.Valid || !a.JobID.Valid || a.LeaseGeneration < 1 || a.Bucket != "tag-files" || a.ProfileVersion != text(Profile) || a.ObjectKey != posterKey(library.LibraryID, a.ID) {
		return ErrConflict
	}
	p := candidatePlan{AssetID: a.ID, Generation: a.LeaseGeneration, Key: a.ObjectKey, Size: a.SizeBytes, SHA256: a.Sha256}
	if err := s.io.Drain(ctx, p.Key, ""); err != nil {
		return err
	}
	if err := s.preflightWrites(ctx, p); err != nil {
		return err
	}
	cursor := ""
	unsettled := false
	for {
		intents, err := s.io.ListKey(ctx, p.Key, cursor, 100)
		if err != nil {
			return err
		}
		for _, i := range intents {
			err := s.io.ReconcileAfterRestart(ctx, i, func(c objectio.ReconciliationClient, current objectio.Intent) error {
				if err := planIntent(p, current); err != nil {
					return err
				}
				fresh, err := q.GetCleanupAsset(ctx, id)
				if err != nil {
					return err
				}
				if fresh.State != domain.AssetPublished || fresh.ItemID != a.ItemID || fresh.ObjectKey != p.Key || fresh.Sha256 != p.SHA256 || fresh.SizeBytes != p.Size || fresh.LeaseGeneration != p.Generation {
					return ErrConflict
				}
				_, err = c.Verify(ctx, p.Key, p.Size, p.SHA256)
				return err
			})
			if errors.Is(err, objectio.ErrUnsettled) {
				unsettled = true
			} else if err != nil {
				return err
			}
			cursor = i.ID
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
