package httpapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/mikd1/tag-files/app/internal/capacity"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/ingestion"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/operations"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type UploadOptions struct {
	Coordinator *ingestion.Coordinator
	Budget      *capacity.Manager
	Limits      ingestion.Limits
	Wake        func()
}

func (s *server) uploads() (*UploadOptions, error) {
	u := s.options.Uploads
	if u == nil || u.Coordinator == nil || u.Budget == nil {
		return nil, &protocolError{503, "uploads_unavailable", "Upload service is unavailable"}
	}
	return u, nil
}
func (s *server) wakeUploads() {
	if u := s.options.Uploads; u != nil && u.Wake != nil {
		u.Wake()
	}
}

func partDTO(p ingestion.Part) api.UploadPart {
	row := api.UploadPart{PartNumber: p.Number, Size: p.Size, Sha256: p.SHA256, State: api.UploadPartState(p.State)}
	if p.ETag != "" {
		row.Etag = ptr(p.ETag)
	}
	return row
}
func sessionDTO(session ingestion.Session) api.UploadSession {
	row := api.UploadSession{Id: uuid.MustParse(session.ID), BatchId: uuid.MustParse(session.BatchID), ClientFileId: uuid.MustParse(session.ClientFileID), Filename: session.ClientFilename, Size: session.ExpectedSize, PartSize: session.PartSize, PartCount: int(session.PartCount), Generation: session.Generation, State: api.UploadSessionState(session.State), Parts: []api.UploadPart{}, CollectionId: null[int64](nil)}
	if session.Fingerprint.Valid {
		row.Fingerprint = ptr(session.Fingerprint.String)
	}
	if session.CollectionID.Valid {
		row.CollectionId = null(&session.CollectionID.Int64)
	}
	if session.ErrorCode.Valid {
		row.ErrorCode = ptr(session.ErrorCode.String)
	}
	if session.JobID != "" {
		row.JobId = ptr(uuid.MustParse(session.JobID))
	}
	for _, p := range session.Parts {
		row.Parts = append(row.Parts, partDTO(p))
	}
	return row
}
func outcomeDTO(id string, o ingestion.Outcome, operation *api.UUID) api.UploadOutcome {
	row := api.UploadOutcome{UploadId: uuid.MustParse(id), State: api.UploadOutcomeState(o.State), OperationId: operation}
	if o.Session != nil {
		row.Session = ptr(sessionDTO(*o.Session))
	}
	if o.ItemID != 0 {
		row.ItemId = ptr(o.ItemID)
	}
	if o.JobID != "" {
		row.JobId = ptr(uuid.MustParse(o.JobID))
	}
	return row
}
func (s *server) inspect(ctx context.Context, key api.UUID, body any) (operations.Operation, bool, error) {
	info := ctx.Value(scopeKey{}).(scope)
	raw, err := json.Marshal(body)
	if err != nil {
		return operations.Operation{}, false, err
	}
	return operations.Inspect(ctx, s.s.Reader(), operations.Request{Key: key.String(), Method: info.method, Route: info.path, Body: raw}, s.now())
}

// A concurrent request can commit the same key after Inspect but before this
// admission check. Its receipt still wins over a later capacity/outage error.
func (s *server) afterAdmission(ctx context.Context, key api.UUID, body any, result response, err error) (response, error) {
	if err != nil {
		op, found, replayErr := s.inspect(ctx, key, body)
		if found || replayErr != nil {
			return response{op.Result}, replayErr
		}
	}
	return result, err
}

