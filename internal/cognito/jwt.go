package cognito

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"time"
)

const (
	accessTokenExpiry       = 3600
	sessionExpiry           = 180
	defaultRefreshTokenDays = 30
	secondsPerDay           = 24 * 60 * 60
	cognitoClaimPrefix      = "cognito:"

	jwtClaimIssuer    = "iss"
	jwtClaimExp       = "exp"
	jwtClaimTokenUse  = "token_use"
	jwtClaimSub       = "sub"
	jwtTokenUseAccess = "access"
)

// issuerURL returns the AWS-format issuer URL for a user pool.
func issuerURL(poolID string) string {
	return "https://cognito-idp." + poolRegion + ".amazonaws.com/" + poolID
}

// b64url encodes data using base64 URL encoding (no padding), as required by JWT spec.
func b64url(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func b64urlDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

// buildJWT constructs and signs an RS256 JWT.
func buildJWT(privateKey *rsa.PrivateKey, keyID string, claims map[string]any) (string, error) {
	header := map[string]string{"alg": "RS256", "kid": keyID}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		// unreachable: header is always a valid map
		return "", fmt.Errorf("marshal header: %w", err)
	}

	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}

	signingInput := b64url(headerJSON) + "." + b64url(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}

	return signingInput + "." + b64url(sig), nil
}

// verifyJWT parses and verifies an RS256 JWT signature. Returns the claims on success.
func verifyJWT(tokenStr string, publicKey *rsa.PublicKey) (map[string]any, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}

	signingInput := parts[0] + "." + parts[1]
	digest := sha256.Sum256([]byte(signingInput))

	sigBytes, err := b64urlDecode(parts[2])
	if err != nil {
		return nil, fmt.Errorf("decode JWT signature: %w", err)
	}

	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		return nil, fmt.Errorf("invalid JWT signature: %w", err)
	}

	claimsData, err := b64urlDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT claims: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(claimsData, &claims); err != nil {
		return nil, fmt.Errorf("parse JWT claims: %w", err)
	}
	return claims, nil
}

// buildJWKS returns the JWKS representation of the pool's RSA public key.
func buildJWKS(publicKey *rsa.PublicKey, keyID string) map[string]any {
	nBytes := publicKey.N.Bytes()
	eBytes := big.NewInt(int64(publicKey.E)).Bytes()
	return map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"use": "sig",
				"alg": "RS256",
				"kid": keyID,
				"n":   b64url(nBytes),
				"e":   b64url(eBytes),
			},
		},
	}
}

// issueTokens generates a new access token, ID token, and refresh token for the given user.
// groups is the list of group names to include in the cognito:groups claim; pass nil if the user
// has no group membership.
func issueTokens(
	privateKey *rsa.PrivateKey,
	keyID, poolID, clientID string,
	user *UserMetadata,
	groups []string,
) (accessToken, idToken, refreshToken string, err error) {
	now := time.Now().Unix()
	exp := now + accessTokenExpiry
	originJTI, err := generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", "", "", fmt.Errorf("generate origin_jti: %w", err)
	}

	accessJTI, err := generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", "", "", fmt.Errorf("generate access jti: %w", err)
	}

	idJTI, err := generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", "", "", fmt.Errorf("generate id jti: %w", err)
	}

	refreshToken, err = generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", "", "", fmt.Errorf("generate refresh token: %w", err)
	}

	iss := issuerURL(poolID)

	accessClaims := map[string]any{
		"sub":        user.Sub,
		"iss":        iss,
		"version":    2,
		"client_id":  clientID,
		"origin_jti": originJTI,
		"token_use":  "access",
		"scope":      "aws.cognito.signin.user.admin",
		"auth_time":  now,
		"exp":        exp,
		"iat":        now,
		"jti":        accessJTI,
		"username":   user.Username,
	}
	if len(groups) > 0 {
		accessClaims["cognito:groups"] = groups
	}

	idClaims := map[string]any{
		"sub":              user.Sub,
		"iss":              iss,
		"aud":              clientID,
		"token_use":        "id",
		"cognito:username": user.Username,
		"origin_jti":       originJTI,
		"auth_time":        now,
		"exp":              exp,
		"iat":              now,
		"jti":              idJTI,
	}
	reservedClaims := map[string]bool{
		"sub": true, "iss": true, "aud": true, "token_use": true,
		"cognito:username": true, "origin_jti": true, "auth_time": true,
		"exp": true, "iat": true, "jti": true,
	}
	for _, attr := range user.Attributes {
		if !reservedClaims[attr.Name] && !strings.HasPrefix(attr.Name, cognitoClaimPrefix) {
			idClaims[attr.Name] = attr.Value
		}
	}
	if len(groups) > 0 {
		idClaims["cognito:groups"] = groups
	}

	accessToken, err = buildJWT(privateKey, keyID, accessClaims)
	if err != nil {
		return "", "", "", fmt.Errorf("build access token: %w", err)
	}

	idToken, err = buildJWT(privateKey, keyID, idClaims)
	if err != nil {
		// unreachable: same key and algorithm as access token; if access token signing succeeded, this will too
		return "", "", "", fmt.Errorf("build id token: %w", err)
	}

	return accessToken, idToken, refreshToken, nil
}

