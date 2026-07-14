package cognito

import (
	"crypto/rsa"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/optiflowic/kumolo/internal/cognitotest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── SRP test helpers ─────────────────────────────────────────────────────────

func doInitSRPAuth(
	t *testing.T, ro *Router, clientID string, params map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"ClientId":       clientID,
		"AuthFlow":       "USER_SRP_AUTH",
		"AuthParameters": params,
	})
	return doOp(t, ro, "InitiateAuth", string(body))
}

func doPasswordVerifier(
	t *testing.T, ro *Router, clientID, session string, responses map[string]string,
) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"ClientId":           clientID,
		"ChallengeName":      "PASSWORD_VERIFIER",
		"Session":            session,
		"ChallengeResponses": responses,
	})
	return doOp(t, ro, "RespondToAuthChallenge", string(body))
}

type srpChallengeParams struct {
	Salt         string `json:"SALT"`
	SRPB         string `json:"SRP_B"`
	SecretBlock  string `json:"SECRET_BLOCK"`
	UserIDForSRP string `json:"USER_ID_FOR_SRP"`
}

type srpInitiateAuthResponse struct {
	ChallengeName       string             `json:"ChallengeName"`
	ChallengeParameters srpChallengeParams `json:"ChallengeParameters"`
	Session             string             `json:"Session"`
}

// srpLogin drives a full USER_SRP_AUTH + PASSWORD_VERIFIER exchange for
// username/password against ro, using a fresh cognitotest.SRPClient, and
// returns the final RespondToAuthChallenge response — either tokens or a
// chained NEW_PASSWORD_REQUIRED challenge for FORCE_CHANGE_PASSWORD users.
func srpLogin(
	t *testing.T, ro *Router, poolID, clientID, username, password string,
) *httptest.ResponseRecorder {
	t.Helper()
	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)

	w := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": username,
		"SRP_A":    client.AHex(),
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp srpInitiateAuthResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "PASSWORD_VERIFIER", resp.ChallengeName)

	poolName := strings.SplitN(poolID, "_", 2)[1]
	timestamp := cognitotest.NowString()
	sig, err := client.ComputeSignature(
		poolName, username, password,
		resp.ChallengeParameters.Salt, resp.ChallengeParameters.SRPB,
		resp.ChallengeParameters.SecretBlock, timestamp,
	)
	require.NoError(t, err)

	return doPasswordVerifier(t, ro, clientID, resp.Session, map[string]string{
		"USERNAME":                    username,
		"PASSWORD_CLAIM_SECRET_BLOCK": resp.ChallengeParameters.SecretBlock,
		"TIMESTAMP":                   timestamp,
		"PASSWORD_CLAIM_SIGNATURE":    sig,
	})
}

// initSRPChallenge drives USER_SRP_AUTH's InitiateAuth step for username
// against ro, using a fresh cognitotest.SRPClient, and returns the client
// plus the decoded PASSWORD_VERIFIER challenge — for tests that need to
// customize the RespondToAuthChallenge step themselves.
func initSRPChallenge(
	t *testing.T, ro *Router, clientID, username string,
) (*cognitotest.SRPClient, srpInitiateAuthResponse) {
	t.Helper()
	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)

	w := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": username,
		"SRP_A":    client.AHex(),
	})
	require.Equal(t, http.StatusOK, w.Code)

	var resp srpInitiateAuthResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return client, resp
}

// ── padHex / srpVerifierFor unit tests ───────────────────────────────────────

func TestPadHex(t *testing.T) {
	tests := []struct {
		name string
		n    *big.Int
		want []byte
	}{
		{"zero", big.NewInt(0), []byte{0x00}},
		{"low byte, no MSB", big.NewInt(0x14), []byte{0x14}},
		{"high nibble set requires 00 prefix", big.NewInt(0x80), []byte{0x00, 0x80}},
		{"already even length, high bit set", big.NewInt(0xFF14), []byte{0x00, 0xFF, 0x14}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, padHex(tt.n))
		})
	}
}

func TestPadHex_NegativePanics(t *testing.T) {
	assert.Panics(t, func() {
		padHex(big.NewInt(-1))
	})
}

