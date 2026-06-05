# KMS ListGrants — Implementation Contract

**Official URL:** https://docs.aws.amazon.com/kms/latest/APIReference/API_ListGrants.html  
**SDK input:** `kms.ListGrantsInput`  
**SDK output:** `kms.ListGrantsResponse`  
**Last verified:** 2026-06-05

## Request Parameters

| Parameter         | Type   | Required | Notes                                              |
|-------------------|--------|----------|----------------------------------------------------|
| KeyId             | String | Yes      | Key ID, ARN, or alias ref                          |
| GrantId           | String | No       | Filter by exact grant ID                           |
| GranteePrincipal  | String | No       | Filter by exact grantee principal ARN              |
| Limit             | Int    | No       | 1–100; default 50                                  |
| Marker            | String | No       | Opaque pagination token from previous NextMarker   |

## Response Fields

| Field      | Type     | Notes                                              |
|------------|----------|----------------------------------------------------|
| Grants     | []Grant  | Sorted by GrantId; each entry is a GrantListEntry  |
| Truncated  | Bool     | True when more results exist                       |
| NextMarker | String   | Present when Truncated is true; last GrantId seen  |

### Grant entry fields

`GrantId`, `GrantToken`, `KeyId` (ARN), `GranteePrincipal`, `RetiringPrincipal`, `Operations`,
`Constraints`, `Name`, `IssuingAccount`, `CreationDate`

## Implemented Errors

| Error                    | HTTP | Condition                        |
|--------------------------|------|----------------------------------|
| ValidationException      | 400  | Missing KeyId or invalid Limit   |
| InvalidMarkerException   | 400  | Marker is not a valid grant ID   |
| NotFoundException        | 400  | Key does not exist               |
| KMSInvalidStateException | 400  | Key is pending deletion          |
| KMSInternalException     | 500  | Storage failure                  |

## Behavior

- Lists grants associated with the key, sorted by GrantId
- Optional filters `GrantId` and `GranteePrincipal` are applied after loading all grants for the key
- Marker is the last GrantId returned; stale markers advance via binary search (silently)

## kumolo Deviations

- `InvalidArnException` on malformed KeyId ARN is folded into `NotFoundException` via the standard key-ref path
