package storage

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Client struct {
	s3     *s3.Client
	bucket string
}

func New(ctx context.Context, endpoint, bucket, region, accessKey, secretKey string) (*Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("storage: aws config: %w", err)
	}
	cli := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
		o.UsePathStyle = true
	})
	return &Client{s3: cli, bucket: bucket}, nil
}

func (c *Client) Put(ctx context.Context, key string, body io.Reader, contentType string) error {
	_, err := c.s3.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	})
	if err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	return nil
}

func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, string, int64, error) {
	out, err := c.s3.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, "", 0, fmt.Errorf("storage: get %s: %w", key, err)
	}
	ct := ""
	if out.ContentType != nil {
		ct = *out.ContentType
	}
	var sz int64
	if out.ContentLength != nil {
		sz = *out.ContentLength
	}
	return out.Body, ct, sz, nil
}

func (c *Client) Bucket() string {
	return c.bucket
}

// PutWithStorageClass uploads with an explicit S3 storage class (e.g.
// "STANDARD", "STANDARD_IA"). R2 currently accepts the header but only
// honours STANDARD / IA classes; unsupported values are treated as STANDARD.
func (c *Client) PutWithStorageClass(ctx context.Context, key string, body io.Reader, contentType, storageClass string) error {
	in := &s3.PutObjectInput{
		Bucket:      aws.String(c.bucket),
		Key:         aws.String(key),
		Body:        body,
		ContentType: aws.String(contentType),
	}
	if storageClass != "" {
		in.StorageClass = s3types.StorageClass(storageClass)
	}
	if _, err := c.s3.PutObject(ctx, in); err != nil {
		return fmt.Errorf("storage: put %s: %w", key, err)
	}
	return nil
}

// Delete removes an object. Idempotent: a missing object is not treated as
// an error.
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.s3.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}

// Head returns size + content-type without downloading the body. Useful for
// verifying that a destination object was written before deleting the source.
func (c *Client) Head(ctx context.Context, key string) (size int64, contentType string, err error) {
	out, err := c.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return 0, "", fmt.Errorf("storage: head %s: %w", key, err)
	}
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	if out.ContentType != nil {
		contentType = *out.ContentType
	}
	return size, contentType, nil
}
