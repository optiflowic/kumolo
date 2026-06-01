# KMS UntagResource

**Source**: https://docs.aws.amazon.com/kms/latest/APIReference/API_UntagResource.html  
**SDK struct**: `kms.UntagResourceInput`  
**Last verified**: 2026-06-01

## Request

| Field   | Type     | Constraints                             | Required |
|---------|----------|-----------------------------------------|----------|
| KeyId   | string   | 1–2048 chars; key ID or ARN             | Yes      |
| TagKeys | []string | non-empty; each key: 1–128 chars        | Yes      |

## Response

HTTP 200, empty body.

## Errors

| Error                   | HTTP | Condition                             |
|-------------------------|------|---------------------------------------|
| ValidationException     | 400  | Missing/invalid field                 |
| InvalidArnException     | 400  | Invalid key ARN                       |
| NotFoundException       | 400  | Key not found                         |
| KMSInvalidStateException| 400  | Key is PendingDeletion                |
| TagException            | 400  | Invalid tag key                       |
| KMSInternalException    | 500  | Internal error                        |

## kumolo notes

- Removing a tag key that does not exist on the key is silently ignored (no error).
- Key state: Enabled or Disabled are valid; PendingDeletion → KMSInvalidStateException.
- Tag key constraints: 1–128 chars.
