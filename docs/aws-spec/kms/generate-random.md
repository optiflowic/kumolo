# GenerateRandom

URL: https://docs.aws.amazon.com/kms/latest/APIReference/API_GenerateRandom.html
SDK struct: `kms.GenerateRandomInput` / `kms.GenerateRandomOutput`
Last verified: 2026-06-02

## Request Parameters

| Parameter       | Type    | Required | Constraints              | Notes                          |
|-----------------|---------|----------|--------------------------|--------------------------------|
| NumberOfBytes   | integer | Yes      | 1–1024                   | No default                     |
| CustomKeyStoreId| string  | No       | 1–64 chars               | Not supported in kumolo        |
| Recipient       | object  | No       | Nitro Enclave attestation| Not supported in kumolo        |

## Response Fields

| Field               | Notes                                              |
|---------------------|----------------------------------------------------|
| Plaintext           | Random bytes (base64 in HTTP/CLI)                  |
| CiphertextForRecipient | Only when Recipient is used; not returned in kumolo |

## Errors

| Error                        | HTTP | Condition                          |
|------------------------------|------|------------------------------------|
| ValidationException          | 400  | NumberOfBytes missing or out of range |
| UnsupportedOperationException| 400  | CustomKeyStoreId provided          |
| KMSInternalException         | 500  | Entropy read failure               |

## kumolo Deviations

- `CustomKeyStoreId` is not supported. If provided, returns `UnsupportedOperationException`. `Recipient` is silently ignored.
- Does not require a KMS key; entropy is sourced directly from `crypto/rand`.
