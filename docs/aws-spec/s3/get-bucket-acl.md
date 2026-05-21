# GetBucketAcl

**URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetBucketAcl.html  
**SDK struct**: `s3.GetBucketAclInput` / `s3.GetBucketAclOutput`  
**Last verified**: 2026-05-21

## Request

`GET /?acl HTTP/1.1`  
`Host: {bucket}.s3.amazonaws.com`

## Response

`HTTP/1.1 200`

Always returns a fixed stub ACL with a single `FULL_CONTROL` grant to a hardcoded `owner` principal:

```xml
<AccessControlPolicy xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
  <Owner><ID>owner</ID><DisplayName>owner</DisplayName></Owner>
  <AccessControlList>
    <Grant>
      <Grantee xmlns:xsi="..." xsi:type="CanonicalUser">
        <ID>owner</ID><DisplayName>owner</DisplayName>
      </Grantee>
      <Permission>FULL_CONTROL</Permission>
    </Grant>
  </AccessControlList>
</AccessControlPolicy>
```

## Errors

| Code | HTTP | Condition |
|---|---|---|
| `NoSuchBucket` | 404 | Bucket does not exist |

## Kumolo deviations

- Always returns a fixed stub ACL regardless of any ACL set on the bucket.
- Real per-bucket ACLs are not stored or enforced.
