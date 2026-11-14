package media

import (
	"context"
	"io"
)

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}
func copyMedia(ctx context.Context, destination io.Writer, source io.Reader, limit int64) (int64, error) {
	return io.Copy(destination, io.LimitReader(contextReader{ctx: ctx, source: source}, limit))
}
