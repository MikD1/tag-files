// Package migrate is an external, offline tool. It is not linked into the app.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/mikd1/tag-files/app/internal/objectstore"
)

const ArchiveVersion = 1

var (
	ErrSourceChanged = errors.New("frozen source changed")
	ErrUnresolved    = errors.New("unresolved migration exceptions")
	ErrBudget        = errors.New("migration disk budget exhausted")
	ErrNotFound      = errors.New("object not found")
)

func digest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func digestReader(r io.Reader) (int64, string, error) {
	h := sha256.New()
	n, e := io.CopyBuffer(h, r, make([]byte, 128<<10))
	return n, hex.EncodeToString(h.Sum(nil)), e
}

type SourceObject struct {
	Bucket               string            `json:"bucket"`
	Key                  string            `json:"key"`
	VersionID            string            `json:"versionId,omitempty"`
	ETag                 string            `json:"etag,omitempty"`
	Size                 int64             `json:"size"`
	Latest               bool              `json:"latest"`
	DeleteMarker         bool              `json:"deleteMarker,omitempty"`
	ContentType          string            `json:"contentType,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	Tags                 map[string]string `json:"tags,omitempty"`
	LastModified         string            `json:"lastModified,omitempty"`
	CacheControl         string            `json:"cacheControl,omitempty"`
	ContentDisposition   string            `json:"contentDisposition,omitempty"`
	ContentEncoding      string            `json:"contentEncoding,omitempty"`
	ContentLanguage      string            `json:"contentLanguage,omitempty"`
	Expires              string            `json:"expires,omitempty"`
	ServerSideEncryption string            `json:"serverSideEncryption,omitempty"`
	SSEKMSKeyID          string            `json:"sseKmsKeyId,omitempty"`
	StorageClass         string            `json:"storageClass,omitempty"`
	SHA256               string            `json:"sha256,omitempty"`
	ArchivePath          string            `json:"archivePath,omitempty"`
}

func (o SourceObject) Identity() string {
	return o.Bucket + "\x00" + o.Key + "\x00" + o.VersionID + fmt.Sprint(o.DeleteMarker)
}

type Multipart struct {
	Bucket, Key, UploadID, Initiated string
	Parts                            []Part
}
type Part struct {
	Number       int32
	Size         int64
	ETag, SHA256 string
}
type Inventory struct {
	Objects             []SourceObject    `json:"objects"`
	Multipart           []Multipart       `json:"multipart,omitempty"`
	BucketConfiguration map[string]string `json:"bucketConfiguration,omitempty"`
}
type Source interface {
	Inventory(context.Context) (Inventory, error)
	Open(context.Context, SourceObject) (io.ReadCloser, error)
}
type Destination interface {
	Head(context.Context, string) (objectstore.Object, error)
	Put(context.Context, string, io.Reader, int64, string, string, map[string]string) error
	Verify(context.Context, string, int64, string) (objectstore.Verified, error)
}
type NativeFile struct {
	Path        string `json:"path"`
	ArchivePath string `json:"archivePath"`
	Size        int64  `json:"size"`
	SHA256      string `json:"sha256"`
}
type Issue struct {
	ID       string `json:"id"`
	Code     string `json:"code"`
	Entity   string `json:"entity"`
	Reason   string `json:"reason"`
	Blocking bool   `json:"blocking"`
}
type Exception struct {
	IssueID  string `json:"issueId"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Owner    string `json:"owner"`
}
type SQLiteObservation struct {
	UserVersion          int        `json:"userVersion"`
	JournalMode          string     `json:"journalMode"`
	Integrity            string     `json:"integrity"`
	ForeignKeyViolations [][]string `json:"foreignKeyViolations"`
}

type Manifest struct {
	SQLite            SQLiteObservation `json:"sqlite"`
	Version           int               `json:"version"`
	RunID             string            `json:"runId"`
	CreatedAt         string            `json:"createdAt"`
	SnapshotID        string            `json:"snapshotId"`
	RollbackReference string            `json:"rollbackReference"`
	Database          string            `json:"database"`
	NativeFiles       []NativeFile      `json:"nativeFiles"`
	SnapshotSHA256    string            `json:"snapshotSha256"`
	RawRowsSHA256     string            `json:"rawRowsSha256"`
	Inventory         Inventory         `json:"inventory"`
	Objects           []SourceObject    `json:"objects"`
	Model             Model             `json:"model"`
	Issues            []Issue           `json:"issues"`
	Fingerprint       string            `json:"fingerprint"`
}
type ScanConfig struct {
	Database, Archive, SnapshotID, RollbackReference string
	RollbackVerified                                 bool
	ArchiveReserveBytes                              int64
}
type Budget struct {
	WritePeakBytes int64 `json:"writePeakBytes"`
	MetadataBytes  int64 `json:"metadataBytes"`
	ReserveBytes   int64 `json:"reserveBytes"`
}
type ApplyConfig struct {
	Archive, Journal, TargetID string
	PartSize, SinglePutLimit   int64
	Budget                     Budget
	Exceptions                 []Exception
	// Capacity must measure the destination filesystem, not the caller's host.
	Capacity func(context.Context) (availableBytes, availableInodes int64, err error)
}
type CategoryResult struct {
	ID        int64   `json:"id"`
	SourceIDs []int64 `json:"sourceIds"`
	TargetIDs []int64 `json:"targetIds"`
}
type ItemResult struct {
	ID                   int64    `json:"id"`
	PrimaryAssetID       string   `json:"primaryAssetId,omitempty"`
	Bucket               string   `json:"sourceBucket,omitempty"`
	Key                  string   `json:"sourceKey,omitempty"`
	VersionID            string   `json:"sourceVersionId,omitempty"`
	SHA256               string   `json:"sha256,omitempty"`
	Provenance           string   `json:"provenance"`
	OriginalAvailability string   `json:"originalAvailability"`
	Evidence             []string `json:"evidence"`
}

type Report struct {
	Items             []ItemResult     `json:"items"`
	SourceObjectCount int              `json:"sourceObjectCount"`
	SourceBytes       int64            `json:"sourceBytes"`
	Version           int              `json:"version"`
	RunID             string           `json:"runId"`
	Complete          bool             `json:"complete"`
	ObjectsVerified   int              `json:"objectsVerified"`
	BytesVerified     int64            `json:"bytesVerified"`
	Categories        []CategoryResult `json:"categories"`
	Issues            []Issue          `json:"issues"`
	Exceptions        []Exception      `json:"exceptions"`
	VerifiedAt        string           `json:"verifiedAt"`
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
