// Package objectstore owns the tested S3 wire profile. It performs no SQLite
// work and never decides ownership, publication, retries or cleanup policy.
package objectstore

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

var ErrIntegrity = errors.New("stored object failed full size/SHA256 verification")

type Config struct {
	Endpoint, Region, Bucket, AccessKey, SecretKey string
	HTTPClient                                     aws.HTTPClient
}
type Client struct {
	s3                             *s3.Client
	bucket                         string
	storageID, libraryID, volumeID string
	epoch                          string
	control                        *storageprotocol.Client
}
type Object struct {
	Key, ETag, ContentType string
	Size                   int64
	Metadata               map[string]string
	LastModified           time.Time
}
type GetOptions struct {
	Range, IfMatch, IfNoneMatch        string
	IfModifiedSince, IfUnmodifiedSince *time.Time
}
type Download struct {
	Object
	Body         io.ReadCloser
	ContentRange string
}
type Verified struct {
	Object
	SHA256 string
}
type Page struct {
	Objects   []Object
	NextToken string
}

// Part.SHA256 is present on a verified UploadPart acknowledgement and is
// required for Complete. Seaweed 4.53 omits it from ListParts: callers must
// reconcile with their immutable durable binding, never invent a checksum.
type Part struct {
	Number       int
	Size         int64
	ETag, SHA256 string
}
type Multipart struct{ Key, UploadID string }

type Error struct {
	Status int
	Code   string
	Cause  error
}

func (e *Error) Error() string { return fmt.Sprintf("S3 %s (HTTP %d): %v", e.Code, e.Status, e.Cause) }
func (e *Error) Unwrap() error { return e.Cause }
func failure(err error) error {
	if err == nil {
		return nil
	}
	e := &Error{Cause: err}
	var response *smithyhttp.ResponseError
	if errors.As(err, &response) {
		e.Status = response.HTTPStatusCode()
	}
	var api smithy.APIError
	if errors.As(err, &api) {
		e.Code = api.ErrorCode()
	}
	return e
}

func New(config Config) (*Client, error) {
	u, err := url.Parse(config.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("S3 endpoint must be an HTTP(S) origin")
	}
	if config.Region == "" || config.Bucket == "" || config.AccessKey == "" || config.SecretKey == "" {
		return nil, fmt.Errorf("explicit private S3 region/bucket/credentials are required")
	}
	credentials := aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: config.AccessKey, SecretAccessKey: config.SecretKey, Source: "tag-files-config"}, nil
	})
	client := s3.NewFromConfig(aws.Config{Region: config.Region, Credentials: credentials, HTTPClient: config.HTTPClient}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(config.Endpoint)
		o.UsePathStyle = true
		o.RetryMaxAttempts = 3
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	control, err := storageprotocol.NewClient(config.Endpoint, config.AccessKey, config.SecretKey, config.HTTPClient)
	if err != nil {
		return nil, err
	}
	return &Client{s3: client, bucket: config.Bucket, control: control}, nil
}

