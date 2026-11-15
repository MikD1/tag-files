package httpapi

import (
	"context"
	"database/sql"

	"github.com/google/uuid"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

func (s *server) Liveness(context.Context, api.LivenessRequestObject) (api.LivenessResponseObject, error) {
	return api.Liveness200Response{}, nil
}
func (s *server) Readiness(ctx context.Context, _ api.ReadinessRequestObject) (api.ReadinessResponseObject, error) {
	if s.options.Readiness == nil || s.options.Readiness(ctx) != nil {
		return nil, &protocolError{503, "runtime_not_ready", "Application dependencies are not ready"}
	}
	return api.Readiness200Response{}, nil
}
func (s *server) GetSystemStatus(ctx context.Context, _ api.GetSystemStatusRequestObject) (api.GetSystemStatusResponseObject, error) {
	status, err := read(ctx, s, func(tx *sql.Tx) (api.SystemStatus, error) {
		q := dbgen.New(tx)
		meta, err := q.GetLibraryMeta(ctx)
		if err != nil {
			return api.SystemStatus{}, err
		}
		rows, err := q.CountJobsByState(ctx)
		if err != nil {
			return api.SystemStatus{}, err
		}
		queue := map[string]int{"queued": 0, "running": 0, "retry_wait": 0, "failed": 0}
		for _, row := range rows {
			queue[string(row.State)] = int(row.Count)
		}
		return api.SystemStatus{AppVersion: s.options.Version, SchemaVersion: store.SchemaVersion, FormatVersion: store.FormatVersion, LibraryId: uuid.MustParse(meta.LibraryID), Queue: queue}, nil
	})
	if err != nil {
		return nil, err
	}
	status.Disk = map[string]any{"storage": map[string]any{"status": "unknown", "availableBytes": nil}}
	if s.options.Disk != nil {
		status.Disk, err = s.options.Disk(ctx)
		if err != nil {
			return nil, err
		}
	}
	return api.GetSystemStatus200JSONResponse(status), nil
}
