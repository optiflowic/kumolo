# ListKeys

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_ListKeys.html
- **Target**: `TrentService.ListKeys`
- **SDK input**: `kms.ListKeysInput`
- **SDK output**: `kms.ListKeysOutput`
- **Last verified**: 2026-05-30

## Request

| Field | Type | Required | Notes |
|---|---|---|---|
| `Limit` | integer | No | 1–1000, default 100 |
| `Marker` | string | No | opaque pagination cursor from previous `NextMarker` |

## Response

```json
{
  "Keys": [{"KeyId": "...", "KeyArn": "..."}],
  "NextMarker": "...",
  "Truncated": false
}
```

`NextMarker` is present only when `Truncated == true`.

## Pagination

Keys are returned in alphabetical order by key ID. The `Marker` value is the key ID of the last item in the previous page. If the marker key no longer exists, the next page starts from the first key alphabetically after where the marker would have been.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `InvalidMarkerException` | 400 | Marker is not a valid UUID (8-4-4-4-12 hex format) |
| `KMSInternalException` | 500 | storage failure |

## kumolo deviations

None. Stale markers (valid UUID but key was deleted) advance silently.
