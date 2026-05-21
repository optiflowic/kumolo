# DynamoDB Expression — Document Path Syntax

- Official URL: https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/Expressions.Attributes.html
- Operators & Functions: https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/Expressions.OperatorsAndFunctions.html
- Last verified: 2026-05-21

## Scope

Cross-cutting syntax used in FilterExpression, ConditionExpression, UpdateExpression, and
ProjectionExpression. This memo covers document path parsing and the functions that accept
a `path` argument.

## Document Path Rules

A document path identifies an attribute (possibly nested) within a DynamoDB item.

- Top-level: bare attribute name, e.g. `Title`
- Map child: dot separator, e.g. `Pictures.FrontView`
- List element: `[N]` where N ≥ 0, e.g. `RelatedItems[2]`
- Mixed / deeply nested: `ProductReviews.FiveStar[0]`, `a[0][1].b`
- Maximum dereference depth: 32 operators total (dots + brackets combined)
- Negative or fractional indexes are invalid: `MyList[-1]`, `MyList[0.4]` → error

### Attribute Name Requirements

Bare names must start with `a-z`, `A-Z`, or `0-9`. If the name contains reserved words,
special characters, or dots (e.g., `Safety.Warning`), use an expression attribute name
(`#nameRef`) as a placeholder.

`#nameRef` tokens can appear at any segment of a path: `#addr.#city`, `a.#b[0].c`.

## Condition / Filter Expression Functions

All path-argument functions accept a document path (including nested paths).
They do **not** accept value references (`:val`) as their path argument —
parse-time validation rejects that.

### attribute_exists(path)
Returns true when the item contains the attribute at `path`. For nested paths,
returns false (not an error) if any intermediate node is absent or is not a map/list.

### attribute_not_exists(path)
Complement of `attribute_exists`. Missing intermediate nodes → true (attribute considered absent).

### attribute_type(path, :type)
Checks the DynamoDB type string of the attribute at `path`. `:type` must be a value ref
holding one of: `S`, `SS`, `N`, `NS`, `B`, `BS`, `BOOL`, `NULL`, `L`, `M`.

### begins_with(path, :substr)
True if the String attribute at `path` starts with the substring `:substr`.

### contains(path, :operand)
- String attribute: true if the string contains `:operand` as a substring.
- Set attribute: true if the set contains `:operand` as an element.
- `path` and operand must refer to distinct values.

### size(path)
Returns the size of the attribute:
- String → character count
- Binary → byte count
- Set, List, Map → number of elements

## kumolo Implementation Notes

- `pathOperand` in `expression.go` handles multi-segment paths for filter/condition expressions.
  For single-segment paths, `nameRefOperand` / `plainOperand` are used as before.
- `parseAttrPath()` builds the segment list from the token stream (token-based parser).
- `parseUpdatePath()` in `handler_helpers.go` builds segments for UpdateExpression paths (string-split approach, since the path string is already pre-tokenized by the UpdateExpression clause parser).
- Both parsers enforce the 32-dereference-operator limit; error message: "Nesting Levels have exceeded supported limits".
- `attribute_exists` / `attribute_not_exists` use `resolve()` for all operand types;
  a nil return from `resolve()` is treated as "absent" (safe because DynamoDB typed values
  are never nil in kumolo storage).
- Missing intermediate nodes during `resolve()` always return nil (no error) — consistent
  with AWS: traversal into a scalar or absent key returns "not found", not an error.