func (s *server) CreateUploadSessions(ctx context.Context, r api.CreateUploadSessionsRequestObject) (api.CreateUploadSessionsResponseObject, error) {
	u, err := s.uploads()
	if err != nil {
		return nil, err
	}
	if op, found, err := s.inspect(ctx, r.Params.IdempotencyKey, r.Body); found || err != nil {
		return response{op.Result}, err
	}
	files := make([]ingestion.File, len(r.Body.Files))
	sizes := make([]int64, len(files))
	for i, f := range r.Body.Files {
		files[i] = ingestion.File{ClientFileID: f.ClientFileId.String(), Filename: f.Filename, Fingerprint: value(f.Fingerprint), Size: f.Size}
		sizes[i] = f.Size
	}
	collection := nullablePtr(r.Body.CollectionId)
	limits := u.Limits
	if limits == (ingestion.Limits{}) {
		limits = ingestion.DefaultLimits()
	}
	create := func(tx *sql.Tx) (response, error) {
		batch, err := ingestion.Create(ctx, tx, files, collection, limits, s.now())
		if err != nil {
			return response{}, err
		}
		dto := api.UploadsCreated{OperationId: r.Params.IdempotencyKey, BatchId: uuid.MustParse(batch.ID), Sessions: []api.UploadSession{}, MaxParallelTransfers: limits.MaxParallelTransfers, MaxFileSize: limits.MaxFileSize}
		for _, session := range batch.Sessions {
			dto.Sessions = append(dto.Sessions, sessionDTO(session))
		}
		return jsonResult(201, dto, 0)
	}
	if err := ingestion.ValidateFiles(files, collection, limits); err != nil {
		return s.command(ctx, r.Params.IdempotencyKey, r.Body, func(*sql.Tx) (response, error) { return response{}, err })
	}
	extra, err := u.Budget.Files(sizes)
	if err != nil {
		return nil, err
	}
	var result response
	err = u.Budget.Admit(ctx, extra, func() error {
		var err error
		result, err = s.command(ctx, r.Params.IdempotencyKey, r.Body, create)
		return err
	})
	return s.afterAdmission(ctx, r.Params.IdempotencyKey, r.Body, result, err)
}
func (s *server) GetUploadSession(ctx context.Context, r api.GetUploadSessionRequestObject) (api.GetUploadSessionResponseObject, error) {
	if _, err := s.uploads(); err != nil {
		return nil, err
	}
	outcome, err := ingestion.Lookup(ctx, s.s.Reader(), r.Id.String())
	if err != nil {
		return nil, err
	}
	return jsonResult(200, outcomeDTO(r.Id.String(), outcome, nil), 0)
}
func (s *server) ListUploadBatches(ctx context.Context, _ api.ListUploadBatchesRequestObject) (api.ListUploadBatchesResponseObject, error) {
	if _, err := s.uploads(); err != nil {
		return nil, err
	}
	batches, err := ingestion.ListBatches(ctx, s.s.Reader())
	if err != nil {
		return nil, err
	}
	dto := []api.UploadBatch{}
	for _, batch := range batches {
		row := api.UploadBatch{Id: uuid.MustParse(batch.ID), Sessions: []api.UploadSession{}}
		for _, session := range batch.Sessions {
			row.Sessions = append(row.Sessions, sessionDTO(session))
		}
		dto = append(dto, row)
	}
	return jsonResult(200, dto, 0)
}
func (s *server) CompleteUpload(ctx context.Context, r api.CompleteUploadRequestObject) (api.CompleteUploadResponseObject, error) {
	if _, err := s.uploads(); err != nil {
		return nil, err
	}
	op, found, err := s.inspect(ctx, r.Params.IdempotencyKey, r.Body)
	if err != nil && !errors.Is(err, operations.ErrOperationExpired) {
		return nil, err
	}
	if found && err == nil {
		return response{op.Result}, nil
	}
	if errors.Is(err, operations.ErrOperationExpired) {
		ready, lookupErr := ingestion.Lookup(ctx, s.s.Reader(), r.Id.String())
		if lookupErr == nil && ready.State == "ready" {
			return jsonResult(200, outcomeDTO(r.Id.String(), ready, &r.Params.IdempotencyKey), 0)
		}
		return nil, err
	}
	result, err := s.command(ctx, r.Params.IdempotencyKey, r.Body, func(tx *sql.Tx) (response, error) {
		parts := make([]ingestion.CompletePart, len(r.Body.Parts))
		for i, p := range r.Body.Parts {
			parts[i] = ingestion.CompletePart{Number: p.PartNumber, ETag: p.Etag}
		}
		outcome, err := ingestion.ScheduleComplete(ctx, tx, r.Id.String(), r.Body.Generation, parts, s.now())
		if err != nil {
			return response{}, err
		}
		if outcome.State == "ready" {
			return jsonResult(200, outcomeDTO(r.Id.String(), outcome, &r.Params.IdempotencyKey), 0)
		}
		if outcome.State == "failed" {
			return response{}, &protocolError{422, "upload_failed", "Retry or cancel the failed upload"}
		}
		if err := operations.Link(ctx, tx, r.Params.IdempotencyKey.String(), "upload_complete", r.Id.String()); err != nil {
			return response{}, err
		}
		return jsonResult(202, outcomeDTO(r.Id.String(), outcome, &r.Params.IdempotencyKey), 0)
	})
	if err == nil {
		s.wakeUploads()
	}
	return result, err
}

