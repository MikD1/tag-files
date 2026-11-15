package objectio

import (
	"context"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectstore"
)

// KeyReader cannot read another object under a narrower drain registration.
// The callback must consume/close bodies and finish downstream writes before
// returning; the registration covers all of that work.
type KeyReader interface {
	Head(context.Context) (objectstore.Object, error)
	Get(context.Context, objectstore.GetOptions) (objectstore.Download, error)
}
type keyReader struct {
	client *objectstore.Client
	key    string
}

func (r keyReader) Head(ctx context.Context) (objectstore.Object, error) {
	return r.client.Head(ctx, r.key)
}
func (r keyReader) Get(ctx context.Context, o objectstore.GetOptions) (objectstore.Download, error) {
	return r.client.Get(ctx, r.key, o)
}
func (m *Manager) ReadKey(ctx context.Context, key string, fn func(KeyReader) error) error {
	if !keyValid(key) {
		return domain.Invalid("key", "Invalid read key")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	id := domain.NewUUID()
	m.add(id, Request{Key: key})
	defer m.finish(id)
	epoch, err := m.base.StorageEpoch(ctx)
	if err != nil {
		return err
	}
	bound, err := m.base.AtEpoch(epoch)
	if err != nil {
		return err
	}
	return fn(keyReader{client: bound, key: key})
}
