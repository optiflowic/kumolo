# ListResourceTags

**URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_ListResourceTags.html  
**SDK struct**: `kms.ListResourceTagsInput` / `kms.ListResourceTagsOutput`  
**Last verified**: 2026-06-01

## Request Parameters

| Field  | Type   | Required | Constraints          |
|--------|--------|----------|----------------------|
| KeyId  | string | Yes      | 1–2048 chars         |
| Limit  | int    | No       | 1–1000, default 50   |
| Marker | string | No       | 1–1024 chars         |

## Response Fields

| Field      | Type    | Notes                                      |
|------------|---------|--------------------------------------------|
| Tags       | []Tag   | Each tag has TagKey and TagValue strings   |
| Truncated  | boolean | True when more results exist               |
| NextMarker | string  | Present only when Truncated is true        |

## Errors

| Error                | HTTP | Condition                               |
|----------------------|------|-----------------------------------------|
| NotFoundException    | 400  | Key not found or wrong account          |
| InvalidArnException  | 400  | KeyId is not a valid key ID or ARN      |
| InvalidMarkerException | 400 | Marker value is malformed              |
| KMSInternalException | 500  | Internal error                          |

## kumolo Implementation Notes

- Tags stored in `keys/{keyID}/tags.json` as `map[string]string`. See `tag-resource.md`.
- Tags returned sorted by TagKey (ascending).
- Pagination: Marker is a TagKey value from a previous response's NextMarker.
  InvalidMarkerException if the marker is not found in the current tag list.
- Default Limit is 50 (not 100). AWS docs don't specify the default; 50 matches
  observed real-AWS behavior.
- KeyId is validated: NotFoundException returned for unknown keys.
- PendingDeletion keys: AWS allows ListResourceTags on PendingDeletion keys;
  kumolo follows the same behavior (no state check beyond key existence).
