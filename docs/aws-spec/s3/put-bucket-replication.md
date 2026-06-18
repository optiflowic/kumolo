# PutBucketReplication

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketReplication.html  
**SDK struct**: `s3.PutBucketReplicationInput` / `s3.PutBucketReplicationOutput`  
**Last verified**: 2026-06-08

## Request

`PUT /?replication HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `ReplicationConfiguration` XML document. Configuration is stored verbatim and applied on each subsequent object write.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Object replication behavior

After each successful `PutObject`, `CopyObject`, or `CompleteMultipartUpload`, kumolo evaluates all `Enabled` rules against the object key. For each matching rule:

- The object is copied to the destination bucket extracted from `Destination.Bucket` ARN (`arn:aws:s3:::bucket-name`). Replication runs in the same request goroutine after the HTTP response is flushed to the client.
- The destination copy receives `ReplicationStatus: REPLICA` in its metadata; this value is returned as `X-Amz-Replication-Status` on `GetObject` / `HeadObject`.
- The source object receives `ReplicationStatus: COMPLETED`.
- Objects already marked `REPLICA` are not re-replicated (prevents cascading loops).
- If replication to a destination fails, the failure is logged; the source write succeeds and the source object is not marked `COMPLETED` (its `X-Amz-Replication-Status` remains unset).
- Object tags are copied to the destination after the object body is replicated.

## Delete marker replication behavior

After each `DeleteObject` or `DeleteObjects` that creates a delete marker (versioning enabled), kumolo evaluates all `Enabled` rules whose `DeleteMarkerReplication.Status` is `Enabled`. For each matching rule:

- A delete marker is written to the destination bucket for the same key via `DeleteObjectVersioned`.
- The source delete marker is not marked with a replication status (AWS does not set `X-Amz-Replication-Status` on delete markers).
- Failures are logged and do not affect the response to the caller.
- `DeleteObject` with an explicit `versionId` permanently removes a version and never creates a delete marker, so it does not trigger delete marker replication.

## Kumolo deviations

- Replication runs in the same request goroutine (no background job); when `X-Amz-Replication-Status` is present it is always `COMPLETED` or `REPLICA` — never `PENDING` or `FAILED` as in real AWS async replication.
- Same-instance replication only; cross-instance / real-AWS destination is not supported.
- `Filter.Tag` and `Filter.And.Tags` are evaluated at replication time by reading the object's `.tags.json` sidecar; tags are loaded lazily only when a rule with a tag filter is encountered.
- Tag-filtered rules require `DeleteMarkerReplication.Status=Disabled` per AWS spec; this is enforced correctly by the existing DMR status check in `replicateDeleteMarker`.
