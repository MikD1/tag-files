// Package catalog owns item retrieval and atomic metadata commands.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type Item struct {
	dbgen.Item
	Tags          []dbgen.Tag
	PosterAssetID *string
}

func get(ctx context.Context, q *dbgen.Queries, id int64) (Item, error) {
	raw, err := q.GetItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, domain.ErrNotFound
	}
	if err != nil {
		return Item{}, err
	}
	item := Item{Item: raw}
	item.Tags, err = q.GetItemTags(ctx, id)
	if err != nil {
		return Item{}, err
	}
	poster, err := q.GetItemPoster(ctx, sql.NullInt64{Int64: id, Valid: true})
	if err == nil {
		item.PosterAssetID = &poster
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Item{}, err
	}
	return item, nil
}

func Get(ctx context.Context, db *sql.DB, id int64) (Item, error) {
	if err := domain.ValidateID(id); err != nil {
		return Item{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Item{}, err
	}
	defer tx.Rollback()
	item, err := get(ctx, dbgen.New(tx), id)
	if err != nil {
		return Item{}, err
	}
	return item, tx.Commit()
}

func SetFavorite(ctx context.Context, tx *sql.Tx, id int64, favorite bool) error {
	if err := domain.ValidateID(id); err != nil {
		return err
	}
	q := dbgen.New(tx)
	if _, err := q.GetItem(ctx, id); err != nil {
		return notFound(err)
	}
	value := int64(0)
	if favorite {
		value = 1
	}
	changed, err := q.SetFavorite(ctx, dbgen.SetFavoriteParams{ID: id, Favorite: value})
	if err != nil {
		return err
	}
	if changed == 0 {
		return nil
	}
	return q.AdvanceSearchGeneration(ctx)
}

func RecordView(ctx context.Context, tx *sql.Tx, id int64, eventID string, now time.Time) error {
	if err := domain.ValidateID(id); err != nil {
		return err
	}
	if err := domain.ValidateUUID(eventID); err != nil {
		return err
	}
	q := dbgen.New(tx)
	if _, err := q.GetItem(ctx, id); err != nil {
		return notFound(err)
	}
	inserted, err := q.InsertViewEvent(ctx, dbgen.InsertViewEventParams{ItemID: id, EventID: strings.ToLower(eventID), RecordedAt: store.Timestamp(now)})
	if err != nil {
		return err
	}
	if inserted == 0 {
		return nil
	}
	changed, err := q.IncrementView(ctx, dbgen.IncrementViewParams{ID: id, LastViewAtUtc: sql.NullString{String: store.Timestamp(now), Valid: true}})
	if err != nil {
		return err
	}
	if changed != 1 {
		return domain.ErrNotFound
	}
	return q.AdvanceSearchGeneration(ctx)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
