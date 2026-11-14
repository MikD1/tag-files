package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/mikd1/tag-files/app/internal/catalog"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/navigation"
	"github.com/mikd1/tag-files/app/internal/operations"
)

func (s *server) CreateNavigationContext(ctx context.Context, r api.CreateNavigationContextRequestObject) (api.CreateNavigationContextResponseObject, error) {
	if r.Body.SavedSearchId != nil && (r.Body.Q != nil || r.Body.Kind != nil) {
		return nil, &protocolError{400, "filter_source_conflict", "Choose a saved search or query and kind"}
	}
	return s.command(ctx, r.Params.IdempotencyKey, r.Body, func(tx *sql.Tx) (response, error) {
		p := r.Body
		created, err := navigation.Create(ctx, tx, navigation.Input{Origin: p.Origin, OrderedIDs: p.OrderedIds, Options: catalog.ListOptions{Filter: filter(p.Q, p.Kind, p.SavedSearchId, p.CollectionId), Sort: catalog.Sort(value(p.Sort)), Seed: value(p.Seed)}}, s.now())
		if err != nil {
			return response{}, err
		}
		dto := api.NavigationCreated{OperationId: r.Params.IdempotencyKey, ContextId: uuid.MustParse(created.ID), ExpiresAt: created.ExpiresAt}
		if created.Seed != "" {
			dto.Seed = &created.Seed
		}
		return jsonResult(201, dto, 0)
	})
}
func (s *server) GetNeighbors(ctx context.Context, r api.GetNeighborsRequestObject) (api.GetNeighborsResponseObject, error) {
	neighbors, err := navigation.Get(ctx, s.s.Reader(), r.Id.String(), r.Params.ItemId, s.now())
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(neighbors.Origin)
	if err != nil {
		return nil, err
	}
	var origin api.NavigationOrigin
	if err := json.Unmarshal(raw, &origin); err != nil {
		return nil, err
	}
	return jsonResult(200, api.Neighbors{Expired: neighbors.Expired, Origin: origin, Total: neighbors.Total, PreviousId: null(neighbors.PreviousID), NextId: null(neighbors.NextID), Position: null(neighbors.Position)}, 0)
}
func (s *server) GetOperation(ctx context.Context, r api.GetOperationRequestObject) (api.GetOperationResponseObject, error) {
	op, err := operations.Get(ctx, s.s.Reader(), r.Id.String(), s.now())
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	if err := json.Unmarshal(op.Result.Body, &result); err != nil {
		return nil, err
	}
	dto := api.Operation{OperationId: uuid.MustParse(op.ID), State: api.OperationState(op.State), HttpStatus: op.Result.Status, CreatedAt: op.CreatedAt, ExpiresAt: op.ExpiresAt, Result: null[map[string]interface{}](nil)}
	if result != nil {
		dto.Result = null(&result)
	}
	return jsonResult(200, dto, 0)
}
