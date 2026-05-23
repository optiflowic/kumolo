# DynamoDB ConsumedCapacity

**Official URL**: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_ConsumedCapacity.html  
**Last verified**: 2026-05-23

## ReturnConsumedCapacity enum

Accepted on all data-plane operations (`PutItem`, `GetItem`, `DeleteItem`, `UpdateItem`,
`Query`, `Scan`, `BatchGetItem`, `BatchWriteItem`, `TransactGetItems`, `TransactWriteItems`).

| Value | Behaviour |
|-------|-----------|
| `NONE` (default) | No `ConsumedCapacity` field in response |
| `TOTAL` | Top-level `CapacityUnits` only; no per-index breakdown |
| `INDEXES` | `CapacityUnits` + `Table` breakdown; `GlobalSecondaryIndexes` / `LocalSecondaryIndexes` maps when indexes were accessed |

## ConsumedCapacity object fields

| Field | Type | When present |
|-------|------|--------------|
| `TableName` | string | Always |
| `CapacityUnits` | float64 | Always (aggregate) |
| `Table` | Capacity | INDEXES mode only |
| `GlobalSecondaryIndexes` | map[string]Capacity | INDEXES mode, when a GSI was accessed |
| `LocalSecondaryIndexes` | map[string]Capacity | INDEXES mode, when an LSI was accessed |
| `ReadCapacityUnits` | float64 | Optional sub-field |
| `WriteCapacityUnits` | float64 | Optional sub-field |

**Capacity** sub-object: `{ "CapacityUnits": float64 }`

## Response shape by operation type

- **Single-item ops** (`PutItem`, `GetItem`, `DeleteItem`, `UpdateItem`): `ConsumedCapacity` is a single object.
- **Batch/transact ops** (`BatchGetItem`, `BatchWriteItem`, `TransactGetItems`, `TransactWriteItems`): `ConsumedCapacity` is an array — one element per table involved.

## Errors

| Error | HTTP | Condition |
|-------|------|-----------|
| `ValidationException` | 400 | `ReturnConsumedCapacity` value is not one of `INDEXES`, `TOTAL`, `NONE` |

## kumolo deviations

- Exact RCU/WCU calculation is not implemented. kumolo returns `1.0` for `CapacityUnits` on every
  operation and every table/index, regardless of item size or provisioned throughput.
- `ReadCapacityUnits` / `WriteCapacityUnits` sub-fields are omitted.
- `GlobalSecondaryIndexes` / `LocalSecondaryIndexes` breakdowns are omitted even in INDEXES mode.
  The `Table` sub-object is included with `CapacityUnits: 1.0`.

## SDK struct names

- `types.ConsumedCapacity` / `types.ReturnConsumedCapacity`
