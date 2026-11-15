// Package httpapi adapts the generated JSON contract. Raw upload/media handlers
// are registered separately and never pass through this JSON validator/binder.
package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/httpapi/api"
	"github.com/mikd1/tag-files/app/internal/mediaserve"
	"github.com/mikd1/tag-files/app/internal/operations"
	"github.com/mikd1/tag-files/app/internal/search"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/oapi-codegen/nethttp-middleware"
)

const MaxJSONBytes = 1024 * 1024

type Options struct {
	Clock     func() time.Time
	Media     *mediaserve.Service
	Version   string
	Readiness func(context.Context) error
	Disk      func(context.Context) (map[string]any, error)
	Admit     func() (release func(), accepted bool)
}
type server struct {
	s       *store.Store
	now     func() time.Time
	options Options
}
type scope struct {
	method, path string
	trace        api.UUID
}
type scopeKey struct{}
type protocolError struct {
	status        int
	code, message string
}

func (e *protocolError) Error() string { return e.message }

func New(s *store.Store, options Options) (http.Handler, error) {
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Version == "" {
		options.Version = "development"
	}
	service := &server{s: s, now: options.Clock, options: options}
	spec, err := api.GetSwagger()
	if err != nil {
		return nil, err
	}
	if err := spec.Validate(context.Background()); err != nil {
		return nil, err
	}
	jsonRoutes, err := gorillamux.NewRouter(spec)
	if err != nil {
		return nil, err
	}
	strict := api.NewStrictHandlerWithOptions(service, nil, api.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
			writeProblem(w, r.Context(), &protocolError{400, "invalid_request", "Request does not match the API contract"})
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) { writeProblem(w, r.Context(), err) },
	})
	routes := api.HandlerWithOptions(strict, api.StdHTTPServerOptions{ErrorHandlerFunc: func(w http.ResponseWriter, r *http.Request, err error) {
		writeProblem(w, r.Context(), &protocolError{400, "invalid_request", "Invalid path or query parameter"})
	}})
	validated := nethttpmiddleware.OapiRequestValidatorWithOptions(spec, &nethttpmiddleware.Options{
		ErrorHandlerWithOpts: func(ctx context.Context, err error, w http.ResponseWriter, r *http.Request, options nethttpmiddleware.ErrorHandlerOpts) {
			status, code, message := 400, "invalid_request", "Request does not match the API contract"
			if options.MatchedRoute == nil {
				status, code, message = options.StatusCode, "route_not_found", "Unknown API route"
				if status != 405 {
					status = 404
				}
			}
			var max *http.MaxBytesError
			if errors.As(err, &max) {
				status, code, message = 413, "request_too_large", "JSON request is too large"
			}
			writeProblem(w, ctx, &protocolError{status, code, message})
		},
	})(routes)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := scope{method: r.Method, path: r.URL.Path, trace: uuid.MustParse(domain.NewUUID())}
		r = r.WithContext(context.WithValue(r.Context(), scopeKey{}, info))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if options.Admit != nil {
			release, accepted := options.Admit()
			if !accepted {
				writeProblem(w, r.Context(), &protocolError{503, "runtime_stopping", "Application is stopping"})
				return
			}
			defer release()
		}
		if options.Media != nil && strings.HasPrefix(r.URL.Path, "/media/") {
			if err := options.Media.Serve(w, r); err != nil {
				var mediaError *mediaserve.Error
				if errors.As(err, &mediaError) {
					if mediaError.Started {
						panic(http.ErrAbortHandler)
					}
					err = &protocolError{mediaError.Status, mediaError.Code, mediaError.Message}
				}
				if r.Context().Err() == nil {
					writeProblem(w, r.Context(), err)
				}
			}
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, MaxJSONBytes)
		// The validator's streaming decoder accepts the first JSON value. Check
		// the bounded original document before validation can apply defaults or
		// retain that value for the generated binder. Raw/unknown routes are not
		// consumed here.
		route, _, routeErr := jsonRoutes.FindRoute(r)
		if routeErr == nil && route.Operation.RequestBody != nil && route.Operation.RequestBody.Value.Content.Get("application/json") != nil {
			if err := requireJSONDocument(r); err != nil {
				writeProblem(w, r.Context(), err)
				return
			}
		}
		validated.ServeHTTP(w, r)
	}), nil
}

func requireJSONDocument(r *http.Request) error {
	data, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if err != nil {
		var max *http.MaxBytesError
		if errors.As(err, &max) {
			return &protocolError{413, "request_too_large", "JSON request is too large"}
		}
		return &protocolError{400, "invalid_request", "Could not read JSON request"}
	}
	if !json.Valid(data) {
		return &protocolError{400, "invalid_request", "Expected exactly one JSON document"}
	}
	r.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(data)), nil }
	r.Body, _ = r.GetBody()
	return nil
}

