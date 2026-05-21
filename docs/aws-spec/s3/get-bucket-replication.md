# GetBucketReplication

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketReplication.html  
**SDK struct**: `s3.GetBucketReplicationInput` / `s3.GetBucketReplicationOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?replication HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Returns the stored `ReplicationConfiguration` XML.

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `ReplicationConfigurationNotFoundError` | 404 | No replication configuration is set |
| `InternalError` | 500 | Storage failure |
