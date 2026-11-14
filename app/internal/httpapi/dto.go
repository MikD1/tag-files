package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mikd1/tag-files/app/internal/catalog"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/organization"
	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
	"github.com/oapi-codegen/nullable"
)

func ptr[T any](v T) *T { return &v }
func value[T any](p *T) T {
	if p != nil {
		return *p
	}
	var zero T
	return zero
}
func null[T any](p *T) nullable.Nullable[T] {
	if p == nil {
		return nullable.NewNullNullable[T]()
	}
	return nullable.NewNullableWithValue(*p)
}
func nullablePtr[T any](n nullable.Nullable[T]) *T {
	if !n.IsSpecified() || n.IsNull() {
		return nil
	}
	return ptr(n.MustGet())
}
func tagDTO(row dbgen.Tag) (api.Tag, error) {
	if err := domain.ValidateID(row.ID); err != nil {
		return api.Tag{}, fmt.Errorf("invalid stored tag ID")
	}
	if row.Version < 1 || row.Version > domain.MaxSafeInteger {
		return api.Tag{}, fmt.Errorf("invalid stored tag version")
	}
	return api.Tag{Id: row.ID, Name: row.Name, Version: row.Version}, nil
}
func itemDTO(item catalog.Item) (api.Item, error) {
	if err := domain.ValidateID(item.ID); err != nil {
		return api.Item{}, fmt.Errorf("invalid stored item ID")
	}
	if item.Version < 1 || item.Version > domain.MaxSafeInteger || item.ViewCount < 0 || item.ViewCount > domain.MaxSafeInteger {
		return api.Item{}, fmt.Errorf("invalid stored numeric item metadata")
	}
	if item.CollectionID.Valid {
		if err := domain.ValidateID(item.CollectionID.Int64); err != nil {
			return api.Item{}, fmt.Errorf("invalid stored collection ID")
		}
	}
	uploaded, err := time.Parse(time.RFC3339Nano, item.UploadedAtUtc)
	if err != nil {
		return api.Item{}, err
	}
	row := api.Item{Id: item.ID, MediaKind: api.MediaKind(item.MediaKind), UploadedAt: uploaded, IsFavorite: item.IsFavorite == 1, ViewCount: item.ViewCount, Version: item.Version, ProcessingState: api.ItemProcessingState(item.State), Tags: []api.Tag{}, Description: null[string](nil), DurationSeconds: null[float64](nil), CollectionId: null[int64](nil), LastViewAt: null[time.Time](nil)}
	if item.Description.Valid {
		row.Description = null(&item.Description.String)
	}
	if item.DurationTicks.Valid {
		seconds := float64(item.DurationTicks.Int64) / 10000000
		row.DurationSeconds = null(&seconds)
	}
	if item.CollectionID.Valid {
		row.CollectionId = null(&item.CollectionID.Int64)
	}
	if item.LastViewAtUtc.Valid {
		last, err := time.Parse(time.RFC3339Nano, item.LastViewAtUtc.String)
		if err != nil {
			return row, err
		}
		row.LastViewAt = null(&last)
	}
	for _, tag := range item.Tags {
		dto, err := tagDTO(tag)
		if err != nil {
			return row, err
		}
		row.Tags = append(row.Tags, dto)
	}
	if item.State == domain.ItemReady && item.PrimaryAssetID.Valid {
		row.ContentUrl = ptr("/media/" + item.PrimaryAssetID.String)
	}
	if item.PosterAssetID != nil {
		row.PosterUrl = ptr("/media/" + *item.PosterAssetID)
	}
	return row, nil
}
func pageDTO(page catalog.Page) (api.ItemPage, error) {
	result := api.ItemPage{Items: []api.Item{}, Page: page.Page, PageSize: page.PageSize, TotalItems: page.TotalItems, TotalPages: page.TotalPages}
	if page.Seed != "" {
		result.Seed = &page.Seed
	}
	for _, item := range page.Items {
		dto, err := itemDTO(item)
		if err != nil {
			return result, err
		}
		result.Items = append(result.Items, dto)
	}
	return result, nil
}
func savedDTO(ctx context.Context, q *dbgen.Queries, row dbgen.SavedSearch) (api.SavedSearch, error) {
	if err := domain.ValidateID(row.ID); err != nil {
		return api.SavedSearch{}, fmt.Errorf("invalid stored query ID")
	}
	if row.Version < 1 || row.Version > domain.MaxSafeInteger {
		return api.SavedSearch{}, fmt.Errorf("invalid stored query version")
	}
	result := api.SavedSearch{Id: row.ID, Name: row.Name, QueryText: row.QueryText, Version: row.Version, ParserVersion: int(row.ParserVersion), ValidationState: api.SavedSearchValidationState(row.ValidationState), DisplayTerms: []api.DisplayTerm{}, MediaKind: null[api.SavedSearchMediaKind](nil)}
	if row.MediaKind.Valid {
		result.MediaKind = null(ptr(api.SavedSearchMediaKind(row.MediaKind.String)))
	}
	if row.LegacySourceText.Valid {
		result.LegacySourceText = &row.LegacySourceText.String
	}
	if err := json.Unmarshal([]byte(row.QueryAstJson), &result.QueryAst); err != nil {
		return result, err
	}
	if row.ValidationState == "needs_review" {
		return result, nil
	}
	ast, err := organization.SavedAST(row)
	if err != nil {
		return result, err
	}
	tags, err := q.ListTags(ctx)
	if err != nil {
		return result, err
	}
	names := make([]search.NamedTag, len(tags))
	for i, tag := range tags {
		names[i] = search.NamedTag{ID: tag.ID, Name: tag.Name}
	}
	terms, err := search.DisplayTerms(ast, names)
	if err != nil {
		return result, err
	}
	for _, term := range terms {
		display := api.DisplayTerm{CurrentName: term.CurrentName, TagId: term.TagID}
		if term.SourceName != "" {
			display.SourceName = &term.SourceName
		}
		result.DisplayTerms = append(result.DisplayTerms, display)
	}
	return result, nil
}
func collectionDTO(ctx context.Context, q *dbgen.Queries, id int64) (api.Collection, error) {
	row, err := q.GetCollectionSummary(ctx, id)
	if err != nil {
		return api.Collection{}, err
	}
	return collectionValue(row.ID, row.Version, row.Name, row.ItemCount, row.CoverAssetID)
}
func collectionValue(id, version int64, name string, count int64, cover sql.NullString) (api.Collection, error) {
	if err := domain.ValidateID(id); err != nil {
		return api.Collection{}, fmt.Errorf("invalid stored collection ID")
	}
	if version < 1 || version > domain.MaxSafeInteger {
		return api.Collection{}, fmt.Errorf("invalid stored collection version")
	}
	result := api.Collection{Id: id, Name: name, Version: version, ItemCount: int(count)}
	if cover.Valid {
		result.CoverUrl = ptr("/media/" + cover.String)
	}
	return result, nil
}
