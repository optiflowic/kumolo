# ListParts

Official URL: https://docs.aws.amazon.com/AmazonS3/latest/API/API_ListParts.html
SDK struct: `s3.ListPartsInput` / `s3.ListPartsOutput`
Last verified: 2026-05-21

## Request parameters

| Parameter | Type | Default | Notes |
|---|---|---|---|
| `max-parts` | int | 1000 | Max parts to return |
| `part-number-marker` | int | — | Return only parts with part number > this value |
| `uploadId` | string | — | Required; identifies the multipart upload |

## Response fields

`ListPartsResult`: `Bucket`, `Key`, `UploadId`, `PartNumberMarker`, `NextPartNumberMarker`, `MaxParts`, `IsTruncated`, `Part[]`, `StorageClass`, `Initiator`, `Owner`

## Pagination

- `IsTruncated=true` when more parts exist
- `NextPartNumberMarker` is the part number of the last part returned
- Next page: use `NextPartNumberMarker` as `part-number-marker`
- Parts are ordered by part number ascending

## Implemented parameters

- `max-parts` ✓
- `part-number-marker` ✓

## Errors

- `NoSuchUpload` 404 — upload ID does not exist
