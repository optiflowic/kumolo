package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
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
		// Non-final parts must be >= 5 MiB; only the last part may be smaller.
		bigPart := strings.Repeat("x", 5*1024*1024)
		parts := []string{bigPart, bigPart, "part-three-data"}
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

		// Parts must be >= 5 MiB so the order check fires before the size check
		// when submitting them in descending order below.
		bigPart := strings.Repeat("x", 5*1024*1024)
		var etags [2]string
		for i := range etags {
			upOut, err := clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   aws.String(uploadID),
				PartNumber: aws.Int32(int32(i + 1)),
				Body:       strings.NewReader(bigPart),
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

		// Part 1 (non-final) must be >= 5 MiB.
		partBodies := []string{strings.Repeat("x", 5*1024*1024), "part data"}
		var completedParts []s3types.CompletedPart
		for i := 0; i < numParts; i++ {
			upOut, err := clients.s3.UploadPart(ctx, &awss3.UploadPartInput{
				Bucket:     aws.String(bucket),
				Key:        aws.String(key),
				UploadId:   aws.String(uploadID),
				PartNumber: aws.Int32(int32(i + 1)),
				Body:       strings.NewReader(partBodies[i]),
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

// TestLifecycleConfigRoundTrip checks that PutBucketLifecycleConfiguration + GetBucketLifecycleConfiguration
// produces a response that the Terraform AWS Provider v6 lifecycleConfigEqual check would accept.
// The provider compares desired rules (from PUT input) with actual rules (from GET output) using
// reflect.DeepEqual, and also compares TransitionDefaultMinimumObjectSize.
func TestLifecycleConfigRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-lifecycle-roundtrip"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)

	_, err = clients.s3.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	// Build the desired rules exactly as Terraform AWS Provider v6 would for our e2e config.
	const (
		noncurrentExpirationDays = int32(90)
		noncurrentTransitionDays = int32(30)
		multipartAbortDays       = int32(7)
	)
	allObjectsPrefix := aws.String("")
	desiredRules := []s3types.LifecycleRule{
		{
			ID:     aws.String("expire-noncurrent"),
			Status: s3types.ExpirationStatusEnabled,
			Filter: &s3types.LifecycleRuleFilter{Prefix: allObjectsPrefix},
			NoncurrentVersionExpiration: &s3types.NoncurrentVersionExpiration{
				NoncurrentDays: aws.Int32(noncurrentExpirationDays),
			},
		},
		{
			ID:     aws.String("transition-noncurrent"),
			Status: s3types.ExpirationStatusEnabled,
			Filter: &s3types.LifecycleRuleFilter{Prefix: allObjectsPrefix},
			NoncurrentVersionTransitions: []s3types.NoncurrentVersionTransition{
				{
					NoncurrentDays: aws.Int32(noncurrentTransitionDays),
					StorageClass:   s3types.TransitionStorageClassGlacier,
				},
			},
		},
		{
			ID:     aws.String("abort-multipart-uploads"),
			Status: s3types.ExpirationStatusEnabled,
			Filter: &s3types.LifecycleRuleFilter{Prefix: allObjectsPrefix},
			AbortIncompleteMultipartUpload: &s3types.AbortIncompleteMultipartUpload{
				DaysAfterInitiation: aws.Int32(multipartAbortDays),
			},
		},
	}
	desiredTransitionMinSize := s3types.TransitionDefaultMinimumObjectSizeAllStorageClasses128k

	putInput := &awss3.PutBucketLifecycleConfigurationInput{
		Bucket: aws.String(bucket),
		LifecycleConfiguration: &s3types.BucketLifecycleConfiguration{
			Rules: desiredRules,
		},
		TransitionDefaultMinimumObjectSize: desiredTransitionMinSize,
	}
	_, err = clients.s3.PutBucketLifecycleConfiguration(ctx, putInput)
	require.NoError(t, err)

	getOut, err := clients.s3.GetBucketLifecycleConfiguration(
		ctx,
		&awss3.GetBucketLifecycleConfigurationInput{
			Bucket: aws.String(bucket),
		},
	)
	require.NoError(t, err)

	assert.Equal(t, desiredTransitionMinSize, getOut.TransitionDefaultMinimumObjectSize,
		"TransitionDefaultMinimumObjectSize mismatch")

	require.Truef(
		t,
		reflect.DeepEqual(desiredRules, getOut.Rules),
		"rules mismatch: reflect.DeepEqual returned false (Terraform provider v6 lifecycleConfigEqual would time out)\ndesired: %#v\nactual: %#v",
		desiredRules,
		getOut.Rules,
	)
}

func TestBucketVersioningRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-versioning"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// Initially, versioning is not configured.
	getOut, err := clients.s3.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Empty(t, getOut.Status)

	_, err = clients.s3.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	getOut, err = clients.s3.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Equal(t, s3types.BucketVersioningStatusEnabled, getOut.Status)

	_, err = clients.s3.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusSuspended,
		},
	})
	require.NoError(t, err)

	getOut, err = clients.s3.GetBucketVersioning(ctx, &awss3.GetBucketVersioningInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Equal(t, s3types.BucketVersioningStatusSuspended, getOut.Status)
}

func TestBucketCorsRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-cors"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// No CORS configured initially.
	_, err = clients.s3.GetBucketCors(ctx, &awss3.GetBucketCorsInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchCORSConfiguration", apiErrorCode(err))

	_, err = clients.s3.PutBucketCors(ctx, &awss3.PutBucketCorsInput{
		Bucket: aws.String(bucket),
		CORSConfiguration: &s3types.CORSConfiguration{
			CORSRules: []s3types.CORSRule{
				{
					AllowedMethods: []string{"GET", "PUT"},
					AllowedOrigins: []string{"https://example.com"},
					AllowedHeaders: []string{"Authorization"},
					MaxAgeSeconds:  aws.Int32(3600),
				},
			},
		},
	})
	require.NoError(t, err)

	corsOut, err := clients.s3.GetBucketCors(ctx, &awss3.GetBucketCorsInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.Len(t, corsOut.CORSRules, 1)
	assert.ElementsMatch(t, []string{"GET", "PUT"}, corsOut.CORSRules[0].AllowedMethods)
	assert.Equal(t, []string{"https://example.com"}, corsOut.CORSRules[0].AllowedOrigins)
	assert.Equal(t, []string{"Authorization"}, corsOut.CORSRules[0].AllowedHeaders)
	assert.Equal(t, int32(3600), aws.ToInt32(corsOut.CORSRules[0].MaxAgeSeconds))

	_, err = clients.s3.DeleteBucketCors(ctx, &awss3.DeleteBucketCorsInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	_, err = clients.s3.GetBucketCors(ctx, &awss3.GetBucketCorsInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchCORSConfiguration", apiErrorCode(err))
}

func TestBucketTaggingRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-bucket-tagging"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// No tags configured initially.
	_, err = clients.s3.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchTagSet", apiErrorCode(err))

	_, err = clients.s3.PutBucketTagging(ctx, &awss3.PutBucketTaggingInput{
		Bucket: aws.String(bucket),
		Tagging: &s3types.Tagging{
			TagSet: []s3types.Tag{
				{Key: aws.String("env"), Value: aws.String("test")},
				{Key: aws.String("project"), Value: aws.String("kumolo")},
			},
		},
	})
	require.NoError(t, err)

	tagOut, err := clients.s3.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.Len(t, tagOut.TagSet, 2)
	tagMap := make(map[string]string, len(tagOut.TagSet))
	for _, tag := range tagOut.TagSet {
		tagMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
	}
	assert.Equal(t, "test", tagMap["env"])
	assert.Equal(t, "kumolo", tagMap["project"])

	_, err = clients.s3.DeleteBucketTagging(ctx, &awss3.DeleteBucketTaggingInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	_, err = clients.s3.GetBucketTagging(ctx, &awss3.GetBucketTaggingInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchTagSet", apiErrorCode(err))
}

func TestBucketEncryptionRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-encryption"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// No encryption configured initially.
	_, err = clients.s3.GetBucketEncryption(ctx, &awss3.GetBucketEncryptionInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "ServerSideEncryptionConfigurationNotFoundError", apiErrorCode(err))

	_, err = clients.s3.PutBucketEncryption(ctx, &awss3.PutBucketEncryptionInput{
		Bucket: aws.String(bucket),
		ServerSideEncryptionConfiguration: &s3types.ServerSideEncryptionConfiguration{
			Rules: []s3types.ServerSideEncryptionRule{
				{
					ApplyServerSideEncryptionByDefault: &s3types.ServerSideEncryptionByDefault{
						SSEAlgorithm: s3types.ServerSideEncryptionAes256,
					},
				},
			},
		},
	})
	require.NoError(t, err)

	encOut, err := clients.s3.GetBucketEncryption(ctx, &awss3.GetBucketEncryptionInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.NotNil(t, encOut.ServerSideEncryptionConfiguration)
	require.Len(t, encOut.ServerSideEncryptionConfiguration.Rules, 1)
	require.NotNil(
		t,
		encOut.ServerSideEncryptionConfiguration.Rules[0].ApplyServerSideEncryptionByDefault,
	)
	assert.Equal(
		t,
		s3types.ServerSideEncryptionAes256,
		encOut.ServerSideEncryptionConfiguration.Rules[0].ApplyServerSideEncryptionByDefault.SSEAlgorithm,
	)

	_, err = clients.s3.DeleteBucketEncryption(ctx, &awss3.DeleteBucketEncryptionInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	_, err = clients.s3.GetBucketEncryption(ctx, &awss3.GetBucketEncryptionInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "ServerSideEncryptionConfigurationNotFoundError", apiErrorCode(err))
}

func TestBucketPolicyRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-policy"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// No policy configured initially.
	_, err = clients.s3.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchBucketPolicy", apiErrorCode(err))

	const policy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::test-policy/*"}]}`

	_, err = clients.s3.PutBucketPolicy(ctx, &awss3.PutBucketPolicyInput{
		Bucket: aws.String(bucket),
		Policy: aws.String(policy),
	})
	require.NoError(t, err)

	policyOut, err := clients.s3.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.NotNil(t, policyOut.Policy)
	// Normalize both sides before comparing.
	var wantNorm, gotNorm any
	require.NoError(t, json.Unmarshal([]byte(policy), &wantNorm))
	require.NoError(t, json.Unmarshal([]byte(aws.ToString(policyOut.Policy)), &gotNorm))
	assert.Equal(t, wantNorm, gotNorm)

	_, err = clients.s3.DeleteBucketPolicy(ctx, &awss3.DeleteBucketPolicyInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	_, err = clients.s3.GetBucketPolicy(ctx, &awss3.GetBucketPolicyInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchBucketPolicy", apiErrorCode(err))
}

func TestBucketPublicAccessBlockRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-public-access-block"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// No public access block configured initially.
	_, err = clients.s3.GetPublicAccessBlock(ctx, &awss3.GetPublicAccessBlockInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchPublicAccessBlockConfiguration", apiErrorCode(err))

	_, err = clients.s3.PutPublicAccessBlock(ctx, &awss3.PutPublicAccessBlockInput{
		Bucket: aws.String(bucket),
		PublicAccessBlockConfiguration: &s3types.PublicAccessBlockConfiguration{
			BlockPublicAcls:       aws.Bool(true),
			IgnorePublicAcls:      aws.Bool(true),
			BlockPublicPolicy:     aws.Bool(true),
			RestrictPublicBuckets: aws.Bool(true),
		},
	})
	require.NoError(t, err)

	pabOut, err := clients.s3.GetPublicAccessBlock(ctx, &awss3.GetPublicAccessBlockInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.NotNil(t, pabOut.PublicAccessBlockConfiguration)
	assert.True(t, aws.ToBool(pabOut.PublicAccessBlockConfiguration.BlockPublicAcls))
	assert.True(t, aws.ToBool(pabOut.PublicAccessBlockConfiguration.IgnorePublicAcls))
	assert.True(t, aws.ToBool(pabOut.PublicAccessBlockConfiguration.BlockPublicPolicy))
	assert.True(t, aws.ToBool(pabOut.PublicAccessBlockConfiguration.RestrictPublicBuckets))

	_, err = clients.s3.DeletePublicAccessBlock(ctx, &awss3.DeletePublicAccessBlockInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	_, err = clients.s3.GetPublicAccessBlock(ctx, &awss3.GetPublicAccessBlockInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchPublicAccessBlockConfiguration", apiErrorCode(err))
}

func TestBucketOwnershipControlsRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-ownership-controls"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// No ownership controls configured initially.
	_, err = clients.s3.GetBucketOwnershipControls(ctx, &awss3.GetBucketOwnershipControlsInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "OwnershipControlsNotFoundError", apiErrorCode(err))

	_, err = clients.s3.PutBucketOwnershipControls(ctx, &awss3.PutBucketOwnershipControlsInput{
		Bucket: aws.String(bucket),
		OwnershipControls: &s3types.OwnershipControls{
			Rules: []s3types.OwnershipControlsRule{
				{ObjectOwnership: s3types.ObjectOwnershipBucketOwnerPreferred},
			},
		},
	})
	require.NoError(t, err)

	ocOut, err := clients.s3.GetBucketOwnershipControls(ctx, &awss3.GetBucketOwnershipControlsInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.NotNil(t, ocOut.OwnershipControls)
	require.Len(t, ocOut.OwnershipControls.Rules, 1)
	assert.Equal(
		t,
		s3types.ObjectOwnershipBucketOwnerPreferred,
		ocOut.OwnershipControls.Rules[0].ObjectOwnership,
	)

	_, err = clients.s3.DeleteBucketOwnershipControls(
		ctx,
		&awss3.DeleteBucketOwnershipControlsInput{
			Bucket: aws.String(bucket),
		},
	)
	require.NoError(t, err)

	_, err = clients.s3.GetBucketOwnershipControls(ctx, &awss3.GetBucketOwnershipControlsInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "OwnershipControlsNotFoundError", apiErrorCode(err))
}

func TestBucketLoggingRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const (
		srcBucket = "test-logging-src"
		logBucket = "test-logging-target"
	)

	for _, b := range []string{srcBucket, logBucket} {
		_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
			Bucket: aws.String(b),
		})
		require.NoError(t, err)
	}

	// No logging configured initially: returns empty BucketLoggingStatus.
	logOut, err := clients.s3.GetBucketLogging(ctx, &awss3.GetBucketLoggingInput{
		Bucket: aws.String(srcBucket),
	})
	require.NoError(t, err)
	assert.Nil(t, logOut.LoggingEnabled)

	_, err = clients.s3.PutBucketLogging(ctx, &awss3.PutBucketLoggingInput{
		Bucket: aws.String(srcBucket),
		BucketLoggingStatus: &s3types.BucketLoggingStatus{
			LoggingEnabled: &s3types.LoggingEnabled{
				TargetBucket: aws.String(logBucket),
				TargetPrefix: aws.String("access-logs/"),
			},
		},
	})
	require.NoError(t, err)

	logOut, err = clients.s3.GetBucketLogging(ctx, &awss3.GetBucketLoggingInput{
		Bucket: aws.String(srcBucket),
	})
	require.NoError(t, err)
	require.NotNil(t, logOut.LoggingEnabled)
	assert.Equal(t, logBucket, aws.ToString(logOut.LoggingEnabled.TargetBucket))
	assert.Equal(t, "access-logs/", aws.ToString(logOut.LoggingEnabled.TargetPrefix))

	// Disable logging by sending an empty BucketLoggingStatus.
	_, err = clients.s3.PutBucketLogging(ctx, &awss3.PutBucketLoggingInput{
		Bucket:              aws.String(srcBucket),
		BucketLoggingStatus: &s3types.BucketLoggingStatus{},
	})
	require.NoError(t, err)

	logOut, err = clients.s3.GetBucketLogging(ctx, &awss3.GetBucketLoggingInput{
		Bucket: aws.String(srcBucket),
	})
	require.NoError(t, err)
	assert.Nil(t, logOut.LoggingEnabled)
}

func TestBucketReplicationRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const (
		bucket  = "test-replication"
		dstArn  = "arn:aws:s3:::replication-dst"
		roleArn = "arn:aws:iam::000000000000:role/replication-role"
	)

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// Replication requires versioning.
	_, err = clients.s3.PutBucketVersioning(ctx, &awss3.PutBucketVersioningInput{
		Bucket: aws.String(bucket),
		VersioningConfiguration: &s3types.VersioningConfiguration{
			Status: s3types.BucketVersioningStatusEnabled,
		},
	})
	require.NoError(t, err)

	// No replication configured initially.
	_, err = clients.s3.GetBucketReplication(ctx, &awss3.GetBucketReplicationInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "ReplicationConfigurationNotFoundError", apiErrorCode(err))

	// Sub-tests run sequentially and share the bucket; each Put overwrites the previous config.
	t.Run("PrefixFilter", func(t *testing.T) {
		_, err := clients.s3.PutBucketReplication(ctx, &awss3.PutBucketReplicationInput{
			Bucket: aws.String(bucket),
			ReplicationConfiguration: &s3types.ReplicationConfiguration{
				Role: aws.String(roleArn),
				Rules: []s3types.ReplicationRule{
					{
						ID:     aws.String("prefix-rule"),
						Status: s3types.ReplicationRuleStatusEnabled,
						Filter: &s3types.ReplicationRuleFilter{
							Prefix: aws.String("logs/"),
						},
						Destination: &s3types.Destination{
							Bucket: aws.String(dstArn),
						},
					},
				},
			},
		})
		require.NoError(t, err)

		repOut, err := clients.s3.GetBucketReplication(ctx, &awss3.GetBucketReplicationInput{
			Bucket: aws.String(bucket),
		})
		require.NoError(t, err)
		require.NotNil(t, repOut.ReplicationConfiguration)
		require.Len(t, repOut.ReplicationConfiguration.Rules, 1)
		rule := repOut.ReplicationConfiguration.Rules[0]
		assert.Equal(t, "prefix-rule", aws.ToString(rule.ID))
		require.NotNil(t, rule.Filter)
		assert.Equal(t, "logs/", aws.ToString(rule.Filter.Prefix))
	})

	t.Run("TagFilter", func(t *testing.T) {
		_, err := clients.s3.PutBucketReplication(ctx, &awss3.PutBucketReplicationInput{
			Bucket: aws.String(bucket),
			ReplicationConfiguration: &s3types.ReplicationConfiguration{
				Role: aws.String(roleArn),
				Rules: []s3types.ReplicationRule{
					{
						ID:     aws.String("tag-rule"),
						Status: s3types.ReplicationRuleStatusEnabled,
						Filter: &s3types.ReplicationRuleFilter{
							Tag: &s3types.Tag{
								Key:   aws.String("replicate"),
								Value: aws.String("true"),
							},
						},
						Destination: &s3types.Destination{
							Bucket: aws.String(dstArn),
						},
						DeleteMarkerReplication: &s3types.DeleteMarkerReplication{
							Status: s3types.DeleteMarkerReplicationStatusDisabled,
						},
					},
				},
			},
		})
		require.NoError(t, err)

		repOut, err := clients.s3.GetBucketReplication(ctx, &awss3.GetBucketReplicationInput{
			Bucket: aws.String(bucket),
		})
		require.NoError(t, err)
		require.NotNil(t, repOut.ReplicationConfiguration)
		require.Len(t, repOut.ReplicationConfiguration.Rules, 1)
		rule := repOut.ReplicationConfiguration.Rules[0]
		require.NotNil(t, rule.Filter)
		require.NotNil(t, rule.Filter.Tag)
		assert.Equal(t, "replicate", aws.ToString(rule.Filter.Tag.Key))
		assert.Equal(t, "true", aws.ToString(rule.Filter.Tag.Value))
	})

	t.Run("AndTagsFilter", func(t *testing.T) {
		_, err := clients.s3.PutBucketReplication(ctx, &awss3.PutBucketReplicationInput{
			Bucket: aws.String(bucket),
			ReplicationConfiguration: &s3types.ReplicationConfiguration{
				Role: aws.String(roleArn),
				Rules: []s3types.ReplicationRule{
					{
						ID:     aws.String("and-tags-rule"),
						Status: s3types.ReplicationRuleStatusEnabled,
						Filter: &s3types.ReplicationRuleFilter{
							And: &s3types.ReplicationRuleAndOperator{
								Prefix: aws.String("data/"),
								Tags: []s3types.Tag{
									{Key: aws.String("env"), Value: aws.String("prod")},
									{Key: aws.String("tier"), Value: aws.String("hot")},
								},
							},
						},
						Destination: &s3types.Destination{
							Bucket: aws.String(dstArn),
						},
						DeleteMarkerReplication: &s3types.DeleteMarkerReplication{
							Status: s3types.DeleteMarkerReplicationStatusDisabled,
						},
					},
				},
			},
		})
		require.NoError(t, err)

		repOut, err := clients.s3.GetBucketReplication(ctx, &awss3.GetBucketReplicationInput{
			Bucket: aws.String(bucket),
		})
		require.NoError(t, err)
		require.NotNil(t, repOut.ReplicationConfiguration)
		require.Len(t, repOut.ReplicationConfiguration.Rules, 1)
		rule := repOut.ReplicationConfiguration.Rules[0]
		require.NotNil(t, rule.Filter)
		require.NotNil(t, rule.Filter.And)
		assert.Equal(t, "data/", aws.ToString(rule.Filter.And.Prefix))
		require.Len(t, rule.Filter.And.Tags, 2)
		tagMap := make(map[string]string, len(rule.Filter.And.Tags))
		for _, tag := range rule.Filter.And.Tags {
			tagMap[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		assert.Equal(t, "prod", tagMap["env"])
		assert.Equal(t, "hot", tagMap["tier"])
	})

	t.Run("Delete", func(t *testing.T) {
		_, err := clients.s3.DeleteBucketReplication(ctx, &awss3.DeleteBucketReplicationInput{
			Bucket: aws.String(bucket),
		})
		require.NoError(t, err)

		_, err = clients.s3.GetBucketReplication(ctx, &awss3.GetBucketReplicationInput{
			Bucket: aws.String(bucket),
		})
		require.Error(t, err)
		assert.Equal(t, "ReplicationConfigurationNotFoundError", apiErrorCode(err))
	})
}

func TestBucketAclRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-acl"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// Default ACL is private (owner FULL_CONTROL only).
	aclOut, err := clients.s3.GetBucketAcl(ctx, &awss3.GetBucketAclInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.NotNil(t, aclOut.Owner)
	assert.NotEmpty(t, aws.ToString(aclOut.Owner.ID))
	hasFullControl := false
	for _, g := range aclOut.Grants {
		if g.Permission == s3types.PermissionFullControl {
			hasFullControl = true
		}
	}
	assert.True(t, hasFullControl, "default ACL must include FULL_CONTROL grant")

	// Set public-read canned ACL.
	_, err = clients.s3.PutBucketAcl(ctx, &awss3.PutBucketAclInput{
		Bucket: aws.String(bucket),
		ACL:    s3types.BucketCannedACLPublicRead,
	})
	require.NoError(t, err)

	aclOut, err = clients.s3.GetBucketAcl(ctx, &awss3.GetBucketAclInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	hasAllUsersRead := false
	for _, g := range aclOut.Grants {
		if g.Grantee != nil &&
			aws.ToString(g.Grantee.URI) == "http://acs.amazonaws.com/groups/global/AllUsers" &&
			g.Permission == s3types.PermissionRead {
			hasAllUsersRead = true
		}
	}
	assert.True(t, hasAllUsersRead, "public-read ACL must grant READ to AllUsers")
}

func TestBucketWebsiteRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-website"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// No website configuration initially.
	_, err = clients.s3.GetBucketWebsite(ctx, &awss3.GetBucketWebsiteInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchWebsiteConfiguration", apiErrorCode(err))

	_, err = clients.s3.PutBucketWebsite(ctx, &awss3.PutBucketWebsiteInput{
		Bucket: aws.String(bucket),
		WebsiteConfiguration: &s3types.WebsiteConfiguration{
			IndexDocument: &s3types.IndexDocument{Suffix: aws.String("index.html")},
			ErrorDocument: &s3types.ErrorDocument{Key: aws.String("error.html")},
		},
	})
	require.NoError(t, err)

	webOut, err := clients.s3.GetBucketWebsite(ctx, &awss3.GetBucketWebsiteInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	require.NotNil(t, webOut.IndexDocument)
	assert.Equal(t, "index.html", aws.ToString(webOut.IndexDocument.Suffix))
	require.NotNil(t, webOut.ErrorDocument)
	assert.Equal(t, "error.html", aws.ToString(webOut.ErrorDocument.Key))

	_, err = clients.s3.DeleteBucketWebsite(ctx, &awss3.DeleteBucketWebsiteInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	_, err = clients.s3.GetBucketWebsite(ctx, &awss3.GetBucketWebsiteInput{
		Bucket: aws.String(bucket),
	})
	require.Error(t, err)
	assert.Equal(t, "NoSuchWebsiteConfiguration", apiErrorCode(err))
}

func TestBucketAccelerateRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-accelerate"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// Default: no accelerate status configured.
	accOut, err := clients.s3.GetBucketAccelerateConfiguration(
		ctx,
		&awss3.GetBucketAccelerateConfigurationInput{
			Bucket: aws.String(bucket),
		},
	)
	require.NoError(t, err)
	assert.Empty(t, accOut.Status)

	_, err = clients.s3.PutBucketAccelerateConfiguration(
		ctx,
		&awss3.PutBucketAccelerateConfigurationInput{
			Bucket: aws.String(bucket),
			AccelerateConfiguration: &s3types.AccelerateConfiguration{
				Status: s3types.BucketAccelerateStatusEnabled,
			},
		},
	)
	require.NoError(t, err)

	accOut, err = clients.s3.GetBucketAccelerateConfiguration(
		ctx,
		&awss3.GetBucketAccelerateConfigurationInput{
			Bucket: aws.String(bucket),
		},
	)
	require.NoError(t, err)
	assert.Equal(t, s3types.BucketAccelerateStatusEnabled, accOut.Status)
}

func TestBucketRequestPaymentRoundTrip(t *testing.T) {
	clients := newTestClients(t)
	ctx := context.Background()
	const bucket = "test-request-payment"

	_, err := clients.s3.CreateBucket(ctx, &awss3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)

	// Default payer is BucketOwner.
	rpOut, err := clients.s3.GetBucketRequestPayment(ctx, &awss3.GetBucketRequestPaymentInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Equal(t, s3types.PayerBucketOwner, rpOut.Payer)

	_, err = clients.s3.PutBucketRequestPayment(ctx, &awss3.PutBucketRequestPaymentInput{
		Bucket: aws.String(bucket),
		RequestPaymentConfiguration: &s3types.RequestPaymentConfiguration{
			Payer: s3types.PayerRequester,
		},
	})
	require.NoError(t, err)

	rpOut, err = clients.s3.GetBucketRequestPayment(ctx, &awss3.GetBucketRequestPaymentInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Equal(t, s3types.PayerRequester, rpOut.Payer)

	_, err = clients.s3.PutBucketRequestPayment(ctx, &awss3.PutBucketRequestPaymentInput{
		Bucket: aws.String(bucket),
		RequestPaymentConfiguration: &s3types.RequestPaymentConfiguration{
			Payer: s3types.PayerBucketOwner,
		},
	})
	require.NoError(t, err)

	rpOut, err = clients.s3.GetBucketRequestPayment(ctx, &awss3.GetBucketRequestPaymentInput{
		Bucket: aws.String(bucket),
	})
	require.NoError(t, err)
	assert.Equal(t, s3types.PayerBucketOwner, rpOut.Payer)
}
