# KMS TagResource

**Source**: https://docs.aws.amazon.com/kms/latest/APIReference/API_TagResource.html  
**SDK struct**: `kms.TagResourceInput`  
**Last verified**: 2026-06-01

## Request

| Field  | Type            | Constraints                  | Required |
|--------|-----------------|------------------------------|----------|
| KeyId  | string          | 1–2048 chars; key ID or ARN  | Yes      |
| Tags   | []Tag           | non-empty                    | Yes      |

`Tag` object:
- `TagKey`: string (case-sensitive)
- `TagValue`: string (can be empty)

Adding a tag with a key that already exists overwrites the existing value.

## Response

HTTP 200, empty body.

## Errors

| Error                   | HTTP | Condition                                     |
|-------------------------|------|-----------------------------------------------|
| ValidationException     | 400  | Missing/invalid field                         |
| InvalidArnException     | 400  | Invalid key ARN                               |
| NotFoundException       | 400  | Key not found                                 |
| KMSInvalidStateException| 400  | Key is PendingDeletion                        |
| LimitExceededException  | 400  | Tag quota exceeded (50 tags per key)          |
| TagException            | 400  | Invalid tag key/value (e.g. reserved prefix)  |
| KMSInternalException    | 500  | Internal error                                |

## kumolo notes

- Tags stored as `keys/{keyID}/tags.json` (map[string]string, key→value).
- Tag key constraints: 1–128 chars; tag value constraints: 0–256 chars.
- Limit: 50 tags per key (AWS quota).
- Key state: Enabled or Disabled are valid; PendingDeletion → KMSInvalidStateException.
- AWS-managed keys cannot be tagged; kumolo only creates CUSTOMER keys so this check is not needed.
