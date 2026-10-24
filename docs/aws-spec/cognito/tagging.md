# Cognito Tagging — TagResource / UntagResource / ListTagsForResource

- TagResource: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_TagResource.html
- UntagResource: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UntagResource.html
- ListTagsForResource: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListTagsForResource.html
- Last verified: 2026-10-25

## Overview

All three operations act on a user pool identified by its ARN. Tags are key-value string pairs stored on the user pool.

- Max 50 tags per user pool.
- Key: 1–128 chars. Value: 0–256 chars.

## TagResource

- SDK: `cognitoidentityprovider.TagResourceInput` / `TagResourceOutput`
- Target: `AWSCognitoIdentityProviderService.TagResource`
- Request: `ResourceArn` (required, ARN pattern), `Tags` (required, map[string]string)
- Response: HTTP 200, empty body `{}`
- Errors: `ResourceNotFoundException` (404→400), `InvalidParameterException` (400; also when the merged tag set would exceed 50 tags), `InternalErrorException` (500)
- Behaviour: merges tags — existing keys are overwritten, unmentioned keys are preserved.
- Tag limit: checked against the merged result, so 51 new keys in one request and one new key
  on a pool with 50 tags both fail with `InvalidParameterException`
  ("The resultant tag set must not have more than 50 user tags."). Overwriting an existing key on
  a full pool succeeds. Verified on real AWS 2026-10-25.

## UntagResource

- SDK: `cognitoidentityprovider.UntagResourceInput` / `UntagResourceOutput`
- Target: `AWSCognitoIdentityProviderService.UntagResource`
- Request: `ResourceArn` (required), `TagKeys` (required, []string)
- Response: HTTP 200, empty body `{}`
- Errors: `ResourceNotFoundException`, `InvalidParameterException`, `InternalErrorException`
- Behaviour: removes only the listed keys; non-existent keys are silently ignored.

## ListTagsForResource

- SDK: `cognitoidentityprovider.ListTagsForResourceInput` / `ListTagsForResourceOutput`
- Target: `AWSCognitoIdentityProviderService.ListTagsForResource`
- Request: `ResourceArn` (required)
- Response: `{ "Tags": { "key": "value" } }` — empty map `{}` when no tags
- Errors: `ResourceNotFoundException`, `InvalidParameterException`, `InternalErrorException`
- No pagination.

## ARN lookup

ResourceArn must resolve to an existing user pool. kumolo derives pool ID from the ARN suffix and looks up the pool. Returns `ResourceNotFoundException` if the pool does not exist.

## kumolo deviations

- `ResourceArn` is validated against the model's ARN pattern and length (20–2048) before the
  handler runs (see `input_validation.md`). An ARN that passes but is not a
  `cognito-idp` `userpool/` ARN returns `InvalidParameterException`.
- `NotAuthorizedException` (IAM authorization) is not returned by any of the three operations:
  kumolo does not verify SigV4. `TooManyRequestsException` and `OperationNotEnabledException`
  are not returned either.