func TestSRPVerifierFor(t *testing.T) {
	salt1, verifier1, err := srpVerifierFor("us-east-1_abc123", "alice", "Password123!")
	require.NoError(t, err)
	assert.NotEmpty(t, salt1)
	assert.NotEmpty(t, verifier1)

	// A fresh derivation uses a new random salt, so repeated calls (even with
	// the same inputs) must not collide.
	salt2, verifier2, err := srpVerifierFor("us-east-1_abc123", "alice", "Password123!")
	require.NoError(t, err)
	assert.NotEqual(t, salt1, salt2)
	assert.NotEqual(t, verifier1, verifier2)

	// verifier must be parseable back into a big.Int (stored as plain hex).
	_, ok := new(big.Int).SetString(verifier1, 16)
	assert.True(t, ok)
}

// ── InitiateAuth: USER_SRP_AUTH ───────────────────────────────────────────────

func TestUserSRPAuth_Success(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "alice", "Password123!")
	confirmUser(t, ro, clientID, "alice")

	w := srpLogin(t, ro, poolID, clientID, "alice", "Password123!")
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		AuthenticationResult struct {
			AccessToken  string `json:"AccessToken"`
			IdToken      string `json:"IdToken"`
			RefreshToken string `json:"RefreshToken"`
		} `json:"AuthenticationResult"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.NotEmpty(t, resp.AuthenticationResult.AccessToken)
	assert.NotEmpty(t, resp.AuthenticationResult.IdToken)
	assert.NotEmpty(t, resp.AuthenticationResult.RefreshToken)
}

func TestUserSRPAuth_MissingUsername(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)

	w := doInitSRPAuth(t, ro, clientID, map[string]string{"SRP_A": client.AHex()})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestUserSRPAuth_MissingSRPA(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	w := doInitSRPAuth(t, ro, clientID, map[string]string{"USERNAME": "alice"})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestUserSRPAuth_InvalidSRPA_NotHex(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	w := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": "alice", "SRP_A": "not-hex!!",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestUserSRPAuth_InvalidSRPA_Negative(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	w := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": "alice", "SRP_A": "-1",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestUserSRPAuth_InvalidSRPA_ModNZero(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	w := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": "alice", "SRP_A": "0",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestUserSRPAuth_UnknownUser(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)

	w := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": "ghost", "SRP_A": client.AHex(),
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotFoundException)
}

func TestUserSRPAuth_UserDisabled(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "bob", "Password123!")
	confirmUser(t, ro, clientID, "bob")

	w := doOp(t, ro, "AdminDisableUser", mustJSON(map[string]any{
		"UserPoolId": mustPoolID(t, ro, clientID), "Username": "bob",
	}))
	require.Equal(t, http.StatusOK, w.Code)

	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)
	w2 := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": "bob", "SRP_A": client.AHex(),
	})
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestUserSRPAuth_UserUnconfirmed(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "carol", "Password123!")

	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)
	w := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": "carol", "SRP_A": client.AHex(),
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeUserNotConfirmedException)
}

func TestUserSRPAuth_NoSRPVerifierStored(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	storage := ro.storage.(*Storage)
	// Simulate a user created before this feature existed: PasswordHash set,
	// but no SRPVerifier.
	hash, err := hashPassword("Password123!", ro.bcryptCost)
	require.NoError(t, err)
	require.NoError(t, storage.CreateUser(poolID, &UserMetadata{
		Username: "dave", Sub: "dave-sub", Status: userStatusConfirmed,
		Enabled: true, PasswordHash: hash,
	}))

	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)
	w := doInitSRPAuth(t, ro, clientID, map[string]string{
		"USERNAME": "dave", "SRP_A": client.AHex(),
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestUserSRPAuth_GetUserStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "us-east-1_test", nil },
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("boom")
		},
	}}
	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)

	w := doInitSRPAuth(t, ro, "client-1", map[string]string{
		"USERNAME": "eve", "SRP_A": client.AHex(),
	})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestUserSRPAuth_GetPoolKeysStorageError(t *testing.T) {
	saltHex, verifierHex, err := srpVerifierFor("us-east-1_test", "frank", "Password123!")
	require.NoError(t, err)
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "us-east-1_test", nil },
		getUserFn: func(string, string) (*UserMetadata, error) {
			return &UserMetadata{
				Username: "frank", Status: userStatusConfirmed, Enabled: true,
				SRPSalt: saltHex, SRPVerifier: verifierHex,
			}, nil
		},
	}}
	client, err := cognitotest.NewSRPClient()
	require.NoError(t, err)

	w := doInitSRPAuth(t, ro, "client-1", map[string]string{
		"USERNAME": "frank", "SRP_A": client.AHex(),
	})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

// ── RespondToAuthChallenge: PASSWORD_VERIFIER ─────────────────────────────────

func TestPasswordVerifierChallenge_WrongPassword(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "grace", "Password123!")
	confirmUser(t, ro, clientID, "grace")

	// Log in with the wrong password: the client-side math will produce a
	// signature that does not match the stored verifier.
	w := srpLogin(t, ro, poolID, clientID, "grace", "WrongPassword1!")
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestPasswordVerifierChallenge_TamperedSecretBlock(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "heidi", "Password123!")
	confirmUser(t, ro, clientID, "heidi")

	client, resp := initSRPChallenge(t, ro, clientID, "heidi")

	poolName := strings.SplitN(poolID, "_", 2)[1]
	timestamp := cognitotest.NowString()
	sig, err := client.ComputeSignature(
		poolName, "heidi", "Password123!",
		resp.ChallengeParameters.Salt, resp.ChallengeParameters.SRPB,
		resp.ChallengeParameters.SecretBlock, timestamp,
	)
	require.NoError(t, err)

	// PASSWORD_CLAIM_SECRET_BLOCK below is valid base64, but not the SECRET_BLOCK
	// the signature was computed over — not a credential, just an opaque nonce
	// echoed back in the protocol.
	w2 := doPasswordVerifier(t, ro, clientID, resp.Session, map[string]string{ //nolint:gosec
		"USERNAME":                    "heidi",
		"PASSWORD_CLAIM_SECRET_BLOCK": "dGFtcGVyZWQ=",
		"TIMESTAMP":                   timestamp,
		"PASSWORD_CLAIM_SIGNATURE":    sig,
	})
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestPasswordVerifierChallenge_InvalidSecretBlockBase64(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "ivan", "Password123!")
	confirmUser(t, ro, clientID, "ivan")

	_, resp := initSRPChallenge(t, ro, clientID, "ivan")
	_ = poolID

	// Not a credential — an opaque nonce that happens to be invalid base64 here.
	w2 := doPasswordVerifier(t, ro, clientID, resp.Session, map[string]string{ //nolint:gosec
		"USERNAME":                    "ivan",
		"PASSWORD_CLAIM_SECRET_BLOCK": "not-valid-base64!!",
		"TIMESTAMP":                   cognitotest.NowString(),
		"PASSWORD_CLAIM_SIGNATURE":    "AAAA",
	})
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeInvalidParameterException)
}

func TestPasswordVerifierChallenge_MissingRequiredFields(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)

	w := doPasswordVerifier(t, ro, clientID, "some-session", map[string]string{})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeInvalidParameterException)
}

func TestPasswordVerifierChallenge_ExpiredSession(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "judy", "Password123!")
	confirmUser(t, ro, clientID, "judy")

	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)
	expiredToken, err := buildJWT(privateKey, keys.KeyID, map[string]any{
		"pool_id":    poolID,
		"username":   "judy",
		"challenge":  "PASSWORD_VERIFIER",
		"srp_a":      "00",
		"srp_b_priv": "01",
		"iat":        int64(0),
		"exp":        int64(0),
	})
	require.NoError(t, err)

	w := doPasswordVerifier(t, ro, clientID, expiredToken, map[string]string{
		"PASSWORD_CLAIM_SECRET_BLOCK": "AAAA",
		"TIMESTAMP":                   "x",
		"PASSWORD_CLAIM_SIGNATURE":    "AAAA",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestPasswordVerifierChallenge_WrongChallengeInSession(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "karl", "Password123!")
	confirmUser(t, ro, clientID, "karl")

	keys, privateKey, err := ro.storage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)
	session, err := buildSessionToken(
		privateKey, keys.KeyID, poolID, "karl", "NEW_PASSWORD_REQUIRED", nil,
	)
	require.NoError(t, err)

	w := doPasswordVerifier(t, ro, clientID, session, map[string]string{
		"PASSWORD_CLAIM_SECRET_BLOCK": "AAAA",
		"TIMESTAMP":                   "x",
		"PASSWORD_CLAIM_SIGNATURE":    "AAAA",
	})
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assertErrType(t, w, ErrTypeNotAuthorizedException)
}

func TestPasswordVerifierChallenge_UsernameMismatch(t *testing.T) {
	ro := newTestRouter(t)
	_, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "leo", "Password123!")
	confirmUser(t, ro, clientID, "leo")

	_, resp := initSRPChallenge(t, ro, clientID, "leo")

	w2 := doPasswordVerifier(t, ro, clientID, resp.Session, map[string]string{
		"USERNAME":                    "someone-else",
		"PASSWORD_CLAIM_SECRET_BLOCK": resp.ChallengeParameters.SecretBlock,
		"TIMESTAMP":                   cognitotest.NowString(),
		"PASSWORD_CLAIM_SIGNATURE":    "AAAA",
	})
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestPasswordVerifierChallenge_GetPoolKeysStorageError(t *testing.T) {
	ro := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return "us-east-1_test", nil },
	}}
	w := doPasswordVerifier(t, ro, "client-1", "some-session", map[string]string{
		"PASSWORD_CLAIM_SECRET_BLOCK": "AAAA",
		"TIMESTAMP":                   "x",
		"PASSWORD_CLAIM_SIGNATURE":    "AAAA",
	})
	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assertErrType(t, w, ErrTypeInternalErrorException)
}

func TestPasswordVerifierChallenge_UsernameFromSessionClaim(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "nina", "Password123!")
	confirmUser(t, ro, clientID, "nina")

	client, resp := initSRPChallenge(t, ro, clientID, "nina")

	poolName := strings.SplitN(poolID, "_", 2)[1]
	timestamp := cognitotest.NowString()
	sig, err := client.ComputeSignature(
		poolName, "nina", "Password123!",
		resp.ChallengeParameters.Salt, resp.ChallengeParameters.SRPB,
		resp.ChallengeParameters.SecretBlock, timestamp,
	)
	require.NoError(t, err)

	// USERNAME omitted from ChallengeResponses: falls back to the session's claim.
	w2 := doPasswordVerifier(t, ro, clientID, resp.Session, map[string]string{
		"PASSWORD_CLAIM_SECRET_BLOCK": resp.ChallengeParameters.SecretBlock,
		"TIMESTAMP":                   timestamp,
		"PASSWORD_CLAIM_SIGNATURE":    sig,
	})
	assert.Equal(t, http.StatusOK, w2.Code)
}

func TestPasswordVerifierChallenge_UserDeletedAfterInitiateAuth(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "oscar", "Password123!")
	confirmUser(t, ro, clientID, "oscar")

	client, resp := initSRPChallenge(t, ro, clientID, "oscar")

	require.NoError(t, ro.storage.DeleteUser(poolID, "oscar"))

	poolName := strings.SplitN(poolID, "_", 2)[1]
	timestamp := cognitotest.NowString()
	sig, err := client.ComputeSignature(
		poolName, "oscar", "Password123!",
		resp.ChallengeParameters.Salt, resp.ChallengeParameters.SRPB,
		resp.ChallengeParameters.SecretBlock, timestamp,
	)
	require.NoError(t, err)

	w2 := doPasswordVerifier(t, ro, clientID, resp.Session, map[string]string{
		"USERNAME":                    "oscar",
		"PASSWORD_CLAIM_SECRET_BLOCK": resp.ChallengeParameters.SecretBlock,
		"TIMESTAMP":                   timestamp,
		"PASSWORD_CLAIM_SIGNATURE":    sig,
	})
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeUserNotFoundException)
}

func TestPasswordVerifierChallenge_VerifierClearedAfterInitiateAuth(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "quinn", "Password123!")
	confirmUser(t, ro, clientID, "quinn")

	client, resp := initSRPChallenge(t, ro, clientID, "quinn")

	// Simulate the user's password being reset (clearing SRPVerifier) between
	// InitiateAuth and RespondToAuthChallenge.
	require.NoError(t, ro.storage.UpdateUser(poolID, "quinn", func(u *UserMetadata) error {
		u.SRPVerifier = ""
		return nil
	}))

	poolName := strings.SplitN(poolID, "_", 2)[1]
	timestamp := cognitotest.NowString()
	sig, err := client.ComputeSignature(
		poolName, "quinn", "Password123!",
		resp.ChallengeParameters.Salt, resp.ChallengeParameters.SRPB,
		resp.ChallengeParameters.SecretBlock, timestamp,
	)
	require.NoError(t, err)

	w2 := doPasswordVerifier(t, ro, clientID, resp.Session, map[string]string{
		"USERNAME":                    "quinn",
		"PASSWORD_CLAIM_SECRET_BLOCK": resp.ChallengeParameters.SecretBlock,
		"TIMESTAMP":                   timestamp,
		"PASSWORD_CLAIM_SIGNATURE":    sig,
	})
	assert.Equal(t, http.StatusBadRequest, w2.Code)
	assertErrType(t, w2, ErrTypeNotAuthorizedException)
}

func TestPasswordVerifierChallenge_GetUserStorageError(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)
	signUpUser(t, ro, clientID, "peggy", "Password123!")
	confirmUser(t, ro, clientID, "peggy")

	client, resp := initSRPChallenge(t, ro, clientID, "peggy")

	realStorage := ro.storage.(*Storage)
	keys, privateKey, err := realStorage.GetOrCreatePoolKeys(poolID)
	require.NoError(t, err)

	mockRo := &Router{storage: &mockStore{
		getPoolForClient: func(string) (string, error) { return poolID, nil },
		getOrCreateKeysFn: func(string) (*poolKeys, *rsa.PrivateKey, error) {
			return keys, privateKey, nil
		},
		getUserFn: func(string, string) (*UserMetadata, error) {
			return nil, errors.New("boom")
		},
	}}

	poolName := strings.SplitN(poolID, "_", 2)[1]
	timestamp := cognitotest.NowString()
	sig, err := client.ComputeSignature(
		poolName, "peggy", "Password123!",
		resp.ChallengeParameters.Salt, resp.ChallengeParameters.SRPB,
		resp.ChallengeParameters.SecretBlock, timestamp,
	)
	require.NoError(t, err)

	w2 := doPasswordVerifier(t, mockRo, clientID, resp.Session, map[string]string{
		"USERNAME":                    "peggy",
		"PASSWORD_CLAIM_SECRET_BLOCK": resp.ChallengeParameters.SecretBlock,
		"TIMESTAMP":                   timestamp,
		"PASSWORD_CLAIM_SIGNATURE":    sig,
	})
	assert.Equal(t, http.StatusInternalServerError, w2.Code)
	assertErrType(t, w2, ErrTypeInternalErrorException)
}

func TestPasswordVerifierChallenge_ForceChangePasswordChaining(t *testing.T) {
	ro := newTestRouter(t)
	poolID, clientID := setupPool(t, ro)

	saltHex, verifierHex, err := srpVerifierFor(poolID, "mia", "TempPass123!")
	require.NoError(t, err)
	hash, err := hashPassword("TempPass123!", ro.bcryptCost)
	require.NoError(t, err)
	storage := ro.storage.(*Storage)
	require.NoError(t, storage.CreateUser(poolID, &UserMetadata{
		Username: "mia", Sub: "mia-sub", Status: userStatusForceChangePasswd,
		Enabled: true, PasswordHash: hash, SRPSalt: saltHex, SRPVerifier: verifierHex,
	}))

	w := srpLogin(t, ro, poolID, clientID, "mia", "TempPass123!")
	require.Equal(t, http.StatusOK, w.Code)

	var resp srpInitiateAuthResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, "NEW_PASSWORD_REQUIRED", resp.ChallengeName)
	require.NotEmpty(t, resp.Session)

	// Complete the chained NEW_PASSWORD_REQUIRED challenge via the
	// already-implemented handler, to confirm the chain works end-to-end.
	w2 := doOp(t, ro, "RespondToAuthChallenge", mustJSON(map[string]any{
		"ClientId":      clientID,
		"ChallengeName": "NEW_PASSWORD_REQUIRED",
		"Session":       resp.Session,
		"ChallengeResponses": map[string]string{
			"USERNAME":     "mia",
			"NEW_PASSWORD": "NewPassword123!",
		},
	}))
	assert.Equal(t, http.StatusOK, w2.Code)
}

// ── small JSON helpers ────────────────────────────────────────────────────────

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func mustPoolID(t *testing.T, ro *Router, clientID string) string {
	t.Helper()
	poolID, err := ro.storage.GetPoolIDForClient(clientID)
	require.NoError(t, err)
	return poolID
}
