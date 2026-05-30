---
source: https://docs.aws.amazon.com/kms/latest/APIReference/API_CancelKeyDeletion.html
sdk_input: kms.CancelKeyDeletionInput
sdk_output: kms.CancelKeyDeletionOutput
last_verified: 2026-05-31
---

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| KeyId | string | Yes | Key ID or key ARN. Length 1–2048. |

## Response

```json
{
  "KeyId": "<key ARN>"
}
```

## State machine

| From state | Result |
|------------|--------|
| PendingDeletion | → Disabled (not Enabled) |
| Any other | KMSInvalidStateException (400) |

## Errors

| Error | HTTP |
|-------|------|
| NotFoundException | 400 |
| KMSInvalidStateException | 400 |
| InvalidArnException | 400 |
| KMSInternalException | 500 |

## kumolo notes

- Sets `KeyState = "Disabled"`, `Enabled = false`, clears `DeletionDate` from meta.json.
- Key rotation config (rotation.json) is preserved; if rotation was enabled before deletion was scheduled, it resumes after re-enabling.
