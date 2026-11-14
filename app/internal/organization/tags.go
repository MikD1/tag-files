// Package organization owns tag identity, bound saved searches and collections.
// Mutations receive a transaction from their command owner, allowing metadata
// and HTTP idempotency results to commit together without nested transactions.
package organization

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func namedTags(ctx context.Context, q *dbgen.Queries) ([]search.NamedTag, error) {
	tags, err := q.ListTags(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]search.NamedTag, len(tags))
	for i, tag := range tags {
		result[i] = search.NamedTag{ID: tag.ID, Name: tag.Name}
	}
	return result, nil
}

func CreateTag(ctx context.Context, tx *sql.Tx, name string) (dbgen.Tag, error) {
	if err := search.ValidateTagName(name); err != nil {
		return dbgen.Tag{}, domain.Invalid("name", err.Error())
	}
	q := dbgen.New(tx)
	tag, err := q.CreateTag(ctx, name)
	if err != nil {
		return tag, err
	}
	tags, err := namedTags(ctx, q)
	if err != nil {
		return tag, err
	}
	queries, err := q.ListSavedSearches(ctx)
	if err != nil {
		return tag, err
	}
	for _, saved := range queries {
		if saved.ValidationState == "needs_review" {
			continue
		}
		ast, err := SavedAST(saved)
		if err != nil {
			return tag, err
		}
		expanded, changed, err := search.ExpandOnCreate(ast, name, tags)
		if err != nil {
			return tag, err
		}
		if !changed {
			continue
		}
		if _, err := updateBoundSearch(ctx, q, saved, expanded, tags); err != nil {
			return tag, err
		}
	}
	return tag, q.AdvanceSearchGeneration(ctx)
}

func RenameTag(ctx context.Context, tx *sql.Tx, id, version int64, name string) (dbgen.Tag, error) {
	if err := domain.ValidateID(id); err != nil {
		return dbgen.Tag{}, err
	}
	if err := search.ValidateTagName(name); err != nil {
		return dbgen.Tag{}, domain.Invalid("name", err.Error())
	}
	q := dbgen.New(tx)
	before, err := q.GetTag(ctx, id)
	if err != nil {
		return dbgen.Tag{}, notFound(err)
	}
	if before.Version != version {
		return dbgen.Tag{}, domain.ErrStaleVersion
	}
	tag, err := q.RenameTag(ctx, dbgen.RenameTagParams{ID: id, Version: version, Name: name})
	if err != nil {
		return tag, err
	}
	tags, err := namedTags(ctx, q)
	if err != nil {
		return tag, err
	}
	queries, err := q.ListSavedSearches(ctx)
	if err != nil {
		return tag, err
	}
	for _, saved := range queries {
		if saved.ValidationState == "needs_review" {
			continue
		}
		ast, err := SavedAST(saved)
		if err != nil {
			return tag, err
		}
		if !search.UsesTag(ast, id) {
			continue
		}
		if _, err := updateBoundSearch(ctx, q, saved, ast, tags); err != nil {
			return tag, err
		}
	}
	return tag, q.AdvanceSearchGeneration(ctx)
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
