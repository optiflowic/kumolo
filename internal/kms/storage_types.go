package kms

import (
	"fmt"
	"strings"
)

const (
	fixedAccount = "000000000000"
	fixedRegion  = "us-east-1"

	defaultPolicy = `{"Version":"2012-10-17","Id":"key-default-1","Statement":[{"Sid":"Enable IAM User Permissions","Effect":"Allow","Principal":{"AWS":"arn:aws:iam::000000000000:root"},"Action":"kms:*","Resource":"*"}]}`
)

// KeyMetadata is the stored and returned metadata for a KMS key.
type KeyMetadata struct {
	KeyID                  string   `json:"KeyId"`
	Arn                    string   `json:"Arn"`
	AWSAccountID           string   `json:"AWSAccountId"`
	Description            string   `json:"Description"`
	KeySpec                string   `json:"KeySpec"`
	CustomerMasterKeySpec  string   `json:"CustomerMasterKeySpec"`
	KeyUsage               string   `json:"KeyUsage"`
	KeyState               string   `json:"KeyState"`
	Enabled                bool     `json:"Enabled"`
	KeyManager             string   `json:"KeyManager"`
	Origin                 string   `json:"Origin"`
	MultiRegion            bool     `json:"MultiRegion"`
	CreationDate           float64  `json:"CreationDate"`
	DeletionDate           *float64 `json:"DeletionDate,omitempty"`
	EncryptionAlgorithms   []string `json:"EncryptionAlgorithms,omitempty"`
	SigningAlgorithms      []string `json:"SigningAlgorithms,omitempty"`
	KeyAgreementAlgorithms []string `json:"KeyAgreementAlgorithms,omitempty"`
	MacAlgorithms          []string `json:"MacAlgorithms,omitempty"`
}

// KeyRotationConfig holds automatic rotation settings for a KMS key.
// Stored in keys/{id}/rotation.json; omitted when rotation has never been configured.
type KeyRotationConfig struct {
	Enabled              bool    `json:"Enabled"`
	RotationPeriodInDays int     `json:"RotationPeriodInDays,omitempty"`
	NextRotationDate     float64 `json:"NextRotationDate,omitempty"`
}

func keyARN(keyID string) string {
	return fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", fixedRegion, fixedAccount, keyID)
}

func aliasARN(aliasName string) string {
	return fmt.Sprintf("arn:aws:kms:%s:%s:%s", fixedRegion, fixedAccount, aliasName)
}

// normalizeAliasRef extracts the canonical alias name from an alias name or alias ARN.
// "alias/foo" → ("alias/foo", true)
// "arn:aws:kms:...:alias/foo" → ("alias/foo", true)
// anything else → ("", false)
func normalizeAliasRef(ref string) (string, bool) {
	if strings.HasPrefix(ref, "alias/") {
		return ref, true
	}
	if strings.HasPrefix(ref, "arn:") {
		parts := strings.SplitN(ref, ":alias/", 2)
		if len(parts) == 2 && parts[1] != "" {
			return "alias/" + parts[1], true
		}
		return "", false
	}
	return "", false // unreachable: only called via resolveKeyRef after isAliasRef confirms "alias/" or "arn:" prefix
}

// AliasEntry is the stored and returned metadata for a KMS alias.
type AliasEntry struct {
	AliasName       string  `json:"AliasName"`
	AliasArn        string  `json:"AliasArn"`
	TargetKeyId     string  `json:"TargetKeyId"`
	CreationDate    float64 `json:"CreationDate"`
	LastUpdatedDate float64 `json:"LastUpdatedDate"`
}

// resolveKeyID extracts the plain key UUID from a key ID or key ARN.
// Returns ("", false) for alias names, alias ARNs, or malformed ARNs.
func resolveKeyID(keyID string) (string, bool) {
	if strings.HasPrefix(keyID, "arn:") {
		parts := strings.SplitN(keyID, ":key/", 2)
		if len(parts) == 2 && parts[1] != "" {
			return parts[1], true
		}
		return "", false
	}
	if strings.HasPrefix(keyID, "alias/") {
		return "", false
	}
	return keyID, true
}

