# KMS ListKeyRotations — Implementation Contract

**Official URL:** https://docs.aws.amazon.com/kms/latest/APIReference/API_ListKeyRotations.html  
**SDK input:** `kms.ListKeyRotationsInput`  
**SDK output:** `kms.ListKeyRotationsOutput`  
**Last verified:** 2026-06-05

## Request Parameters

| Parameter | Type    | Required | Notes                                               |
|-----------|---------|----------|-----------------------------------------------------|
| KeyId     | String  | Yes      | Key ID, ARN, or alias ref                           |
| Limit     | Integer | No       | 1–1000; default 100                                 |
| Marker    | String  | No       | Opaque cursor from previous response's NextMarker   |

## Response Fields

| Field      | Type    | Notes                                                    |
|------------|---------|----------------------------------------------------------|
| Rotations  | Array   | List of `RotationsListEntry`; see below                  |
| Truncated  | Boolean | True when more items remain                              |
| NextMarker | String  | Present and non-empty only when Truncated is true        |

### RotationsListEntry fields returned by kumolo

| Field        | Type   | Notes                              |
|--------------|--------|------------------------------------|
| KeyId        | String | ARN of the key                     |
| RotationDate | Number | Unix timestamp of the rotation     |
| RotationType | String | `AUTOMATIC` or `ON_DEMAND`         |

Fields related to imported key material (`ImportState`, `KeyMaterialId`, etc.) are not returned because kumolo does not support imported key material.

## Implemented Errors

| Error                       | HTTP | Condition                                         |
|-----------------------------|------|---------------------------------------------------|
| ValidationException         | 400  | Missing/invalid KeyId or Limit out of range       |
| NotFoundException           | 400  | Key does not exist                                |
| KMSInvalidStateException    | 400  | Key is pending deletion                           |
| UnsupportedOperationException | 400 | Key is not SYMMETRIC_DEFAULT                      |
| InvalidMarkerException      | 400  | Marker is not a valid cursor                      |
| KMSInternalException        | 500  | Storage failure                                   |

## Behavior

- Returns rotation records sorted oldest-first (insertion order in `rotation_history.json`)
- Pagination uses an opaque cursor; `NextMarker` from one page must be passed as `Marker` in the next
- Keys with no rotation history return `Rotations: []`, `Truncated: false`
