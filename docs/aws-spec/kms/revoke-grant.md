# KMS RevokeGrant — Implementation Contract

**Official URL:** https://docs.aws.amazon.com/kms/latest/APIReference/API_RevokeGrant.html  
**SDK input:** `kms.RevokeGrantInput`  
**SDK output:** `kms.RevokeGrantOutput`  
**Last verified:** 2026-06-05

## Request Parameters

| Parameter | Type   | Required | Notes                     |
|-----------|--------|----------|---------------------------|
| KeyId     | String | Yes      | Key ID, ARN, or alias ref |
| GrantId   | String | Yes      | UUID of the grant to revoke |

## Response Fields

Empty body on success (HTTP 200).

## Implemented Errors

| Error                    | HTTP | Condition                               |
|--------------------------|------|-----------------------------------------|
| ValidationException      | 400  | Missing KeyId or GrantId                |
| NotFoundException        | 400  | Key or grant does not exist             |
| KMSInvalidStateException | 400  | Key is pending deletion                 |
| KMSInternalException     | 500  | Storage failure                         |

## Behavior

- Deletes `keys/{keyID}/grants/{grantID}.json`
- Returns `NotFoundException` if the grant file does not exist

## kumolo Deviations

- Caller identity is not checked (AWS enforces that only the key owner or a principal with kms:RevokeGrant permission can revoke)
- `DryRun` parameter is not implemented