func (s *server) cancelUpload(ctx context.Context, tx *sql.Tx, id string, operation *api.UUID) (response, error) {
	cancelled, err := ingestion.Cancel(ctx, tx, id, s.now())
	if err != nil {
		return response{}, err
	}
	switch cancelled.State {
	case "ready":
		return jsonResult(200, outcomeDTO(id, ingestion.Outcome{State: "ready", ItemID: cancelled.ItemID}, operation), 0)
	case "absent":
		return jsonResult(204, nil, 0)
	default:
		if operation != nil {
			if err := operations.Link(ctx, tx, operation.String(), "job", cancelled.JobID); err != nil {
				return response{}, err
			}
		}
		return jsonResult(202, api.JobReceipt{JobId: uuid.MustParse(cancelled.JobID), OperationId: operation}, 0)
	}
}
func (s *server) CancelUpload(ctx context.Context, r api.CancelUploadRequestObject) (api.CancelUploadResponseObject, error) {
	if _, err := s.uploads(); err != nil {
		return nil, err
	}
	result, err := s.mutation(ctx, func(tx *sql.Tx) (response, error) { return s.cancelUpload(ctx, tx, r.Id.String(), nil) })
	if err == nil {
		s.wakeUploads()
	}
	return result, err
}

func (s *server) uploadJob(ctx context.Context, id string) (dbgen.Job, error) {
	job, err := dbgen.New(s.s.Reader()).GetJob(ctx, id)
	if err != nil {
		return job, err
	}
	if job.Type != domain.JobComplete && job.Type != domain.JobIngest {
		return job, &protocolError{409, "job_not_upload", "This job is managed independently of uploads"}
	}
	return job, nil
}
func (s *server) RetryJob(ctx context.Context, r api.RetryJobRequestObject) (api.RetryJobResponseObject, error) {
	u, err := s.uploads()
	if err != nil {
		return nil, err
	}
	if op, found, err := s.inspect(ctx, r.Params.IdempotencyKey, nil); found || err != nil {
		return response{op.Result}, err
	}
	job, err := s.uploadJob(ctx, r.Id.String())
	if err != nil {
		return nil, err
	}
	outcome, err := ingestion.Lookup(ctx, s.s.Reader(), job.SubjectID)
	if err != nil {
		return nil, err
	}
	if outcome.Session == nil {
		return nil, domain.ErrNotFound
	}
	var extra capacity.Extra
	if outcome.State == "failed" {
		if outcome.Session.SourceSha256.Valid {
			extra, err = u.Budget.Retry(outcome.Session.ExpectedSize)
		} else {
			extra, err = u.Budget.Files([]int64{outcome.Session.ExpectedSize})
		}
		if err != nil {
			return nil, err
		}
	}
	var result response
	err = u.Budget.Admit(ctx, extra, func() error {
		var err error
		result, err = s.command(ctx, r.Params.IdempotencyKey, nil, func(tx *sql.Tx) (response, error) {
			current, err := dbgen.New(tx).GetJob(ctx, job.ID)
			if err != nil {
				return response{}, err
			}
			if current.Type != job.Type || current.SubjectID != job.SubjectID {
				return response{}, ingestion.ErrConflict
			}
			outcome, err := ingestion.Retry(ctx, tx, job.SubjectID, s.now())
			if err != nil {
				return response{}, err
			}
			if outcome.JobID != job.ID {
				return response{}, ingestion.ErrConflict
			}
			if err := operations.Link(ctx, tx, r.Params.IdempotencyKey.String(), "job", job.ID); err != nil {
				return response{}, err
			}
			return jsonResult(202, api.JobReceipt{JobId: r.Id, OperationId: &r.Params.IdempotencyKey}, 0)
		})
		return err
	})
	if err == nil {
		s.wakeUploads()
	}
	return s.afterAdmission(ctx, r.Params.IdempotencyKey, nil, result, err)
}
func (s *server) CancelJob(ctx context.Context, r api.CancelJobRequestObject) (api.CancelJobResponseObject, error) {
	if _, err := s.uploads(); err != nil {
		return nil, err
	}
	if op, found, err := s.inspect(ctx, r.Params.IdempotencyKey, nil); found || err != nil {
		return response{op.Result}, err
	}
	job, err := s.uploadJob(ctx, r.Id.String())
	if err != nil {
		return nil, err
	}
	result, err := s.command(ctx, r.Params.IdempotencyKey, nil, func(tx *sql.Tx) (response, error) {
		return s.cancelUpload(ctx, tx, job.SubjectID, &r.Params.IdempotencyKey)
	})
	if err == nil {
		s.wakeUploads()
	}
	return result, err
}

