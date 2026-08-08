package cognito

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
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
	jwtClaimJTI       = "jti"
	jwtClaimOriginJTI = "origin_jti"
	jwtTokenUseAccess = "access"

	tokenUnitSeconds = "seconds"
	tokenUnitMinutes = "minutes"
	tokenUnitHours   = "hours"
	tokenUnitDays    = "days"

	// defaultAccessIDTokenUnit and defaultRefreshTokenUnit are the units AWS assumes for
	// AccessTokenValidity/IdTokenValidity and RefreshTokenValidity, respectively, when
	// TokenValidityUnits does not specify one.
	defaultAccessIDTokenUnit = tokenUnitHours
	defaultRefreshTokenUnit  = tokenUnitDays
)

// tokenValidityUnits mirrors AWS's TokenValidityUnitsType.
type tokenValidityUnits struct {
	AccessToken  string `json:"AccessToken"`
	IdToken      string `json:"IdToken"`
	RefreshToken string `json:"RefreshToken"`
}

// parseTokenValidityUnits decodes a UserPoolClient's stored TokenValidityUnits.
// Malformed or absent input yields the zero value, which falls back to AWS defaults.
func parseTokenValidityUnits(raw json.RawMessage) tokenValidityUnits {
	var u tokenValidityUnits
	if len(raw) == 0 {
		return u
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		slog.Debug(
			"parseTokenValidityUnits",
			"error",
			fmt.Errorf("malformed TokenValidityUnits: %w", err),
		)
	}
	return u
}

// namedUnitSeconds returns the number of seconds in a recognized TokenValidityUnits value.
func namedUnitSeconds(unit string) (int64, bool) {
	switch unit {
	case tokenUnitSeconds:
		return 1, true
	case tokenUnitMinutes:
		return 60, true
	case tokenUnitHours:
		return 3600, true
	case tokenUnitDays:
		return secondsPerDay, true
	default:
		return 0, false
	}
}

// unitSeconds returns the number of seconds in unit, falling back to defaultUnit
// (always defaultAccessIDTokenUnit or defaultRefreshTokenUnit) when unit is unset or
// unrecognized.
func unitSeconds(unit, defaultUnit string) int64 {
	if sec, ok := namedUnitSeconds(unit); ok {
		return sec
	}
	sec, _ := namedUnitSeconds(defaultUnit)
	return sec
}

// resolveValiditySeconds converts a UserPoolClient validity value (in unit, defaulting to
// defaultUnit when unset) to seconds. value <= 0 (unset) yields fallbackSeconds, matching
// AWS's behavior of substituting its own default when a client leaves a validity field unset.
func resolveValiditySeconds(value int, unit, defaultUnit string, fallbackSeconds int64) int64 {
	if value <= 0 {
		return fallbackSeconds
	}
	return int64(value) * unitSeconds(unit, defaultUnit)
}

// issuerURL returns the AWS-format issuer URL for a user pool, deriving the region from
// the pool ID (format: {region}_{suffix}) rather than a fixed constant.
func issuerURL(poolID string) string {
	return "https://cognito-idp." + regionFromPoolID(poolID) + ".amazonaws.com/" + poolID
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
// originJTI ties all tokens issued from the same refresh token family together; pass "" to
// generate a new one (initial auth). Pass the stored origin_jti when refreshing so that
// RevokeToken can revoke the entire family in one operation.
// accessTokenExpirySeconds and idTokenExpirySeconds set each token's lifetime; callers resolve
// these from the requesting UserPoolClient's AccessTokenValidity/IdTokenValidity/TokenValidityUnits
// (falling back to accessTokenExpiry) via resolveValiditySeconds.
// Returns accessJTI and the used originJTI so callers can associate tokens with the refresh token.
func issueTokens(
	privateKey *rsa.PrivateKey,
	keyID, poolID, clientID string,
	user *UserMetadata,
	groups []string,
	originJTI string,
	accessTokenExpirySeconds, idTokenExpirySeconds int64,
) (accessToken, idToken, refreshToken, accessJTI, retOriginJTI string, err error) {
	now := time.Now().Unix()
	accessExp := now + accessTokenExpirySeconds
	idExp := now + idTokenExpirySeconds
	if originJTI == "" {
		originJTI, err = generateTokenID()
		if err != nil {
			// untestable: crypto/rand.Read only fails on OS-level entropy source errors
			return "", "", "", "", "", fmt.Errorf("generate origin_jti: %w", err)
		}
	}

	accessJTI, err = generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", "", "", "", "", fmt.Errorf("generate access jti: %w", err)
	}

	idJTI, err := generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", "", "", "", "", fmt.Errorf("generate id jti: %w", err)
	}

	refreshToken, err = generateTokenID()
	if err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", "", "", "", "", fmt.Errorf("generate refresh token: %w", err)
	}

	iss := issuerURL(poolID)

	accessClaims := map[string]any{
		"sub":             user.Sub,
		"iss":             iss,
		"version":         2,
		"client_id":       clientID,
		jwtClaimOriginJTI: originJTI,
		jwtClaimTokenUse:  "access",
		"scope":           "aws.cognito.signin.user.admin",
		"auth_time":       now,
		jwtClaimExp:       accessExp,
		"iat":             now,
		jwtClaimJTI:       accessJTI,
		"username":        user.Username,
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
		jwtClaimOriginJTI:  originJTI,
		"auth_time":        now,
		jwtClaimExp:        idExp,
		"iat":              now,
		"jti":              idJTI,
	}
	reservedClaims := map[string]bool{
		"sub": true, "iss": true, "aud": true, "token_use": true,
		"cognito:username": true, jwtClaimOriginJTI: true, "auth_time": true,
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
		return "", "", "", "", "", fmt.Errorf("build access token: %w", err)
	}

	idToken, err = buildJWT(privateKey, keyID, idClaims)
	if err != nil {
		// unreachable: same key and algorithm as access token; if access token signing succeeded, this will too
		return "", "", "", "", "", fmt.Errorf("build id token: %w", err)
	}

	return accessToken, idToken, refreshToken, accessJTI, originJTI, nil
}

// buildSessionToken creates a signed session JWT for challenge flows.
// clientID binds the session to the app client that started the challenge, so a
// session issued for one client cannot be redeemed with another client's ClientId.
// extraClaims, if non-nil, is merged into the claims map (e.g. SRP ephemeral
// state for the PASSWORD_VERIFIER challenge); pass nil when not needed.
func buildSessionToken(
	privateKey *rsa.PrivateKey,
	keyID, poolID, clientID, username, challengeName string,
	extraClaims map[string]any,
) (string, error) {
	now := time.Now().Unix()
	claims := map[string]any{}
	for k, v := range extraClaims {
		claims[k] = v
	}
	claims["pool_id"] = poolID
	claims["client_id"] = clientID
	claims["username"] = username
	claims["challenge"] = challengeName
	claims["iat"] = now
	claims["exp"] = now + sessionExpiry
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
