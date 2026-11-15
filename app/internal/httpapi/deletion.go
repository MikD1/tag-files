package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/google/uuid"
	"github.com/mikd1/tag-files/app/internal/deletion"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/operations"
)

func (s *server) DeleteItem(ctx context.Context, r api.DeleteItemRequestObject) (api.DeleteItemResponseObject, error) {
	version, err := match(r.Params.IfMatch)
	if err != nil {
		return nil, err
	}
	return s.mutation(ctx, func(tx *sql.Tx) (response, error) {
		result, err := deletion.Request(ctx, tx, r.Id, version, s.now())
		if err != nil {
			return response{}, err
		}
		if result.Absent {
			return response{operations.Result{Status: 204}}, nil
		}
		id, err := uuid.Parse(result.JobID)
		if err != nil {
			return response{}, err
		}
		return jsonResult(202, api.JobReceipt{JobId: id}, 0)
	})
}
func (r response) VisitDeleteItemResponse(w http.ResponseWriter) error { return r.write(w) }
