package integration_test

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestS3Integration(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()

	const (
		bucket  = "test-bucket"
		key     = "hello.txt"
		copyKey = "hello-copy.txt"
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

	t.Run("HeadObject", func(t *testing.T) {
		out, err := clients.s3.HeadObject(ctx, &awss3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		assert.EqualValues(t, len(content), aws.ToInt64(out.ContentLength))
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

	t.Run("CopyObject", func(t *testing.T) {
		_, err := clients.s3.CopyObject(ctx, &awss3.CopyObjectInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(copyKey),
			CopySource: aws.String(bucket + "/" + key),
		})
		require.NoError(t, err)

		out, err := clients.s3.GetObject(ctx, &awss3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(copyKey),
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
		require.Len(t, out.Contents, 2)
		keys := []string{
			aws.ToString(out.Contents[0].Key),
			aws.ToString(out.Contents[1].Key),
		}
		assert.Contains(t, keys, key)
		assert.Contains(t, keys, copyKey)
	})

	t.Run("DeleteObjects", func(t *testing.T) {
		out, err := clients.s3.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{
				Objects: []s3types.ObjectIdentifier{
					{Key: aws.String(key)},
					{Key: aws.String(copyKey)},
				},
			},
		})
		require.NoError(t, err)
		assert.Len(t, out.Deleted, 2)
		assert.Empty(t, out.Errors)

		list, err := clients.s3.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket: aws.String(bucket),
		})
		require.NoError(t, err)
		assert.Empty(t, list.Contents)
	})
}
