// Package srptest implements the client side of AWS Cognito's SRP-6a
// authentication protocol, for use by internal/cognito's unit tests and
// tests/integration to drive kumolo's server-side USER_SRP_AUTH /
// PASSWORD_VERIFIER implementation end-to-end. It is not part of the shipped
// kumolo binary. See docs/aws-spec/cognito/srp_protocol.md for the protocol.
package srptest

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"math/big"
	"time"

	"github.com/optiflowic/kumolo/internal/cognito/srpmath"
)

// SRPClient implements the client side of one SRP authentication attempt:
// it holds the ephemeral private value a and public value A = g^a mod N.
type SRPClient struct {
	a *big.Int
	A *big.Int
}

// ephemeralPrivateKeyBytes is the byte length of the entropy used to derive
// the client's ephemeral private value a.
const ephemeralPrivateKeyBytes = 128

// NewSRPClient generates a fresh ephemeral client keypair.
func NewSRPClient() (*SRPClient, error) {
	aBytes := make([]byte, ephemeralPrivateKeyBytes)
	if _, err := rand.Read(aBytes); err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return nil, fmt.Errorf("read entropy: %w", err)
	}
	a := new(big.Int).SetBytes(aBytes)
	A := new(big.Int).Exp(srpmath.G, a, srpmath.N)
	return &SRPClient{a: a, A: A}, nil
}

// AHex returns the client's public value A, to send as SRP_A in
// InitiateAuth's AuthParameters.
func (c *SRPClient) AHex() string {
	return c.A.Text(16)
}

// ComputeSignature computes PASSWORD_CLAIM_SIGNATURE for the
// PASSWORD_VERIFIER challenge response, given the account's plaintext
// password and the fields returned by InitiateAuth's USER_SRP_AUTH response.
func (c *SRPClient) ComputeSignature(
	userPoolName, username, password, saltHex, serverBHex, secretBlockB64, timestamp string,
) (string, error) {
	salt, ok := new(big.Int).SetString(saltHex, 16)
	if !ok {
		return "", fmt.Errorf("invalid SALT hex")
	}
	serverB, ok := new(big.Int).SetString(serverBHex, 16)
	if !ok {
		return "", fmt.Errorf("invalid SRP_B hex")
	}

	u := new(big.Int).SetBytes(srpmath.SHA256Concat(srpmath.PadHex(c.A), srpmath.PadHex(serverB)))
	if u.Sign() == 0 {
		// unreachable: constructing A, B such that SHA256(padHex(A)||padHex(B)) == 0
		// is a preimage attack, computationally infeasible to hit in a test
		return "", fmt.Errorf("u cannot be zero")
	}

	innerHash := srpmath.SHA256Concat([]byte(userPoolName + username + ":" + password))
	x := new(big.Int).SetBytes(srpmath.SHA256Concat(srpmath.PadHex(salt), innerHash))

	gx := new(big.Int).Exp(srpmath.G, x, srpmath.N)
	base := new(big.Int).Sub(serverB, new(big.Int).Mul(srpmath.K, gx))
	base.Mod(base, srpmath.N)
	if base.Sign() == 0 {
		return "", fmt.Errorf("invalid server B value")
	}

	exponent := new(big.Int).Add(c.a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exponent, srpmath.N)

	key := srpmath.DeriveSessionKey(u, S)

	secretBlockRaw, err := base64.StdEncoding.DecodeString(secretBlockB64)
	if err != nil {
		return "", fmt.Errorf("decode secret block: %w", err)
	}

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(userPoolName))
	mac.Write([]byte(username))
	mac.Write(secretBlockRaw)
	mac.Write([]byte(timestamp))

	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// NowString returns the current UTC time formatted as Cognito's TIMESTAMP
// ChallengeResponses field expects: "ddd MMM D HH:mm:ss UTC YYYY" (the day of
// month is not zero-padded; hour/minute/second are).
func NowString() string {
	now := time.Now().UTC()
	return fmt.Sprintf("%s %s %d %02d:%02d:%02d UTC %d",
		now.Format("Mon"), now.Format("Jan"), now.Day(),
		now.Hour(), now.Minute(), now.Second(), now.Year())
}
