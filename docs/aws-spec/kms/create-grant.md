# KMS CreateGrant — Implementation Contract

**Official URL:** https://docs.aws.amazon.com/kms/latest/APIReference/API_CreateGrant.html  
**SDK input:** `kms.CreateGrantInput`  
**SDK output:** `kms.CreateGrantOutput`  
**Last verified:** 2026-06-05

## Request Parameters

| Parameter          | Type     | Required | Notes                                                        |
|--------------------|----------|----------|--------------------------------------------------------------|
| KeyId              | String   | Yes      | Key ID, ARN, or alias ref                                    |
| GranteePrincipal   | String   | Yes      | IAM principal ARN receiving the grant                        |
| Operations         | []String | Yes      | List of allowed KMS operations (see valid operations below)  |
| RetiringPrincipal  | String   | No       | IAM principal ARN that can retire the grant                  |
| Constraints        | Object   | No       | EncryptionContextEquals or EncryptionContextSubset           |
| Name               | String   | No       | Friendly name; max 256 chars; pattern `^[a-zA-Z0-9:/_-]+$`  |
| GrantTokens        | []String | No       | Accepted but ignored (not used in kumolo)                    |

### Valid Operations

CreateGrant, Decrypt, DescribeKey, Encrypt, GenerateDataKey, GenerateDataKeyPair,
GenerateDataKeyPairWithoutPlaintext, GenerateDataKeyWithoutPlaintext, GenerateMac,
GetPublicKey, ReEncryptFrom, ReEncryptTo, RetireGrant, Sign, Verify, VerifyMac,
DeriveSharedSecret

## Response Fields

| Field      | Type   | Notes                                                              |
|------------|--------|--------------------------------------------------------------------|
| GrantId    | String | UUID identifying the grant                                         |
| GrantToken | String | Opaque string usable in cryptographic calls before grant propagates |

## Implemented Errors

| Error                    | HTTP | Condition                                        |
|--------------------------|------|--------------------------------------------------|
| ValidationException      | 400  | Missing required fields or invalid Operations    |
| NotFoundException        | 400  | Key does not exist                               |
| DisabledException        | 400  | Key is disabled                                  |
| KMSInvalidStateException | 400  | Key is pending deletion                          |
| KMSInternalException     | 500  | Storage failure                                  |

## Behavior

- Stores grant as `keys/{id}/grants/{grantID}.json`
- `GrantId` is a UUID v4; `GrantToken` is a separate UUID v4
- `IssuingAccount` is always `000000000000`
- `CreationDate` is Unix timestamp (float64)

## kumolo Deviations

- Access control (grant constraints evaluation, grantee enforcement) is not implemented
- `DryRun` parameter is not implemented
- `LimitExceededException` for exceeding the per-key grant quota is not implemented
- `InvalidArnException` on malformed principal ARNs is not validated (any non-empty string accepted)
