package integration_test

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestS3Integration runs sub-tests sequentially against shared state.
// Each sub-test depends on the state left by the previous one; order matters.
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
		t.Cleanup(func() { _ = out.Body.Close() })

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
		t.Cleanup(func() { _ = out.Body.Close() })
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

// TestS3MultipartUpload verifies the multipart upload round-trip and abort paths.
// CreateBucket is shared setup; sub-tests run sequentially against the same bucket.
func TestS3MultipartUpload(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()

	const bucket = "mpu-test-bucket"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	t.Run("CompleteRoundtrip", func(t *testing.T) {
		const key = "multipart-object"
		parts := []string{"part-one-data", "part-two-data", "part-three-data"}
		want := strings.Join(parts, "")

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)
		require.NotEmpty(t, uploadID)

		var completedParts []s3types.CompletedPart
		for i, body := range parts {
			partNum := int32(i + 1)
			upOut, err := clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   aws.String(uploadID),
				PartNumber: aws.Int32(partNum),
				Body:       strings.NewReader(body),
			})
			require.NoError(t, err)
			completedParts = append(completedParts, s3types.CompletedPart{
				PartNumber: aws.Int32(partNum),
				ETag:       upOut.ETag,
			})
		}

		_, err = clients.s3.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
			MultipartUpload: &s3types.CompletedMultipartUpload{
				Parts: completedParts,
			},
		})
		require.NoError(t, err)

		getOut, err := clients.s3.GetObject(ctx, &awss3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = getOut.Body.Close() })

		got, err := io.ReadAll(getOut.Body)
		require.NoError(t, err)
		assert.Equal(t, want, string(got))
		assert.EqualValues(t, len(want), aws.ToInt64(getOut.ContentLength))
	})

	t.Run("AbortCancelsUpload", func(t *testing.T) {
		const key = "aborted-object"

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)

		_, err = clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(1),
			Body:       strings.NewReader("some data"),
		})
		require.NoError(t, err)

		_, err = clients.s3.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		require.NoError(t, err)

		// ListParts must return NoSuchUpload after abort.
		_, err = clients.s3.ListParts(ctx, &awss3.ListPartsInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		require.Error(t, err)
		assert.Equal(t, "NoSuchUpload", apiErrorCode(err), "ListParts after abort: %v", err)

		// The object must not exist after abort.
		_, err = clients.s3.GetObject(ctx, &awss3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.Error(t, err)
		assert.Equal(t, "NoSuchKey", apiErrorCode(err), "GetObject after abort: %v", err)
	})

	t.Run("AbortWithNoParts", func(t *testing.T) {
		const key = "aborted-no-parts"

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)

		_, err = clients.s3.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		require.NoError(t, err)

		_, err = clients.s3.ListParts(ctx, &awss3.ListPartsInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		require.Error(t, err)
		assert.Equal(t, "NoSuchUpload", apiErrorCode(err), "ListParts after abort: %v", err)
	})

	t.Run("ListPartsVerification", func(t *testing.T) {
		const key = "list-parts-object"
		partBodies := []string{"first-part-content", "second-part-content"}

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)
		t.Cleanup(func() {
			_, _ = clients.s3.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      aws.String(key),
				UploadId: aws.String(uploadID),
			})
		})

		var uploadedETags []string
		for i, body := range partBodies {
			upOut, err := clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   aws.String(uploadID),
				PartNumber: aws.Int32(int32(i + 1)),
				Body:       strings.NewReader(body),
			})
			require.NoError(t, err)
			uploadedETags = append(uploadedETags, aws.ToString(upOut.ETag))
		}

		listOut, err := clients.s3.ListParts(ctx, &awss3.ListPartsInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		})
		require.NoError(t, err)
		require.Len(t, listOut.Parts, 2)
		assert.Equal(t, int32(1), aws.ToInt32(listOut.Parts[0].PartNumber))
		assert.Equal(t, uploadedETags[0], aws.ToString(listOut.Parts[0].ETag))
		assert.EqualValues(t, len(partBodies[0]), aws.ToInt64(listOut.Parts[0].Size))
		assert.Equal(t, int32(2), aws.ToInt32(listOut.Parts[1].PartNumber))
		assert.Equal(t, uploadedETags[1], aws.ToString(listOut.Parts[1].ETag))
		assert.EqualValues(t, len(partBodies[1]), aws.ToInt64(listOut.Parts[1].Size))
	})

	t.Run("CompleteWithWrongETag", func(t *testing.T) {
		const key = "wrong-etag-object"

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)
		t.Cleanup(func() {
			_, _ = clients.s3.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      aws.String(key),
				UploadId: aws.String(uploadID),
			})
		})

		_, err = clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(1),
			Body:       strings.NewReader("some content"),
		})
		require.NoError(t, err)

		_, err = clients.s3.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
			MultipartUpload: &s3types.CompletedMultipartUpload{
				Parts: []s3types.CompletedPart{
					{
						PartNumber: aws.Int32(1),
						ETag:       aws.String(`"00000000000000000000000000000000"`),
					},
				},
			},
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPart", apiErrorCode(err))
	})

	t.Run("CompleteWithOutOfOrderParts", func(t *testing.T) {
		const key = "out-of-order-object"

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)
		t.Cleanup(func() {
			_, _ = clients.s3.AbortMultipartUpload(ctx, &awss3.AbortMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      aws.String(key),
				UploadId: aws.String(uploadID),
			})
		})

		var etags [2]string
		for i := range etags {
			upOut, err := clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   aws.String(uploadID),
				PartNumber: aws.Int32(int32(i + 1)),
				Body:       strings.NewReader("data"),
			})
			require.NoError(t, err)
			etags[i] = aws.ToString(upOut.ETag)
		}

		_, err = clients.s3.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
			MultipartUpload: &s3types.CompletedMultipartUpload{
				Parts: []s3types.CompletedPart{
					{PartNumber: aws.Int32(2), ETag: aws.String(etags[1])},
					{PartNumber: aws.Int32(1), ETag: aws.String(etags[0])},
				},
			},
		})
		require.Error(t, err)
		assert.Equal(t, "InvalidPartOrder", apiErrorCode(err))
	})

	t.Run("ETagFormat", func(t *testing.T) {
		const (
			key      = "etag-format-object"
			numParts = 2
		)

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)

		var completedParts []s3types.CompletedPart
		for i := 0; i < numParts; i++ {
			upOut, err := clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   aws.String(uploadID),
				PartNumber: aws.Int32(int32(i + 1)),
				Body:       strings.NewReader("part data"),
			})
			require.NoError(t, err)
			completedParts = append(completedParts, s3types.CompletedPart{
				PartNumber: aws.Int32(int32(i + 1)),
				ETag:       upOut.ETag,
			})
		}

		completeOut, err := clients.s3.CompleteMultipartUpload(
			ctx,
			&awss3.CompleteMultipartUploadInput{
				Bucket:   aws.String(bucket),
				Key:      aws.String(key),
				UploadId: aws.String(uploadID),
				MultipartUpload: &s3types.CompletedMultipartUpload{
					Parts: completedParts,
				},
			},
		)
		require.NoError(t, err)
		assert.Regexp(t, `^"[0-9a-f]+-2"$`, aws.ToString(completeOut.ETag))
	})

	t.Run("ContentTypePreserved", func(t *testing.T) {
		const (
			key         = "content-type-object"
			contentType = "text/plain; charset=utf-8"
		)

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket:      aws.String(bucket),
			Key:         aws.String(key),
			ContentType: aws.String(contentType),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)

		upOut, err := clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(key),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(1),
			Body:       strings.NewReader("hello"),
		})
		require.NoError(t, err)

		_, err = clients.s3.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
			MultipartUpload: &s3types.CompletedMultipartUpload{
				Parts: []s3types.CompletedPart{
					{PartNumber: aws.Int32(1), ETag: upOut.ETag},
				},
			},
		})
		require.NoError(t, err)

		headOut, err := clients.s3.HeadObject(ctx, &awss3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
		require.NoError(t, err)
		assert.Equal(t, contentType, aws.ToString(headOut.ContentType))
	})

	t.Run("UploadPartCopy", func(t *testing.T) {
		const (
			srcKey  = "upload-part-copy-source"
			destKey = "upload-part-copy-dest"
			content = "source object content"
		)

		// Create the source object.
		_, err := clients.s3.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(srcKey),
			Body:   strings.NewReader(content),
		})
		require.NoError(t, err)

		createOut, err := clients.s3.CreateMultipartUpload(ctx, &awss3.CreateMultipartUploadInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(destKey),
		})
		require.NoError(t, err)
		uploadID := aws.ToString(createOut.UploadId)

		copyOut, err := clients.s3.UploadPartCopy(ctx, &awss3.UploadPartCopyInput{
			Bucket:     aws.String(bucket),
			Key:        aws.String(destKey),
			UploadId:   aws.String(uploadID),
			PartNumber: aws.Int32(1),
			CopySource: aws.String(bucket + "/" + srcKey),
		})
		require.NoError(t, err)
		require.NotNil(t, copyOut.CopyPartResult)
		require.NotEmpty(t, aws.ToString(copyOut.CopyPartResult.ETag))

		_, err = clients.s3.CompleteMultipartUpload(ctx, &awss3.CompleteMultipartUploadInput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(destKey),
			UploadId: aws.String(uploadID),
			MultipartUpload: &s3types.CompletedMultipartUpload{
				Parts: []s3types.CompletedPart{
					{PartNumber: aws.Int32(1), ETag: copyOut.CopyPartResult.ETag},
				},
			},
		})
		require.NoError(t, err)

		getOut, err := clients.s3.GetObject(ctx, &awss3.GetObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(destKey),
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = getOut.Body.Close() })

		got, err := io.ReadAll(getOut.Body)
		require.NoError(t, err)
		assert.Equal(t, content, string(got))
	})
}
