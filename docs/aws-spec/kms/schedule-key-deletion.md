---
source: https://docs.aws.amazon.com/kms/latest/APIReference/API_ScheduleKeyDeletion.html
sdk_input: kms.ScheduleKeyDeletionInput
sdk_output: kms.ScheduleKeyDeletionOutput
last_verified: 2026-05-31
---

# Schedule Key Deletion

## Request

| Field | Type | Required | Notes |
|-------|------|----------|-------|
| KeyId | string | Yes | Key ID or key ARN. Length 1–2048. |
| PendingWindowInDays | integer | No | 7–30. Default: 30. |

## Response

```json
{
  "DeletionDate": <timestamp>,
  "KeyId": "<key ARN>",
  "KeyState": "PendingDeletion",
  "PendingWindowInDays": <number>
}
```

## State machine

| From state | Result |
|------------|--------|
| Enabled | → PendingDeletion |
| Disabled | → PendingDeletion |
| PendingDeletion | KMSInvalidStateException (400) |

## Errors

| Error | HTTP |
|-------|------|
| NotFoundException | 400 |
| KMSInvalidStateException | 400 |
| InvalidArnException | 400 |
| KMSInternalException | 500 |

## kumolo notes

- Sets `KeyState = "PendingDeletion"`, `Enabled = false`.
- `DeletionDate = now + pendingWindowInDays * 86400` (Unix seconds), stored in meta.json.
- kumolo does NOT actually delete the key after the window; it only records the state.
- `DescribeKey` returns `DeletionDate` for PendingDeletion keys.
