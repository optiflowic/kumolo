# DeleteBucketReplication

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_DeleteBucketReplication.html  
**SDK struct**: `s3.DeleteBucketReplicationInput` / `s3.DeleteBucketReplicationOutput`  
**Last verified**: 2026-05-21

## Request

`DELETE /?replication HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 204 No Content`

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |
| `InternalError` | 500 | Storage failure |
