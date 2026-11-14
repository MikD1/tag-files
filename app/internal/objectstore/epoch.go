package objectstore

import (
	"context"
	"errors"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/mikd1/tag-files/app/internal/domain"
	"github.com/mikd1/tag-files/app/internal/storageprotocol"
)

var ErrNoStorageEpoch = errors.New("S3 endpoint does not publish a valid owned storage epoch")

// StorageEpoch is transient backend-lifetime identity, not the persistent
// storage/library identity or evidence that an earlier write succeeded.
func (c *Client) StorageEpoch(ctx context.Context) (string, error) {
	result, err := c.s3.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(c.bucket)})
	if err != nil {
		return "", failure(err)
	}
	response, ok := awsmiddleware.GetRawResponse(result.ResultMetadata).(*smithyhttp.Response)
	if !ok || response.Response == nil {
		return "", ErrNoStorageEpoch
	}
	values := response.Header.Values(storageprotocol.EpochHeader)
	if len(values) != 1 {
		return "", ErrNoStorageEpoch
	}
	epoch := values[0]
	if domain.ValidateUUID(epoch) != nil {
		return "", ErrNoStorageEpoch
	}
	return strings.ToLower(epoch), nil
}

// AtEpoch returns an immutable client binding. Runtime write intents save this
// same epoch before forwarding; a new gateway refuses the old binding before
// it can reach the backend. The base client is unchanged for later discovery.
func (c *Client) AtEpoch(epoch string) (*Client, error) {
	if err := domain.ValidateUUID(epoch); err != nil {
		return nil, err
	}
	options := c.s3.Options()
	options.APIOptions = append(options.APIOptions, smithyhttp.SetHeaderValue(storageprotocol.ExpectedEpochHeader, strings.ToLower(epoch)))
	return &Client{s3: s3.New(options), bucket: c.bucket}, nil
}
