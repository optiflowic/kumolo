# KMS RotateKeyOnDemand — Implementation Contract

**Official URL:** https://docs.aws.amazon.com/kms/latest/APIReference/API_RotateKeyOnDemand.html  
**SDK input:** `kms.RotateKeyOnDemandInput`  
**SDK output:** `kms.RotateKeyOnDemandOutput`  
**Last verified:** 2026-06-05

## Request Parameters

| Parameter | Type   | Required | Notes                          |
|-----------|--------|----------|--------------------------------|
| KeyId     | String | Yes      | Key ID, ARN, or alias ref      |

## Response Fields

| Field | Type   | Notes                                |
|-------|--------|--------------------------------------|
| KeyId | String | ARN of the key that was rotated      |

## Implemented Errors

| Error                       | HTTP | Condition                                             |
|-----------------------------|------|-------------------------------------------------------|
| ValidationException         | 400  | Missing or invalid KeyId                              |
| NotFoundException           | 400  | Key does not exist                                    |
| DisabledException           | 400  | Key is not enabled                                    |
| KMSInvalidStateException    | 400  | Key is pending deletion                               |
| UnsupportedOperationException | 400 | Key is not SYMMETRIC_DEFAULT with ENCRYPT_DECRYPT     |
| LimitExceededException      | 400  | More than 25 on-demand rotations already recorded     |
| KMSInternalException        | 500  | Storage failure                                       |

## Behavior

- Rotates the key material immediately: generates 32 new AES-256 bytes + new `KeyMaterialId`
- Moves the current `material.json` to `keys/{id}/materials/{oldMaterialId}.json` so decryption of old ciphertexts continues to work
- Appends `{ KeyId, RotationDate, RotationType: "ON_DEMAND" }` to `rotation_history.json`
- Does not affect the automatic-rotation schedule (`rotation.json`)

## kumolo Deviations

- `ConflictException` (automatic rotation in progress) is not implemented; kumolo has no background rotation scheduler
- `InvalidArnException` is folded into `ValidationException` / `NotFoundException` via the standard key-ref resolution path
