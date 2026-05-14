# End-to-End Verification

This directory contains configurations and scripts for verifying kumolo compatibility
with real-world AWS tooling.

## Prerequisites

Start kumolo before running any verification:

```sh
# Docker Compose (recommended)
docker compose up -d

# Binary
make build && ./build/kumolo
```

kumolo listens on `http://localhost:5566` by default.

## When to run

These are manual verification tools, not part of CI. Run them locally before
opening a PR when:

- You added or changed S3 / DynamoDB operations
- You added support for a new IaC tool or AWS SDK version

CI covers unit and integration tests (`make test` / `make integration`), which
catch most regressions. E2E tests add a real-tool smoke test on top of that.

## Tools

| Directory | Description |
|-----------|-------------|
| [`terraform/`](terraform/) | Terraform configurations for S3 and DynamoDB |
| [`aws-cli/`](aws-cli/) | AWS CLI verification scripts |

Run all CLI verifications at once:

```sh
make e2e
```

Run Terraform verification:

```sh
make e2e-terraform
```

## Other IaC Tools

Any tool that supports AWS endpoint overrides works with kumolo. Key settings:

- **Endpoint URL**: `http://localhost:5566` (all services share one port)
- **Credentials**: any non-empty value (e.g. `access_key = "test"`)
- **Region**: any valid region (e.g. `us-east-1`)
- **S3 path-style**: required — kumolo uses path-style URLs

### Pulumi (TypeScript)

```typescript
import * as aws from "@pulumi/aws";

const provider = new aws.Provider("kumolo", {
    region: "us-east-1",
    accessKey: "test",
    secretKey: "test",
    skipCredentialsValidation: true,
    skipRequestingAccountId: true,
    s3UsePathStyle: true,
    endpoints: [{
        s3:       "http://localhost:5566",
        dynamodb: "http://localhost:5566",
        sts:      "http://localhost:5566",
    }],
});

// Pass provider to each resource:
// const bucket = new aws.s3.BucketV2("my-bucket", {}, { provider });
```

### AWS CDK

CDK synthesizes CloudFormation; deploy-time SDK calls pick up endpoint overrides
via environment variables. Set these before running `cdk deploy`:

```sh
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1
export AWS_ENDPOINT_URL_S3=http://localhost:5566
export AWS_ENDPOINT_URL_DYNAMODB=http://localhost:5566
export AWS_ENDPOINT_URL_STS=http://localhost:5566
```

> CDK also requires a CloudFormation endpoint. kumolo does not implement
> CloudFormation, so use a tool like LocalStack or `cdk synth` + manual apply
> for full CDK deployments.