func oneAttempt(o *s3.Options) { o.RetryMaxAttempts = 1 }
func nonseekable(o *s3.Options) {
	oneAttempt(o)
	// The explicit SHA256 checksum still covers the actual bytes. This avoids
	// the signer's seek and any implicit retry of an exhausted browser body.
	o.APIOptions = append(o.APIOptions, v4.SwapComputePayloadSHA256ForUnsignedPayloadMiddleware)
}
func hexChecksum(value string) (string, error) {
	b, err := hex.DecodeString(value)
	if err != nil || len(b) != sha256.Size {
		return "", fmt.Errorf("expected a SHA256 hex digest")
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
func checksum(value string) ([]byte, error) {
	b, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(b) != sha256.Size {
		return nil, fmt.Errorf("expected a base64 SHA256 digest")
	}
	return b, nil
}

func (c *Client) Put(ctx context.Context, key string, body io.Reader, size int64, hash, contentType string, metadata map[string]string) error {
	sum, err := hexChecksum(hash)
	if err != nil {
		return err
	}
	if size < 0 || key == "" || body == nil {
		return fmt.Errorf("invalid object input")
	}
	_, err = c.s3.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key), Body: io.LimitReader(body, size), ContentLength: aws.Int64(size), ContentType: aws.String(contentType), Metadata: metadata, ChecksumAlgorithm: types.ChecksumAlgorithmSha256, ChecksumSHA256: aws.String(sum)}, nonseekable)
	return failure(err)
}
func (c *Client) Head(ctx context.Context, key string) (Object, error) {
	o, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)})
	if err != nil {
		return Object{}, failure(err)
	}
	return Object{Key: key, ETag: aws.ToString(o.ETag), Size: aws.ToInt64(o.ContentLength), ContentType: aws.ToString(o.ContentType), Metadata: o.Metadata, LastModified: aws.ToTime(o.LastModified)}, nil
}
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func (c *Client) Get(ctx context.Context, key string, options GetOptions) (Download, error) {
	o, err := c.s3.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key), Range: optional(options.Range), IfMatch: optional(options.IfMatch), IfNoneMatch: optional(options.IfNoneMatch), IfModifiedSince: options.IfModifiedSince, IfUnmodifiedSince: options.IfUnmodifiedSince})
	if err != nil {
		return Download{}, failure(err)
	}
	return Download{Object: Object{Key: key, ETag: aws.ToString(o.ETag), Size: aws.ToInt64(o.ContentLength), ContentType: aws.ToString(o.ContentType), Metadata: o.Metadata, LastModified: aws.ToTime(o.LastModified)}, Body: o.Body, ContentRange: aws.ToString(o.ContentRange)}, nil
}
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(c.bucket), Key: aws.String(key)})
	return failure(err)
}
func (c *Client) List(ctx context.Context, prefix, token string, limit int32) (Page, error) {
	if limit < 1 || limit > 1000 {
		return Page{}, fmt.Errorf("S3 listing page must be 1..1000")
	}
	o, err := c.s3.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(c.bucket), Prefix: aws.String(prefix), ContinuationToken: optional(token), MaxKeys: aws.Int32(limit)})
	if err != nil {
		return Page{}, failure(err)
	}
	page := Page{NextToken: aws.ToString(o.NextContinuationToken)}
	for _, v := range o.Contents {
		page.Objects = append(page.Objects, Object{Key: aws.ToString(v.Key), ETag: aws.ToString(v.ETag), Size: aws.ToInt64(v.Size), LastModified: aws.ToTime(v.LastModified)})
	}
	return page, nil
}
func (c *Client) CreateMultipart(ctx context.Context, key, contentType string, metadata map[string]string) (string, error) {
	o, err := c.s3.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: aws.String(c.bucket), Key: aws.String(key), ContentType: aws.String(contentType), Metadata: metadata, ChecksumAlgorithm: types.ChecksumAlgorithmSha256, ChecksumType: types.ChecksumTypeComposite}, oneAttempt)
	if err != nil {
		return "", failure(err)
	}
	if aws.ToString(o.UploadId) == "" {
		return "", fmt.Errorf("S3 returned an empty upload ID")
	}
	return *o.UploadId, nil
}
func (c *Client) UploadPart(ctx context.Context, key, id string, number int, size int64, sum string, body io.Reader) (Part, error) {
	if _, err := checksum(sum); err != nil {
		return Part{}, err
	}
	if number < 1 || number > 10000 || size < 1 || size > 5*1024*1024*1024 || body == nil {
		return Part{}, fmt.Errorf("invalid part input")
	}
	o, err := c.s3.UploadPart(ctx, &s3.UploadPartInput{Bucket: aws.String(c.bucket), Key: aws.String(key), UploadId: aws.String(id), PartNumber: aws.Int32(int32(number)), Body: io.LimitReader(body, size), ContentLength: aws.Int64(size), ChecksumAlgorithm: types.ChecksumAlgorithmSha256, ChecksumSHA256: aws.String(sum)}, nonseekable)
	if err != nil {
		return Part{}, failure(err)
	}
	if aws.ToString(o.ChecksumSHA256) != sum || aws.ToString(o.ETag) == "" {
		return Part{}, fmt.Errorf("%w: missing/mismatched part acknowledgement", ErrIntegrity)
	}
	return Part{Number: number, Size: size, ETag: *o.ETag, SHA256: sum}, nil
}
func (c *Client) ListParts(ctx context.Context, key, id string) ([]Part, error) {
	pages := s3.NewListPartsPaginator(c.s3, &s3.ListPartsInput{Bucket: aws.String(c.bucket), Key: aws.String(key), UploadId: aws.String(id)})
	seen := map[int]Part{}
	for pages.HasMorePages() {
		o, err := pages.NextPage(ctx)
		if err != nil {
			return nil, failure(err)
		}
		for _, v := range o.Parts {
			p := Part{Number: int(aws.ToInt32(v.PartNumber)), Size: aws.ToInt64(v.Size), ETag: aws.ToString(v.ETag), SHA256: aws.ToString(v.ChecksumSHA256)}
			if p.Number < 1 || p.Number > 10000 || p.Size < 1 || p.Size > 5*1024*1024*1024 || p.ETag == "" {
				return nil, fmt.Errorf("%w: invalid listed part", ErrIntegrity)
			}
			if p.SHA256 != "" {
				if _, err := checksum(p.SHA256); err != nil {
					return nil, fmt.Errorf("%w: listed checksum", ErrIntegrity)
				}
			}
			// This pinned gateway can retain identical retry aliases. A different
			// size/ETag/digest is ambiguous and cannot be chosen by recency.
			if old, ok := seen[p.Number]; ok && old != p {
				return nil, fmt.Errorf("%w: conflicting part %d aliases", ErrIntegrity, p.Number)
			}
			seen[p.Number] = p
		}
		if len(seen) > 10000 {
			return nil, fmt.Errorf("too many S3 parts")
		}
	}
	parts := make([]Part, 0, len(seen))
	for _, p := range seen {
		parts = append(parts, p)
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
	return parts, nil
}
func (c *Client) ListMultipart(ctx context.Context, prefix string) ([]Multipart, error) {
	pages := s3.NewListMultipartUploadsPaginator(c.s3, &s3.ListMultipartUploadsInput{Bucket: aws.String(c.bucket), Prefix: aws.String(prefix)})
	all := []Multipart{}
	for pages.HasMorePages() {
		o, err := pages.NextPage(ctx)
		if err != nil {
			return nil, failure(err)
		}
		for _, v := range o.Uploads {
			all = append(all, Multipart{Key: aws.ToString(v.Key), UploadID: aws.ToString(v.UploadId)})
		}
		if len(all) > 10000 {
			return nil, fmt.Errorf("multipart inventory exceeds bounded batch; narrow the prefix")
		}
	}
	return all, nil
}
func (c *Client) Abort(ctx context.Context, key, id string) error {
	_, err := c.s3.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{Bucket: aws.String(c.bucket), Key: aws.String(key), UploadId: aws.String(id)})
	var api smithy.APIError
	if errors.As(err, &api) && api.ErrorCode() == "NoSuchUpload" {
		return nil
	}
	return failure(err)
}

