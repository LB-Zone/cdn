package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/mstgnz/cdn/pkg/config"
)

// Public-read bucket policy as code (lb-zone M2-CDN-3).
//
// MinIO buckets are private by default, and until now making product and brand
// images readable meant clicking through the MinIO console — a manual step that
// silently breaks every freshly provisioned environment. `EnsurePublicReadBuckets`
// runs at start-up so a new environment serves images without any console work.
//
// The policy grants anonymous `s3:GetObject` on the bucket's objects and nothing
// else: uploads and deletes stay behind the bearer token, and no anonymous caller
// can list the bucket or discover object names.

// PublicReadPolicyDocument is the S3 policy document applied to an image bucket.
func PublicReadPolicyDocument(bucket string) string {
	return fmt.Sprintf(`{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "PublicReadObjects",
      "Effect": "Allow",
      "Principal": {"AWS": ["*"]},
      "Action": ["s3:GetObject"],
      "Resource": ["arn:aws:s3:::%s/*"]
    }
  ]
}`, bucket)
}

// PublicReadBuckets is the configured list of buckets that must be publicly
// readable. Defaults to the single lb-zone image bucket.
func PublicReadBuckets() []string {
	raw := config.GetEnvOrDefault("PUBLIC_READ_BUCKETS", "lbzone")

	buckets := make([]string, 0, 2)
	for _, candidate := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' '
	}) {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			buckets = append(buckets, trimmed)
		}
	}
	return buckets
}

// EnsurePublicReadBuckets creates each configured bucket if it is missing and
// applies the public-read policy. It returns the first error it hits so the caller
// can decide whether that is fatal; images being private is a visible, not a
// silent, failure, so start-up logs it loudly but keeps serving.
func EnsurePublicReadBuckets(ctx context.Context, client *minio.Client) error {
	if client == nil {
		return fmt.Errorf("minio client is not initialised")
	}

	for _, bucket := range PublicReadBuckets() {
		if err := ensurePublicReadBucket(ctx, client, bucket); err != nil {
			return fmt.Errorf("bucket %q: %w", bucket, err)
		}
	}

	return nil
}

func ensurePublicReadBucket(ctx context.Context, client *minio.Client, bucket string) error {
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}

	if !exists {
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("create bucket: %w", err)
		}
	}

	// SetBucketPolicy is idempotent: re-applying the same document is a no-op, so
	// this is safe to run on every boot.
	if err := client.SetBucketPolicy(ctx, bucket, PublicReadPolicyDocument(bucket)); err != nil {
		return fmt.Errorf("apply public-read policy: %w", err)
	}

	return nil
}
