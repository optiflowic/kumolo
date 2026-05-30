---
source: https://docs.aws.amazon.com/kms/latest/APIReference/API_GetKeyRotationStatus.html
sdk_input: kms.GetKeyRotationStatusInput
sdk_output: kms.GetKeyRotationStatusOutput
last_verified: 2026-05-31
---

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| KeyId | string | Yes | Key ID or key ARN. Length 1–2048. |

## Response

```json
{
  "KeyId": "<key ARN>",
  "KeyRotationEnabled": <bool>,
  "NextRotationDate": <timestamp>,       // omitted when disabled
  "RotationPeriodInDays": <number>       // omitted when disabled
}
```

## Constraints

- Only valid for `SYMMETRIC_DEFAULT` keys → `UnsupportedOperationException` otherwise.
- Works for both `Enabled` and `Disabled` key states.
- `PendingDeletion` → `KMSInvalidStateException` (400).

## Errors

| Error | HTTP |
|-------|------|
| NotFoundException | 400 |
| KMSInvalidStateException | 400 |
| UnsupportedOperationException | 400 |
| InvalidArnException | 400 |
| KMSInternalException | 500 |

## kumolo notes

- Reads `rotation.json`; if the file does not exist, returns `KeyRotationEnabled=false` with no period/date.
- `NextRotationDate` and `RotationPeriodInDays` are only included in the response when rotation is enabled.
