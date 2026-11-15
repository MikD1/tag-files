package objectio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectstore"
)

// CopyVerified registers a complete source read until body close and verifies
// bytes while streaming to scratch. It never treats an ETag as a content hash.
func (m *Manager) CopyVerified(ctx context.Context, key string, size int64, hash string, destination io.Writer) error {
	if !keyValid(key) || size < 1 || size > domain.MaxSafeInteger || !hexSHA(hash) {
		return domain.Invalid("source", "Invalid immutable source descriptor")
	}
	return m.ReadKey(ctx, key, func(reader KeyReader) error {
		download, err := reader.Get(ctx, objectstore.GetOptions{})
		if err != nil {
			return err
		}
		defer download.Body.Close()
		if download.Size != size {
			return objectstore.ErrIntegrity
		}
		digest := sha256.New()
		n, err := io.CopyBuffer(io.MultiWriter(destination, digest), &sourceReader{ctx: ctx, reader: io.LimitReader(download.Body, size+1)}, make([]byte, 32768))
		if err != nil {
			return err
		}
		if n != size || hex.EncodeToString(digest.Sum(nil)) != hash {
			return objectstore.ErrIntegrity
		}
		return nil
	})
}

type sourceReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *sourceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}
