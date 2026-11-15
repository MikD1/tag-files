package migrate

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mikd1/tag-files/app/internal/objectstore"
)

type multipartDestination interface {
	CreateMultipart(context.Context, string, string, map[string]string) (string, error)
	ListMultipart(context.Context, string) ([]objectstore.Multipart, error)
	ListParts(context.Context, string, string) ([]objectstore.Part, error)
	UploadPart(context.Context, string, string, int, int64, string, io.Reader) (objectstore.Part, error)
	Complete(context.Context, string, string, []objectstore.Part) (objectstore.Verified, error)
}

func currentEpoch(d Destination) string {
	if bound, ok := d.(interface{ BoundEpoch() string }); ok {
		return bound.BoundEpoch()
	}
	return ""
}
func multipartPartSize(c ApplyConfig, p mapping) (int64, error) {
	partSize := c.PartSize
	if partSize == 0 {
		partSize = 16 << 20
	}
	if partSize < 5<<20 || partSize > 5<<30 || p.Size <= 0 || (p.Size-1)/partSize+1 > 10000 {
		return 0, fmt.Errorf("multipart geometry exceeds S3 limits")
	}
	return partSize, nil
}

func copyMultipart(ctx context.Context, c ApplyConfig, j *journal, p mapping, file *os.File, d Destination, credit multipartCredit, beforeComplete func() error) error {
	remote, ok := d.(multipartDestination)
	if !ok {
		return fmt.Errorf("large object needs resumable multipart destination")
	}
	partSize, e := multipartPartSize(c, p)
	if e != nil {
		return e
	}
	var uploadID, state, epoch string
	var persistedSize int64
	e = j.db.QueryRowContext(ctx, `SELECT upload_id,state,epoch,part_size FROM multipart WHERE object_id=?`, p.ID).Scan(&uploadID, &state, &epoch, &persistedSize)
	fresh := errors.Is(e, sql.ErrNoRows)
	if e != nil && !fresh {
		return e
	}
	if fresh {
		epoch = currentEpoch(d)
		if _, e = j.db.ExecContext(ctx, `INSERT INTO multipart(object_id,state,epoch,part_size) VALUES(?,'creating',?,?)`, p.ID, epoch, partSize); e != nil {
			return e
		}
	} else if persistedSize != partSize {
		return fmt.Errorf("multipart geometry changed on resume")
	}
	if uploadID == "" {
		listed, e := remote.ListMultipart(ctx, p.Key)
		if e != nil {
			return e
		}
		matches := []string{}
		for _, m := range listed {
			if m.Key == p.Key || m.Key == "" {
				matches = append(matches, m.UploadID)
			}
		}
		if len(matches) > 1 {
			return fmt.Errorf("multiple owned multipart uploads require explicit reconciliation")
		}
		if len(matches) == 1 {
			uploadID = matches[0]
		} else {
			dispatchEpoch := currentEpoch(d)
			if !fresh && (epoch == "" || dispatchEpoch == "" || epoch == dispatchEpoch) {
				return fmt.Errorf("uncertain CreateMultipart cannot be repeated before a storage epoch change")
			}
			// Each request can become uncertain independently. Persist this
			// attempt's epoch under WAL/FULL before network dispatch, including
			// an allowed retry after storage has crossed an epoch barrier.
			result, err := j.db.ExecContext(ctx, `UPDATE multipart SET epoch=?,state='creating' WHERE object_id=? AND upload_id='' AND epoch=?`, dispatchEpoch, p.ID, epoch)
			if err != nil {
				return err
			}
			if rows, err := result.RowsAffected(); err != nil || rows != 1 {
				return fmt.Errorf("multipart attempt fence changed: rows=%d error=%v", rows, err)
			}
			uploadID, e = remote.CreateMultipart(ctx, p.Key, p.ContentType, p.Metadata)
			if e != nil {
				return e
			}
		}
		if _, e = j.db.ExecContext(ctx, `UPDATE multipart SET upload_id=?,state='copying' WHERE object_id=? AND upload_id=''`, uploadID, p.ID); e != nil {
			return e
		}
	}
	listed, e := remote.ListParts(ctx, p.Key, uploadID)
	if e != nil {
		return e
	}
	byNumber := map[int]objectstore.Part{}
	for _, part := range listed {
		if _, ok := byNumber[part.Number]; ok {
			return fmt.Errorf("duplicate multipart part")
		}
		byNumber[part.Number] = part
	}
	if len(credit.parts) > 0 && credit.uploadID != uploadID {
		return fmt.Errorf("%w: credited multipart identity changed", objectstore.ErrIntegrity)
	}
	for number, proven := range credit.parts {
		if current, ok := byNumber[number]; !ok || current != proven {
			return fmt.Errorf("%w: credited multipart part %d changed before dispatch", objectstore.ErrIntegrity, number)
		}
	}
	var parts []objectstore.Part
	for offset, number := int64(0), 1; offset < p.Size; offset, number = offset+partSize, number+1 {
		n := partSize
		if p.Size-offset < n {
			n = p.Size - offset
		}
		_, hash, e := digestReader(io.NewSectionReader(file, offset, n))
		if e != nil {
			return e
		}
		raw, _ := hex.DecodeString(hash)
		sha := base64.StdEncoding.EncodeToString(raw)
		var oldSize int64
		var oldSHA string
		var etag sql.NullString
		e = j.db.QueryRowContext(ctx, `SELECT size,sha256,etag FROM parts WHERE object_id=? AND number=?`, p.ID, number).Scan(&oldSize, &oldSHA, &etag)
		bound := e == nil
		if errors.Is(e, sql.ErrNoRows) {
			if _, e = j.db.ExecContext(ctx, `INSERT INTO parts(object_id,number,size,sha256) VALUES(?,?,?,?)`, p.ID, number, n, sha); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else if oldSize != n || oldSHA != sha {
			return fmt.Errorf("source no longer matches immutable part binding")
		}
		part, exists := byNumber[number]
		if exists {
			if !bound || part.Size != n || (part.SHA256 != "" && part.SHA256 != sha) || (etag.Valid && etag.String != part.ETag) || part.ETag == "" {
				return fmt.Errorf("listed part differs from immutable binding")
			}
			part.SHA256 = sha
			delete(byNumber, number)
		} else {
			part, e = remote.UploadPart(ctx, p.Key, uploadID, number, n, sha, io.NewSectionReader(file, offset, n))
			if e != nil {
				return e
			}
			if part.Number != number || part.Size != n || part.SHA256 != sha || part.ETag == "" {
				return fmt.Errorf("multipart acknowledgement differs from immutable binding")
			}
			if e = persistMultipartACK(ctx, j, p.ID, uploadID, part); e != nil {
				return e
			}
		}
		if _, e = j.db.ExecContext(ctx, `UPDATE parts SET etag=? WHERE object_id=? AND number=?`, part.ETag, p.ID, number); e != nil {
			return e
		}
		parts = append(parts, part)
	}
	if len(byNumber) > 0 {
		return fmt.Errorf("destination has unbound multipart parts")
	}
	if e = beforeComplete(); e != nil {
		return e
	}
	if _, e = j.db.ExecContext(ctx, `UPDATE multipart SET state='completing' WHERE object_id=?`, p.ID); e != nil {
		return e
	}
	if _, e = remote.Complete(ctx, p.Key, uploadID, parts); e != nil {
		return e
	}
	if _, e = d.Verify(ctx, p.Key, p.Size, p.SHA256); e != nil {
		return e
	}
	_, e = j.db.ExecContext(ctx, `UPDATE multipart SET state='verified' WHERE object_id=?`, p.ID)
	return e
}
