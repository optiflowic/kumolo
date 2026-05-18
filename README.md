<p align="center">
  <img src="assets/logo.png" alt="kumolo" width="280" />
</p>

# kumolo

A high-fidelity AWS emulator for local development and testing.

> **If it works on kumolo, it works on real AWS — and vice versa.**

kumolo runs as a standalone server that accepts standard AWS SDK v2 requests. No mocking, no stubs — it behaves like real AWS.

## Quick Start

### Docker

```bash
docker run -p 5566:5566 ghcr.io/optiflowic/kumolo:latest
```

Or with persistent storage:

```bash
docker run -p 5566:5566 -v $(pwd)/data:/data ghcr.io/optiflowic/kumolo:latest
```

### Docker Compose

```yaml
services:
  kumolo:
    image: ghcr.io/optiflowic/kumolo:latest
    ports:
      - "5566:5566"
    volumes:
      - ./data:/data
```

### Binary

Download the latest binary from [GitHub Releases](https://github.com/optiflowic/kumolo/releases) and run:

```bash
kumolo
```

## AWS SDK v2 Usage

Point your AWS SDK at `http://localhost:5566` — no other changes needed.

```go
import (
    "context"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/credentials"
    "github.com/aws/aws-sdk-go-v2/service/s3"
)

cfg, err := config.LoadDefaultConfig(context.Background(),
    config.WithRegion("us-east-1"),
    config.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("test", "test", "")),
)
if err != nil {
    panic(err)
}

client := s3.NewFromConfig(cfg, func(o *s3.Options) {
    o.BaseEndpoint = aws.String("http://localhost:5566")
    o.UsePathStyle = true
})
```

The same pattern applies to DynamoDB, STS, and other supported services.

## Supported Services

### S3

**Bucket operations**

| Operation | Notes |
|-----------|-------|
| ListBuckets | |
| CreateBucket | |
| HeadBucket | |
| DeleteBucket | |
| GetBucketLocation | |
| GetBucketVersioning / PutBucketVersioning | |
| GetBucketPolicy / PutBucketPolicy / DeleteBucketPolicy | |
| GetBucketTagging / PutBucketTagging / DeleteBucketTagging | |
| GetBucketCors / PutBucketCors / DeleteBucketCors | |
| GetBucketAcl / PutBucketAcl | |
| GetBucketPublicAccessBlock / PutBucketPublicAccessBlock / DeleteBucketPublicAccessBlock | |
| GetBucketEncryption / PutBucketEncryption / DeleteBucketEncryption | |
| GetBucketOwnershipControls / PutBucketOwnershipControls / DeleteBucketOwnershipControls | |
| GetBucketNotificationConfiguration / PutBucketNotificationConfiguration | |
| GetBucketLifecycleConfiguration / PutBucketLifecycleConfiguration / DeleteBucketLifecycleConfiguration | |
| GetBucketWebsite / PutBucketWebsite / DeleteBucketWebsite | |
| GetBucketLogging / PutBucketLogging | |
| GetBucketAccelerateConfiguration / PutBucketAccelerateConfiguration | |
| GetBucketReplication / PutBucketReplication / DeleteBucketReplication | |
| GetBucketRequestPayment / PutBucketRequestPayment | |
| GetBucketObjectLockConfiguration / PutBucketObjectLockConfiguration | |
| ListObjectsV1 / ListObjectsV2 | |
| ListObjectVersions | |
| ListMultipartUploads | |
| DeleteObjects | |

**Object operations**

| Operation | Notes |
|-----------|-------|
| GetObject | Range requests, conditional GET (If-Match, If-None-Match, If-Modified-Since, If-Unmodified-Since) |
| PutObject | SSE, Object Lock, storage class, checksums (CRC32, SHA256, SHA1, CRC32C), conditional write (If-None-Match) |
| HeadObject | |
| DeleteObject | |
| CopyObject | COPY/REPLACE metadata directive, SSE, Object Lock |
| GetObjectAcl / PutObjectAcl | |
| GetObjectTagging / PutObjectTagging / DeleteObjectTagging | |
| GetObjectRetention / PutObjectRetention | GOVERNANCE and COMPLIANCE modes |
| GetObjectLegalHold / PutObjectLegalHold | |
| GetObjectAttributes | |
| RestoreObject | GLACIER and DEEP_ARCHIVE |
| CreateMultipartUpload / UploadPart / UploadPartCopy / CompleteMultipartUpload / AbortMultipartUpload | |
| ListParts | |

### DynamoDB

| Operation | Notes |
|-----------|-------|
| CreateTable / DeleteTable / DescribeTable / ListTables | |
| UpdateTable | |
| PutItem / GetItem / DeleteItem / UpdateItem | |
| BatchGetItem / BatchWriteItem | |
| Query | |
| Scan | |
| TransactGetItems / TransactWriteItems | |
| UpdateTimeToLive / DescribeTimeToLive | TTL expiry enforced on read |
| TagResource / UntagResource / ListTagsOfResource | |
| DescribeContinuousBackups / UpdateContinuousBackups | |
| DescribeKinesisStreamingDestination / EnableKinesisStreamingDestination / DisableKinesisStreamingDestination | |
| DescribeLimits | |
| DescribeEndpoints | |

### STS

| Operation | Notes |
|-----------|-------|
| GetCallerIdentity | |
| AssumeRole | |
| GetSessionToken | |

## Configuration

| Environment Variable  | Default   | Description                       |
|-----------------------|-----------|-----------------------------------|
| `KUMOLO_PORT`         | `5566`    | HTTP listen port                  |
| `KUMOLO_DATA_DIR`     | `./data`  | Persistent storage directory      |
| `KUMOLO_LOG_LEVEL`    | `info`    | Log level (`debug`, `info`, `warn`, `error`) |

## Contributing

See [CONTRIBUTING.md](.github/CONTRIBUTING.md).

## License

MIT — see [LICENSE](LICENSE).
