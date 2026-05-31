---
source: https://docs.aws.amazon.com/kms/latest/APIReference/API_DisableKey.html
sdk_input: kms.DisableKeyInput
sdk_output: kms.DisableKeyOutput
last_verified: 2026-05-31
---

# Disable Key

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| KeyId | string | Yes | Key ID, key ARN. Length 1–2048. |

## Response

HTTP 200 with empty body.

## State machine

| From state | Result |
|------------|--------|
| Enabled | → Disabled |
| Disabled | no-op (idempotent) |
| PendingDeletion | KMSInvalidStateException (400) |

## Errors

| Error | HTTP |
|-------|------|
| NotFoundException | 400 |
| KMSInvalidStateException | 400 |
| InvalidArnException | 400 |
| KMSInternalException | 500 |

## kumolo notes

- Sets `KeyState = "Disabled"`, `Enabled = false` in meta.json.
- Disabling a key does not affect key rotation config; rotation is paused while disabled but config is preserved.
