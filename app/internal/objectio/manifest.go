package objectio

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"unicode/utf8"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/objectstore"
)

type manifestPart struct {
	Number int    `json:"partNumber"`
	Size   int64  `json:"size"`
	ETag   string `json:"etag"`
	SHA256 string `json:"sha256"`
}

// CompletionIdentity hashes version-1 canonical ordered request facts. This is
// an intent/manifest identity, never the SHA-256 of the completed file.
func CompletionIdentity(parts []objectstore.Part) (int64, string, error) {
	if len(parts) < 1 || len(parts) > 10000 {
		return 0, "", domain.Invalid("parts", "Invalid multipart manifest length")
	}
	entries := make([]manifestPart, len(parts))
	var size int64
	for i, part := range parts {
		if part.Number != i+1 || part.Size < 1 || part.Size > 5*1024*1024*1024 || part.Size > domain.MaxSafeInteger-size || (i < len(parts)-1 && part.Size < 5*1024*1024) || part.ETag == "" || !utf8.ValidString(part.ETag) || !base64SHA(part.SHA256) {
			return 0, "", domain.Invalid("parts", "Invalid ordered multipart manifest")
		}
		size += part.Size
		entries[i] = manifestPart{Number: part.Number, Size: part.Size, ETag: part.ETag, SHA256: part.SHA256}
	}
	data, err := json.Marshal(struct {
		Version int            `json:"version"`
		Parts   []manifestPart `json:"parts"`
	}{Version: 1, Parts: entries})
	if err != nil {
		return 0, "", err
	}
	sum := sha256.Sum256(data)
	return size, hex.EncodeToString(sum[:]), nil
}

// Complete accepts only the caller's already-drained, frozen binding manifest.
// Its identity must match the durable request before the adapter can send it.
// The adapter verifies HEAD and every part/full-file hash after every success.
func (m *Manager) Complete(ctx context.Context, prepare func(*sql.Tx) (Request, error), parts []objectstore.Part, commit func(objectstore.Verified) error) (objectstore.Verified, error) {
	manifest := append([]objectstore.Part(nil), parts...)
	size, identity, err := CompletionIdentity(manifest)
	if err != nil {
		return objectstore.Verified{}, err
	}
	checked := func(tx *sql.Tx) (Request, error) {
		r, err := prepare(tx)
		if err != nil {
			return Request{}, err
		}
		if r.Arguments.Size != size || r.Arguments.ManifestSHA256 != identity {
			return Request{}, domain.Invalid("parts", "Frozen manifest does not match the write intent")
		}
		return r, nil
	}
	return run(ctx, m, expecting(CompleteMPU, checked), func(bound *objectstore.Client, r Request) (objectstore.Verified, error) {
		return bound.Complete(ctx, r.Key, r.Arguments.MultipartID, manifest)
	}, commit)
}
