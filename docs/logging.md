# Logging

## Overview

kumolo uses Go's `log/slog` for structured logging. All output goes to stderr.

Log format (default):

```
[2006-01-02T15:04:05Z] [INFO] request op=CreateBucket status=200 duration=1.2ms
[2006-01-02T15:04:05Z] [ERROR] request op=PutObject status=500 err="disk full" duration=0.8ms
```

## Log Streams

There are two distinct log streams:

| Stream | What it captures | Volume |
|--------|-----------------|--------|
| **Request log** | One line per request (`emitRequestLog`) | Scales with request count |
| **Application log** | Validation details, rollback failures, internal events | Low regardless of request count |

## Level Rules

### Request log (`emitRequestLog`)

| Status | Level | Rationale |
|--------|-------|-----------|
| 5xx | `ERROR` | kumolo server error — always visible |
| 4xx | `INFO` | Client error or expected polling response (e.g., 404 while Terraform waits for a resource to exist) — visible by default |
| 2xx writes (POST/PUT/DELETE) | `INFO` | State change — visible by default |
| 2xx reads (GET/HEAD in S3) | `DEBUG` | High-volume reads that add noise in typical workflows |

**Why 4xx is INFO, not WARN or DEBUG:**

During Terraform `apply`, the provider checks resource existence with HEAD/DescribeTable calls that return 4xx until the resource is ready. These are expected and not warnings; making them INFO ensures the developer sees activity during "still creating" without alarming WARN markers.

**Why S3 reads stay at DEBUG:**

GET and HEAD operations can be high volume (e.g., repeated GetObject calls in a test suite). Keeping them at DEBUG avoids drowning out write operations and errors at the default INFO level. Set `KUMOLO_LOG_LEVEL=debug` to see them.

### Application log

| Event | Level |
|-------|-------|
| Rollback failures after a partial write | `WARN` or `ERROR` |
| Validation detail ("RoleArn too short", etc.) | `DEBUG` |
| Storage initialization errors | `ERROR` |

## Configuration

Set `KUMOLO_LOG_LEVEL` to control which logs appear:

| Level | What you see |
|-------|-------------|
| `debug` | Everything — all requests including reads, validation details |
| `info` (default) | All requests except S3 reads, application warnings and errors |
| `warn` | Application warnings and errors only |
| `error` | Server errors only |

## Common Use Cases

**Terraform `apply` (default INFO):**

```
INFO  request  op=DescribeTable  status=400  code=ResourceNotFoundException  duration=1ms
INFO  request  op=CreateTable    status=200  duration=3ms
INFO  request  op=DescribeTable  status=200  duration=1ms
INFO  request  method=PUT  path=/my-bucket  status=200  duration=2ms
INFO  request  method=PUT  path=/my-bucket/key  status=200  duration=1ms
```

Polling responses (4xx before the resource exists, 2xx writes) are all visible. S3 HEAD verification calls after creation are at DEBUG and not shown.

**Debugging a specific S3 read:**

```
KUMOLO_LOG_LEVEL=debug kumolo
```

Now all GET/HEAD operations appear. Pipe through `grep op=GetObject` or similar to focus on one operation.

**CI / automated tests (reduce noise):**

```
KUMOLO_LOG_LEVEL=warn kumolo
```

Only application warnings and server errors appear. Useful when running a large test suite where request volume would otherwise produce thousands of INFO lines.
