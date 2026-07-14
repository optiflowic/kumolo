package cognito

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/optiflowic/kumolo/internal/cognito/srpmath"
)

// userPoolNameFromID returns the part of a Cognito pool ID after the region
// prefix (e.g. "AbCdEfGhI" from "us-east-1_AbCdEfGhI") — the "user pool name"
// component used throughout the SRP protocol's hash inputs.
func userPoolNameFromID(poolID string) string {
	_, name, found := strings.Cut(poolID, "_")
	if !found {
		// unreachable: kumolo pool IDs are always generated as "{region}_{suffix}"
		return poolID
	}
	return name
}

// srpVerifierFor derives the SRP-6a salt and verifier for a (new or changed)
// password. See docs/aws-spec/cognito/srp_protocol.md for the derivation.
func srpVerifierFor(poolID, username, password string) (saltHex, verifierHex string, err error) {
	saltBytes := make([]byte, 16)
	if _, err := rand.Read(saltBytes); err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", "", fmt.Errorf("read SRP salt entropy: %w", err)
	}
	saltPadded := srpmath.PadHex(new(big.Int).SetBytes(saltBytes))

	poolName := userPoolNameFromID(poolID)
	innerHash := srpmath.SHA256Concat([]byte(poolName + username + ":" + password))
	x := new(big.Int).SetBytes(srpmath.SHA256Concat(saltPadded, innerHash))

	verifier := new(big.Int).Exp(srpmath.G, x, srpmath.N)

	return hex.EncodeToString(saltPadded), verifier.Text(16), nil
}

// computeSRPB computes the server's public SRP value B = (k*v + g^b mod N) mod N.
func computeSRPB(v, b *big.Int) *big.Int {
	gb := new(big.Int).Exp(srpmath.G, b, srpmath.N)
	kv := new(big.Int).Mul(srpmath.K, v)
	B := new(big.Int).Add(kv, gb)
	return B.Mod(B, srpmath.N)
}

// srpSessionKey derives a 32-byte AES-256 key from the pool's RSA signing key,
// used to encrypt the server's private SRP ephemeral value `b` before it is
// embedded in the (client-visible) session JWT. `b` must never be readable by
// the client: B = (k*v + g^b) mod N is public, so anyone holding plaintext `b`
// can recover the SRP verifier v = (B - g^b) * k^-1 mod N and mount an offline
// dictionary attack against it. A signed-but-unencrypted JWT claim would leak
// exactly that. The key is a domain-separated hash of the RSA private
// exponent, not the exponent itself, so a symmetric-key compromise can't be
// mistaken for (or trivially converted into) the RSA private key.
func srpSessionKey(privateKey *rsa.PrivateKey) []byte {
	sum := sha256.Sum256(append([]byte("kumolo-cognito-srp-b-key:"), privateKey.D.Bytes()...))
	return sum[:]
}

