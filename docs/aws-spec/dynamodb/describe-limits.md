# DynamoDB — DescribeLimits / DescribeEndpoints

- Official URLs:
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeLimits.html
  - https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_DescribeEndpoints.html
- SDK structs:
  - `dynamodb.DescribeLimitsInput` / `dynamodb.DescribeLimitsOutput`
  - `dynamodb.DescribeEndpointsInput` / `dynamodb.DescribeEndpointsOutput`
- Last verified: 2026-05-21

## DescribeLimits

### Request Parameters

None (empty body `{}`).

### Response

| Field | Type | kumolo value |
|---|---|---|
| `AccountMaxReadCapacityUnits` | long | 80000 |
| `AccountMaxWriteCapacityUnits` | long | 80000 |
| `TableMaxReadCapacityUnits` | long | 40000 |
| `TableMaxWriteCapacityUnits` | long | 40000 |

These are hardcoded fixed values; real AWS returns actual account quota (defaults: AccountMax 20000, TableMax 10000 — may differ per account/region).

### Errors

| Error | HTTP | Condition |
|---|---|---|
| `InternalServerError` | 500 | Server-side error |

## DescribeEndpoints

### Request Parameters

None (no body required).

### Response

| Field | Notes |
|---|---|
| `Endpoints` | `[{Address: "localhost:5566", CachePeriodInMinutes: 1440}]` |

Returns kumolo's own address for SDK endpoint discovery.

### Errors

None documented beyond common errors.

## kumolo-Specific Deviations

- `DescribeLimits`: returns fixed hardcoded values; no real quota enforcement.
- `DescribeEndpoints`: always returns `localhost:5566`; does not dynamically reflect the configured listen address.
