package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type Client struct {
	s3        *s3.Client
	presignS3 *s3.Client
	bucket    string
}

// New builds a storage client. endpoint is the S3/MinIO URL the *server*
// uses for object I/O (e.g. http://minio:9000 inside docker compose).
//
// publicEndpoint is the URL the *browser* will see when following presigned
// URLs (e.g. http://localhost:9000). It must be reachable from the user's
// machine, not from the API container. When empty, presigning falls back to
// endpoint — which is fine for production where both addresses are the same
// (a real S3 / R2 bucket) but breaks in dev where the API talks to MinIO via
// a docker network alias the browser cannot resolve.
func New(ctx context.Context, endpoint, publicEndpoint, bucket, region, accessKey, secretKey string) (*Client, error) {
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
		// AWS SDK Go v2 (≥ jan/2025, e estamos em s3 v1.100.1) calcula um
		// checksum CRC em TODO PutObject por padrão (when_supported). Contra um
		// endpoint SEM TLS (MinIO em http://) com body em stream não-seekable, o
		// SDK não consegue anexar o trailing checksum e o PutObject FALHA com
		// "unseekable stream is not supported without TLS and trailing checksum"
		// — derrubando todo upload e tiering de evidência (incidente 2026-06-22).
		// when_required volta ao comportamento antigo (sem CRC default).
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
	})
	pe := publicEndpoint
	if pe == "" {
		pe = endpoint
	}
	presign := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(pe)
		o.UsePathStyle = true
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired // ver nota acima
	})
	return &Client{s3: cli, presignS3: presign, bucket: bucket}, nil
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

// PresignGet returns a time-limited URL the browser can GET directly from
// the bucket without going through the API. Used by the internal frontend
// so HTML media tags (<audio>, <a download>) — which cannot send the
// Authorization header — still receive authenticated access to evidence
// clips. ttl bounds how long the URL stays valid; the returned expiresAt
// is the absolute deadline so the caller can cache and refresh.
func (c *Client) PresignGet(ctx context.Context, key string, ttl time.Duration) (url string, expiresAt time.Time, err error) {
	p := s3.NewPresignClient(c.presignS3)
	req, err := p.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(c.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("storage: presign %s: %w", key, err)
	}
	return req.URL, time.Now().Add(ttl), nil
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
