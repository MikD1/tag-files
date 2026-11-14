package catalog

import (
	"context"
	"database/sql"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

const MaxBulkItems = 500

type VersionedItem struct{ ID, Version int64 }

func ReplaceMetadata(ctx context.Context, tx *sql.Tx, items []VersionedItem, tagIDs []int64, collectionID *int64) error {
	return replace(ctx, tx, items, true, tagIDs, collectionID, true, true)
}
func ReplaceTags(ctx context.Context, tx *sql.Tx, itemIDs, tagIDs []int64) error {
	items := make([]VersionedItem, len(itemIDs))
	for i, id := range itemIDs {
		items[i] = VersionedItem{ID: id}
	}
	return replace(ctx, tx, items, false, tagIDs, nil, true, false)
}
func ReplaceCollection(ctx context.Context, tx *sql.Tx, itemIDs []int64, collectionID *int64) error {
	items := make([]VersionedItem, len(itemIDs))
	for i, id := range itemIDs {
		items[i] = VersionedItem{ID: id}
	}
	return replace(ctx, tx, items, false, nil, collectionID, false, true)
}

func replace(ctx context.Context, tx *sql.Tx, items []VersionedItem, checkVersions bool, tagIDs []int64, collectionID *int64, setTags, setCollection bool) error {
	if len(items) < 1 || len(items) > MaxBulkItems {
		return domain.Invalid("items", "Select 1 to 500 items")
	}
	q := dbgen.New(tx)
	seen := map[int64]bool{}
	for _, item := range items {
		if err := domain.ValidateID(item.ID); err != nil {
			return err
		}
		if seen[item.ID] {
			return domain.Invalid("items", "Item IDs must be unique")
		}
		seen[item.ID] = true
		row, err := q.GetItem(ctx, item.ID)
		if err != nil {
			return notFound(err)
		}
		if checkVersions && row.Version != item.Version {
			return domain.ErrStaleVersion
		}
	}
	seen = map[int64]bool{}
	for _, id := range tagIDs {
		if err := domain.ValidateID(id); err != nil {
			return err
		}
		if seen[id] {
			return domain.Invalid("tagIds", "Tag IDs must be unique")
		}
		seen[id] = true
		if _, err := q.GetTag(ctx, id); err != nil {
			return notFound(err)
		}
	}
	collection := sql.NullInt64{}
	if setCollection && collectionID != nil {
		if err := domain.ValidateID(*collectionID); err != nil {
			return err
		}
		if _, err := q.GetCollection(ctx, *collectionID); err != nil {
			return notFound(err)
		}
		collection = sql.NullInt64{Int64: *collectionID, Valid: true}
	}
	for _, item := range items {
		if setTags {
			if err := q.ClearItemTags(ctx, item.ID); err != nil {
				return err
			}
			for _, tagID := range tagIDs {
				if err := q.AddItemTag(ctx, dbgen.AddItemTagParams{ItemID: item.ID, TagID: tagID}); err != nil {
					return err
				}
			}
		}
		var affected int64
		var err error
		if setCollection {
			affected, err = q.SetItemCollection(ctx, dbgen.SetItemCollectionParams{ID: item.ID, CollectionID: collection})
		} else {
			affected, err = q.BumpItemVersion(ctx, item.ID)
		}
		if err != nil {
			return err
		}
		if affected != 1 {
			return domain.ErrNotFound
		}
	}
	return q.AdvanceSearchGeneration(ctx)
}
