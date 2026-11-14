package objectio

import (
	"context"
	"database/sql"
	"io"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectstore"
)

// Reader cannot send data or rebind a client to a different epoch. A callback
// must consume/close any returned body before it returns.
type Reader interface {
	Head(context.Context, string) (objectstore.Object, error)
	Get(context.Context, string, objectstore.GetOptions) (objectstore.Download, error)
	List(context.Context, string, string, int32) (objectstore.Page, error)
	ListParts(context.Context, string, string) ([]objectstore.Part, error)
	ListMultipart(context.Context, string) ([]objectstore.Multipart, error)
	Verify(context.Context, string, int64, string) (objectstore.Verified, error)
	VerifyParts(context.Context, string, []objectstore.Part) (objectstore.Verified, error)
}

// ReconciliationClient permits only reads and idempotent removal of already
// fenced owned resources. New data writes always require their own intent.
type ReconciliationClient interface {
	Reader
	Abort(context.Context, string, string) error
	Delete(context.Context, string) error
}

func (m *Manager) Read(ctx context.Context, read func(Reader) error) error {
	epoch, err := m.base.StorageEpoch(ctx)
	if err != nil {
		return err
	}
	bound, err := m.base.AtEpoch(epoch)
	if err != nil {
		return err
	}
	return read(bound)
}

func expecting(operation Operation, prepare func(*sql.Tx) (Request, error)) func(*sql.Tx) (Request, error) {
	return func(tx *sql.Tx) (Request, error) {
		request, err := prepare(tx)
		if err != nil {
			return Request{}, err
		}
		if request.Operation != operation {
			return Request{}, domain.Invalid("writeIntent", "Write intent operation does not match the request")
		}
		return request, nil
	}
}

func (m *Manager) CreateMultipart(ctx context.Context, prepare func(*sql.Tx) (Request, error), commit func(string) error) (string, error) {
	return run(ctx, m, expecting(CreateMPU, prepare), func(bound *objectstore.Client, request Request) (string, error) {
		return bound.CreateMultipart(ctx, request.Key, request.Arguments.ContentType, nil)
	}, commit)
}

// Put owns the actual RPC and its verification. A lost PUT response must not
// become settled merely because an older identical object is already readable.
// Separate read-only recovery may publish verified bytes while that uncertain
// write intent remains available to later deletion/reconciliation.
func (m *Manager) Put(ctx context.Context, prepare func(*sql.Tx) (Request, error), body io.Reader, commit func(objectstore.Verified) error) (objectstore.Verified, error) {
	if body == nil {
		return objectstore.Verified{}, domain.Invalid("body", "An output body is required")
	}
	return run(ctx, m, expecting(PutObject, prepare), func(bound *objectstore.Client, r Request) (objectstore.Verified, error) {
		if err := bound.Put(ctx, r.Key, body, r.Arguments.Size, r.Arguments.SHA256, r.Arguments.ContentType, nil); err != nil {
			return objectstore.Verified{}, err
		}
		return bound.Verify(ctx, r.Key, r.Arguments.Size, r.Arguments.SHA256)
	}, commit)
}

func (m *Manager) UploadPart(ctx context.Context, prepare func(*sql.Tx) (Request, error), body io.Reader, commit func(objectstore.Part) error) (objectstore.Part, error) {
	if body == nil {
		return objectstore.Part{}, domain.Invalid("body", "A part body is required")
	}
	return run(ctx, m, expecting(UploadPart, prepare), func(bound *objectstore.Client, r Request) (objectstore.Part, error) {
		return bound.UploadPart(ctx, r.Key, r.Arguments.MultipartID, r.Arguments.PartNumber, r.Arguments.Size, r.Arguments.SHA256, body)
	}, commit)
}
