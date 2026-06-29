# Cognito Group Management Operations

Official URLs:
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_CreateGroup.html
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_DeleteGroup.html
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_GetGroup.html
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_UpdateGroup.html
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListGroups.html
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminAddUserToGroup.html
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminRemoveUserFromGroup.html
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_AdminListGroupsForUser.html
- https://docs.aws.amazon.com/cognito-user-identity-pools/latest/APIReference/API_ListUsersInGroup.html

SDK structs: `cognitoidentityprovider.CreateGroupInput`, `GroupType`, etc.

Last verified: 2026-06-28

## GroupType shape

```json
{
  "CreationDate":     number,   // Unix float
  "Description":      "string",
  "GroupName":        "string",
  "LastModifiedDate": number,
  "Precedence":       number,   // nullable; omit when not set
  "RoleArn":          "string", // nullable; omit when not set
  "UserPoolId":       "string"
}
```

## CreateGroup

- Required: `GroupName`, `UserPoolId`
- Optional: `Description` (max 2048), `Precedence` (≥0, max 2^31-1), `RoleArn`
- Response 200: `{"Group": GroupType}`
- Errors: `GroupExistsException` (400), `ResourceNotFoundException` (400), `InvalidParameterException` (400)

## DeleteGroup

- Required: `GroupName`, `UserPoolId`
- Response 200: empty body `{}`
- Errors: `ResourceNotFoundException` (400), `InvalidParameterException` (400)

## GetGroup

- Required: `GroupName`, `UserPoolId`
- Response 200: `{"Group": GroupType}`
- Errors: `ResourceNotFoundException` (400)

## UpdateGroup

- Required: `GroupName`, `UserPoolId`
- Optional: `Description`, `Precedence`, `RoleArn`
- Response 200: `{"Group": GroupType}`
- Errors: `ResourceNotFoundException` (400)

## ListGroups

- Required: `UserPoolId`
- Optional: `Limit` (0–60), `NextToken`
- Response 200: `{"Groups": [GroupType], "NextToken": "string"}`
- Pagination: cursor-based; `NextToken` omitted when no more pages

## AdminAddUserToGroup

- Required: `GroupName`, `Username`, `UserPoolId`
- Response 200: empty body `{}`
- Errors: `ResourceNotFoundException` (400, group or pool not found), `UserNotFoundException` (400)
- Idempotent: adding a user already in the group succeeds silently

## AdminRemoveUserFromGroup

- Required: `GroupName`, `Username`, `UserPoolId`
- Response 200: empty body `{}`
- Errors: `ResourceNotFoundException` (400), `UserNotFoundException` (400)
- Idempotent: removing a user not in the group succeeds silently

## AdminListGroupsForUser

- Required: `Username`, `UserPoolId`
- Optional: `Limit` (0–60), `NextToken`
- Response 200: `{"Groups": [GroupType], "NextToken": "string"}`
- Errors: `UserNotFoundException` (400), `ResourceNotFoundException` (400)

## ListUsersInGroup

- Required: `GroupName`, `UserPoolId`
- Optional: `Limit` (0–60), `NextToken`
- Response 200: `{"Users": [UserType], "NextToken": "string"}`
- Errors: `ResourceNotFoundException` (400)

## JWT claims

Both access and ID tokens must include `cognito:groups` — a list of group names the user belongs to.
When the user has no groups, the claim is omitted.

## kumolo storage layout

```text
pools/{poolID}/groups/{sha256(groupName)}.json          — GroupMetadata
pools/{poolID}/group_members/{sha256(groupName)}/{sha256(username)}.json  — membership marker
pools/{poolID}/user_groups/{sha256(username)}/{sha256(groupName)}.json    — reverse index for AdminListGroupsForUser
```

## kumolo deviations

- No IAM authorization check (kumolo skips all IAM/signature verification).
- `Precedence` uses `*int` in Go to distinguish "not set" from 0.
- Pagination tokens are opaque base64-encoded offsets (same scheme as ListUserPools).
