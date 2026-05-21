# ListObjectVersions

Official URL: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListObjectVersions.html
SDK struct: `s3.ListObjectVersionsInput` / `s3.ListObjectVersionsOutput`
Last verified: 2026-05-21

## Request parameters

| Parameter | Type | Default | Notes |
|---|---|---|---|
| `delimiter` | string | — | Groups keys into `CommonPrefixes` |
| `key-marker` | string | — | Start listing after this key. Combined with `version-id-marker` for same-key cursor |
| `max-keys` | int | 1000 | Max entries (versions + delete markers) returned |
| `prefix` | string | — | Filter to keys starting with prefix |
| `version-id-marker` | string | — | Used with `key-marker`; start after this version ID within same key |

## Response fields

`ListVersionsResult`: `IsTruncated`, `KeyMarker`, `VersionIdMarker`, `NextKeyMarker`, `NextVersionIdMarker`, `Name`, `Prefix`, `Delimiter`, `MaxKeys`, `Version[]`, `DeleteMarker[]`, `CommonPrefixes[]`

## Sorting

Keys sorted ascending. Within same key, versions sorted by last-modified descending (newest first = IsLatest first).

## Pagination

- `IsTruncated=true` when cut off
- Next page: use `NextKeyMarker` as `key-marker` and `NextVersionIdMarker` as `version-id-marker`
- CommonPrefixes count as one result against `max-keys`

## Implemented parameters

- `max-keys` ✓
- `key-marker` ✓ (key-only; version-id-marker within same key not implemented)
- `prefix` ✓
- `delimiter` ✓

## kumolo deviations

- `version-id-marker` within the same key is ignored
- Versions and delete markers are returned in separate XML elements but interleaved ordering matches storage order

## Errors

- `NoSuchBucket` 404 — bucket does not exist
