// Package mediaserve owns same-origin delivery of item-owned immutable assets.
// It never exposes storage keys or serves upload/candidate resources.
package mediaserve

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectio"
	"github.com/mikd1/tag-files/app/internal/objectstore"
	"github.com/mikd1/tag-files/app/internal/poster"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

type Config struct {
	MaxStreams int
	Clock      func() time.Time
}
type Service struct {
	db    *store.Store
	io    *objectio.Manager
	slots chan struct{}
	now   func() time.Time
}
type Error struct {
	Status        int
	Code, Message string
	Err           error
	Started       bool
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }
func badHeader() error {
	return &Error{Status: 400, Code: "invalid_media_header", Message: "Invalid media request header"}
}
func gateway(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return &Error{Status: 502, Code: "media_storage_unavailable", Message: "Media storage is unavailable", Err: err}
}
func New(db *store.Store, manager *objectio.Manager, c Config) (*Service, error) {
	if c.MaxStreams == 0 {
		c.MaxStreams = 16
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if db == nil || manager == nil || c.MaxStreams < 1 || c.MaxStreams > 128 {
		return nil, domain.Invalid("media", "Invalid media serving dependencies or limits")
	}
	return &Service{db: db, io: manager, slots: make(chan struct{}, c.MaxStreams), now: c.Clock}, nil
}
func (s *Service) lookup(ctx context.Context, id string) (dbgen.GetMediaAssetRow, error) {
	q := dbgen.New(s.db.Reader())
	a, err := q.GetMediaAsset(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return a, domain.ErrNotFound
	}
	if err != nil {
		return a, err
	}
	meta, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return a, err
	}
	if a.Bucket != "tag-files" || !domain.OwnedObjectKey(meta.LibraryID, a.ID, a.Role, a.ObjectKey) {
		return a, domain.ErrNotFound
	}
	return a, nil
}
func (s *Service) missing(ctx context.Context, a dbgen.GetMediaAssetRow) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		q := dbgen.New(tx)
		fresh, err := q.GetMediaAsset(ctx, a.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if fresh.OwnerID != a.OwnerID || fresh.ObjectKey != a.ObjectKey {
			return domain.ErrNotFound
		}
		if a.Role == "primary" {
			if _, err := q.MarkPrimaryMissing(ctx, dbgen.MarkPrimaryMissingParams{ID: a.OwnerID, PrimaryAssetID: sql.NullString{String: a.ID, Valid: true}}); err != nil {
				return err
			}
		} else {
			if _, err := q.BumpItemVersion(ctx, a.OwnerID); err != nil {
				return err
			}
		}
		if _, err := q.MarkMediaAssetMissing(ctx, dbgen.MarkMediaAssetMissingParams{ID: a.ID, ItemID: a.ItemID}); err != nil {
			return err
		}
		if a.Role == "poster" && fresh.OwnerState == domain.ItemReady && fresh.CurrentPrimary.Valid && (domain.MediaKind(fresh.ItemMediaKind) == domain.Image || domain.MediaKind(fresh.ItemMediaKind) == domain.Video) {
			_, err := poster.Enqueue(ctx, tx, a.OwnerID, fresh.CurrentPrimary.String, s.now())
			return err
		}
		return nil
	})
}
func missingObject(err error) bool {
	var wire *objectstore.Error
	return errors.As(err, &wire) && wire.Status == 404
}
func publicType(a dbgen.GetMediaAssetRow) (string, string, string) {
	typeName, _, err := mime.ParseMediaType(a.ContentType)
	if err != nil {
		typeName = ""
	}
	images := map[string]string{"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp", "image/gif": "gif", "image/avif": "avif", "image/bmp": "bmp", "image/tiff": "tiff", "image/x-icon": "ico", "image/vnd.microsoft.icon": "ico"}
	videos := map[string]string{"video/mp4": "mp4", "video/webm": "webm", "video/quicktime": "mov", "video/ogg": "ogv"}
	kind := domain.MediaKind(a.ItemMediaKind)
	if a.Role == "poster" {
		kind = domain.Image
	}
	if kind == domain.Image {
		if ext := images[typeName]; ext != "" {
			return typeName, "inline", ext
		}
	}
	if kind == domain.Video {
		if ext := videos[typeName]; ext != "" {
			return typeName, "inline", ext
		}
	}
	ext := map[string]string{"audio/flac": "flac", "audio/mpeg": "mp3", "audio/mp4": "m4a", "audio/wav": "wav", "audio/ogg": "ogg", "text/plain": "txt", "video/x-matroska": "mkv", "video/x-msvideo": "avi"}[typeName]
	if ext == "" {
		ext = "bin"
	}
	return "application/octet-stream", "attachment", ext
}
func contentRange(r byteRange, size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.last(), size)
}
func rangeHeader(r byteRange) string { return fmt.Sprintf("bytes=%d-%d", r.start, r.last()) }
func partHeaders(r byteRange, size int64, typ string) textproto.MIMEHeader {
	return textproto.MIMEHeader{"Content-Type": {typ}, "Content-Range": {contentRange(r, size)}}
}

