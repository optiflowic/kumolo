# KMS ListRetirableGrants — Implementation Contract

**Official URL:** https://docs.aws.amazon.com/kms/latest/APIReference/API_ListRetirableGrants.html  
**SDK input:** `kms.ListRetirableGrantsInput`  
**SDK output:** `kms.ListRetirableGrantsOutput`  
**Last verified:** 2026-06-05

## Request Parameters

| Parameter         | Type   | Required | Notes                                             |
|-------------------|--------|----------|---------------------------------------------------|
| RetiringPrincipal | String | Yes      | Filters grants by exact RetiringPrincipal match   |
| Limit             | Int    | No       | 1–100; default 50                                 |
| Marker            | String | No       | Opaque pagination token from previous NextMarker  |

## Response Fields

| Field      | Type     | Notes                                              |
|------------|----------|----------------------------------------------------|
| Grants     | []Grant  | Matching grants across all keys, sorted by GrantId |
| Truncated  | Bool     | True when more results exist                       |
| NextMarker | String   | Present when Truncated is true; last GrantId seen  |

## Implemented Errors

| Error                 | HTTP | Condition                                |
|-----------------------|------|------------------------------------------|
| ValidationException   | 400  | Missing RetiringPrincipal or invalid Limit |
| KMSInternalException  | 500  | Storage failure                          |

Note: `InvalidMarkerException` is **not** returned — stale markers are silently advanced via binary search.

## Behavior

- Scans all key directories and collects grants where `RetiringPrincipal` matches exactly
- Results are sorted by GrantId for stable pagination
- Marker is the last GrantId returned; stale markers advance via binary search (silently)

## kumolo Deviations

- Keys in PendingDeletion state still have their grants included (AWS may exclude them; not verified)