// encryptSRPPrivB seals the server's private SRP ephemeral value `b` with
// AES-256-GCM under a key derived from the pool's RSA signing key, returning
// a base64 string safe to embed as a session JWT claim. See srpSessionKey for
// why `b` must not be embedded in plaintext.
func encryptSRPPrivB(privateKey *rsa.PrivateKey, b *big.Int) (string, error) {
	block, err := aes.NewCipher(srpSessionKey(privateKey))
	if err != nil {
		// unreachable: srpSessionKey always returns exactly 32 bytes, a valid AES-256 key size
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		// unreachable: aes.NewCipher's block always has the fixed AES block size cipher.NewGCM requires
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return "", fmt.Errorf("read GCM nonce entropy: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, srpmath.PadHex(b), nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// decryptSRPPrivB reverses encryptSRPPrivB, recovering the server's private
// SRP ephemeral value `b`.
func decryptSRPPrivB(privateKey *rsa.PrivateKey, encoded string) (*big.Int, error) {
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode srp_b_priv: %w", err)
	}
	block, err := aes.NewCipher(srpSessionKey(privateKey))
	if err != nil {
		// unreachable: srpSessionKey always returns exactly 32 bytes, a valid AES-256 key size
		return nil, fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		// unreachable: aes.NewCipher's block always has the fixed AES block size cipher.NewGCM requires
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("srp_b_priv ciphertext too short")
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt srp_b_priv: %w", err)
	}
	return new(big.Int).SetBytes(plain), nil
}

// ──── InitiateAuth: USER_SRP_AUTH ────────────────────────────────────────────

func (ro *Router) handleUserSRPAuth(
	w http.ResponseWriter,
	poolID, clientID string,
	params map[string]string,
) {
	username := params["USERNAME"]
	srpAHex := params["SRP_A"]
	if username == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"USERNAME is required in AuthParameters")
		return
	}
	if srpAHex == "" {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"SRP_A is required in AuthParameters")
		return
	}

	A, ok := new(big.Int).SetString(srpAHex, 16)
	if !ok || A.Sign() < 0 {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"SRP_A is not a valid hex number")
		return
	}
	if new(big.Int).Mod(A, srpmath.N).Sign() == 0 {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"SRP_A mod N cannot be 0")
		return
	}

	user, err := ro.storage.GetUser(poolID, username)
	if err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user")
		return
	}

	if !user.Enabled {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "User is disabled.")
		return
	}
	if user.Status == userStatusUnconfirmed {
		writeError(w, http.StatusBadRequest, ErrTypeUserNotConfirmedException,
			"User is not confirmed.")
		return
	}
	if user.SRPVerifier == "" {
		// No verifier stored (pre-migration or passwordless user): fail closed,
		// same wording as USER_PASSWORD_AUTH's wrong-password path.
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
			"Incorrect username or password.")
		return
	}

	v, ok := new(big.Int).SetString(user.SRPVerifier, 16)
	if !ok {
		// unreachable: SRPVerifier is always written by srpVerifierFor as a valid hex string
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to parse SRP verifier")
		return
	}

	b, err := rand.Int(rand.Reader, srpmath.N)
	if err != nil {
		// untestable: crypto/rand.Int only fails on OS-level entropy source errors
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate SRP ephemeral value")
		return
	}
	if b.Sign() == 0 {
		// unreachable: crypto/rand.Int returning exactly 0 from a ~3072-bit range
		// is astronomically unlikely; nudge away from the degenerate case anyway.
		b.SetInt64(1)
	}

	B := computeSRPB(v, b)

	secretBlock := make([]byte, 32)
	if _, err := rand.Read(secretBlock); err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to generate secret block")
		return
	}

	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get pool keys")
		return
	}

	encryptedB, err := encryptSRPPrivB(privateKey, b)
	if err != nil {
		// untestable: only fails on AES/GCM setup or crypto/rand entropy errors
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to seal session state")
		return
	}

	sessionToken, err := buildSessionToken(
		privateKey, keys.KeyID, poolID, username, "PASSWORD_VERIFIER",
		map[string]any{
			"srp_a":      hex.EncodeToString(srpmath.PadHex(A)),
			"srp_b_priv": encryptedB,
		},
	)
	if err != nil {
		// unreachable: buildJWT fails only if claims contain non-serializable types (all primitives here)
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to build session token")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ChallengeName": "PASSWORD_VERIFIER",
		"ChallengeParameters": map[string]string{
			"SALT":            user.SRPSalt,
			"SRP_B":           hex.EncodeToString(srpmath.PadHex(B)),
			"SECRET_BLOCK":    base64.StdEncoding.EncodeToString(secretBlock),
			"USER_ID_FOR_SRP": username,
		},
		"Session": sessionToken,
	})
}

// ──── RespondToAuthChallenge: PASSWORD_VERIFIER ──────────────────────────────