// buildSessionToken creates a signed session JWT for challenge flows.
func buildSessionToken(
	privateKey *rsa.PrivateKey,
	keyID, poolID, username, challengeName string,
) (string, error) {
	now := time.Now().Unix()
	claims := map[string]any{
		"pool_id":   poolID,
		"username":  username,
		"challenge": challengeName,
		"iat":       now,
		"exp":       now + sessionExpiry,
	}
	token, err := buildJWT(privateKey, keyID, claims)
	if err != nil {
		// unreachable: all claim values are primitives, so buildJWT never fails here
		return "", fmt.Errorf("build session token: %w", err)
	}
	return token, nil
}

// parseRawClaims decodes the payload of a JWT without verifying the signature.
// Use only to extract identifiers (e.g. pool ID from iss) before signature verification.
func parseRawClaims(tokenStr string) (map[string]any, error) {
	parts := strings.Split(tokenStr, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format")
	}
	claimsData, err := b64urlDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode JWT claims: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(claimsData, &claims); err != nil {
		return nil, fmt.Errorf("parse JWT claims: %w", err)
	}
	return claims, nil
}

// extractPoolID returns the pool ID from a Cognito issuer URL.
// Only accepts the exact shape https://cognito-idp.<region>.amazonaws.com/<poolID>.
// Returns "" for any other form.
func extractPoolID(iss string) string {
	const prefix = "https://cognito-idp."
	if !strings.HasPrefix(iss, prefix) {
		return ""
	}
	// Strip scheme and split into host + path without importing net/url.
	rest := iss[len("https://"):]
	slash := strings.IndexByte(rest, '/')
	if slash < 0 {
		return ""
	}
	host := rest[:slash]
	poolID := rest[slash+1:]

	// Host must be exactly cognito-idp.<region>.amazonaws.com (4 dot-separated parts).
	parts := strings.SplitN(host, ".", 4)
	if len(parts) != 4 || parts[0] != "cognito-idp" || parts[1] == "" ||
		parts[2] != "amazonaws" || parts[3] != "com" {
		return ""
	}

	// Pool ID must be a single non-empty path segment.
	if poolID == "" || strings.Contains(poolID, "/") {
		return ""
	}
	return poolID
}

// parseSessionToken verifies and parses a session JWT. Returns the claims if valid.
func parseSessionToken(tokenStr string, publicKey *rsa.PublicKey) (map[string]any, error) {
	claims, err := verifyJWT(tokenStr, publicKey)
	if err != nil {
		return nil, err
	}

	exp, ok := claims["exp"].(float64)
	if !ok || int64(exp) < time.Now().Unix() {
		return nil, fmt.Errorf("session expired")
	}
	return claims, nil
}