// isAliasRef reports whether keyID is an alias name or alias ARN.
func isAliasRef(keyID string) bool {
	return strings.HasPrefix(keyID, "alias/") ||
		(strings.HasPrefix(keyID, "arn:") && strings.Contains(keyID, ":alias/"))
}

var validKeySpecs = map[string]bool{
	"SYMMETRIC_DEFAULT":     true,
	"RSA_2048":              true,
	"RSA_3072":              true,
	"RSA_4096":              true,
	"ECC_NIST_P256":         true,
	"ECC_NIST_P384":         true,
	"ECC_NIST_P521":         true,
	"ECC_SECG_P256K1":       true,
	"ECC_NIST_EDWARDS25519": true,
	"ML_DSA_44":             true,
	"ML_DSA_65":             true,
	"ML_DSA_87":             true,
	"HMAC_224":              true,
	"HMAC_256":              true,
	"HMAC_384":              true,
	"HMAC_512":              true,
	"SM2":                   true,
}

var defaultKeyUsage = map[string]string{
	"SYMMETRIC_DEFAULT":     "ENCRYPT_DECRYPT",
	"RSA_2048":              "ENCRYPT_DECRYPT",
	"RSA_3072":              "ENCRYPT_DECRYPT",
	"RSA_4096":              "ENCRYPT_DECRYPT",
	"ECC_NIST_P256":         "SIGN_VERIFY",
	"ECC_NIST_P384":         "SIGN_VERIFY",
	"ECC_NIST_P521":         "SIGN_VERIFY",
	"ECC_SECG_P256K1":       "SIGN_VERIFY",
	"ECC_NIST_EDWARDS25519": "SIGN_VERIFY",
	"ML_DSA_44":             "SIGN_VERIFY",
	"ML_DSA_65":             "SIGN_VERIFY",
	"ML_DSA_87":             "SIGN_VERIFY",
	"HMAC_224":              "GENERATE_VERIFY_MAC",
	"HMAC_256":              "GENERATE_VERIFY_MAC",
	"HMAC_384":              "GENERATE_VERIFY_MAC",
	"HMAC_512":              "GENERATE_VERIFY_MAC",
	"SM2":                   "ENCRYPT_DECRYPT",
}

// validKeyUsages maps KeySpec → set of valid KeyUsage values.
var validKeyUsages = map[string][]string{
	"SYMMETRIC_DEFAULT":     {"ENCRYPT_DECRYPT"},
	"RSA_2048":              {"ENCRYPT_DECRYPT", "SIGN_VERIFY"},
	"RSA_3072":              {"ENCRYPT_DECRYPT", "SIGN_VERIFY"},
	"RSA_4096":              {"ENCRYPT_DECRYPT", "SIGN_VERIFY"},
	"ECC_NIST_P256":         {"SIGN_VERIFY", "KEY_AGREEMENT"},
	"ECC_NIST_P384":         {"SIGN_VERIFY", "KEY_AGREEMENT"},
	"ECC_NIST_P521":         {"SIGN_VERIFY", "KEY_AGREEMENT"},
	"ECC_SECG_P256K1":       {"SIGN_VERIFY"},
	"ECC_NIST_EDWARDS25519": {"SIGN_VERIFY"},
	"ML_DSA_44":             {"SIGN_VERIFY"},
	"ML_DSA_65":             {"SIGN_VERIFY"},
	"ML_DSA_87":             {"SIGN_VERIFY"},
	"HMAC_224":              {"GENERATE_VERIFY_MAC"},
	"HMAC_256":              {"GENERATE_VERIFY_MAC"},
	"HMAC_384":              {"GENERATE_VERIFY_MAC"},
	"HMAC_512":              {"GENERATE_VERIFY_MAC"},
	"SM2":                   {"ENCRYPT_DECRYPT", "SIGN_VERIFY", "KEY_AGREEMENT"},
}

