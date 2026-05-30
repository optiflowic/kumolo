---
source: https://docs.aws.amazon.com/kms/latest/APIReference/API_DisableKeyRotation.html
sdk_input: kms.DisableKeyRotationInput
sdk_output: kms.DisableKeyRotationOutput
last_verified: 2026-05-31
---

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| KeyId | string | Yes | Key ID or key ARN. Length 1–2048. |

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

- Writes `rotation.json` with `Enabled=false`, clearing `RotationPeriodInDays` and `NextRotationDate`.
