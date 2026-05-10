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

## Contributing

Contributions are welcome! Please open an issue before submitting a pull request for non-trivial changes.

### Development Environment

The dev environment is managed with [Nix](https://nixos.org/). With Nix installed, enter the shell:

```bash
nix develop
```

Or, if you use [direnv](https://direnv.net/), run `direnv allow` once and the shell activates automatically.

### Common Commands

| Command | Description |
|---|---|
| `make build` | Build the binary |
| `make test` | Run tests |
| `make cover` | Run tests with coverage report |
| `make lint` | Run golangci-lint |
| `make fmt` | Format code |
| `make all` | fmt-check, vet, lint, test, build |

### Conventions

- Commits follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `chore:`, `docs:`).
- Each PR should contain one logical change.
- New operations must include table-driven tests with 100% package coverage.

## License

MIT — see [LICENSE](LICENSE).
