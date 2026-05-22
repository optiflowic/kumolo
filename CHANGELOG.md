# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] - 2026-05-24

Initial release of kumolo — a high-fidelity AWS emulator for local development and testing.

### Added

#### S3

- Bucket operations: `CreateBucket`, `DeleteBucket`, `HeadBucket`, `ListBuckets`
- Object operations: `PutObject`, `GetObject`, `HeadObject`, `DeleteObject`, `CopyObject`
- Batch delete: `DeleteObjects`
- Multipart upload: `CreateMultipartUpload`, `UploadPart`, `UploadPartCopy`, `CompleteMultipartUpload`, `AbortMultipartUpload`
- Listing: `ListObjects` (v1), `ListObjectsV2`, `ListObjectVersions`, `ListMultipartUploads`, `ListParts` — all with full pagination support
- Versioning: `PutBucketVersioning`, `GetBucketVersioning`; versioned object reads, deletes, and delete markers
- Object Lock: bucket-level configuration, per-object retention (GOVERNANCE / COMPLIANCE), legal hold, delete enforcement
- Tagging: `GetObjectTagging`, `PutObjectTagging`, `DeleteObjectTagging`, `GetBucketTagging`, `PutBucketTagging`, `DeleteBucketTagging`
- CORS: `PutBucketCors`, `GetBucketCors`, `DeleteBucketCors`; preflight `OPTIONS` enforcement
- Bucket policy: `PutBucketPolicy`, `GetBucketPolicy`, `DeleteBucketPolicy`
- Encryption config: `PutBucketEncryption`, `GetBucketEncryption`, `DeleteBucketEncryption`
- Lifecycle: `PutBucketLifecycleConfiguration`, `GetBucketLifecycleConfiguration`, `DeleteBucketLifecycleConfiguration`; background rule enforcement
- Website, logging, replication, ownership controls, request payment, accelerate, public access block config endpoints
- ACL: `GetBucketAcl`, `PutBucketAcl`, `GetObjectAcl`, `PutObjectAcl` (stored; not enforced)
- `GetObjectAttributes`, `RestoreObject`
- User-defined metadata (`x-amz-meta-*`) on `PutObject` / `CopyObject` / multipart upload
- Conditional `GET` via `If-Match`, `If-None-Match`, `If-Modified-Since`, `If-Unmodified-Since`
- `Range` header for partial content (206) and `416` on unsatisfiable range
- Conditional `PutObject` via `If-None-Match: *`
- SSE header pass-through (`x-amz-server-side-encryption`, `x-amz-server-side-encryption-aws-kms-key-id`)
- Checksum validation: `Content-MD5`, `x-amz-checksum-crc32`, `crc32c`, `sha1`, `sha256`
- Presigned URL support
- 5 MB minimum part size enforcement on `UploadPart`

#### DynamoDB

- Table operations: `CreateTable`, `DeleteTable`, `DescribeTable`, `ListTables`, `UpdateTable`
- Item operations: `PutItem`, `GetItem`, `DeleteItem`, `UpdateItem`
- Batch operations: `BatchGetItem`, `BatchWriteItem`
- Transactions: `TransactGetItems`, `TransactWriteItems`
- Querying: `Query`, `Scan` — with `FilterExpression`, `ProjectionExpression`, `KeyConditionExpression`, `Limit`, `ExclusiveStartKey`, `ScanIndexForward`
- Parallel scan support
- Expression language: `ConditionExpression`, `UpdateExpression` (`SET`, `REMOVE`, `ADD`, `DELETE` clauses), nested attribute paths, `if_not_exists()`, `list_append()`, `IN` operator
- Secondary indexes: GSI and LSI (queries routed to correct index)
- `ReturnValues`: `ALL_OLD`, `ALL_NEW`, `UPDATED_OLD`, `UPDATED_NEW`
- TTL: `UpdateTimeToLive`, `DescribeTimeToLive`; background item expiry
- Tagging: `TagResource`, `UntagResource`, `ListTagsOfResource`
- `DescribeLimits`, `DescribeContinuousBackups`, `UpdateContinuousBackups`
- Kinesis streaming destination: `EnableKinesisStreamingDestination`, `DisableKinesisStreamingDestination`, `DescribeKinesisStreamingDestination`

#### STS

- `GetCallerIdentity`, `AssumeRole`, `GetSessionToken`

#### Infrastructure

- Single binary server on port 5566; dispatches S3, DynamoDB, and STS by path and `Host` header
- Filesystem-backed persistent storage under `KUMOLO_DATA_DIR`
- Docker image published to `ghcr.io/optiflowic/kumolo`
- GoReleaser-based binary releases for Linux and macOS (amd64 / arm64)
- Nix flake for reproducible development environment
- AWS SDK v2 integration test suite (`tests/integration/`)
- AWS CLI and Terraform e2e verification suite (`e2e/`)
- CI: build, vet, lint (golangci-lint), test with race detector, Docker image publish

[0.1.0]: https://github.com/optiflowic/kumolo/releases/tag/v0.1.0
