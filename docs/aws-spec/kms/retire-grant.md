# KMS RetireGrant — Implementation Contract

**Official URL:** https://docs.aws.amazon.com/kms/latest/APIReference/API_RetireGrant.html  
**SDK input:** `kms.RetireGrantInput`  
**SDK output:** `kms.RetireGrantOutput`  
**Last verified:** 2026-06-05

## Request Parameters

| Parameter  | Type   | Required | Notes                                                                |
|------------|--------|----------|----------------------------------------------------------------------|
| GrantToken | String | No*      | Opaque token from CreateGrant; used alone to identify the grant      |
| KeyId      | String | No*      | Key ID, ARN, or alias ref; required when using GrantId               |
| GrantId    | String | No*      | UUID of the grant; requires KeyId to be present                      |

*Exactly one form must be provided: `GrantToken` alone, or `KeyId` + `GrantId` together.

## Response Fields

Empty body on success (HTTP 200).

## Implemented Errors

| Error                    | HTTP | Condition                                           |
|--------------------------|------|-----------------------------------------------------|
| ValidationException      | 400  | Neither form provided, or GrantId without KeyId     |
| NotFoundException        | 400  | Grant (by token or ID) or key does not exist        |
| KMSInvalidStateException | 400  | Key is pending deletion (ID-based form only)        |
| KMSInternalException     | 500  | Storage failure                                     |

## Behavior

- **Token-based**: scans all keys' grant directories to find the grant with matching `GrantToken`; deletes that grant file
- **ID-based**: resolves the key ref, then deletes `keys/{keyID}/grants/{grantID}.json`

## kumolo Deviations

- Caller identity is not validated (AWS enforces that only the `RetiringPrincipal` or grantee can retire)
- `DryRun` parameter is not implemented
