package migrate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/mikd1/tag-files/app/internal/objectstore"
)

type S3Source struct {
	client  *s3.Client
	origin  string
	buckets []string
}

func newS3(config objectstore.Config) (*s3.Client, string, error) {
	u, e := url.Parse(config.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, "", fmt.Errorf("explicit S3 HTTP(S) origin required")
	}
	if config.AccessKey == "" || config.SecretKey == "" || config.Region == "" {
		return nil, "", fmt.Errorf("explicit source S3 credentials and region required")
	}
	origin := u.Scheme + "://" + strings.ToLower(u.Host)
	client := s3.NewFromConfig(aws.Config{Region: config.Region, HTTPClient: config.HTTPClient, Credentials: aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
		return aws.Credentials{AccessKeyID: config.AccessKey, SecretAccessKey: config.SecretKey, Source: "external-migration"}, nil
	})}, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(origin)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.ResponseChecksumValidation = aws.ResponseChecksumValidationWhenRequired
	})
	return client, origin, nil
}
func NewS3Source(config objectstore.Config, buckets []string) (*S3Source, error) {
	client, origin, e := newS3(config)
	if e != nil {
		return nil, e
	}
	if len(buckets) == 0 {
		buckets = []string{"library", "temporary", "thumbnail"}
	}
	seen := map[string]bool{}
	for _, b := range buckets {
		if b == "" || seen[b] {
			return nil, fmt.Errorf("empty/duplicate source bucket")
		}
		seen[b] = true
	}
	return &S3Source{client, origin, append([]string(nil), buckets...)}, nil
}
func apiCode(e error, codes ...string) bool {
	var api smithy.APIError
	if !errors.As(e, &api) {
		return false
	}
	for _, c := range codes {
		if api.ErrorCode() == c {
			return true
		}
	}
	return false
}
func encode(v any) string { b, _ := json.Marshal(v); return string(b) }
func (s *S3Source) Inventory(ctx context.Context) (Inventory, error) {
	v := Inventory{Objects: []SourceObject{}, BucketConfiguration: map[string]string{"source-origin": s.origin}}
	// An unlisted source bucket cannot silently disappear from a full archive.
	all, e := s.client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if e != nil {
		return v, e
	}
	known := map[string]bool{}
	for _, b := range s.buckets {
		known[b] = true
	}
	for _, b := range all.Buckets {
		if !known[aws.ToString(b.Name)] {
			return v, fmt.Errorf("source has uninventoried bucket %q; include it explicitly", aws.ToString(b.Name))
		}
	}
	for _, bucket := range s.buckets {
		versioning, e := s.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: aws.String(bucket)})
		if e != nil {
			return v, e
		}
		v.BucketConfiguration[bucket+"/versioning"] = string(versioning.Status)
		acl, e := s.client.GetBucketAcl(ctx, &s3.GetBucketAclInput{Bucket: aws.String(bucket)})
		if e != nil {
			return v, e
		}
		v.BucketConfiguration[bucket+"/acl"] = encode(struct {
			Owner  any
			Grants any
		}{acl.Owner, acl.Grants})
		policy, e := s.client.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucket)})
		if e != nil && !apiCode(e, "NoSuchBucketPolicy") {
			return v, e
		}
		if e == nil {
			v.BucketConfiguration[bucket+"/policy"] = aws.ToString(policy.Policy)
		}
		lifecycle, e := s.client.GetBucketLifecycleConfiguration(ctx, &s3.GetBucketLifecycleConfigurationInput{Bucket: aws.String(bucket)})
		if e != nil && !apiCode(e, "NoSuchLifecycleConfiguration") {
			return v, e
		}
		if e == nil {
			v.BucketConfiguration[bucket+"/lifecycle"] = encode(lifecycle.Rules)
		}
		encryption, e := s.client.GetBucketEncryption(ctx, &s3.GetBucketEncryptionInput{Bucket: aws.String(bucket)})
		if e != nil && !apiCode(e, "ServerSideEncryptionConfigurationNotFoundError", "NoSuchConfiguration") {
			return v, e
		}
		if e == nil {
			v.BucketConfiguration[bucket+"/encryption"] = encode(encryption.ServerSideEncryptionConfiguration)
		}
		if versioning.Status != "" {
			pages := s3.NewListObjectVersionsPaginator(s.client, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), MaxKeys: aws.Int32(1000)})
			for pages.HasMorePages() {
				page, e := pages.NextPage(ctx)
				if e != nil {
					return v, e
				}
				for _, o := range page.Versions {
					v.Objects = append(v.Objects, SourceObject{Bucket: bucket, Key: aws.ToString(o.Key), VersionID: aws.ToString(o.VersionId), ETag: aws.ToString(o.ETag), Size: aws.ToInt64(o.Size), Latest: aws.ToBool(o.IsLatest), LastModified: aws.ToTime(o.LastModified).UTC().Format(time.RFC3339Nano), StorageClass: string(o.StorageClass)})
				}
				for _, o := range page.DeleteMarkers {
					v.Objects = append(v.Objects, SourceObject{Bucket: bucket, Key: aws.ToString(o.Key), VersionID: aws.ToString(o.VersionId), Latest: aws.ToBool(o.IsLatest), DeleteMarker: true, LastModified: aws.ToTime(o.LastModified).UTC().Format(time.RFC3339Nano)})
				}
			}
		} else {
			pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: aws.String(bucket), MaxKeys: aws.Int32(1000)})
			for pages.HasMorePages() {
				page, e := pages.NextPage(ctx)
				if e != nil {
					return v, e
				}
				for _, o := range page.Contents {
					v.Objects = append(v.Objects, SourceObject{Bucket: bucket, Key: aws.ToString(o.Key), ETag: aws.ToString(o.ETag), Size: aws.ToInt64(o.Size), Latest: true, LastModified: aws.ToTime(o.LastModified).UTC().Format(time.RFC3339Nano), StorageClass: string(o.StorageClass)})
				}
			}
		}
		mpus := s3.NewListMultipartUploadsPaginator(s.client, &s3.ListMultipartUploadsInput{Bucket: aws.String(bucket)})
		for mpus.HasMorePages() {
			page, e := mpus.NextPage(ctx)
			if e != nil {
				return v, e
			}
			for _, u := range page.Uploads {
				m := Multipart{Bucket: bucket, Key: aws.ToString(u.Key), UploadID: aws.ToString(u.UploadId), Initiated: aws.ToTime(u.Initiated).UTC().Format(time.RFC3339Nano)}
				parts := s3.NewListPartsPaginator(s.client, &s3.ListPartsInput{Bucket: aws.String(bucket), Key: u.Key, UploadId: u.UploadId})
				for parts.HasMorePages() {
					page, e := parts.NextPage(ctx)
					if e != nil {
						return v, e
					}
					for _, p := range page.Parts {
						m.Parts = append(m.Parts, Part{Number: aws.ToInt32(p.PartNumber), Size: aws.ToInt64(p.Size), ETag: aws.ToString(p.ETag), SHA256: aws.ToString(p.ChecksumSHA256)})
					}
				}
				v.Multipart = append(v.Multipart, m)
			}
		}
	}
	for idx := range v.Objects {
		o := &v.Objects[idx]
		if o.DeleteMarker {
			continue
		}
		head, e := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(o.Bucket), Key: aws.String(o.Key), VersionId: optional(o.VersionID), IfMatch: optional(o.ETag)})
		if e != nil {
			return v, e
		}
		if aws.ToInt64(head.ContentLength) != o.Size || aws.ToString(head.ETag) != o.ETag {
			return v, ErrSourceChanged
		}
		o.ContentType = aws.ToString(head.ContentType)
		o.Metadata = head.Metadata
		o.CacheControl = aws.ToString(head.CacheControl)
		o.ContentDisposition = aws.ToString(head.ContentDisposition)
		o.ContentEncoding = aws.ToString(head.ContentEncoding)
		o.ContentLanguage = aws.ToString(head.ContentLanguage)
		if head.Expires != nil {
			o.Expires = head.Expires.UTC().Format(time.RFC3339Nano)
		}
		o.ServerSideEncryption = string(head.ServerSideEncryption)
		o.SSEKMSKeyID = aws.ToString(head.SSEKMSKeyId)
		tags, e := s.client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: aws.String(o.Bucket), Key: aws.String(o.Key), VersionId: optional(o.VersionID)})
		if e != nil {
			return v, e
		}
		o.Tags = map[string]string{}
		for _, tag := range tags.TagSet {
			k := aws.ToString(tag.Key)
			if _, ok := o.Tags[k]; ok {
				return v, fmt.Errorf("duplicate S3 object tag key")
			}
			o.Tags[k] = aws.ToString(tag.Value)
		}
	}
	sort.Slice(v.Objects, func(i, j int) bool { return v.Objects[i].Identity() < v.Objects[j].Identity() })
	return v, nil
}
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func (s *S3Source) Open(ctx context.Context, o SourceObject) (io.ReadCloser, error) {
	if o.DeleteMarker {
		return nil, fmt.Errorf("cannot read delete marker")
	}
	out, e := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(o.Bucket), Key: aws.String(o.Key), VersionId: optional(o.VersionID), IfMatch: optional(o.ETag)})
	if e != nil {
		return nil, e
	}
	if aws.ToInt64(out.ContentLength) != o.Size || aws.ToString(out.ETag) != o.ETag {
		out.Body.Close()
		return nil, ErrSourceChanged
	}
	return out.Body, nil
}
