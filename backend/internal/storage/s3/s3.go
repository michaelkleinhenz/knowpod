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
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
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

func (s *Store) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, error) {
	in := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + key)}
	if offset > 0 || length >= 0 {
		rng := fmt.Sprintf("bytes=%d-", offset)
		if length >= 0 {
			rng += strconv.FormatInt(offset+length-1, 10)
		}
		in.Range = aws.String(rng)
	}
	out, err := s.client.GetObject(ctx, in)
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

// List calls fn with every object under the store's prefix. The content type comes from a
// HEAD request per object.
func (s *Store) List(ctx context.Context, fn func(ports.ObjectInfo) error) error {
	pages := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket), Prefix: aws.String(s.prefix),
	})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return err
		}
		for _, o := range page.Contents {
			full := aws.ToString(o.Key)
			if strings.HasSuffix(full, "/") && aws.ToInt64(o.Size) == 0 {
				continue // a console-made "folder" placeholder
			}
			head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: o.Key})
			if err != nil {
				return err
			}
			info := ports.ObjectInfo{
				Key: strings.TrimPrefix(full, s.prefix), Size: aws.ToInt64(o.Size),
				ContentType: aws.ToString(head.ContentType),
			}
			if err := fn(info); err != nil {
				return err
			}
		}
	}
	return nil
}
