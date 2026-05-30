# DescribeKey

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_DescribeKey.html
- **Target**: `TrentService.DescribeKey`
- **SDK input**: `kms.DescribeKeyInput`
- **SDK output**: `kms.DescribeKeyOutput`
- **Last verified**: 2026-05-30

## Request

| Field | Type | Required | Notes |
|---|---|---|---|
| `KeyId` | string | Yes | key ID, key ARN, alias name, or alias ARN |
| `GrantTokens` | []string | No | ignored by kumolo |

## KeyId resolution

kumolo accepts:
- Plain UUID key ID: `1234abcd-12ab-34cd-56ef-1234567890ab`
- Key ARN: `arn:aws:kms:<region>:<account>:key/<uuid>`

Alias names (`alias/foo`) and alias ARNs (`arn:aws:kms:...:alias/foo`) are supported after #256.

## Response

Returns `{"KeyMetadata": {...}}`. See create-key.md for field list.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NotFoundException` | 400 | key not found or alias not found |
| `InvalidArnException` | 400 | malformed ARN |
| `KMSInternalException` | 500 | storage failure |