// Complete never reports success from S3's response alone. Every accepted
// manifest is checked against HEAD and every byte of the completed object.
func (c *Client) Complete(ctx context.Context, key, id string, parts []Part) (Verified, error) {
	if _, err := validateManifest(parts); err != nil {
		return Verified{}, err
	}
	manifest := make([]types.CompletedPart, len(parts))
	for i, p := range parts {
		manifest[i] = types.CompletedPart{PartNumber: aws.Int32(int32(p.Number)), ETag: aws.String(p.ETag), ChecksumSHA256: aws.String(p.SHA256)}
	}
	_, err := c.s3.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{Bucket: aws.String(c.bucket), Key: aws.String(key), UploadId: aws.String(id), MultipartUpload: &types.CompletedMultipartUpload{Parts: manifest}, ChecksumType: types.ChecksumTypeComposite}, oneAttempt)
	if err != nil {
		return Verified{}, failure(err)
	}
	return c.VerifyParts(ctx, key, parts)
}
func validateManifest(parts []Part) (int64, error) {
	if len(parts) < 1 || len(parts) > 10000 {
		return 0, fmt.Errorf("invalid manifest length")
	}
	var total int64
	for i, p := range parts {
		if p.Number != i+1 || p.Size < 1 || p.Size > 5*1024*1024*1024 || p.ETag == "" || p.Size > math.MaxInt64-total {
			return 0, fmt.Errorf("invalid ordered manifest")
		}
		if _, err := checksum(p.SHA256); err != nil {
			return 0, err
		}
		total += p.Size
	}
	return total, nil
}
func (c *Client) VerifyParts(ctx context.Context, key string, parts []Part) (Verified, error) {
	total, err := validateManifest(parts)
	if err != nil {
		return Verified{}, err
	}
	info, err := c.Head(ctx, key)
	if err != nil {
		return Verified{}, err
	}
	if info.Size != total {
		return Verified{}, fmt.Errorf("%w: HEAD size", ErrIntegrity)
	}
	got, err := c.Get(ctx, key, GetOptions{IfMatch: info.ETag})
	if err != nil {
		return Verified{}, err
	}
	defer got.Body.Close()
	full := sha256.New()
	for _, p := range parts {
		digest := sha256.New()
		expected, _ := checksum(p.SHA256)
		if _, err := io.CopyN(io.MultiWriter(digest, full), got.Body, p.Size); err != nil {
			return Verified{}, fmt.Errorf("%w: part %d: %v", ErrIntegrity, p.Number, err)
		}
		if subtle.ConstantTimeCompare(digest.Sum(nil), expected) != 1 {
			return Verified{}, fmt.Errorf("%w: part %d SHA256", ErrIntegrity, p.Number)
		}
	}
	if err := requireEOF(got.Body); err != nil {
		return Verified{}, err
	}
	return Verified{Object: info, SHA256: hex.EncodeToString(full.Sum(nil))}, nil
}
func (c *Client) Verify(ctx context.Context, key string, size int64, hash string) (Verified, error) {
	if size < 0 {
		return Verified{}, fmt.Errorf("negative expected object size")
	}
	if _, err := hexChecksum(hash); err != nil {
		return Verified{}, err
	}
	info, err := c.Head(ctx, key)
	if err != nil {
		return Verified{}, err
	}
	if info.Size != size {
		return Verified{}, fmt.Errorf("%w: HEAD size", ErrIntegrity)
	}
	got, err := c.Get(ctx, key, GetOptions{IfMatch: info.ETag})
	if err != nil {
		return Verified{}, err
	}
	defer got.Body.Close()
	digest := sha256.New()
	if _, err := io.CopyN(digest, got.Body, size); err != nil {
		return Verified{}, fmt.Errorf("%w: full GET: %v", ErrIntegrity, err)
	}
	if err := requireEOF(got.Body); err != nil {
		return Verified{}, err
	}
	expected, _ := hex.DecodeString(hash)
	if subtle.ConstantTimeCompare(digest.Sum(nil), expected) != 1 {
		return Verified{}, fmt.Errorf("%w: full SHA256", ErrIntegrity)
	}
	return Verified{Object: info, SHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}
func requireEOF(r io.Reader) error {
	var extra [1]byte
	n, err := io.ReadFull(r, extra[:])
	if n != 0 || err != io.EOF {
		return fmt.Errorf("%w: trailing bytes or failed EOF: %v", ErrIntegrity, err)
	}
	return nil
}
