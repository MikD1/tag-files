// Package ingestion owns upload state, immutable part bindings and publication.
// Transaction commands do no object/media I/O; their caller owns commit/rollback.
package ingestion

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/store"
	"github.com/mikd1/tag-files/app/internal/store/dbgen"
)

var ErrConflict = errors.New("upload generation, state or immutable binding conflicts")

type Limits struct {
	MaxFileSize, PartSize int64
	MaxParallelTransfers  int
}

func DefaultLimits() Limits {
	return Limits{MaxFileSize: 20 * 1024 * 1024 * 1024, PartSize: 32 * 1024 * 1024, MaxParallelTransfers: 2}
}
func (l Limits) validate() error {
	if l.MaxFileSize < 1 || l.MaxFileSize > domain.MaxSafeInteger || l.PartSize < 5*1024*1024 || l.PartSize > 5*1024*1024*1024 || l.MaxFileSize > l.PartSize*10000 || l.MaxParallelTransfers < 1 || l.MaxParallelTransfers > 32 {
		return domain.Invalid("limits", "Invalid bounded upload limits")
	}
	return nil
}

type File struct {
	ClientFileID, Filename, Fingerprint string
	Size                                int64
}
type PartBinding struct {
	Number int
	Size   int64
	SHA256 string
}
type Part struct {
	PartBinding
	ETag, State string
}
type Session struct {
	dbgen.UploadSession
	Parts []Part
}
type Batch struct {
	ID       string
	Sessions []Session
}
type Outcome struct {
	State   string
	ItemID  int64
	Session *Session
	JobID   string
}

func validHash(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}
func validFilename(value string) bool {
	if !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return false
	}
	count := utf8.RuneCountInString(value)
	return count > 0 && count <= 1024
}
func canonicalID(value string) (string, error) {
	if err := domain.ValidateUUID(value); err != nil {
		return "", err
	}
	return strings.ToLower(value), nil
}
func missing(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}

// Create persists the whole batch's intent. In particular it does not initiate
// an MPU; that may happen only after the caller commits this transaction.
func Create(ctx context.Context, tx *sql.Tx, files []File, collectionID *int64, limits Limits, now time.Time) (Batch, error) {
	if err := limits.validate(); err != nil {
		return Batch{}, err
	}
	if len(files) < 1 || len(files) > 500 {
		return Batch{}, domain.Invalid("files", "Select 1 to 500 files")
	}
	seen := map[string]bool{}
	for _, file := range files {
		id, err := canonicalID(file.ClientFileID)
		if err != nil || seen[id] {
			return Batch{}, domain.Invalid("clientFileId", "File identities must be unique UUIDs")
		}
		seen[id] = true
		if !validFilename(file.Filename) {
			return Batch{}, domain.Invalid("filename", "Filename must contain 1 to 1024 characters")
		}
		if file.Size < 1 {
			return Batch{}, domain.Invalid("size", "File must be nonempty")
		}
		if file.Size > limits.MaxFileSize {
			return Batch{}, &domain.ValidationError{Field: "size", Code: "file_too_large", Message: "File exceeds the configured upload limit"}
		}
		if file.Fingerprint != "" && !validHash(file.Fingerprint) {
			return Batch{}, domain.Invalid("fingerprint", "Fingerprint must be a lowercase SHA-256 hex digest")
		}
	}
	q := dbgen.New(tx)
	collection := sql.NullInt64{}
	if collectionID != nil {
		if err := domain.ValidateID(*collectionID); err != nil {
			return Batch{}, err
		}
		if _, err := q.GetCollection(ctx, *collectionID); err != nil {
			return Batch{}, missing(err)
		}
		collection = sql.NullInt64{Int64: *collectionID, Valid: true}
	}
	library, err := q.GetLibraryMeta(ctx)
	if err != nil {
		return Batch{}, err
	}
	batch := Batch{ID: domain.NewUUID(), Sessions: make([]Session, 0, len(files))}
	for _, file := range files {
		id := domain.NewUUID()
		row, err := q.CreateUploadSession(ctx, dbgen.CreateUploadSessionParams{ID: id, BatchID: batch.ID, ClientFileID: strings.ToLower(file.ClientFileID), ClientFilename: file.Filename, Fingerprint: sql.NullString{String: file.Fingerprint, Valid: file.Fingerprint != ""}, ExpectedSize: file.Size, PartSize: limits.PartSize, PartCount: (file.Size-1)/limits.PartSize + 1, IncomingKey: "incoming/" + library.LibraryID + "/" + id + "/source", CollectionID: collection, CreatedAtUtc: store.Timestamp(now)})
		if err != nil {
			return Batch{}, err
		}
		batch.Sessions = append(batch.Sessions, Session{UploadSession: row, Parts: []Part{}})
	}
	return batch, nil
}

func readSession(ctx context.Context, q *dbgen.Queries, id string) (Session, error) {
	row, err := q.GetUploadSession(ctx, id)
	if err != nil {
		return Session{}, missing(err)
	}
	return sessionParts(ctx, q, row)
}
func sessionParts(ctx context.Context, q *dbgen.Queries, row dbgen.UploadSession) (Session, error) {
	parts, err := q.ListUploadParts(ctx, row.ID)
	if err != nil {
		return Session{}, err
	}
	session := Session{UploadSession: row, Parts: make([]Part, 0, len(parts))}
	for _, part := range parts {
		session.Parts = append(session.Parts, partFromRow(part))
	}
	return session, nil
}
func partFromRow(part dbgen.UploadPart) Part {
	return Part{PartBinding: PartBinding{Number: int(part.PartNumber), Size: part.ExpectedSize, SHA256: part.Sha256}, ETag: part.Etag.String, State: part.State}
}