func (s *server) uploadPart(w http.ResponseWriter, r *http.Request) error {
	if r.Method != http.MethodPut {
		w.Header().Set("Allow", "PUT")
		return &protocolError{405, "method_not_allowed", "Use PUT for an upload part"}
	}
	p := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/v1/upload-sessions/"), "/")
	if len(p) != 3 || p[1] != "parts" || domain.ValidateUUID(p[0]) != nil {
		return &protocolError{400, "invalid_request", "Invalid upload part route"}
	}
	number, err := strconv.Atoi(p[2])
	if err != nil || number < 1 || number > 10000 {
		return &protocolError{400, "invalid_request", "Invalid part number"}
	}
	if len(r.Header.Values("X-Upload-Generation")) != 1 || len(r.Header.Values("X-Part-SHA256")) != 1 {
		return &protocolError{400, "invalid_request", "Expected one generation and SHA-256 header"}
	}
	generation, err := strconv.ParseInt(r.Header.Get("X-Upload-Generation"), 10, 64)
	if err != nil || generation < 1 || generation > domain.MaxSafeInteger {
		return &protocolError{400, "invalid_request", "Invalid upload generation"}
	}
	digest := r.Header.Get("X-Part-SHA256")
	sum, err := base64.StdEncoding.Strict().DecodeString(digest)
	if err != nil || len(sum) != 32 || base64.StdEncoding.EncodeToString(sum) != digest {
		return &protocolError{400, "invalid_request", "Expected a canonical base64 SHA-256"}
	}
	if r.ContentLength < 0 || len(r.TransferEncoding) > 0 {
		return &protocolError{411, "length_required", "Upload parts require a known Content-Length"}
	}
	if r.ContentLength < 1 || r.ContentLength > 5<<30 {
		return &protocolError{413, "invalid_part_size", "Upload part is outside its size limit"}
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/octet-stream" || len(r.Header.Values("Content-Type")) != 1 {
		return &protocolError{415, "unsupported_media_type", "Upload parts require application/octet-stream"}
	}
	u, err := s.uploads()
	if err != nil {
		return err
	}
	binding := ingestion.PartBinding{Number: number, Size: r.ContentLength, SHA256: digest}
	if err := ingestion.CheckPart(r.Context(), s.s.Reader(), p[0], generation, binding); err != nil {
		return err
	}
	if err := u.Budget.Check(r.Context()); err != nil {
		return err
	}
	if _, err := u.Coordinator.EnsureMultipart(r.Context(), p[0]); err != nil {
		return uploadStorageError(err)
	}
	part, err := u.Coordinator.UploadPart(r.Context(), p[0], generation, binding, r.Body)
	if err != nil {
		return uploadStorageError(err)
	}
	result, err := jsonResult(200, partDTO(part), 0)
	if err != nil {
		return err
	}
	return result.write(w)
}
func uploadStorageError(err error) error {
	var wire *objectstore.Error
	if errors.As(err, &wire) {
		if wire.Code == "BadDigest" || wire.Code == "InvalidDigest" {
			return &protocolError{422, "part_checksum_mismatch", "Part bytes do not match SHA-256"}
		}
		if wire.Status != 507 {
			return &protocolError{503, "storage_unavailable", "Upload storage is unavailable"}
		}
	}
	return err
}

func (r response) VisitCreateUploadSessionsResponse(w http.ResponseWriter) error { return r.write(w) }
func (r response) VisitGetUploadSessionResponse(w http.ResponseWriter) error     { return r.write(w) }
func (r response) VisitListUploadBatchesResponse(w http.ResponseWriter) error    { return r.write(w) }
func (r response) VisitCompleteUploadResponse(w http.ResponseWriter) error       { return r.write(w) }
func (r response) VisitCancelUploadResponse(w http.ResponseWriter) error         { return r.write(w) }
func (r response) VisitRetryJobResponse(w http.ResponseWriter) error             { return r.write(w) }
func (r response) VisitCancelJobResponse(w http.ResponseWriter) error            { return r.write(w) }
