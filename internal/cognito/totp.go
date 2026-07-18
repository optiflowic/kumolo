package cognito

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP mandates HMAC-SHA1; not used for anything else here
	"encoding/base32"
	"encoding/binary"
	"fmt"
)

const (
	totpSecretBytes = 20 // 160 bits, the RFC 6238 recommended HOTP secret length
	totpDigits      = 6
	totpStepSeconds = 30
	totpSkewSteps   = 1 // tolerate ±1 step (±30s) of client/server clock skew
)

// generateTOTPSecret returns a new random TOTP shared secret, base32-encoded
// (RFC 4648, no padding) so it can be typed into an authenticator app.
func generateTOTPSecret() (string, error) {
	b := make([]byte, totpSecretBytes)
	if _, err := rand.Read(b); err != nil {
		// untestable: crypto/rand.Read never errors in Go 1.20+
		return "", fmt.Errorf("read TOTP secret entropy: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b), nil
}

// totpCodeAt computes the RFC 6238 TOTP code for secret (base32-encoded) at the given step
// counter, per RFC 4226's HOTP algorithm with HMAC-SHA1.
func totpCodeAt(secret string, counter uint64) (string, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("decode TOTP secret: %w", err)
	}

	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])

	mod := uint32(1)
	for range totpDigits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, code%mod), nil
}

// totpStep returns the RFC 6238 time-step counter for unixTime.
// unixTime is always >= 0 (time.Now().Unix()), so the uint64 conversion cannot wrap.
func totpStep(unixTime int64) uint64 {
	return uint64(unixTime) / totpStepSeconds //nolint:gosec
}

// verifyTOTP reports whether userCode is a valid TOTP code for secret at unixTime, allowing
// ±totpSkewSteps steps of clock skew.
func verifyTOTP(secret string, userCode string, unixTime int64) bool {
	step := totpStep(unixTime)
	for delta := -totpSkewSteps; delta <= totpSkewSteps; delta++ {
		counter := step
		if delta < 0 {
			if uint64(-delta) > counter {
				continue
			}
			counter -= uint64(-delta)
		} else {
			counter += uint64(delta)
		}
		code, err := totpCodeAt(secret, counter)
		if err != nil {
			// In production secret is always written by generateTOTPSecret as valid
			// base32; this only guards against a malformed value reaching verifyTOTP.
			return false
		}
		if hmac.Equal([]byte(code), []byte(userCode)) {
			return true
		}
	}
	return false
}
