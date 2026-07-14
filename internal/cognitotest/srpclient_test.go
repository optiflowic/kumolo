package cognitotest

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPadHex(t *testing.T) {
	tests := []struct {
		name string
		n    *big.Int
		want []byte
	}{
		{"zero", big.NewInt(0), []byte{0x00}},
		{"low byte, no MSB", big.NewInt(0x14), []byte{0x14}},
		{"high nibble set requires 00 prefix", big.NewInt(0x80), []byte{0x00, 0x80}},
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

func TestSha256Concat(t *testing.T) {
	a := sha256Concat([]byte("foo"), []byte("bar"))
	b := sha256Concat([]byte("foobar"))
	assert.Equal(t, b, a, "concatenating parts must hash identically to a single pre-joined slice")
}

func TestNewSRPClient(t *testing.T) {
	client, err := NewSRPClient()
	require.NoError(t, err)
	require.NotNil(t, client.a)
	require.NotNil(t, client.A)
	assert.NotEmpty(t, client.AHex())
}

func TestNowString(t *testing.T) {
	// "ddd MMM D HH:mm:ss UTC YYYY" — day-of-month is not zero-padded.
	re := regexp.MustCompile(
		`^[A-Z][a-z]{2} [A-Z][a-z]{2} \d{1,2} \d{2}:\d{2}:\d{2} UTC \d{4}$`,
	)
	assert.Regexp(t, re, NowString())
}

// serverSRPChallenge is a self-contained simulation of the server-side SRP
// state needed to drive SRPClient.ComputeSignature in these tests: it derives
// a verifier from a password the same way internal/cognito's srpVerifierFor
// does, then computes B and a SECRET_BLOCK the way handleUserSRPAuth does.
type serverSRPChallenge struct {
	saltHex        string
	serverBHex     string
	secretBlockB64 string
	timestamp      string
	key            []byte // K, for computing the independently-expected signature
}

func newServerSRPChallenge(
	t *testing.T,
	clientA *big.Int,
	userPoolName, username, password string,
) serverSRPChallenge {
	t.Helper()
	saltBytes := make([]byte, 16)
	_, err := rand.Read(saltBytes)
	require.NoError(t, err)
	saltPadded := padHex(new(big.Int).SetBytes(saltBytes))

	innerHash := sha256Concat([]byte(userPoolName + username + ":" + password))
	x := new(big.Int).SetBytes(sha256Concat(saltPadded, innerHash))
	v := new(big.Int).Exp(srpG, x, srpN)

	b, err := rand.Int(rand.Reader, srpN)
	require.NoError(t, err)
	gb := new(big.Int).Exp(srpG, b, srpN)
	kv := new(big.Int).Mul(srpK, v)
	B := new(big.Int).Mod(new(big.Int).Add(kv, gb), srpN)

	secretBlock := make([]byte, 32)
	_, err = rand.Read(secretBlock)
	require.NoError(t, err)

	// Server-side S = (A * v^u mod N)^b mod N.
	u := new(big.Int).SetBytes(sha256Concat(padHex(clientA), padHex(B)))
	require.NotZero(t, u.Sign())
	vu := new(big.Int).Exp(v, u, srpN)
	avu := new(big.Int).Mod(new(big.Int).Mul(clientA, vu), srpN)
	S := new(big.Int).Exp(avu, b, srpN)
	key := deriveSRPKey(u, S)

	return serverSRPChallenge{
		saltHex:        hex.EncodeToString(saltPadded),
		serverBHex:     hex.EncodeToString(padHex(B)),
		secretBlockB64: base64.StdEncoding.EncodeToString(secretBlock),
		timestamp:      NowString(),
		key:            key,
	}
}

func (ch serverSRPChallenge) expectedSignature(userPoolName, username string) string {
	secretBlockRaw, _ := base64.StdEncoding.DecodeString(ch.secretBlockB64)
	mac := hmac.New(sha256.New, ch.key)
	mac.Write([]byte(userPoolName))
	mac.Write([]byte(username))
	mac.Write(secretBlockRaw)
	mac.Write([]byte(ch.timestamp))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestComputeSignature_Success(t *testing.T) {
	const (
		userPoolName = "TestPool123"
		username     = "alice"
		password     = "Password123!"
	)
	client, err := NewSRPClient()
	require.NoError(t, err)

	ch := newServerSRPChallenge(t, client.A, userPoolName, username, password)

	sig, err := client.ComputeSignature(
		userPoolName,
		username,
		password,
		ch.saltHex,
		ch.serverBHex,
		ch.secretBlockB64,
		ch.timestamp,
	)
	require.NoError(t, err)
	assert.Equal(t, ch.expectedSignature(userPoolName, username), sig)
}

func TestComputeSignature_InvalidSaltHex(t *testing.T) {
	client, err := NewSRPClient()
	require.NoError(t, err)

	_, err = client.ComputeSignature("pool", "alice", "pw", "not-hex!!", "abcd", "AAAA", "ts")
	assert.Error(t, err)
}

func TestComputeSignature_InvalidSRPBHex(t *testing.T) {
	client, err := NewSRPClient()
	require.NoError(t, err)

	_, err = client.ComputeSignature("pool", "alice", "pw", "abcd", "not-hex!!", "AAAA", "ts")
	assert.Error(t, err)
}

func TestComputeSignature_MaliciousServerB_SmallSubgroupAttack(t *testing.T) {
	const (
		userPoolName = "TestPool123"
		username     = "alice"
		password     = "Password123!"
	)
	client, err := NewSRPClient()
	require.NoError(t, err)

	saltBytes := make([]byte, 16)
	_, err = rand.Read(saltBytes)
	require.NoError(t, err)
	saltPadded := padHex(new(big.Int).SetBytes(saltBytes))

	innerHash := sha256Concat([]byte(userPoolName + username + ":" + password))
	x := new(big.Int).SetBytes(sha256Concat(saltPadded, innerHash))
	gx := new(big.Int).Exp(srpG, x, srpN)

	// A malicious/compromised server sending B ≡ k*g^x (mod N) would drive
	// the client's shared secret to zero absent the base != 0 check.
	maliciousB := new(big.Int).Mod(new(big.Int).Mul(srpK, gx), srpN)

	_, err = client.ComputeSignature(
		userPoolName, username, password,
		hex.EncodeToString(saltPadded),
		hex.EncodeToString(padHex(maliciousB)),
		base64.StdEncoding.EncodeToString([]byte("secretblock1234")),
		NowString(),
	)
	assert.ErrorContains(t, err, "invalid server B value")
}

func TestComputeSignature_InvalidSecretBlockBase64(t *testing.T) {
	const (
		userPoolName = "TestPool123"
		username     = "alice"
		password     = "Password123!"
	)
	client, err := NewSRPClient()
	require.NoError(t, err)
	ch := newServerSRPChallenge(t, client.A, userPoolName, username, password)

	_, err = client.ComputeSignature(
		userPoolName,
		username,
		password,
		ch.saltHex,
		ch.serverBHex,
		"not-valid-base64!!",
		ch.timestamp,
	)
	assert.Error(t, err)
}
