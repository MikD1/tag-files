package httpapi

import (
	"context"
	"database/sql"

	"github.com/mikd1/tag-files/app/internal/catalog"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
)

func filter(q *string, kind *api.MediaKind, saved, collection *int64) catalog.Filter {
	f := catalog.Filter{Query: q, SavedSearchID: saved, CollectionID: collection}
	if kind != nil {
		f.Kind = ptr(domain.MediaKind(*kind))
	}
	return f
}
func (s *server) ListItems(ctx context.Context, r api.ListItemsRequestObject) (api.ListItemsResponseObject, error) {
	p := r.Params
	if p.SavedSearchId != nil && (p.Q != nil || p.Kind != nil) {
		return nil, &protocolError{400, "filter_source_conflict", "Choose a saved search or query and kind"}
	}
	page, err := catalog.List(ctx, s.s.Reader(), catalog.ListOptions{Filter: filter(p.Q, p.Kind, p.SavedSearchId, p.CollectionId), Sort: catalog.Sort(value(p.Sort)), Page: value(p.Page), PageSize: value(p.PageSize), Seed: value(p.Seed)})
	if err != nil {
		return nil, err
	}
	dto, err := pageDTO(page)
	if err != nil {
		return nil, err
	}
	return jsonResult(200, dto, 0)
}
func (s *server) GetItem(ctx context.Context, r api.GetItemRequestObject) (api.GetItemResponseObject, error) {
	item, err := catalog.Get(ctx, s.s.Reader(), r.Id)
	if err != nil {
		return nil, err
	}
	dto, err := itemDTO(item)
	if err != nil {
		return nil, err
	}
	return jsonResult(200, dto, item.Version)
}
func itemResponse(ctx context.Context, tx *sql.Tx, id int64) (response, error) {
	item, err := catalog.GetInTx(ctx, tx, id)
	if err != nil {
		return response{}, err
	}
	dto, err := itemDTO(item)
	if err != nil {
		return response{}, err
	}
	return jsonResult(200, dto, item.Version)
}
func (s *server) PatchItem(ctx context.Context, r api.PatchItemRequestObject) (api.PatchItemResponseObject, error) {
	version, err := match(r.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		if err := catalog.ReplaceMetadata(ctx, tx, []catalog.VersionedItem{{ID: r.Id, Version: version}}, r.Body.TagIds, nullablePtr(r.Body.CollectionId)); err != nil {
			return response{}, err
		}
		return itemResponse(ctx, tx, r.Id)
	})
}
func (s *server) SetFavorite(ctx context.Context, r api.SetFavoriteRequestObject) (api.SetFavoriteResponseObject, error) {
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		if err := catalog.SetFavorite(ctx, tx, r.Id, r.Body.IsFavorite); err != nil {
			return response{}, err
		}
		return itemResponse(ctx, tx, r.Id)
	})
}
func (s *server) RecordView(ctx context.Context, r api.RecordViewRequestObject) (api.RecordViewResponseObject, error) {
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		if err := catalog.RecordView(ctx, tx, r.Id, r.Body.EventId.String(), s.now()); err != nil {
			return response{}, err
		}
		return itemResponse(ctx, tx, r.Id)
	})
}
func (s *server) GetSimilar(ctx context.Context, r api.GetSimilarRequestObject) (api.GetSimilarResponseObject, error) {
	page, err := catalog.Similar(ctx, s.s.Reader(), r.Id, value(r.Params.Page), value(r.Params.PageSize))
	if err != nil {
		return nil, err
	}
	dto, err := pageDTO(page)
	if err != nil {
		return nil, err
	}
	return jsonResult(200, dto, 0)
}
func (s *server) ReplaceBulkMetadata(ctx context.Context, r api.ReplaceBulkMetadataRequestObject) (api.ReplaceBulkMetadataResponseObject, error) {
	return s.command(ctx, r.Params.IdempotencyKey, r.Body, func(tx *sql.Tx) (response, error) {
		items := make([]catalog.VersionedItem, len(r.Body.Items))
		ids := make([]int64, len(items))
		for i, item := range r.Body.Items {
			items[i] = catalog.VersionedItem{ID: item.Id, Version: item.Version}
			ids[i] = item.Id
		}
		if err := catalog.ReplaceMetadata(ctx, tx, items, r.Body.TagIds, nullablePtr(r.Body.CollectionId)); err != nil {
			return response{}, err
		}
		return jsonResult(200, api.BulkCommand{OperationId: r.Params.IdempotencyKey, ItemIds: ids}, 0)
	})
}
func (s *server) ReplaceBulkTags(ctx context.Context, r api.ReplaceBulkTagsRequestObject) (api.ReplaceBulkTagsResponseObject, error) {
	return s.command(ctx, r.Params.IdempotencyKey, r.Body, func(tx *sql.Tx) (response, error) {
		if err := catalog.ReplaceTags(ctx, tx, r.Body.ItemIds, r.Body.TagIds); err != nil {
			return response{}, err
		}
		return jsonResult(200, api.BulkCommand{OperationId: r.Params.IdempotencyKey, ItemIds: r.Body.ItemIds}, 0)
	})
}
func (s *server) ReplaceBulkCollection(ctx context.Context, r api.ReplaceBulkCollectionRequestObject) (api.ReplaceBulkCollectionResponseObject, error) {
	return s.command(ctx, r.Params.IdempotencyKey, r.Body, func(tx *sql.Tx) (response, error) {
		if err := catalog.ReplaceCollection(ctx, tx, r.Body.ItemIds, nullablePtr(r.Body.CollectionId)); err != nil {
			return response{}, err
		}
		return jsonResult(200, api.BulkCommand{OperationId: r.Params.IdempotencyKey, ItemIds: r.Body.ItemIds}, 0)
	})
}
