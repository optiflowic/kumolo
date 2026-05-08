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

| Service      | Operations |
|--------------|------------|
| **S3**       | Bucket CRUD, Object CRUD, Multipart Upload, Versioning, Tagging, CORS, Policy, Lifecycle, ACL, Encryption, and more |
| **DynamoDB** | Table CRUD, Item operations (Get/Put/Delete/Update), Query, Scan, Batch operations, Transactions, TTL, Tags, Kinesis streaming destinations |
| **STS**      | GetCallerIdentity, AssumeRole, GetSessionToken |

## Configuration

| Environment Variable  | Default   | Description                       |
|-----------------------|-----------|-----------------------------------|
| `KUMOLO_PORT`         | `5566`    | HTTP listen port                  |
| `KUMOLO_DATA_DIR`     | `./data`  | Persistent storage directory      |
| `KUMOLO_LOG_LEVEL`    | `info`    | Log level (`debug`, `info`, `warn`, `error`) |

## License

MIT — see [LICENSE](LICENSE).
