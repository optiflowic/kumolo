// Package srpmath implements the bit-exact primitives of AWS Cognito's SRP-6a
// protocol variant: the safe prime N, generator g, and multiplier k; the
// big.Int-to-byte padding used throughout the protocol's hash/HMAC inputs;
// and Cognito's non-standard key derivation. internal/cognito (the SRP
// server) and internal/cognito/srptest (a test-only SRP client) both need
// byte-identical output from these — there's no protocol-level room for
// divergence — so they live here once instead of being reimplemented on
// each side. See docs/aws-spec/cognito/srp_protocol.md for the protocol
// these implement.
package srpmath

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
)

// nHex is the 3072-bit safe prime used by AWS Cognito's SRP-6a group.
const nHex = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD1" +
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

// N, G, and K are the SRP-6a group parameters: the safe prime modulus, the
// generator, and the multiplier k = SHA256(PadHex(N) || PadHex(G)).
var (
	G = big.NewInt(2)
	N = mustParseHex(nHex)
	K = new(big.Int).SetBytes(SHA256Concat(PadHex(N), PadHex(G)))
)

func mustParseHex(hexStr string) *big.Int {
	n, ok := new(big.Int).SetString(hexStr, 16)
	if !ok {
		// unreachable: nHex is a fixed, valid hex literal
		panic("srpmath: invalid SRP N constant")
	}
	return n
}

// PadHex returns the two's-complement-safe byte encoding of a non-negative
// big.Int: hex-encode, pad to even length, and prepend a zero byte if the
// top bit of the first byte would otherwise be set. Every value in this
// protocol (N, g, A, B, S, U, salt, verifier) is always non-negative, so only
// this positive branch is implemented. Real Cognito SDK clients apply the
// same padding before hashing/HMAC'ing these values, so it must match
// bit-for-bit — see docs/aws-spec/cognito/srp_protocol.md.
// Callers must reject negative input themselves; PadHex panics rather than
// silently producing wrong output if that precondition is violated.
func PadHex(n *big.Int) []byte {
	if n.Sign() < 0 {
		panic("srpmath: PadHex requires a non-negative big.Int")
	}
	h := n.Text(16)
	if len(h)%2 != 0 {
		h = "0" + h
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		// unreachable: h is always a valid hex string built from n.Text(16)
		panic("srpmath: PadHex produced invalid hex: " + err.Error())
	}
	if len(b) > 0 && b[0] >= 0x80 {
		b = append([]byte{0x00}, b...)
	}
	return b
}

// SHA256Concat hashes the concatenation of parts as a single SHA-256 input.
func SHA256Concat(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

// DeriveSessionKey derives the 16-byte SRP session key K from the shared
// secret S and scrambling parameter u, using Cognito's non-standard "HKDF":
// the salt/IKM roles are swapped relative to RFC 5869, and the info string
// is fixed.
func DeriveSessionKey(u, s *big.Int) []byte {
	prkMAC := hmac.New(sha256.New, PadHex(u))
	prkMAC.Write(PadHex(s))
	prk := prkMAC.Sum(nil)

	info := append([]byte("Caldera Derived Key"), 0x01)
	okmMAC := hmac.New(sha256.New, prk)
	okmMAC.Write(info)
	return okmMAC.Sum(nil)[:16]
}