func problem(ctx context.Context, err error) api.Problem {
	info, _ := ctx.Value(scopeKey{}).(scope)
	if info.trace == uuid.Nil {
		info.trace = uuid.MustParse(domain.NewUUID())
	}
	status, code, message := 500, "internal_error", "An internal error occurred"
	var validation *domain.ValidationError
	var syntax *search.SyntaxError
	var request *protocolError
	var fieldErrors *map[string][]string
	var position *int
	switch {
	case errors.As(err, &request):
		status, code, message = request.status, request.code, request.message
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		status, code, message = 404, "not_found", "Resource not found"
	case errors.Is(err, domain.ErrStaleVersion):
		status, code, message = 412, "stale_version", "The resource changed; reload it before saving"
	case errors.Is(err, operations.ErrKeyConflict):
		status, code, message = 409, "idempotency_conflict", "This key belongs to a different request"
	case errors.Is(err, operations.ErrOperationExpired):
		status, code, message = 409, "operation_expired", "The command result expired; check the resource before retrying"
	case errors.As(err, &syntax):
		status, code, message = 422, syntax.Code, syntax.Message
		position = &syntax.Position
	case errors.As(err, &validation):
		status, code, message = 422, validation.Code, validation.Message
		fields := map[string][]string{validation.Field: {validation.Message}}
		fieldErrors = &fields
	}
	return api.Problem{Type: "about:blank", Title: http.StatusText(status), Status: status, Code: code, TraceId: info.trace, Detail: &message, FieldErrors: fieldErrors, Position: position}
}

func writeProblem(w http.ResponseWriter, ctx context.Context, err error) {
	p := problem(ctx, err)
	w.Header().Set("Content-Type", "application/problem+json")
	if info, ok := ctx.Value(scopeKey{}).(scope); ok && info.method == http.MethodHead {
		body, _ := json.Marshal(p)
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+1))
		w.WriteHeader(p.Status)
		return
	}
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

type response struct{ operations.Result }

func jsonResult(status int, data any, version int64) (response, error) {
	if version < 0 || version > domain.MaxSafeInteger {
		return response{}, fmt.Errorf("unsafe resource version")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return response{}, err
	}
	r := response{operations.Result{Status: status, Body: raw}}
	if version > 0 {
		r.ETag = fmt.Sprintf(`"%d"`, version)
	}
	return r, nil
}
func (r response) write(w http.ResponseWriter) error {
	contentType := "application/json"
	if r.Problem {
		contentType = "application/problem+json"
	}
	w.Header().Set("Content-Type", contentType)
	if r.ETag != "" {
		w.Header().Set("ETag", r.ETag)
	}
	if r.Location != "" {
		w.Header().Set("Location", r.Location)
	}
	w.WriteHeader(r.Status)
	if r.Status == 204 {
		return nil
	}
	_, err := w.Write(r.Body)
	return err
}
func (s *server) command(ctx context.Context, key api.UUID, body any, fn func(*sql.Tx) (response, error)) (response, error) {
	info := ctx.Value(scopeKey{}).(scope)
	raw, err := json.Marshal(body)
	if err != nil {
		return response{}, err
	}
	result, err := operations.Execute(ctx, s.s, operations.Request{Key: key.String(), Method: info.method, Route: info.path, Body: raw}, s.now(), func(tx *sql.Tx) (operations.Result, error) {
		r, err := fn(tx)
		if err == nil {
			return r.Result, nil
		}
		p := problem(ctx, err)
		if p.Status >= 500 {
			return operations.Result{}, err
		}
		failure, e := jsonResult(p.Status, p, 0)
		failure.Problem = true
		return failure.Result, e
	})
	return response{result}, err
}
func (s *server) mutation(ctx context.Context, fn func(*sql.Tx) (response, error)) (r response, err error) {
	err = s.s.Write(ctx, func(tx *sql.Tx) error { var e error; r, e = fn(tx); return e })
	return r, err
}
func read[T any](ctx context.Context, s *server, fn func(*sql.Tx) (T, error)) (out T, err error) {
	tx, err := s.s.Reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out, err = fn(tx)
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}
func match(header string) (int64, error) {
	if len(header) < 3 || header[0] != '"' || header[len(header)-1] != '"' {
		return 0, &protocolError{400, "invalid_if_match", "Expected a quoted resource version"}
	}
	value, err := strconv.ParseInt(strings.Trim(header, `"`), 10, 64)
	if err != nil || value < 1 || value > domain.MaxSafeInteger {
		return 0, &protocolError{400, "invalid_if_match", "Expected a valid resource version"}
	}
	return value, nil
}
