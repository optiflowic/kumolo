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

- The object is copied (synchronously) to the destination bucket extracted from `Destination.Bucket` ARN (`arn:aws:s3:::bucket-name`).
- The destination copy receives `ReplicationStatus: REPLICA` in its metadata; this value is returned as `X-Amz-Replication-Status` on `GetObject` / `HeadObject`.
- The source object receives `ReplicationStatus: COMPLETED`.
- Objects already marked `REPLICA` are not re-replicated (prevents cascading loops).
- If replication to a destination fails, the failure is logged; the source write succeeds and the source object is not marked `COMPLETED` (its `X-Amz-Replication-Status` remains unset).
- Object tags are copied to the destination after the object body is replicated.

## Kumolo deviations

- Replication is synchronous (real AWS is asynchronous); the status is always `COMPLETED` or `REPLICA` — never `PENDING` or `FAILED`.
- Same-instance replication only; cross-instance / real-AWS destination is not supported.
- Delete marker replication is not implemented.
- Tag-based filter rules (`Filter/Tag`, `Filter/And/Tag`) are ignored; only key prefix matching is applied.
