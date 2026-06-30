package cognito

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func genTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	keyID, err := generateTokenID()
	require.NoError(t, err)
	return key, keyID
}

// signRaw signs arbitrary bytes with an RSA key (RS256) and returns a JWT string.
func buildRawJWT(t *testing.T, key *rsa.PrivateKey, header, payload string) string {
	t.Helper()
	sigInput := header + "." + payload
	digest := sha256.Sum256([]byte(sigInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)
	return sigInput + "." + b64url(sig)
}

// ── buildJWT / verifyJWT ──────────────────────────────────────────────────────

func TestBuildAndVerifyJWT_RoundTrip(t *testing.T) {
	key, keyID := genTestKey(t)
	claims := map[string]any{"sub": "user-1", "iss": "https://example.com"}

	token, err := buildJWT(key, keyID, claims)
	require.NoError(t, err)
	require.NotEmpty(t, token)

	got, err := verifyJWT(token, &key.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, "user-1", got["sub"])
	assert.Equal(t, "https://example.com", got["iss"])
}

func TestBuildJWT_UnmarshalableClaimsFails(t *testing.T) {
	key, keyID := genTestKey(t)
	// chan is not JSON-serializable.
	_, err := buildJWT(key, keyID, map[string]any{"ch": make(chan int)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "marshal claims")
}

func TestBuildJWT_SigningFails(t *testing.T) {
	// Construct a key with a 7-bit modulus directly, bypassing rsa.GenerateKey's
	// minimum-size guard. rsa.SignPKCS1v15 rejects it as too small.
	key := &rsa.PrivateKey{
		PublicKey: rsa.PublicKey{N: big.NewInt(127), E: 65537},
		D:         big.NewInt(1),
		Primes:    []*big.Int{big.NewInt(127)},
	}
	_, err := buildJWT(key, "kid", map[string]any{"sub": "x"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sign JWT")
}

func TestVerifyJWT_WrongFormat(t *testing.T) {
	key, _ := genTestKey(t)
	_, err := verifyJWT("only.two", &key.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JWT format")
}

func TestVerifyJWT_BadSignatureEncoding(t *testing.T) {
	key, keyID := genTestKey(t)
	token, err := buildJWT(key, keyID, map[string]any{"sub": "x"})
	require.NoError(t, err)

	parts := splitDots(token)
	// Corrupt the signature part with characters invalid in base64url.
	_, err = verifyJWT(parts[0]+"."+parts[1]+"."+"!!!!not-base64!!!!", &key.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode JWT signature")
}

func TestVerifyJWT_InvalidSignature(t *testing.T) {
	key, keyID := genTestKey(t)
	token, err := buildJWT(key, keyID, map[string]any{"sub": "x"})
	require.NoError(t, err)

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	_, err = verifyJWT(token, &otherKey.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid JWT signature")
}

func TestVerifyJWT_BadClaimsEncoding(t *testing.T) {
	key, _ := genTestKey(t)
	// Build a token whose payload part is invalid base64url but the signature covers it correctly.
	headerJSON, err := json.Marshal(map[string]string{"alg": "RS256", "kid": "x"})
	require.NoError(t, err)
	header := b64url(headerJSON)
	// "!!!" is not valid base64url.
	payload := "!!!invalid-base64!!!"
	token := buildRawJWT(t, key, header, payload)

	_, err = verifyJWT(token, &key.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode JWT claims")
}

func TestVerifyJWT_BadClaimsJSON(t *testing.T) {
	key, _ := genTestKey(t)
	// Build a token whose payload is valid base64url but decodes to non-JSON.
	headerJSON, err := json.Marshal(map[string]string{"alg": "RS256", "kid": "x"})
	require.NoError(t, err)
	header := b64url(headerJSON)
	payload := b64url([]byte("not-json-at-all"))
	token := buildRawJWT(t, key, header, payload)

	_, err = verifyJWT(token, &key.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse JWT claims")
}

// ── buildJWKS ─────────────────────────────────────────────────────────────────

func TestBuildJWKS_Structure(t *testing.T) {
	key, keyID := genTestKey(t)
	jwks := buildJWKS(&key.PublicKey, keyID)
	keys, ok := jwks["keys"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, keys, 1)
	assert.Equal(t, "RSA", keys[0]["kty"])
	assert.Equal(t, "sig", keys[0]["use"])
	assert.Equal(t, "RS256", keys[0]["alg"])
	assert.Equal(t, keyID, keys[0]["kid"])
	assert.NotEmpty(t, keys[0]["n"])
	assert.NotEmpty(t, keys[0]["e"])
}

// ── issueTokens ───────────────────────────────────────────────────────────────

func TestIssueTokens_Success(t *testing.T) {
	key, keyID := genTestKey(t)
	user := &UserMetadata{
		Username: "alice",
		Sub:      "sub-alice",
		Attributes: []AttributeType{
			{Name: "email", Value: "alice@example.com"},
		},
	}
	access, id, refresh, accessJTI, err := issueTokens(
		key,
		keyID,
		"us-east-1_Pool1",
		"client-1",
		user,
		nil,
	)
	require.NoError(t, err)
	require.NotEmpty(t, access)
	require.NotEmpty(t, id)
	require.NotEmpty(t, refresh)
	require.NotEmpty(t, accessJTI)

	// Verify access token claims.
	claims, err := verifyJWT(access, &key.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, "sub-alice", claims["sub"])
	assert.Equal(t, "access", claims["token_use"])
	assert.Equal(t, "alice", claims["username"])

	// Verify ID token claims including user attributes.
	idClaims, err := verifyJWT(id, &key.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, "id", idClaims["token_use"])
	assert.Equal(t, "alice@example.com", idClaims["email"])
}

func TestIssueTokens_ReservedClaimsNotOverridden(t *testing.T) {
	key, keyID := genTestKey(t)
	user := &UserMetadata{
		Username: "alice",
		Sub:      "real-sub",
		Attributes: []AttributeType{
			// Attempt to inject reserved claims via user attributes.
			{Name: "sub", Value: "injected-sub"},
			{Name: "exp", Value: "9999999999"},
			{Name: "token_use", Value: "access"},
			// A legitimate custom attribute must still appear.
			{Name: "email", Value: "alice@example.com"},
		},
	}
	_, id, _, _, err := issueTokens(key, keyID, "us-east-1_Pool1", "client-1", user, nil)
	require.NoError(t, err)

	idClaims, err := verifyJWT(id, &key.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, "real-sub", idClaims["sub"], "sub must not be overridable")
	assert.Equal(t, "id", idClaims["token_use"], "token_use must not be overridable")
	assert.NotEqual(
		t,
		"9999999999",
		fmt.Sprintf("%v", idClaims["exp"]),
		"exp must not be overridable",
	)
	assert.Equal(t, "alice@example.com", idClaims["email"], "legitimate attribute must appear")
}

func TestIssueTokens_CognitoPrefixAttributesBlocked(t *testing.T) {
	key, keyID := genTestKey(t)
	user := &UserMetadata{
		Username: "alice",
		Sub:      "real-sub",
		Attributes: []AttributeType{
			// Attempt to inject cognito: namespace claims.
			{Name: "cognito:groups", Value: "admin"},
			{Name: "cognito:roles", Value: "arn:aws:iam::123:role/Admin"},
			// A legitimate custom attribute must still appear.
			{Name: "custom:plan", Value: "pro"},
		},
	}
	_, id, _, _, err := issueTokens(key, keyID, "us-east-1_Pool1", "client-1", user, nil)
	require.NoError(t, err)

	idClaims, err := verifyJWT(id, &key.PublicKey)
	require.NoError(t, err)
	_, hasGroups := idClaims["cognito:groups"]
	assert.False(t, hasGroups, "cognito:groups must not be injectable via user attributes")
	_, hasRoles := idClaims["cognito:roles"]
	assert.False(t, hasRoles, "cognito:roles must not be injectable via user attributes")
	assert.Equal(t, "pro", idClaims["custom:plan"], "custom: attribute must appear")
}

func TestIssueTokens_AccessTokenBuildFails(t *testing.T) {
	// Construct a key with a 7-bit modulus directly; rsa.SignPKCS1v15 rejects it,
	// so buildJWT fails when building the access token.
	key := &rsa.PrivateKey{
		PublicKey: rsa.PublicKey{N: big.NewInt(127), E: 65537},
		D:         big.NewInt(1),
		Primes:    []*big.Int{big.NewInt(127)},
	}
	user := &UserMetadata{Username: "alice", Sub: "sub-alice"}
	_, _, _, _, err := issueTokens(key, "kid", "us-east-1_Pool1", "client-1", user, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "build access token")
}

// ── buildSessionToken / parseSessionToken ─────────────────────────────────────

func TestBuildAndParseSessionToken_RoundTrip(t *testing.T) {
	key, keyID := genTestKey(t)
	poolID := "us-east-1_Pool1"
	username := "alice"

	token, err := buildSessionToken(key, keyID, poolID, username, "NEW_PASSWORD_REQUIRED")
	require.NoError(t, err)
	require.NotEmpty(t, token)

	claims, err := parseSessionToken(token, &key.PublicKey)
	require.NoError(t, err)
	assert.Equal(t, poolID, claims["pool_id"])
	assert.Equal(t, username, claims["username"])
	assert.Equal(t, "NEW_PASSWORD_REQUIRED", claims["challenge"])
}

func TestParseSessionToken_ExpiredSession(t *testing.T) {
	key, keyID := genTestKey(t)
	past := time.Now().Add(-10 * time.Minute).Unix()
	claims := map[string]any{
		"pool_id":   "p",
		"username":  "u",
		"challenge": "NEW_PASSWORD_REQUIRED",
		"iat":       past,
		"exp":       past, // already expired
	}
	token, err := buildJWT(key, keyID, claims)
	require.NoError(t, err)

	_, err = parseSessionToken(token, &key.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session expired")
}

func TestParseSessionToken_MissingExpClaim(t *testing.T) {
	key, keyID := genTestKey(t)
	claims := map[string]any{"pool_id": "p", "username": "u"} // no "exp"
	token, err := buildJWT(key, keyID, claims)
	require.NoError(t, err)

	_, err = parseSessionToken(token, &key.PublicKey)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session expired")
}

func TestParseSessionToken_InvalidSignature(t *testing.T) {
	key, keyID := genTestKey(t)
	token, err := buildSessionToken(key, keyID, "p", "u", "NEW_PASSWORD_REQUIRED")
	require.NoError(t, err)

	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	_, err = parseSessionToken(token, &otherKey.PublicKey)
	require.Error(t, err)
}

// ── extractPoolID ─────────────────────────────────────────────────────────────

func TestExtractPoolID(t *testing.T) {
	tests := []struct {
		iss  string
		want string
	}{
		{"https://cognito-idp.us-east-1.amazonaws.com/us-east-1_Pool1", "us-east-1_Pool1"},
		{
			"https://cognito-idp.ap-northeast-1.amazonaws.com/ap-northeast-1_AbcDe",
			"ap-northeast-1_AbcDe",
		},
		// extra path segment
		{"https://cognito-idp.us-east-1.amazonaws.com/foo/bar", ""},
		// no path segment
		{"https://cognito-idp.us-east-1.amazonaws.com/", ""},
		{"https://cognito-idp.us-east-1.amazonaws.com", ""},
		// wrong host
		{"https://cognito-idp.evil.com/us-east-1_Pool1", ""},
		// subdomain takeover attempt
		{"https://cognito-idp.us-east-1.amazonaws.com.evil.com/us-east-1_Pool1", ""},
		// wrong prefix
		{"https://not-cognito-idp.us-east-1.amazonaws.com/us-east-1_Pool1", ""},
		// empty
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.iss, func(t *testing.T) {
			assert.Equal(t, tt.want, extractPoolID(tt.iss))
		})
	}
}

// splitDots splits a JWT into its three dot-separated parts.
func splitDots(token string) []string {
	parts := make([]string, 0, 3)
	start := 0
	for i, c := range token {
		if c == '.' {
			parts = append(parts, token[start:i])
			start = i + 1
		}
	}
	return append(parts, token[start:])
}
