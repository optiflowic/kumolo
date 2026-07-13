// Package cognitotest implements the client side of AWS Cognito's SRP-6a
// authentication protocol, for use by internal/cognito's unit tests and
// tests/integration to drive kumolo's server-side USER_SRP_AUTH /
// PASSWORD_VERIFIER implementation end-to-end. It is not part of the shipped
// kumolo binary. See docs/aws-spec/cognito/srp_protocol.md for the protocol.
package cognitotest

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"time"
)

// srpNHex is the same 3072-bit safe prime as internal/cognito/srp.go's srpN.
// Duplicated as a literal (not imported) since internal/cognito exports no
// SRP constants and this package must be usable from both internal/cognito's
// white-box tests and tests/integration's separate binary.
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
		panic("cognitotest: invalid SRP N constant")
	}
	srpK = new(big.Int).SetBytes(sha256Concat(padHex(srpN), padHex(srpG)))
}

// padHex mirrors internal/cognito/srp.go's padHex: the two's-complement-safe
// byte encoding of a non-negative big.Int (hex-encode, pad to even length,
// prepend a zero byte if the top bit would otherwise be set).
func padHex(n *big.Int) []byte {
	h := n.Text(16)
	if len(h)%2 != 0 {
		h = "0" + h
	}
	b, _ := hex.DecodeString(h)
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

// SRPClient implements the client side of one SRP authentication attempt:
// it holds the ephemeral private value a and public value A = g^a mod N.
type SRPClient struct {
	a *big.Int
	A *big.Int
}

// NewSRPClient generates a fresh ephemeral client keypair.
func NewSRPClient() (*SRPClient, error) {
	aBytes := make([]byte, 128)
	if _, err := rand.Read(aBytes); err != nil {
		// untestable: crypto/rand.Read only fails on OS-level entropy source errors
		return nil, fmt.Errorf("read entropy: %w", err)
	}
	a := new(big.Int).SetBytes(aBytes)
	A := new(big.Int).Exp(srpG, a, srpN)
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

	u := new(big.Int).SetBytes(sha256Concat(padHex(c.A), padHex(serverB)))
	if u.Sign() == 0 {
		// unreachable: constructing A, B such that SHA256(padHex(A)||padHex(B)) == 0
		// is a preimage attack, computationally infeasible to hit in a test
		return "", fmt.Errorf("u cannot be zero")
	}

	innerHash := sha256Concat([]byte(userPoolName + username + ":" + password))
	x := new(big.Int).SetBytes(sha256Concat(padHex(salt), innerHash))

	gx := new(big.Int).Exp(srpG, x, srpN)
	base := new(big.Int).Sub(serverB, new(big.Int).Mul(srpK, gx))
	base.Mod(base, srpN)

	exponent := new(big.Int).Add(c.a, new(big.Int).Mul(u, x))
	S := new(big.Int).Exp(base, exponent, srpN)

	key := deriveSRPKey(u, S)

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

// deriveSRPKey mirrors internal/cognito's key derivation: Cognito's
// non-standard "HKDF" with swapped salt/IKM roles and a fixed info string.
func deriveSRPKey(u, s *big.Int) []byte {
	prkMAC := hmac.New(sha256.New, padHex(u))
	prkMAC.Write(padHex(s))
	prk := prkMAC.Sum(nil)

	info := append([]byte("Caldera Derived Key"), 0x01)
	okmMAC := hmac.New(sha256.New, prk)
	okmMAC.Write(info)
	return okmMAC.Sum(nil)[:16]
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
