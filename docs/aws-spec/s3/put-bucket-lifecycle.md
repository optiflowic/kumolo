# PutBucketLifecycleConfiguration

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketLifecycleConfiguration.html  
**SDK struct**: `s3.PutBucketLifecycleConfigurationInput` / `s3.PutBucketLifecycleConfigurationOutput`  
**Last verified**: 2026-05-23

## Request

`PUT /?lifecycle HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `LifecycleConfiguration` XML document. Rules are parsed and stored; kumolo evaluates lifecycle rules for archival transitions and expiration.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Expiration semantics (implementation contract)

### `Expiration.Days`
Deletes objects (or places a delete marker in versioned buckets) where `LastModified < now − Days`.

### `Expiration.Date`
Once `now >= Date`, **all** qualified objects (matching filter/prefix) are expired — regardless of when each object was created. The action is continuous: objects added after the Date are also expired on subsequent enforcer runs. kumolo implements this by passing `now` as the cutoff to `enforceExpiration` so that every object with `LastModified < now` is deleted.

### `Expiration.ExpiredObjectDeleteMarker`
Removes a delete marker that is `IsLatest` with **no remaining non-marker versions** for the same key. Cannot be specified together with `Days` or `Date` in the same rule.

## NoncurrentVersionExpiration semantics (implementation contract)

Deletes noncurrent (non-latest) object versions and delete markers that became noncurrent more than `NoncurrentDays` ago. Uses `NoncurrentSince` if set; falls back to `LastModified`.

## NoncurrentVersionTransition semantics (implementation contract)

Updates the `StorageClass` metadata field on noncurrent versions that became noncurrent more than `NoncurrentDays` ago. No actual data movement occurs — the class is stored as metadata only. Versions already in the target class are skipped. Delete markers are not transitioned. `NewerNoncurrentVersions` is accepted in XML but not evaluated.

## Kumolo deviations

- Lifecycle rule evaluation runs on a background interval, not on every object access.
- `Expiration.Days`, `Expiration.Date`, `ExpiredObjectDeleteMarker`, `NoncurrentVersionExpiration`, and `NoncurrentVersionTransition` are evaluated.
- `NoncurrentVersionTransition` updates the `StorageClass` metadata field only; no actual data movement.
- Mutual-exclusivity of `ExpiredObjectDeleteMarker` with `Days`/`Date` is not validated on PutBucketLifecycle (AWS returns `InvalidArgument` for this).
