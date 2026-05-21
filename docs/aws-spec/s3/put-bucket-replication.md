# PutBucketReplication

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutBucketReplication.html  
**SDK struct**: `s3.PutBucketReplicationInput` / `s3.PutBucketReplicationOutput`  
**Last verified**: 2026-05-21

## Request

`PUT /?replication HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

Stores a `ReplicationConfiguration` XML document. Configuration is stored verbatim; replication is not actually performed.

## Response

`HTTP/1.1 200`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `MalformedXML` | 400 | Request body is not valid XML |
| `InternalError` | 500 | Storage failure |

## Kumolo deviations

- Replication configuration is stored for API compatibility but cross-bucket/cross-region replication is not performed.
