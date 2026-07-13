package cognito

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
)

// srpNHex is the 3072-bit safe prime used by AWS Cognito's SRP-6a group.
// See docs/aws-spec/cognito/srp_protocol.md for the protocol this implements.
const srpNHex = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1" +
	"29024E088A67CC74020BBEA63B139B22514A08798E3404DD" +
	"EF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245" +
	"E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7ED" +
	"EE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3D" +
	"C2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F" +
	"83655D23DCA3AD961C62F356208552BB9ED529077096966D" +
	"670C354E4ABC9804F1746C08CA18217C32905E462E36CE3B" +
	"E39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9" +
	"DE2BCBF6955817183995497CEA956AE515D2261898FA0510" +
	"15728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64" +
	"ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7" +
	"ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6B" +
	"F12FFA06D98A0864D87602733EC86A64521F2B18177B200C" +
	"BBE117577A615D6C770988C0BAD946E208E24FA074E5AB31" +
	"43DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF"

var (
	srpN *big.Int
	srpG = big.NewInt(2)
	srpK *big.Int
)

func init() {
	var ok bool
	srpN, ok = new(big.Int).SetString(srpNHex, 16)
	if !ok {
		// unreachable: srpNHex is a fixed, valid hex literal
		panic("cognito: invalid SRP N constant")
	}
	srpK = new(big.Int).SetBytes(sha256Concat(padHex(srpN), padHex(srpG)))
}

// padHex returns the two's-complement-safe byte encoding of a non-negative
// big.Int: hex-encode, pad to even length, and prepend a zero byte if the
// top bit of the first byte would otherwise be set. Every value in this
// protocol (N, g, A, B, S, U, salt, verifier) is always non-negative, so only
// this positive branch is implemented. Real Cognito SDK clients apply the
// same padding before hashing/HMAC'ing these values, so it must match
// bit-for-bit — see docs/aws-spec/cognito/srp_protocol.md.
// Callers must reject negative input themselves (see handleUserSRPAuth's
// SRP_A validation); padHex panics rather than silently producing wrong
// output if that precondition is violated.
func padHex(n *big.Int) []byte {
	if n.Sign() < 0 {
		panic("cognito: padHex requires a non-negative big.Int")
	}
	h := n.Text(16)
	if len(h)%2 != 0 {
		h = "0" + h
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		// unreachable: h is always a valid hex string built from n.Text(16)
		panic("cognito: padHex produced invalid hex: " + err.Error())
	}
	if len(b) > 0 && b[0] >= 0x80 {
		b = append([]byte{0x00}, b...)
	}
	return b
}

func sha256Concat(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

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
	saltPadded := padHex(new(big.Int).SetBytes(saltBytes))

	poolName := userPoolNameFromID(poolID)
	innerHash := sha256Concat([]byte(poolName + username + ":" + password))
	x := new(big.Int).SetBytes(sha256Concat(saltPadded, innerHash))

	verifier := new(big.Int).Exp(srpG, x, srpN)

	return hex.EncodeToString(saltPadded), verifier.Text(16), nil
}

// computeSRPB computes the server's public SRP value B = (k*v + g^b mod N) mod N.
func computeSRPB(v, b *big.Int) *big.Int {
	gb := new(big.Int).Exp(srpG, b, srpN)
	kv := new(big.Int).Mul(srpK, v)
	B := new(big.Int).Add(kv, gb)
	return B.Mod(B, srpN)
}

// deriveSRPKey derives the 16-byte session key K from the shared secret S and
// scrambling parameter u, using Cognito's non-standard "HKDF": the salt/IKM
// roles are swapped relative to RFC 5869, and the info string is fixed.
func deriveSRPKey(u, s *big.Int) []byte {
	prkMAC := hmac.New(sha256.New, padHex(u))
	prkMAC.Write(padHex(s))
	prk := prkMAC.Sum(nil)

	info := append([]byte("Caldera Derived Key"), 0x01)
	okmMAC := hmac.New(sha256.New, prk)
	okmMAC.Write(info)
	return okmMAC.Sum(nil)[:16]
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
	if new(big.Int).Mod(A, srpN).Sign() == 0 {
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

	b, err := rand.Int(rand.Reader, srpN)
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

	sessionToken, err := buildSessionToken(
		privateKey, keys.KeyID, poolID, username, "PASSWORD_VERIFIER",
		map[string]any{
			"srp_a":      hex.EncodeToString(padHex(A)),
			"srp_b_priv": hex.EncodeToString(padHex(b)),
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
			"SRP_B":           hex.EncodeToString(padHex(B)),
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
	b, okB := new(big.Int).SetString(claimB, 16)
	if !okA || !okB {
		// unreachable: srp_a/srp_b_priv are always written as valid hex by handleUserSRPAuth
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

	u := new(big.Int).SetBytes(sha256Concat(padHex(A), padHex(B)))
	if u.Sign() == 0 {
		// unreachable: constructing A, B such that SHA256(padHex(A)||padHex(B)) == 0
		// is a preimage attack, computationally infeasible to hit in a test
		writeError(w, http.StatusBadRequest, ErrTypeNotAuthorizedException,
			"Incorrect username or password.")
		return
	}

	// S = (A * v^u mod N)^b mod N — the server-side SRP-6a formula.
	vu := new(big.Int).Exp(v, u, srpN)
	avu := new(big.Int).Mod(new(big.Int).Mul(A, vu), srpN)
	S := new(big.Int).Exp(avu, b, srpN)

	key := deriveSRPKey(u, S)

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
