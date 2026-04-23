package storage

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// S3Backend stores files in any S3-compatible bucket.
// Works with AWS S3, Cloudflare R2, and MinIO.
type S3Backend struct {
	client    *s3.Client
	bucket    string
	publicURL string // optional CDN prefix, empty = presign only
}

// S3Config holds the credentials for NewS3Backend.
type S3Config struct {
	Endpoint        string // e.g. https://<account>.r2.cloudflarestorage.com
	Region          string // "auto" for R2
	AccessKeyID     string
	SecretAccessKey string
	Bucket          string
	PublicURLPrefix string // optional: CDN or public bucket URL
}

// NewS3Backend constructs an S3Backend.
// It works for Cloudflare R2 (set Endpoint to the R2 account URL).
func NewS3Backend(ctx context.Context, cfg S3Config) (*S3Backend, error) {
	resolver := aws.EndpointResolverWithOptionsFunc(
		func(service, region string, opts ...interface{}) (aws.Endpoint, error) {
			if cfg.Endpoint != "" {
				return aws.Endpoint{
					URL:               cfg.Endpoint,
					HostnameImmutable: true,
				}, nil
			}
			return aws.Endpoint{}, &aws.EndpointNotFoundError{}
		},
	)

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		),
		config.WithEndpointResolverWithOptions(resolver),
	)
	if err != nil {
		return nil, fmt.Errorf("s3 config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		// Required for Cloudflare R2 (path-style addressing)
		o.UsePathStyle = true
	})

	return &S3Backend{
		client:    client,
		bucket:    cfg.Bucket,
		publicURL: cfg.PublicURLPrefix,
	}, nil
}

// Store uploads the reader to the bucket at the given key.
func (b *S3Backend) Store(ctx context.Context, key string, r io.Reader, mimeType string) (string, error) {
	_, err := b.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(b.bucket),
		Key:         aws.String(key),
		Body:        r,
		ContentType: aws.String(mimeType),
	})
	if err != nil {
		return "", fmt.Errorf("s3 put %s: %w", key, err)
	}
	return key, nil
}

// Open downloads an object and returns a streaming reader.
func (b *S3Backend) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get %s: %w", key, err)
	}
	return out.Body, nil
}

// Delete removes an object from the bucket.
func (b *S3Backend) Delete(ctx context.Context, key string) error {
	_, err := b.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	})
	return err
}

// SignedURL generates a pre-signed GET URL valid for ttlSeconds.
func (b *S3Backend) SignedURL(ctx context.Context, key string, ttlSeconds int64) (string, error) {
	presignClient := s3.NewPresignClient(b.client)
	req, err := presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(b.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(time.Duration(ttlSeconds)*time.Second))
	if err != nil {
		return "", fmt.Errorf("s3 presign %s: %w", key, err)
	}
	return req.URL, nil
}

// PublicURL returns the CDN-prefixed URL if configured, otherwise empty.
func (b *S3Backend) PublicURL(key string) string {
	if b.publicURL == "" {
		return ""
	}
	return b.publicURL + "/" + key
}
