# Cognito Tagging — TagResource / UntagResource / ListTagsForResource

- TagResource: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_TagResource.html
- UntagResource: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UntagResource.html
- ListTagsForResource: https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListTagsForResource.html
- Last verified: 2026-07-06

## Overview

All three operations act on a user pool identified by its ARN. Tags are key-value string pairs stored on the user pool.

- Max 50 tags per user pool.
- Key: 1–128 chars. Value: 0–256 chars.

## TagResource

- SDK: `cognitoidentityprovider.TagResourceInput` / `TagResourceOutput`
- Target: `AWSCognitoIdentityProviderService.TagResource`
- Request: `ResourceArn` (required, ARN pattern), `Tags` (required, map[string]string)
- Response: HTTP 200, empty body `{}`
- Errors: `ResourceNotFoundException` (404→400), `InvalidParameterException` (400), `NotAuthorizedException` (400), `InternalErrorException` (500)
- Behaviour: merges tags — existing keys are overwritten, unmentioned keys are preserved.

## UntagResource

- SDK: `cognitoidentityprovider.UntagResourceInput` / `UntagResourceOutput`
- Target: `AWSCognitoIdentityProviderService.UntagResource`
- Request: `ResourceArn` (required), `TagKeys` (required, []string)
- Response: HTTP 200, empty body `{}`
- Errors: same as TagResource
- Behaviour: removes only the listed keys; non-existent keys are silently ignored.

## ListTagsForResource

- SDK: `cognitoidentityprovider.ListTagsForResourceInput` / `ListTagsForResourceOutput`
- Target: `AWSCognitoIdentityProviderService.ListTagsForResource`
- Request: `ResourceArn` (required)
- Response: `{ "Tags": { "key": "value" } }` — empty map `{}` when no tags
- Errors: same as TagResource
- No pagination.

## ARN lookup

ResourceArn must resolve to an existing user pool. kumolo derives pool ID from the ARN suffix and looks up the pool. Returns `ResourceNotFoundException` if the pool does not exist.

## kumolo deviations

- 50-tag limit is not enforced (test ergonomics).
- ARN pattern is validated only for minimum/maximum length (20–2048); regex is not applied.
