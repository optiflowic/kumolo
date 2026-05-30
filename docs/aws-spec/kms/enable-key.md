---
source: https://docs.aws.amazon.com/kms/latest/APIReference/API_EnableKey.html
sdk_input: kms.EnableKeyInput
sdk_output: kms.EnableKeyOutput
last_verified: 2026-05-31
---

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| KeyId | string | Yes | Key ID, key ARN. Length 1–2048. |

## Response

HTTP 200 with empty body.

## State machine

| From state | Result |
|------------|--------|
| Disabled | → Enabled |
| Enabled | no-op (idempotent) |
| PendingDeletion | KMSInvalidStateException (400) |

## Errors

| Error | HTTP |
|-------|------|
| NotFoundException | 400 |
| KMSInvalidStateException | 400 |
| InvalidArnException | 400 |
| KMSInternalException | 500 |

## kumolo notes

- Sets `KeyState = "Enabled"`, `Enabled = true` in meta.json.
- Alias and ARN refs are resolved before the state check.
