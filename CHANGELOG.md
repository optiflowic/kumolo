# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.1] - 2026-06-12

### Fixed

#### S3

- `CopyObject` and all multipart upload operations now enforce ACL permission checks for anonymous requests; previously anonymous callers could bypass ACL restrictions on these paths

#### STS

- `AssumeRole` now validates `RoleArn` (length 20–2048) and `RoleSessionName` (length 2–64, pattern `[\w+=,.@-]*`), returning `ValidationError` for out-of-range or malformed inputs

#### KMS

- `CreateKey` now persists the `Tags` parameter; previously tags were accepted but silently discarded, causing Terraform's `default_tags` mechanism to loop indefinitely on plan/apply

#### DynamoDB

- `CreateTable` now persists the `Tags` parameter; previously tags were accepted but silently discarded

## [0.2.0] - 2026-06-11

### Added

#### KMS (new service)

- Core key management: `CreateKey`, `DescribeKey`, `ListKeys`, `GetKeyPolicy`, `PutKeyPolicy`
- Data plane: `GenerateDataKey`, `GenerateDataKeyWithoutPlaintext`, `GenerateDataKeyPair`, `GenerateDataKeyPairWithoutPlaintext`, `Encrypt`, `Decrypt`
- Key aliases: `CreateAlias`, `DeleteAlias`, `UpdateAlias`, `ListAliases`
- Key lifecycle: `EnableKey`, `DisableKey`, `ScheduleKeyDeletion`, `CancelKeyDeletion`
- Key rotation: `EnableKeyRotation`, `DisableKeyRotation`, `GetKeyRotationStatus`, `RotateKeyOnDemand`, `ListKeyRotations`
- Grant management: `CreateGrant`, `ListGrants`, `RetireGrant`, `RevokeGrant`, `ListRetirableGrants`
- Tagging: `TagResource`, `UntagResource`, `ListResourceTags`

#### S3

- `SelectObjectContent` — CSV and JSON input/output with SQL expression evaluation
- SSE-C: server-side encryption with customer-provided keys (`x-amz-server-side-encryption-customer-*` headers validated and stored on `PutObject`, `GetObject`, `HeadObject`, `UploadPart`, `CopyObject`)
- SSE header validation: `x-amz-server-side-encryption` now rejects invalid values; `AES256`, `aws:kms`, and `aws:kms:dsse` accepted
- SSE-KMS integration: KMS `GenerateDataKey` called on object writes when a KMS key is specified; `x-amz-server-side-encryption-aws-kms-key-id` resolved and echoed in responses
- `BucketKeyEnabled` request/response header and object metadata
- Default encryption applied to `PutObject`, `CopyObject`, and `CreateMultipartUpload` when a bucket default encryption rule is configured
- BucketLogging: access log records now delivered to the configured target bucket and prefix
- BucketReplication: objects now replicated to the configured destination bucket on `PutObject`, `CopyObject`, and multipart upload completion

#### DynamoDB

- PartiQL: `ExecuteStatement`, `BatchExecuteStatement`, `ExecuteTransaction`
- `ReturnValuesOnConditionCheckFailure` on PartiQL write statements
- DynamoDB Streams: `ListStreams`, `DescribeStream`, `GetShardIterator`, `GetRecords`

#### Go testing library

- `pkg/kumolo`: in-process Go testing library — start kumolo in a `httptest.Server` with a single call, no Docker required

### Fixed

#### S3

- ACL operations (`GetBucketAcl`, `PutBucketAcl`, `GetObjectAcl`, `PutObjectAcl`) stored grants but never enforced them — previously stored grants are now enforced on `GetObject` / `PutObject`
- Lifecycle rule `Expiration.Date` (absolute expiry date) and `ExpiredObjectDeleteMarker` were never evaluated by the background enforcement loop

#### DynamoDB

- `ReturnConsumedCapacity`: `ConsumedCapacity` (or `ConsumedCapacities`) was accepted as a parameter but omitted from all responses; it is now included

## [0.1.1] - 2026-05-28

### Fixed

#### DynamoDB

- `CreateTable` returned `ResourceInUseException` when a previous failed attempt left an orphan directory — table existence is now determined by the presence of the `.table.json` metadata file, not the directory
- `CreateTable` can now reuse an orphan directory left by a failed prior attempt instead of failing with a directory-exists error
- `ListTables` included orphan directories (no `.table.json`) in its output, causing `DescribeTable` to return `ResourceNotFoundException` for the same name
- `ListTables` silently swallowed non-`ErrNotExist` I/O errors from `stat`; these are now propagated to the caller
- `TransactWriteItems` did not roll back writes already applied to disk when a later write in Phase 2 failed, violating atomicity; each item's pre-write state is now snapshotted and restored in reverse order on failure

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
- Lifecycle: `PutBucketLifecycleConfiguration`, `GetBucketLifecycleConfiguration`, `DeleteBucketLifecycle`; background rule enforcement
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

[0.2.1]: https://github.com/optiflowic/kumolo/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/optiflowic/kumolo/releases/tag/v0.2.0
[0.1.1]: https://github.com/optiflowic/kumolo/releases/tag/v0.1.1
[0.1.0]: https://github.com/optiflowic/kumolo/releases/tag/v0.1.0
