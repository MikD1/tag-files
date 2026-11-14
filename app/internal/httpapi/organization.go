package httpapi

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mikd1/tag-files/app/internal/catalog"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/organization"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func (s *server) ListTags(ctx context.Context, r api.ListTagsRequestObject) (api.ListTagsResponseObject, error) {
	rows, err := dbgen.New(s.s.Reader()).ListTags(ctx)
	if err != nil {
		return nil, err
	}
	tags := []api.Tag{}
	for _, row := range rows {
		dto, err := tagDTO(row)
		if err != nil {
			return nil, err
		}
		tags = append(tags, dto)
	}
	return jsonResult(200, tags, 0)
}
func (s *server) CreateTag(ctx context.Context, r api.CreateTagRequestObject) (api.CreateTagResponseObject, error) {
	return s.command(ctx, r.Params.IdempotencyKey, r.Body, func(tx *sql.Tx) (response, error) {
		row, err := organization.CreateTag(ctx, tx, r.Body.Name)
		if err != nil {
			return response{}, err
		}
		dto, err := tagDTO(row)
		if err != nil {
			return response{}, err
		}
		return jsonResult(201, api.TagCommand{OperationId: r.Params.IdempotencyKey, Resource: dto}, row.Version)
	})
}
func (s *server) RenameTag(ctx context.Context, r api.RenameTagRequestObject) (api.RenameTagResponseObject, error) {
	version, err := match(r.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		row, err := organization.RenameTag(ctx, tx, r.Id, version, r.Body.Name)
		if err != nil {
			return response{}, err
		}
		dto, err := tagDTO(row)
		if err != nil {
			return response{}, err
		}
		return jsonResult(200, dto, row.Version)
	})
}
func (s *server) GetTagStatistics(ctx context.Context, r api.GetTagStatisticsRequestObject) (api.GetTagStatisticsResponseObject, error) {
	rows, err := dbgen.New(s.s.Reader()).GlobalTagStatistics(ctx)
	if err != nil {
		return nil, err
	}
	stats := []api.TagStatistic{}
	for _, row := range rows {
		if row.UsageCount < 0 || row.UsageCount > domain.MaxSafeInteger {
			return nil, fmt.Errorf("unsafe tag count")
		}
		stats = append(stats, api.TagStatistic{Name: row.Name, UsageCount: row.UsageCount})
	}
	return jsonResult(200, stats, 0)
}
func (s *server) ListSavedSearches(ctx context.Context, r api.ListSavedSearchesRequestObject) (api.ListSavedSearchesResponseObject, error) {
	return read(ctx, s, func(tx *sql.Tx) (response, error) {
		q := dbgen.New(tx)
		rows, err := q.ListSavedSearches(ctx)
		if err != nil {
			return response{}, err
		}
		results := []api.SavedSearch{}
		for _, row := range rows {
			dto, err := savedDTO(ctx, q, row)
			if err != nil {
				return response{}, err
			}
			results = append(results, dto)
		}
		return jsonResult(200, results, 0)
	})
}
func (s *server) GetSavedSearch(ctx context.Context, r api.GetSavedSearchRequestObject) (api.GetSavedSearchResponseObject, error) {
	return read(ctx, s, func(tx *sql.Tx) (response, error) {
		q := dbgen.New(tx)
		row, err := q.GetSavedSearch(ctx, r.Id)
		if err != nil {
			return response{}, err
		}
		dto, err := savedDTO(ctx, q, row)
		if err != nil {
			return response{}, err
		}
		return jsonResult(200, dto, row.Version)
	})
}
func (s *server) CreateSavedSearch(ctx context.Context, r api.CreateSavedSearchRequestObject) (api.CreateSavedSearchResponseObject, error) {
	return s.command(ctx, r.Params.IdempotencyKey, r.Body, func(tx *sql.Tx) (response, error) {
		var kind *domain.MediaKind
		if v := nullablePtr(r.Body.MediaKind); v != nil {
			kind = ptr(domain.MediaKind(*v))
		}
		row, err := organization.CreateSavedSearch(ctx, tx, organization.NewSavedSearch{Name: r.Body.Name, QueryText: r.Body.QueryText, MediaKind: kind})
		if err != nil {
			return response{}, err
		}
		dto, err := savedDTO(ctx, dbgen.New(tx), row)
		if err != nil {
			return response{}, err
		}
		return jsonResult(201, api.SavedSearchCommand{OperationId: r.Params.IdempotencyKey, Resource: dto}, row.Version)
	})
}
func (s *server) PatchSavedSearch(ctx context.Context, r api.PatchSavedSearchRequestObject) (api.PatchSavedSearchResponseObject, error) {
	version, err := match(r.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		var kind *domain.MediaKind
		if v := nullablePtr(r.Body.MediaKind); v != nil {
			kind = ptr(domain.MediaKind(*v))
		}
		row, err := organization.PatchSavedSearch(ctx, tx, r.Id, version, organization.SavedSearchPatch{Name: r.Body.Name, QueryText: r.Body.QueryText, MediaKind: kind, SetMediaKind: r.Body.MediaKind.IsSpecified()})
		if err != nil {
			return response{}, err
		}
		dto, err := savedDTO(ctx, dbgen.New(tx), row)
		if err != nil {
			return response{}, err
		}
		return jsonResult(200, dto, row.Version)
	})
}
func (s *server) DeleteSavedSearch(ctx context.Context, r api.DeleteSavedSearchRequestObject) (api.DeleteSavedSearchResponseObject, error) {
	version, err := match(r.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		if err := organization.DeleteSavedSearch(ctx, tx, r.Id, version); err != nil {
			return response{}, err
		}
		return jsonResult(204, nil, 0)
	})
}
func (s *server) ListCollections(ctx context.Context, r api.ListCollectionsRequestObject) (api.ListCollectionsResponseObject, error) {
	return read(ctx, s, func(tx *sql.Tx) (response, error) {
		q := dbgen.New(tx)
		rows, err := q.ListCollections(ctx)
		if err != nil {
			return response{}, err
		}
		results := []api.Collection{}
		for _, row := range rows {
			dto, err := collectionValue(row.ID, row.Version, row.Name, row.ItemCount, row.CoverAssetID)
			if err != nil {
				return response{}, err
			}
			results = append(results, dto)
		}
		return jsonResult(200, results, 0)
	})
}
func (s *server) CreateCollection(ctx context.Context, r api.CreateCollectionRequestObject) (api.CreateCollectionResponseObject, error) {
	return s.command(ctx, r.Params.IdempotencyKey, r.Body, func(tx *sql.Tx) (response, error) {
		row, err := organization.CreateCollection(ctx, tx, r.Body.Name)
		if err != nil {
			return response{}, err
		}
		dto, err := collectionDTO(ctx, dbgen.New(tx), row.ID)
		if err != nil {
			return response{}, err
		}
		return jsonResult(201, api.CollectionCommand{OperationId: r.Params.IdempotencyKey, Resource: dto}, row.Version)
	})
}
func (s *server) GetCollection(ctx context.Context, r api.GetCollectionRequestObject) (api.GetCollectionResponseObject, error) {
	return read(ctx, s, func(tx *sql.Tx) (response, error) {
		collection, err := collectionDTO(ctx, dbgen.New(tx), r.Id)
		if err != nil {
			return response{}, err
		}
		page, err := catalog.ListInTx(ctx, tx, catalog.ListOptions{Filter: catalog.Filter{CollectionID: &r.Id}, Page: value(r.Params.Page), PageSize: value(r.Params.PageSize)})
		if err != nil {
			return response{}, err
		}
		members, err := pageDTO(page)
		if err != nil {
			return response{}, err
		}
		return jsonResult(200, api.CollectionDetail{Collection: collection, Members: members}, collection.Version)
	})
}
func (s *server) RenameCollection(ctx context.Context, r api.RenameCollectionRequestObject) (api.RenameCollectionResponseObject, error) {
	version, err := match(r.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		row, err := organization.RenameCollection(ctx, tx, r.Id, version, r.Body.Name)
		if err != nil {
			return response{}, err
		}
		dto, err := collectionDTO(ctx, dbgen.New(tx), row.ID)
		if err != nil {
			return response{}, err
		}
		return jsonResult(200, dto, row.Version)
	})
}
func (s *server) DeleteCollection(ctx context.Context, r api.DeleteCollectionRequestObject) (api.DeleteCollectionResponseObject, error) {
	version, err := match(r.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		if err := organization.DeleteCollection(ctx, tx, r.Id, version); err != nil {
			return response{}, err
		}
		return jsonResult(204, nil, 0)
	})
}
