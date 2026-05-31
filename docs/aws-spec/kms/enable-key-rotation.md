---
source: https://docs.aws.amazon.com/kms/latest/APIReference/API_EnableKeyRotation.html
sdk_input: kms.EnableKeyRotationInput
sdk_output: kms.EnableKeyRotationOutput
last_verified: 2026-05-31
---

# Enable Key Rotation

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| KeyId | string | Yes | Key ID or key ARN. Length 1–2048. |
| RotationPeriodInDays | integer | No | 90–2560. Default: 365. |

## Response

HTTP 200 with empty body.

## Constraints

- Only valid for `SYMMETRIC_DEFAULT` keys → `UnsupportedOperationException` otherwise.
- Key must be in `Enabled` state:
  - `Disabled` → `DisabledException` (400)
  - `PendingDeletion` → `KMSInvalidStateException` (400)

## Errors

| Error | HTTP |
|-------|------|
| NotFoundException | 400 |
| DisabledException | 400 |
| KMSInvalidStateException | 400 |
| UnsupportedOperationException | 400 |
| InvalidArnException | 400 |
| KMSInternalException | 500 |

## kumolo notes

- Stores rotation config in `keys/{id}/rotation.json`: `Enabled=true`, `RotationPeriodInDays`, `NextRotationDate = now + period * 86400`.
- kumolo does NOT perform actual key material rotation; only records the flag and next date.
- Calling EnableKeyRotation again updates the period and resets NextRotationDate.
