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

type multipartCredit struct {
	bytes        int64
	uploadID     string
	parts        map[int]objectstore.Part
	missingProof string
}

// This additive journal table deliberately does not infer an ACK from the old
// parts.etag column: older tools also wrote that field after checksum-less
// ListParts reconciliation. Only an actually validated UploadPart reply creates
// this proof, in a WAL/FULL commit before any later operation can lose the reply.
func persistMultipartACK(ctx context.Context, j *journal, objectID, uploadID string, part objectstore.Part) error {
	_, err := j.db.ExecContext(ctx, `INSERT INTO part_acknowledgements(object_id,number,upload_id,size,sha256,etag) VALUES(?,?,?,?,?,?) ON CONFLICT(object_id,number) DO NOTHING`, objectID, part.Number, uploadID, part.Size, part.SHA256, part.ETag)
	if err != nil {
		return err
	}
	var count int
	err = j.db.QueryRowContext(ctx, `SELECT count(*) FROM part_acknowledgements WHERE object_id=? AND number=? AND upload_id=? AND size=? AND sha256=? AND etag=?`, objectID, part.Number, uploadID, part.Size, part.SHA256, part.ETag).Scan(&count)
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: immutable multipart acknowledgement differs", objectstore.ErrIntegrity)
	}
	return nil
}

func preflightMultipartCredit(ctx context.Context, c ApplyConfig, j *journal, p mapping, d Destination) (multipartCredit, error) {
	credit := multipartCredit{parts: map[int]objectstore.Part{}}
	var persistedSize int64
	err := j.db.QueryRowContext(ctx, `SELECT upload_id,part_size FROM multipart WHERE object_id=?`, p.ID).Scan(&credit.uploadID, &persistedSize)
	if errors.Is(err, sql.ErrNoRows) {
		return credit, nil
	}
	if err != nil {
		return credit, err
	}
	partSize, err := multipartPartSize(c, p)
	if err != nil || partSize != persistedSize {
		return credit, fmt.Errorf("%w: multipart geometry changed on resume", objectstore.ErrIntegrity)
	}
	if credit.uploadID == "" {
		return credit, nil // No part may dispatch before the upload ID is durable.
	}
	remote, ok := d.(multipartDestination)
	if !ok {
		return credit, fmt.Errorf("large object needs resumable multipart destination")
	}
	listed, err := remote.ListParts(ctx, p.Key, credit.uploadID)
	if err != nil {
		return credit, err
	}
	if len(listed) > 10000 {
		return credit, fmt.Errorf("%w: too many listed multipart parts", objectstore.ErrIntegrity)
	}
	path, err := safePath(c.Archive, p.ArchivePath)
	if err != nil {
		return credit, err
	}
	file, err := os.Open(path)
	if err != nil {
		return credit, err
	}
	defer file.Close()
	seen := map[int]bool{}
	for _, part := range listed {
		if err := ctx.Err(); err != nil {
			return credit, err
		}
		if part.Number < 1 || part.Number > 10000 || seen[part.Number] {
			return credit, fmt.Errorf("%w: invalid/duplicate listed multipart part", objectstore.ErrIntegrity)
		}
		seen[part.Number] = true
		offset := int64(part.Number-1) * partSize
		size := min(partSize, p.Size-offset)
		if offset >= p.Size || size <= 0 || part.Size != size || part.ETag == "" {
			return credit, fmt.Errorf("%w: listed multipart geometry differs", objectstore.ErrIntegrity)
		}
		var boundSize int64
		var boundSHA string
		var etag sql.NullString
		err = j.db.QueryRowContext(ctx, `SELECT size,sha256,etag FROM parts WHERE object_id=? AND number=?`, p.ID, part.Number).Scan(&boundSize, &boundSHA, &etag)
		if errors.Is(err, sql.ErrNoRows) {
			return credit, fmt.Errorf("%w: unbound listed multipart part %d", objectstore.ErrIntegrity, part.Number)
		}
		if err != nil {
			return credit, err
		}
		n, sum, err := digestReader(io.NewSectionReader(file, offset, size))
		if err != nil {
			return credit, err
		}
		raw, _ := hex.DecodeString(sum)
		sha := base64.StdEncoding.EncodeToString(raw)
		if n != size || boundSize != size || boundSHA != sha || (etag.Valid && etag.String != part.ETag) {
			return credit, fmt.Errorf("%w: source/listed part differs from immutable binding", objectstore.ErrIntegrity)
		}
		if part.SHA256 != "" {
			// Exact comparison with our canonical source digest rejects wrong,
			// malformed and noncanonical checksums; ETag never substitutes for SHA.
			if part.SHA256 != sha {
				return credit, fmt.Errorf("%w: listed multipart checksum differs", objectstore.ErrIntegrity)
			}
		} else {
			var ackID, ackSHA, ackETag string
			var ackSize int64
			err = j.db.QueryRowContext(ctx, `SELECT upload_id,size,sha256,etag FROM part_acknowledgements WHERE object_id=? AND number=?`, p.ID, part.Number).Scan(&ackID, &ackSize, &ackSHA, &ackETag)
			if errors.Is(err, sql.ErrNoRows) {
				if credit.missingProof == "" {
					credit.missingProof = fmt.Sprintf("partial MPU %s part %d: no checksum or validated acknowledgement; no capacity credit", p.Key, part.Number)
				}
				continue
			}
			if err != nil {
				return credit, err
			}
			if ackID != credit.uploadID || ackSize != size || ackSHA != sha || ackETag != part.ETag {
				return credit, fmt.Errorf("%w: listed multipart part differs from validated acknowledgement", objectstore.ErrIntegrity)
			}
		}
		credit.bytes += size
		credit.parts[part.Number] = part
	}
	return credit, nil
}
