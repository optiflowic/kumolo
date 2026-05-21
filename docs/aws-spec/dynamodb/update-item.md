# DynamoDB — UpdateItem

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/APIReference/API_UpdateItem.html
- SDK struct: `dynamodb.UpdateItemInput` / `dynamodb.UpdateItemOutput`
- Last verified: 2026-05-21

## Request Parameters (implemented)

| Parameter | Type | Required | Notes |
|---|---|---|---|
| `TableName` | string | yes | ARN form also accepted by real AWS; kumolo accepts table name only |
| `Key` | map | yes | Partition key (+ sort key if composite) |
| `UpdateExpression` | string | no | SET / REMOVE / ADD / DELETE clauses |
| `ConditionExpression` | string | no | Condition that must hold for update to proceed |
| `ExpressionAttributeNames` | map | no | `#name` → actual attribute name |
| `ExpressionAttributeValues` | map | no | `:ref` → typed DynamoDB value |
| `ReturnValues` | string | no | `NONE`(default) / `ALL_OLD` / `UPDATED_OLD` / `ALL_NEW` / `UPDATED_NEW` |

## UpdateExpression Clauses

### SET
Adds or replaces attributes. Supports nested document paths (see `expressions-document-path.md`).

Supported operand forms:
- `:valRef` — literal value from ExpressionAttributeValues
- `attrRef` — reads the current attribute value (for arithmetic or copy)
- `if_not_exists(path, operand)` — uses `operand` when `path` is absent, otherwise keeps existing value
- `list_append(left, right)` — concatenates two List values

Nested SET: `SET meta.count = :v` requires the parent attribute `meta` to exist and be a Map.
If the top-level parent is absent → `ValidationException` ("the document path provided in the
update expression is invalid for update").

List index SET: `SET tags[2] = :v`
- Index within bounds → replaces element in place.
- Index ≥ current list length → appends to end (does not pad with nulls).

### REMOVE
Removes attributes or list elements. Supports nested document paths.

- Missing top-level attribute → no-op (AWS: "If the attributes don't exist, nothing happens").
- Missing intermediate node → no-op.
- List element REMOVE: element is deleted and remaining elements shift left (no holes left).

### ADD
- Number attribute: adds the given value (creates attribute with `0 + value` if absent).
- Set attribute: union of existing set and new set (same element type required).
- Not valid for other types.

### DELETE
- Set attribute: removes specified elements from the set.
- Not valid for other types; empty set → error.

## Implemented Errors

| Error | HTTP | Condition |
|---|---|---|
| `ValidationException` | 400 | Invalid expression, missing ExpressionAttributeNames/Values, invalid document path |
| `ConditionalCheckFailedException` | 400 | ConditionExpression evaluated to false |
| `ResourceNotFoundException` | 400 | Table does not exist |

## kumolo-Specific Deviations

- `ReturnConsumedCapacity`, `ReturnItemCollectionMetrics`, `ReturnValuesOnConditionCheckFailure` are accepted but ignored.
- ARN form of TableName is not supported.
- Legacy parameters (`AttributeUpdates`, `Expected`, `ConditionalOperator`) are not implemented.
