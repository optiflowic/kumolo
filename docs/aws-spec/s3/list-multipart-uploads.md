# ListMultipartUploads

Official URL: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListMultipartUploads.html
SDK struct: `s3.ListMultipartUploadsInput` / `s3.ListMultipartUploadsOutput`
Last verified: 2026-05-21

## Request parameters

| Parameter | Type | Default | Notes |
|---|---|---|---|
| `delimiter` | string | — | Groups keys sharing common prefix up to first delimiter occurrence into `CommonPrefixes` |
| `key-marker` | string | — | Start listing after this key. If `upload-id-marker` is also set, entries with this key and upload ID > marker are included |
| `max-uploads` | int | 1000 | Max 1–1000 |
| `prefix` | string | — | Filter to keys starting with this prefix |
| `upload-id-marker` | string | — | Used with `key-marker`; includes uploads with same key if upload ID > this value |

## Response fields

`ListMultipartUploadsResult`: `Bucket`, `KeyMarker`, `UploadIdMarker`, `NextKeyMarker`, `NextUploadIdMarker`, `Prefix`, `Delimiter`, `MaxUploads`, `IsTruncated`, `Upload[]`, `CommonPrefixes[]`

## Sorting

Keys sorted ascending. For same key, uploads sorted ascending by initiation time.

## Pagination

- `IsTruncated=true` when results are cut off
- Next page: use `NextKeyMarker` as `key-marker` and `NextUploadIdMarker` as `upload-id-marker`
- `NextUploadIdMarker` is the upload ID of the last entry returned

## Implemented parameters

- `max-uploads` ✓
- `key-marker` ✓ (key-only; upload-id-marker within same key not implemented)
- `prefix` ✓
- `delimiter` ✓

## kumolo deviations

- `upload-id-marker` within the same key is ignored (same-key tiebreaking by upload ID not implemented)
- `CommonPrefixes` not included in upload count against `max-uploads` (matches real AWS behavior)

## Errors

- `NoSuchBucket` 404 — bucket does not exist