func (ro *Router) handlePasswordVerifierChallenge(
	w http.ResponseWriter,
	poolID, clientID, sessionToken string,
	responses map[string]string,
) {
	secretBlockB64 := responses["PASSWORD_CLAIM_SECRET_BLOCK"]
	timestamp := responses["TIMESTAMP"]
	signatureB64 := responses["PASSWORD_CLAIM_SIGNATURE"]
	if secretBlockB64 == "" || timestamp == "" || signatureB64 == "" {
		writeError(
			w,
			http.StatusBadRequest,
			ErrTypeInvalidParameterException,
			"PASSWORD_CLAIM_SECRET_BLOCK, TIMESTAMP, and PASSWORD_CLAIM_SIGNATURE are required in ChallengeResponses",
		)
		return
	}

	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get pool keys")
		return
	}

	claims, err := parseSessionToken(sessionToken, &privateKey.PublicKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
			"Invalid or expired session.")
		return
	}

	claimPoolID, _ := claims["pool_id"].(string)
	claimChallenge, _ := claims["challenge"].(string)
	claimUsername, _ := claims["username"].(string)
	claimA, _ := claims["srp_a"].(string)
	claimB, _ := claims["srp_b_priv"].(string)

	if claimPoolID != poolID || claimChallenge != "PASSWORD_VERIFIER" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return
	}

	username := responses["USERNAME"]
	if username == "" {
		username = claimUsername
	} else if username != claimUsername {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException, "Invalid session.")
		return
	}

	A, okA := new(big.Int).SetString(claimA, 16)
	b, errB := decryptSRPPrivB(privateKey, claimB)
	if !okA || errB != nil {
		// unreachable: srp_a is always written as valid hex, and srp_b_priv is
		// always written as a validly-encrypted blob, by handleUserSRPAuth
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to parse session SRP state")
		return
	}

	user, err := ro.storage.GetUser(poolID, username)
	if err != nil {
		if errors.Is(err, errUserNotFound) {
			writeError(w, http.StatusBadRequest, ErrTypeUserNotFoundException,
				"User does not exist.")
			return
		}
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to get user")
		return
	}
	if user.SRPVerifier == "" {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
			"Incorrect username or password.")
		return
	}
	v, ok := new(big.Int).SetString(user.SRPVerifier, 16)
	if !ok {
		// unreachable: SRPVerifier is always written by srpVerifierFor as a valid hex string
		writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
			"failed to parse SRP verifier")
		return
	}

	B := computeSRPB(v, b)

	u := new(big.Int).SetBytes(srpmath.SHA256Concat(srpmath.PadHex(A), srpmath.PadHex(B)))
	if u.Sign() == 0 {
		// unreachable: constructing A, B such that SHA256(srpmath.PadHex(A)||srpmath.PadHex(B)) == 0
		// is a preimage attack, computationally infeasible to hit in a test
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
			"Incorrect username or password.")
		return
	}

	// S = (A * v^u mod N)^b mod N — the server-side SRP-6a formula.
	vu := new(big.Int).Exp(v, u, srpmath.N)
	avu := new(big.Int).Mod(new(big.Int).Mul(A, vu), srpmath.N)
	S := new(big.Int).Exp(avu, b, srpmath.N)

	key := srpmath.DeriveSessionKey(u, S)

	secretBlockRaw, err := base64.StdEncoding.DecodeString(secretBlockB64)
	if err != nil {
		writeError(w, http.StatusBadRequest, ErrTypeInvalidParameterException,
			"PASSWORD_CLAIM_SECRET_BLOCK is not valid base64")
		return
	}

	poolName := userPoolNameFromID(poolID)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(poolName))
	mac.Write([]byte(username))
	mac.Write(secretBlockRaw)
	mac.Write([]byte(timestamp))
	expectedSig := mac.Sum(nil)

	submittedSig, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil || subtle.ConstantTimeCompare(expectedSig, submittedSig) != 1 {
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
			"Incorrect username or password.")
		return
	}

	if user.Status == userStatusForceChangePasswd {
		newSession, serr := buildSessionToken(
			privateKey, keys.KeyID, poolID, username, "NEW_PASSWORD_REQUIRED", nil,
		)
		if serr != nil {
			// unreachable: buildJWT fails only if claims contain non-serializable types (all primitives here)
			writeError(w, http.StatusInternalServerError, ErrTypeInternalErrorException,
				"failed to build session token")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ChallengeName": "NEW_PASSWORD_REQUIRED",
			"ChallengeParameters": map[string]string{
				"USER_ID_FOR_SRP":    username,
				"requiredAttributes": "[]",
				"userAttributes":     "{}",
			},
			"Session": newSession,
		})
		return
	}

	ro.writeAuthResult(w, poolID, clientID, user, privateKey, keys.KeyID, true, "")
}
