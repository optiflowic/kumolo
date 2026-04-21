package s3

import (
	"io"
	"os"
	"time"
)

// bucketStore is the subset of Storage used by the Router for bucket operations.
type bucketStore interface {
	ListBuckets() ([]BucketInfo, error)
	CreateBucket(bucket, region string) error
	DeleteBucket(bucket string) error
	BucketExists(bucket string) bool
	GetBucketRegion(bucket string) (string, error)
}

// objectStore is the subset of Storage used by the Router for object operations.
type objectStore interface {
	PutObject(
		bucket, key string,
		r io.Reader,
		contentType string,
		userMetadata map[string]string,
		sseAlgorithm, sseKMSKeyID string,
	) (ObjectMetadata, error)
	GetObject(bucket, key string) (*os.File, ObjectMetadata, error)
	GetObjectVersion(bucket, key, versionID string) (*os.File, ObjectMetadata, error)
	CopyObject(
		srcBucket, srcKey, srcVersionID, dstBucket, dstKey string,
		contentType string,
		userMetadata map[string]string,
		sseAlgorithm, sseKMSKeyID string,
	) (ObjectMetadata, error)
	DeleteObject(bucket, key string) error
	DeleteObjectVersioned(bucket, key string) (versionID string, isDeleteMarker bool, err error)
	DeleteObjectVersion(bucket, key, versionID string) (isDeleteMarker bool, err error)
	HeadObject(bucket, key string) (ObjectMetadata, error)
	HeadObjectVersion(bucket, key, versionID string) (ObjectMetadata, error)
	ListObjects(bucket string) ([]ObjectInfo, error)
	ListObjectVersions(bucket string) ([]VersionInfo, []DeleteMarkerInfo, error)
	SetObjectRestoreInitiated(bucket, key string) error
}

// multipartStore is the subset of Storage used by the Router for multipart upload operations.
type multipartStore interface {
	CreateMultipartUpload(
		bucket, key, contentType, sseAlgorithm, sseKMSKeyID string,
	) (uploadID string, err error)
	UploadPart(uploadID string, partNumber int, r io.Reader) (etag string, err error)
	UploadPartCopy(
		uploadID string,
		partNumber int,
		srcBucket, srcKey, srcVersionID string,
		br *byteRange,
	) (etag string, lastModified time.Time, err error)
	CompleteMultipartUpload(uploadID string, parts []CompletePart) (ObjectMetadata, error)
	AbortMultipartUpload(uploadID string) error
	ListMultipartUploads(bucket string) ([]MultipartUploadInfo, error)
	ListParts(uploadID string) (uploadMeta, []PartInfo, error)
}

// objectTaggingStore is the subset of Storage used by the Router for object tagging operations.
type objectTaggingStore interface {
	PutObjectTagging(bucket, key string, tags []Tag) error
	GetObjectTagging(bucket, key string) ([]Tag, error)
	DeleteObjectTagging(bucket, key string) error
}

// bucketTaggingStore is the subset of Storage used by the Router for bucket tagging operations.
type bucketTaggingStore interface {
	PutBucketTagging(bucket string, tags []Tag) error
	GetBucketTagging(bucket string) ([]Tag, error)
	DeleteBucketTagging(bucket string) error
}

// bucketVersioningStore is the subset of Storage used by the Router for bucket versioning operations.
type bucketVersioningStore interface {
	PutBucketVersioning(bucket, status string) error
	GetBucketVersioning(bucket string) (string, error)
}

// bucketCORSStore is the subset of Storage used by the Router for bucket CORS operations.
type bucketCORSStore interface {
	PutBucketCors(bucket string, rules []CORSRule) error
	GetBucketCors(bucket string) ([]CORSRule, error)
	DeleteBucketCors(bucket string) error
}

// bucketPolicyStore is the subset of Storage used by the Router for bucket policy operations.
type bucketPolicyStore interface {
	PutBucketPolicy(bucket, policy string) error
	GetBucketPolicy(bucket string) (string, error)
	DeleteBucketPolicy(bucket string) error
}

// bucketPublicAccessBlockStore handles public access block configuration.
type bucketPublicAccessBlockStore interface {
	PutPublicAccessBlock(bucket, xmlBody string) error
	GetPublicAccessBlock(bucket string) (string, error)
	DeletePublicAccessBlock(bucket string) error
}

// bucketEncryptionStore handles server-side encryption configuration.
type bucketEncryptionStore interface {
	PutBucketEncryption(bucket, xmlBody string) error
	GetBucketEncryption(bucket string) (string, error)
	DeleteBucketEncryption(bucket string) error
}

// bucketOwnershipControlsStore handles ownership controls configuration.
type bucketOwnershipControlsStore interface {
	PutBucketOwnershipControls(bucket, xmlBody string) error
	GetBucketOwnershipControls(bucket string) (string, error)
	DeleteBucketOwnershipControls(bucket string) error
}

// bucketNotificationStore handles notification configuration.
type bucketNotificationStore interface {
	PutBucketNotification(bucket, xmlBody string) error
	GetBucketNotification(bucket string) (string, error)
}

// bucketLifecycleStore handles lifecycle configuration.
type bucketLifecycleStore interface {
	PutBucketLifecycle(bucket, xmlBody string) error
	GetBucketLifecycle(bucket string) (string, error)
	DeleteBucketLifecycle(bucket string) error
}

// bucketWebsiteStore handles website configuration.
type bucketWebsiteStore interface {
	PutBucketWebsite(bucket, xmlBody string) error
	GetBucketWebsite(bucket string) (string, error)
	DeleteBucketWebsite(bucket string) error
}

// bucketLoggingStore handles logging configuration.
type bucketLoggingStore interface {
	PutBucketLogging(bucket, xmlBody string) error
	GetBucketLogging(bucket string) (string, error)
}

// bucketAccelerateStore handles transfer acceleration configuration.
type bucketAccelerateStore interface {
	PutBucketAccelerate(bucket, xmlBody string) error
	GetBucketAccelerate(bucket string) (string, error)
}

// bucketReplicationStore handles replication configuration.
type bucketReplicationStore interface {
	PutBucketReplication(bucket, xmlBody string) error
	GetBucketReplication(bucket string) (string, error)
	DeleteBucketReplication(bucket string) error
}

// bucketRequestPaymentStore handles request payment configuration.
type bucketRequestPaymentStore interface {
	PutBucketRequestPayment(bucket, xmlBody string) error
	GetBucketRequestPayment(bucket string) (string, error)
}
