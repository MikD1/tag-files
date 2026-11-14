package organization

import (
	"context"
	"database/sql"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func CreateCollection(ctx context.Context, tx *sql.Tx, name string) (dbgen.Collection, error) {
	if err := domain.ValidateName(name); err != nil {
		return dbgen.Collection{}, err
	}
	q := dbgen.New(tx)
	row, err := q.CreateCollection(ctx, name)
	if err != nil {
		return row, err
	}
	return row, q.AdvanceSearchGeneration(ctx)
}

func RenameCollection(ctx context.Context, tx *sql.Tx, id, version int64, name string) (dbgen.Collection, error) {
	if err := domain.ValidateID(id); err != nil {
		return dbgen.Collection{}, err
	}
	if err := domain.ValidateName(name); err != nil {
		return dbgen.Collection{}, err
	}
	q := dbgen.New(tx)
	row, err := q.GetCollection(ctx, id)
	if err != nil {
		return row, notFound(err)
	}
	if row.Version != version {
		return row, domain.ErrStaleVersion
	}
	row, err = q.RenameCollection(ctx, dbgen.RenameCollectionParams{ID: id, Version: version, Name: name})
	if err != nil {
		return row, err
	}
	return row, q.AdvanceSearchGeneration(ctx)
}

func DeleteCollection(ctx context.Context, tx *sql.Tx, id, version int64) error {
	if err := domain.ValidateID(id); err != nil {
		return err
	}
	q := dbgen.New(tx)
	row, err := q.GetCollection(ctx, id)
	if err != nil {
		return notFound(err)
	}
	if row.Version != version {
		return domain.ErrStaleVersion
	}
	if err := q.ClearCollectionItems(ctx, sql.NullInt64{Int64: id, Valid: true}); err != nil {
		return err
	}
	if err := q.ClearCollectionUploads(ctx, sql.NullInt64{Int64: id, Valid: true}); err != nil {
		return err
	}
	n, err := q.DeleteCollection(ctx, dbgen.DeleteCollectionParams{ID: id, Version: version})
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrStaleVersion
	}
	return q.AdvanceSearchGeneration(ctx)
}
