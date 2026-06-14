<p align="center">
  <img src="assets/logo.png" alt="kumolo" width="280" />
</p>

# kumolo

[![Go Report Card](https://goreportcard.com/badge/github.com/optiflowic/kumolo)](https://goreportcard.com/report/github.com/optiflowic/kumolo)
[![Latest Release](https://img.shields.io/github/v/release/optiflowic/kumolo?sort=semver)](https://github.com/optiflowic/kumolo/releases)
[![CI](https://github.com/optiflowic/kumolo/actions/workflows/ci.yaml/badge.svg)](https://github.com/optiflowic/kumolo/actions/workflows/ci.yaml)
[![Release](https://github.com/optiflowic/kumolo/actions/workflows/release.yaml/badge.svg)](https://github.com/optiflowic/kumolo/actions/workflows/release.yaml)
[![Docker](https://github.com/optiflowic/kumolo/actions/workflows/docker-publish.yaml/badge.svg)](https://github.com/optiflowic/kumolo/actions/workflows/docker-publish.yaml)
[![License: MIT](https://img.shields.io/badge/license-MIT-green.svg)](./LICENSE)

A high-fidelity AWS emulator for local development and testing.

> **Goal: if it works on kumolo, it works on real AWS — and vice versa.**

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

The same pattern applies to DynamoDB, KMS, STS, and other supported services.

## Supported Services

| Service      | Operations |
|--------------|------------|
| **DynamoDB** | Table CRUD, Item operations (Get/Put/Delete/Update), Query, Scan, Batch operations, Transactions, PartiQL (ExecuteStatement / BatchExecuteStatement / ExecuteTransaction), Streams, TTL, Tags, Kinesis streaming destinations |
| **KMS**      | Key management (Create/Describe/Enable/Disable/Schedule deletion), Data plane (Encrypt/Decrypt/GenerateDataKey/GenerateDataKeyPair), Aliases, Key rotation, Grants, Tags |
| **S3**       | Bucket CRUD, Object CRUD, Multipart Upload, Versioning, Tagging, CORS, Policy, Lifecycle, ACL (enforced), Encryption (SSE-S3 / SSE-KMS / SSE-C), Object Lock, SelectObjectContent, BucketLogging, BucketReplication, and more |
| **STS**      | GetCallerIdentity, AssumeRole, GetSessionToken |

For the full list of supported operations, see the [documentation](https://optiflowic.github.io/kumolo-docs).

## Configuration

| Environment Variable  | Default   | Description                       |
|-----------------------|-----------|-----------------------------------|
| `KUMOLO_PORT`         | `5566`    | HTTP listen port                  |
| `KUMOLO_DATA_DIR`     | `./data`  | Persistent storage directory      |
| `KUMOLO_LOG_LEVEL`    | `info`    | Log level (`debug`, `info`, `warn`, `error`) |

## Known Limitations

kumolo aims for high fidelity, but some behaviors differ from real AWS by design or as a known gap.

**DynamoDB**

- `ConsumedCapacity` is returned when `ReturnConsumedCapacity` is `TOTAL` or `INDEXES`, but `CapacityUnits` is always `1.0` — actual RCU/WCU are not computed.
- Number attribute comparisons use `float64` precision. Values with more than 15 significant digits may not compare correctly.

**KMS**

- Grant-based access control is not enforced — all KMS operations succeed regardless of grant `Operations` or `Constraints`.
- Key rotation does not run on a schedule. Use `RotateKeyOnDemand` to trigger a rotation in tests.

**S3**

- Bucket Policy rules are stored and returned but not enforced — all requests are permitted regardless of policy content.
- SigV4 request signatures are parsed but not cryptographically verified. This is intentional for local development.

**STS**

- `AssumeRole` always returns the same fixed credentials regardless of which role ARN is specified. Multiple distinct roles are not simulated.

## Contributing

See [CONTRIBUTING.md](.github/CONTRIBUTING.md).

## License

MIT — see [LICENSE](LICENSE).
