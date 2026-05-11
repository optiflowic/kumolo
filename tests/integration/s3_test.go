package integration_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestS3Integration(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()

	const (
		bucket  = "test-bucket"
		key     = "hello.txt"
		content = "hello, world"
	)

	t.Run("CreateBucket", func(t *testing.T) {
		_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
			Bucket: aws.String(bucket),
		})
		require.NoError(t, err)
	})

	t.Run("PutObject", func(t *testing.T) {
		_, err := clients.s3.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
			Body:   bytes.NewReader([]byte(content)),
		})
		require.NoError(t, err)
	})

	t.Run("GetObject", func(t *testing.T) {
		out, err := clients.s3.GetObject(ctx, &awss3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		defer out.Body.Close()

		body, err := io.ReadAll(out.Body)
		require.NoError(t, err)
		assert.Equal(t, content, string(body))
	})

	t.Run("ListObjectsV2", func(t *testing.T) {
		out, err := clients.s3.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
		})
		require.NoError(t, err)
		require.Len(t, out.Contents, 1)
		assert.Equal(t, key, aws.ToString(out.Contents[0].Key))
	})

	t.Run("DeleteObject", func(t *testing.T) {
		_, err := clients.s3.DeleteObject(ctx, &awss3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)

		out, err := clients.s3.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
		})
		require.NoError(t, err)
		assert.Empty(t, out.Contents)
	})
}
