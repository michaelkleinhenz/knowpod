// Package s3 stores objects in an Amazon S3 bucket. Credentials and region come from the
// standard AWS environment (AWS_DEFAULT_REGION or AWS_REGION, AWS_ACCESS_KEY_ID,
// AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN, AWS_PROFILE, instance/task roles, …) via the
// SDK's default chain.
package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
)

// Options configures the store.
type Options struct {
	Bucket string
	// Prefix is prepended to every key (e.g. "knowpod/"); may be empty.
	Prefix string
}

// Store is the S3 implementation of ports.ObjectStore.
type Store struct {
	client *s3.Client
	bucket string
	prefix string
}

// New builds a store from the AWS environment.
func New(ctx context.Context, opts Options) (*Store, error) {
	if opts.Bucket == "" {
		return nil, errors.New("s3: bucket name is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("s3: load AWS config: %w", err)
	}
	return &Store{client: s3.NewFromConfig(cfg), bucket: opts.Bucket, prefix: strings.TrimLeft(opts.Prefix, "/")}, nil
}

// Check verifies that the bucket exists and is accessible with the configured credentials.
func (s *Store) Check(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err != nil {
		return fmt.Errorf("s3: bucket %q not accessible: %w", s.bucket, err)
	}
	return nil
}

// Put uploads body under key. body should be seekable (e.g. an *os.File) so the SDK can
// compute checksums and retry.
func (s *Store) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(s.prefix + key),
		Body:          body,
		ContentLength: aws.Int64(size),
		ContentType:   aws.String(contentType),
	})
	return err
}

func (s *Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	out, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key)})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

func (s *Store) Delete(ctx context.Context, key string) error {
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key)})
	return err
}