// ListBatches is a projection of live sessions, including retained failures.
// Deleting the last session removes a batch without a terminal/history row.
func ListBatches(ctx context.Context, db *sql.DB) ([]Batch, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	q := dbgen.New(tx)
	rows, err := q.ListUploadSessions(ctx)
	if err != nil {
		return nil, err
	}
	batches := []Batch{}
	index := map[string]int{}
	for _, row := range rows {
		session, err := sessionParts(ctx, q, row)
		if err != nil {
			return nil, err
		}
		i, found := index[row.BatchID]
		if !found {
			i = len(batches)
			index[row.BatchID] = i
			batches = append(batches, Batch{ID: row.BatchID, Sessions: []Session{}})
		}
		batches[i].Sessions = append(batches[i].Sessions, session)
	}
	return batches, tx.Commit()
}

// Lookup takes one read snapshot so an atomic ready commit cannot turn a
// ready-first lookup into a transient 404 between the item/session reads.
func Lookup(ctx context.Context, db *sql.DB, id string) (Outcome, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Outcome{}, err
	}
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Outcome{}, err
	}
	defer tx.Rollback()
	result, err := lookup(ctx, dbgen.New(tx), id)
	if err != nil {
		return Outcome{}, err
	}
	return result, tx.Commit()
}
func lookup(ctx context.Context, q *dbgen.Queries, id string) (Outcome, error) {
	item, err := q.FindReadyItemByUpload(ctx, sql.NullString{String: id, Valid: true})
	if err == nil {
		return Outcome{State: "ready", ItemID: item.ID}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, err
	}
	session, err := readSession(ctx, q, id)
	if err != nil {
		return Outcome{}, err
	}
	state := "pending"
	if session.State == domain.SessionFailed {
		state = "failed"
	}
	result := Outcome{State: state, Session: &session}
	job, err := q.GetIngestionJob(ctx, "ingest/"+id)
	if err == nil {
		result.JobID = job.ID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, err
	}
	return result, nil
}

// AttachMultipart records one opaque S3 identity. Unknown outcomes must first
// reconcile this session's exact owned key, rather than replacing an MPU here.
func AttachMultipart(ctx context.Context, tx *sql.Tx, id string, generation int64, mpu string) error {
	id, err := canonicalID(id)
	if err != nil {
		return err
	}
	if len(mpu) < 1 || len(mpu) > 4096 || strings.ContainsAny(mpu, "\x00\r\n") {
		return domain.Invalid("multipartUploadId", "Invalid opaque multipart identity")
	}
	q := dbgen.New(tx)
	row, err := q.GetUploadSession(ctx, id)
	if err != nil {
		return missing(err)
	}
	if row.Generation != generation {
		return ErrConflict
	}
	if row.MultipartUploadID.Valid {
		if row.MultipartUploadID.String == mpu && row.State == domain.SessionReceiving {
			return nil
		}
		return ErrConflict
	}
	if row.State != domain.SessionCreating {
		return ErrConflict
	}
	n, err := q.AttachUploadMultipart(ctx, dbgen.AttachUploadMultipartParams{ID: id, Generation: generation, MultipartUploadID: sql.NullString{String: mpu, Valid: true}})
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// BindPart commits size/content identity before any request body is forwarded.
// A repeated number can never acquire different bytes, including after reopen.
func BindPart(ctx context.Context, tx *sql.Tx, id string, generation int64, binding PartBinding) (Part, error) {
	id, err := canonicalID(id)
	if err != nil {
		return Part{}, err
	}
	q := dbgen.New(tx)
	row, err := q.GetUploadSession(ctx, id)
	if err != nil {
		return Part{}, missing(err)
	}
	if generation != row.Generation || row.State != domain.SessionReceiving || !row.MultipartUploadID.Valid {
		return Part{}, ErrConflict
	}
	if binding.Number < 1 || int64(binding.Number) > row.PartCount {
		return Part{}, ErrConflict
	}
	size := row.PartSize
	if int64(binding.Number) == row.PartCount {
		size = row.ExpectedSize - (row.PartCount-1)*row.PartSize
	}
	if binding.Size != size {
		return Part{}, ErrConflict
	}
	sum, err := base64.StdEncoding.Strict().DecodeString(binding.SHA256)
	if err != nil || len(sum) != 32 || base64.StdEncoding.EncodeToString(sum) != binding.SHA256 {
		return Part{}, domain.Invalid("sha256", "Expected a canonical base64 SHA-256 digest")
	}
	if err := q.BindUploadPart(ctx, dbgen.BindUploadPartParams{SessionID: id, PartNumber: int64(binding.Number), ExpectedSize: binding.Size, Sha256: binding.SHA256}); err != nil {
		return Part{}, err
	}
	part, err := q.GetUploadPart(ctx, dbgen.GetUploadPartParams{SessionID: id, PartNumber: int64(binding.Number)})
	if err != nil {
		return Part{}, err
	}
	if part.ExpectedSize != binding.Size || part.Sha256 != binding.SHA256 {
		return Part{}, ErrConflict
	}
	return partFromRow(part), nil
}