type countWriter struct{ n int64 }

func (w *countWriter) Write(p []byte) (int, error) { w.n += int64(len(p)); return len(p), nil }
func multipartSize(boundary, typ string, rs []byteRange, size int64) int64 {
	w := &countWriter{}
	m := multipart.NewWriter(w)
	_ = m.SetBoundary(boundary)
	for _, r := range rs {
		_, _ = m.CreatePart(partHeaders(r, size, typ))
		w.n += r.length
	}
	_ = m.Close()
	return w.n
}
func selectedETag(s string) bool {
	tag, rest, ok := takeTag(s)
	return ok && rest == "" && tag == s && !strings.HasPrefix(tag, "W/") && len(s) <= 512
}

func (s *Service) Serve(w http.ResponseWriter, r *http.Request) error {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		return &Error{Status: 405, Code: "method_not_allowed", Message: "Use GET or HEAD for media"}
	}
	id := strings.TrimPrefix(r.URL.Path, "/media/")
	if !strings.HasPrefix(r.URL.Path, "/media/") || domain.ValidateUUID(id) != nil {
		return &Error{Status: 400, Code: "invalid_asset_id", Message: "Expected an asset UUID"}
	}
	id = strings.ToLower(id)
	a, err := s.lookup(r.Context(), id)
	if err != nil {
		return err
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-r.Context().Done():
		return r.Context().Err()
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	return s.io.ReadKey(ctx, a.ObjectKey, func(reader objectio.KeyReader) error {
		fresh, err := s.lookup(ctx, id)
		if err != nil {
			return err
		}
		if fresh.OwnerID != a.OwnerID || fresh.ObjectKey != a.ObjectKey || fresh.SizeBytes != a.SizeBytes || fresh.Sha256 != a.Sha256 {
			return domain.ErrNotFound
		}
		head, err := reader.Head(ctx)
		if err != nil {
			if missingObject(err) {
				if err := s.missing(ctx, a); err != nil {
					return err
				}
				return &Error{Status: 404, Code: "media_missing", Message: "Media asset is missing", Err: domain.ErrNotFound}
			}
			return gateway(err)
		}
		if head.Size != a.SizeBytes || !selectedETag(head.ETag) {
			return gateway(objectstore.ErrIntegrity)
		}
		modified := head.LastModified.UTC().Truncate(time.Second)
		if head.LastModified.IsZero() {
			modified = time.Time{}
		}
		status, err := conditions(r, head.ETag, modified)
		if err != nil {
			return err
		}
		if status == 412 {
			return &Error{Status: 412, Code: "media_precondition_failed", Message: "Media precondition did not match"}
		}
		typ, disposition, ext := publicType(a)
		var rs []byteRange
		if status == 200 && r.Method == "GET" {
			if len(r.Header.Values("Range")) > 1 {
				return badHeader()
			}
			if value := r.Header.Get("Range"); value != "" && allowRange(r.Header.Get("If-Range"), head.ETag, modified) {
				var requested bool
				rs, requested = ranges(value, a.SizeBytes)
				if requested && len(rs) == 0 {
					status = 416
				} else if len(rs) > 0 {
					status = 206
				}
			}
		}
		base := func() {
			w.Header().Set("ETag", head.ETag)
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			if !modified.IsZero() {
				w.Header().Set("Last-Modified", modified.Format(http.TimeFormat))
			}
		}
		if status == 304 {
			base()
			w.WriteHeader(304)
			return nil
		}
		if status == 416 {
			base()
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", a.SizeBytes))
			w.Header().Set("Content-Length", "0")
			w.WriteHeader(416)
			return nil
		}
		if len(rs) == 0 {
			rs = []byteRange{{start: 0, length: a.SizeBytes}}
		}
		boundary := ""
		length := rs[0].length
		responseType := typ
		if len(rs) > 1 {
			boundary = strings.ReplaceAll(domain.NewUUID(), "-", "")
			responseType = "multipart/byteranges; boundary=" + boundary
			length = multipartSize(boundary, typ, rs, a.SizeBytes)
		}
		writeHeaders := func() {
			base()
			w.Header().Set("Content-Type", responseType)
			w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": "asset-" + id + "." + ext}))
			if disposition == "attachment" {
				w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
			}
			w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
			if status == 206 && len(rs) == 1 {
				w.Header().Set("Content-Range", contentRange(rs[0], a.SizeBytes))
			}
			w.WriteHeader(status)
		}
		if r.Method == "HEAD" {
			writeHeaders()
			return nil
		}
		buf := make([]byte, 32*1024)
		started := false
		var multi *multipart.Writer
		for index, part := range rs {
			options := objectstore.GetOptions{IfMatch: head.ETag}
			if status == 206 {
				options.Range = rangeHeader(part)
			}
			download, err := reader.Get(ctx, options)
			if err != nil {
				if missingObject(err) {
					_ = s.missing(ctx, a)
					err = &Error{Status: 404, Code: "media_missing", Message: "Media asset is missing", Err: domain.ErrNotFound}
				} else {
					err = gateway(err)
				}
				return streamError(err, started)
			}
			if download.Size != part.length || download.ETag != head.ETag || (status == 206 && download.ContentRange != contentRange(part, a.SizeBytes)) || (status == 200 && download.ContentRange != "") {
				download.Body.Close()
				return streamError(gateway(objectstore.ErrIntegrity), started)
			}
			err = func() error {
				defer download.Body.Close()
				first := min(int64(len(buf)), part.length)
				n, readErr := download.Body.Read(buf[:first])
				if readErr != nil && !(errors.Is(readErr, io.EOF) && int64(n) == part.length) {
					return gateway(readErr)
				}
				if n == 0 && first > 0 {
					return gateway(io.ErrNoProgress)
				}
				if int64(n) == part.length {
					if err := endOfBody(download.Body); err != nil {
						return gateway(err)
					}
				}
				if !started {
					writeHeaders()
					started = true
					if boundary != "" {
						multi = multipart.NewWriter(w)
						_ = multi.SetBoundary(boundary)
					}
				}
				var target io.Writer = w
				if multi != nil {
					var err error
					target, err = multi.CreatePart(partHeaders(part, a.SizeBytes, typ))
					if err != nil {
						return err
					}
				}
				if n > 0 {
					if written, err := target.Write(buf[:n]); err != nil {
						return err
					} else if written != n {
						return io.ErrShortWrite
					}
				}
				if index == 0 {
					if err := http.NewResponseController(w).Flush(); err != nil && !errors.Is(err, http.ErrNotSupported) {
						return err
					}
				}
				remaining := part.length - int64(n)
				for remaining > 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
					count, err := io.ReadFull(download.Body, buf[:min(int64(len(buf)), remaining)])
					if err != nil {
						return err
					}
					if int64(count) == remaining {
						if err := endOfBody(download.Body); err != nil {
							return err
						}
					}
					written, err := target.Write(buf[:count])
					if err != nil {
						return err
					}
					if written != count {
						return io.ErrShortWrite
					}
					remaining -= int64(count)
				}
				return nil
			}()
			if err != nil {
				cancel()
				return streamError(err, started)
			}
		}
		if multi != nil {
			if err := multi.Close(); err != nil {
				return streamError(err, started)
			}
		}
		return nil
	})
}

func endOfBody(body io.Reader) error {
	var tail [1]byte
	n, err := body.Read(tail[:])
	if n != 0 {
		return objectstore.ErrIntegrity
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	return io.ErrNoProgress
}
func streamError(err error, started bool) error {
	if !started {
		return err
	}
	return &Error{Status: 502, Code: "media_stream_interrupted", Message: "Media stream interrupted", Err: err, Started: true}
}
