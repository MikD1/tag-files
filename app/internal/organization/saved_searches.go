package organization

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type NewSavedSearch struct {
	Name, QueryText string
	MediaKind       *domain.MediaKind
}
type SavedSearchPatch struct {
	Name         *string
	QueryText    *string
	MediaKind    *domain.MediaKind
	SetMediaKind bool // distinguishes omitted kind from an explicit NULL
}

func nullableKind(kind *domain.MediaKind) (sql.NullString, error) {
	if kind == nil {
		return sql.NullString{}, nil
	}
	if err := kind.Validate(); err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(*kind), Valid: true}, nil
}

func bindText(ctx context.Context, q *dbgen.Queries, text string) (*search.Expr, error) {
	ast, err := search.Parse(text)
	if err != nil {
		return nil, err
	}
	tags, err := namedTags(ctx, q)
	if err != nil {
		return nil, err
	}
	return search.Bind(ast, tags)
}

func SavedAST(row dbgen.SavedSearch) (*search.Expr, error) {
	if row.ParserVersion != search.ParserVersion || row.ValidationState == "needs_review" {
		return nil, domain.Invalid("savedSearchId", "Saved search requires review")
	}
	var ast search.Expr
	if err := json.Unmarshal([]byte(row.QueryAstJson), &ast); err != nil {
		return nil, fmt.Errorf("invalid stored saved query %d: %w", row.ID, err)
	}
	if err := search.Validate(&ast); err != nil {
		return nil, fmt.Errorf("invalid stored saved query %d: %w", row.ID, err)
	}
	return &ast, nil
}

func CreateSavedSearch(ctx context.Context, tx *sql.Tx, in NewSavedSearch) (dbgen.SavedSearch, error) {
	if err := domain.ValidateName(in.Name); err != nil {
		return dbgen.SavedSearch{}, err
	}
	kind, err := nullableKind(in.MediaKind)
	if err != nil {
		return dbgen.SavedSearch{}, err
	}
	q := dbgen.New(tx)
	ast, err := bindText(ctx, q, in.QueryText)
	if err != nil {
		return dbgen.SavedSearch{}, err
	}
	raw, err := json.Marshal(ast)
	if err != nil {
		return dbgen.SavedSearch{}, err
	}
	row, err := q.CreateSavedSearch(ctx, dbgen.CreateSavedSearchParams{Name: in.Name, QueryText: in.QueryText, QueryAstJson: string(raw), ParserVersion: search.ParserVersion, MediaKind: kind, ValidationState: search.ValidationState(ast)})
	if err != nil {
		return row, err
	}
	return row, q.AdvanceSearchGeneration(ctx)
}

func updateBoundSearch(ctx context.Context, q *dbgen.Queries, row dbgen.SavedSearch, ast *search.Expr, tags []search.NamedTag) (dbgen.SavedSearch, error) {
	text, err := search.Render(ast, tags)
	if err != nil {
		return row, err
	}
	raw, err := json.Marshal(ast)
	if err != nil {
		return row, err
	}
	return q.UpdateSavedSearch(ctx, dbgen.UpdateSavedSearchParams{ID: row.ID, Version: row.Version, Name: row.Name, QueryText: text, QueryAstJson: string(raw), ParserVersion: search.ParserVersion, MediaKind: row.MediaKind, ValidationState: search.ValidationState(ast)})
}

func PatchSavedSearch(ctx context.Context, tx *sql.Tx, id, version int64, patch SavedSearchPatch) (dbgen.SavedSearch, error) {
	if err := domain.ValidateID(id); err != nil {
		return dbgen.SavedSearch{}, err
	}
	q := dbgen.New(tx)
	row, err := q.GetSavedSearch(ctx, id)
	if err != nil {
		return row, notFound(err)
	}
	if row.Version != version {
		return row, domain.ErrStaleVersion
	}
	if patch.Name != nil {
		if err := domain.ValidateName(*patch.Name); err != nil {
			return row, err
		}
		row.Name = *patch.Name
	}
	if patch.SetMediaKind {
		row.MediaKind, err = nullableKind(patch.MediaKind)
		if err != nil {
			return row, err
		}
	}
	if patch.QueryText != nil {
		ast, err := bindText(ctx, q, *patch.QueryText)
		if err != nil {
			return row, err
		}
		raw, err := json.Marshal(ast)
		if err != nil {
			return row, err
		}
		row.QueryText, row.QueryAstJson, row.ParserVersion, row.ValidationState = *patch.QueryText, string(raw), search.ParserVersion, search.ValidationState(ast)
	}
	row, err = q.UpdateSavedSearch(ctx, dbgen.UpdateSavedSearchParams{ID: row.ID, Version: row.Version, Name: row.Name, QueryText: row.QueryText, QueryAstJson: row.QueryAstJson, ParserVersion: row.ParserVersion, MediaKind: row.MediaKind, ValidationState: row.ValidationState})
	if err != nil {
		return row, err
	}
	return row, q.AdvanceSearchGeneration(ctx)
}

func DeleteSavedSearch(ctx context.Context, tx *sql.Tx, id, version int64) error {
	if err := domain.ValidateID(id); err != nil {
		return err
	}
	q := dbgen.New(tx)
	row, err := q.GetSavedSearch(ctx, id)
	if err != nil {
		return notFound(err)
	}
	if row.Version != version {
		return domain.ErrStaleVersion
	}
	n, err := q.DeleteSavedSearch(ctx, dbgen.DeleteSavedSearchParams{ID: id, Version: version})
	if err != nil {
		return err
	}
	if n != 1 {
		return domain.ErrStaleVersion
	}
	return q.AdvanceSearchGeneration(ctx)
}
