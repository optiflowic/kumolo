# GetKeyPolicy

- **URL**: https://docs.aws.amazon.com/kms/latest/APIReference/API_GetKeyPolicy.html
- **Target**: `TrentService.GetKeyPolicy`
- **SDK input**: `kms.GetKeyPolicyInput`
- **SDK output**: `kms.GetKeyPolicyOutput`
- **Last verified**: 2026-05-30

## Request

| Field | Type | Required | Notes |
|---|---|---|---|
| `KeyId` | string | Yes | key ID or ARN (see describe-key.md for resolution) |
| `PolicyName` | string | No | must be `"default"` if provided; default is `"default"` |

## Response

```json
{"Policy": "{...json string...}", "PolicyName": "default"}
```

## Default policy

When a key is created without a policy, kumolo stores this default:

```json
{
  "Version": "2012-10-17",
  "Id": "key-default-1",
  "Statement": [{
    "Sid": "Enable IAM User Permissions",
    "Effect": "Allow",
    "Principal": {"AWS": "arn:aws:iam::000000000000:root"},
    "Action": "kms:*",
    "Resource": "*"
  }]
}
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NotFoundException` | 400 | key not found |
| `InvalidArnException` | 400 | malformed ARN |
| `KMSInvalidStateException` | 400 | key not in a state that allows this operation |
| `KMSInternalException` | 500 | storage failure |
