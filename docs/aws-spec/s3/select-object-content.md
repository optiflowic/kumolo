# SelectObjectContent

- **URL**: https://docs.aws.amazon.com/AmazonS3/latest/API/API_SelectObjectContent.html
- **Event stream appendix**: https://docs.aws.amazon.com/AmazonS3/latest/API/RESTSelectObjectAppendix.html
- **SDK type**: `s3.SelectObjectContentInput` / `s3.SelectObjectContentOutput`
- **Last verified**: 2026-06-09

## Request

```
POST /{Key+}?select&select-type=2 HTTP/1.1
Host: {Bucket}.s3.amazonaws.com
```

Body: `SelectObjectContentRequest` XML

Required fields: `Expression`, `ExpressionType` (must be `"SQL"`), `InputSerialization`, `OutputSerialization`.

### InputSerialization

- `CompressionType`: `NONE` (default), `GZIP`, `BZIP2`
- `CSV`: optional CSV-specific settings
  - `FileHeaderInfo`: `NONE` (default) / `IGNORE` / `USE`
  - `RecordDelimiter`: default `\n`
  - `FieldDelimiter`: default `,`
  - `QuoteCharacter`: default `"`
  - `QuoteEscapeCharacter`: default `"` (doubled-quote escape)
  - `Comments`: comment line prefix character
  - `AllowQuotedRecordDelimiter`: bool
- `JSON`:
  - `Type`: `LINES` or `DOCUMENT`
- `Parquet`: empty element (kumolo: not supported in v1)

### OutputSerialization

- `CSV`:
  - `RecordDelimiter`: default `\n`
  - `FieldDelimiter`: default `,`
  - `QuoteCharacter`: default `"`
  - `QuoteEscapeCharacter`: default `"`
  - `QuoteFields`: `ALWAYS` or `ASNEEDED` (default)
- `JSON`:
  - `RecordDelimiter`: default `\n`

## Response

HTTP 200, `Transfer-Encoding: chunked`.  
Body is a series of binary event stream messages (see below).

## Event Stream Message Format

Each message:

```
[total_length: 4B big-endian]
[headers_length: 4B big-endian]
[prelude_crc: 4B CRC32/IEEE of prelude (8B)]
[headers: variable]
[payload: variable]
[message_crc: 4B CRC32/IEEE of entire message except itself]
```

Total message overhead: 16 bytes.

### Header encoding

Each header: `[name_len: 1B][name: name_len bytes][type: 1B = 7 (string)][value_len: 2B big-endian][value: value_len bytes UTF-8]`

### Event types (`:event-type` header value)

| Event       | `:message-type` | `:event-type`  | `:content-type`               | Payload                           |
|-------------|-----------------|----------------|-------------------------------|-----------------------------------|
| Records     | event           | Records        | application/octet-stream      | raw record bytes                  |
| Stats       | event           | Stats          | text/xml                      | `<Stats>…</Stats>` XML            |
| Progress    | event           | Progress       | text/xml                      | `<Progress>…</Progress>` XML      |
| Cont        | event           | Cont           | (no content-type header)      | empty                             |
| End         | event           | End            | (no content-type header)      | empty                             |
| Error       | error           | (none)         | (none)                        | empty; `:error-code`, `:error-message` headers |

## SQL

- `ExpressionType` must be `SQL`; other values → `InvalidExpressionType` (400)
- FROM must be `S3Object` (case-insensitive) or an alias thereof
- Positional columns in CSV: `_1`, `_2`, … (1-indexed)
- Named columns: require `FileHeaderInfo=USE` for CSV; or JSON key names
- Aggregate: `COUNT(*)`

## Errors

| Code                        | HTTP | Condition                                          |
|-----------------------------|------|----------------------------------------------------|
| `NoSuchBucket`              | 404  | Bucket does not exist                              |
| `NoSuchKey`                 | 404  | Object does not exist                              |
| `InvalidExpressionType`     | 400  | ExpressionType is not SQL                          |
| `MissingRequiredParameter`  | 400  | Expression, ExpressionType, or serialization absent|
| `InvalidDataType`           | 400  | Type conversion failure during evaluation          |
| `ParseUnexpectedToken`      | 400  | SQL syntax error                                   |
| `InternalError`             | 500  | Unexpected server error                            |

## kumolo Deviations

- Parquet input not supported; returns `NotImplemented` error event if Parquet is requested
- BZIP2 decompression supported via `compress/bzip2`
- Progress events not sent (RequestProgress is accepted but ignored)
- ScanRange accepted but ignored in v1
- `ExpressionType` validation is case-sensitive per spec (`SQL`)
- CSV `QuoteCharacter` other than `"` (double-quote) is silently ignored; `encoding/csv` only supports `"` as the quote character
