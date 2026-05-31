# ListAliases

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_ListAliases.html
- **Target**: `TrentService.ListAliases`
- **SDK input**: `kms.ListAliasesInput`
- **SDK output**: `kms.ListAliasesOutput`
- **Last verified**: 2026-05-30

## Request

| Field | Type | Required | Notes |
|---|---|---|---|
| `KeyId` | string | No | filter aliases to this key; accepts key ID or key ARN |
| `Limit` | int | No | 1–100, default 50 |
| `Marker` | string | No | opaque pagination cursor from `NextMarker` |

## Response

```json
{
  "Aliases": [
    {
      "AliasName": "alias/foo",
      "AliasArn": "arn:aws:kms:us-east-1:000000000000:alias/foo",
      "TargetKeyId": "<uuid>",
      "CreationDate": 1234567890.0,
      "LastUpdatedDate": 1234567890.0
    }
  ],
  "Truncated": false,
  "NextMarker": "<opaque>"
}
```

## AliasListEntry fields

| Field | Type | Notes |
|---|---|---|
| `AliasName` | string | e.g. `alias/foo` |
| `AliasArn` | string | `arn:aws:kms:<region>:<account>:<aliasName>` |
| `TargetKeyId` | string | plain key UUID |
| `CreationDate` | float64 | Unix timestamp |
| `LastUpdatedDate` | float64 | Unix timestamp; equals CreationDate on first create |

## Behavior

- Without `KeyId`, returns all aliases sorted by AliasName.
- With `KeyId`, returns only aliases pointing to that key.
- Pagination: aliases are sorted by `AliasName`; `Marker` is the last alias name seen.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `InvalidArnException` | 400 | malformed KeyId ARN |
| `NotFoundException` | 400 | KeyId filter key not found |
| `InvalidMarkerException` | 400 | Marker does not start with `alias/` |
| `KMSInternalException` | 500 | storage failure |

## kumolo deviations

None. Stale markers (valid `alias/` prefix but alias was deleted) advance silently.