func isValidKeyUsageForSpec(spec, usage string) bool {
	for _, v := range validKeyUsages[spec] {
		if v == usage {
			return true
		}
	}
	return false
}

func encryptionAlgorithmsForKey(spec, usage string) []string {
	if usage != "ENCRYPT_DECRYPT" {
		return nil
	}
	switch spec {
	case "SYMMETRIC_DEFAULT":
		return []string{"SYMMETRIC_DEFAULT"}
	case "RSA_2048", "RSA_3072", "RSA_4096":
		return []string{"RSAES_OAEP_SHA_1", "RSAES_OAEP_SHA_256"}
	case "SM2":
		return []string{"SM2PKE"}
	}
	return nil // unreachable: handler validates spec/usage before calling
}

func signingAlgorithmsForKey(spec, usage string) []string {
	if usage != "SIGN_VERIFY" {
		return nil
	}
	switch spec {
	case "RSA_2048", "RSA_3072", "RSA_4096":
		return []string{
			"RSASSA_PKCS1_V1_5_SHA_256", "RSASSA_PKCS1_V1_5_SHA_384", "RSASSA_PKCS1_V1_5_SHA_512",
			"RSASSA_PSS_SHA_256", "RSASSA_PSS_SHA_384", "RSASSA_PSS_SHA_512",
		}
	case "ECC_NIST_P256", "ECC_SECG_P256K1":
		return []string{"ECDSA_SHA_256"}
	case "ECC_NIST_P384":
		return []string{"ECDSA_SHA_384"}
	case "ECC_NIST_P521":
		return []string{"ECDSA_SHA_512"}
	case "ECC_NIST_EDWARDS25519":
		return []string{"ED25519_SHA_512", "ED25519_PH_SHA_512"}
	case "ML_DSA_44":
		return []string{"ML_DSA_44"}
	case "ML_DSA_65":
		return []string{"ML_DSA_65"}
	case "ML_DSA_87":
		return []string{"ML_DSA_87"}
	case "SM2":
		return []string{"SM2DSA"}
	}
	return nil // unreachable: handler validates spec/usage before calling
}

func keyAgreementAlgorithmsForKey(spec, usage string) []string {
	if usage != "KEY_AGREEMENT" {
		return nil
	}
	switch spec {
	case "ECC_NIST_P256", "ECC_NIST_P384", "ECC_NIST_P521", "ECC_NIST_EDWARDS25519":
		return []string{"ECDH"}
	case "SM2":
		return []string{"SM2KE"}
	}
	return nil // unreachable: handler validates spec/usage before calling
}

// TagEntry is one key/value tag on a KMS key.
type TagEntry struct {
	TagKey   string `json:"TagKey"`
	TagValue string `json:"TagValue"`
}

// KeyMaterial holds the cryptographic key material for a KMS key.
// For SYMMETRIC_DEFAULT keys, KeyBytes holds 32 AES-256 bytes.
// For RSA/ECC keys, PrivateKeyDER holds the PKCS#8 DER-encoded private key.
// KeyMaterialID is a 64-char hex identifier generated for all key types.
type KeyMaterial struct {
	KeyBytes      []byte `json:"KeyBytes,omitempty"`      // 32 bytes for AES-256 (SYMMETRIC_DEFAULT)
	PrivateKeyDER []byte `json:"PrivateKeyDER,omitempty"` // PKCS#8 DER for RSA/ECC asymmetric keys
	KeyMaterialID string `json:"KeyMaterialId"`           // 64-char hex, identifies this key material
}

func macAlgorithmsForKey(spec string) []string {
	switch spec {
	case "HMAC_224":
		return []string{"HMAC_SHA_224"}
	case "HMAC_256":
		return []string{"HMAC_SHA_256"}
	case "HMAC_384":
		return []string{"HMAC_SHA_384"}
	case "HMAC_512":
		return []string{"HMAC_SHA_512"}
	}
	return nil
}
