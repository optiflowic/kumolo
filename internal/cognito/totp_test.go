package cognito

import (
	"encoding/base32"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rfc6238Secret is the 20-byte ASCII secret "12345678901234567890" used by the RFC 6238
// Appendix B SHA1 test vectors, base32-encoded (RFC 4648, no padding).
const rfc6238Secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" //nolint:gosec // published RFC 6238 test vector, not a real credential

func TestTotpCodeAt_RFC6238Vector(t *testing.T) {
	// T=59s → counter 59/30=1. RFC 6238's published 8-digit code is 94287082;
	// kumolo always truncates to 6 digits, i.e. its low-order 6 digits: 287082.
	code, err := totpCodeAt(rfc6238Secret, 1)
	require.NoError(t, err)
	assert.Equal(t, "287082", code)
}

func TestTotpCodeAt_InvalidSecret(t *testing.T) {
	_, err := totpCodeAt("not-valid-base32!!!", 1)
	assert.Error(t, err)
}

func TestTotpCodeAt_LeadingZeroPadded(t *testing.T) {
	// Find a counter whose truncated code has a leading zero, to exercise the %0*d
	// zero-padding path (a bare Sprintf("%d", ...) would silently drop the digit count).
	secret, err := generateTOTPSecret()
	require.NoError(t, err)
	found := false
	for counter := range uint64(2000) {
		code, cerr := totpCodeAt(secret, counter)
		require.NoError(t, cerr)
		require.Len(t, code, totpDigits)
		if code[0] == '0' {
			found = true
			break
		}
	}
	assert.True(t, found, "expected at least one zero-padded code in the search range")
}

func TestGenerateTOTPSecret_ValidBase32AndLength(t *testing.T) {
	secret, err := generateTOTPSecret()
	require.NoError(t, err)
	assert.NotEmpty(t, secret)

	decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	require.NoError(t, err)
	assert.Len(t, decoded, totpSecretBytes)
}

func TestGenerateTOTPSecret_Unique(t *testing.T) {
	s1, err := generateTOTPSecret()
	require.NoError(t, err)
	s2, err := generateTOTPSecret()
	require.NoError(t, err)
	assert.NotEqual(t, s1, s2)
}

func TestVerifyTOTP_CurrentStepValid(t *testing.T) {
	secret, err := generateTOTPSecret()
	require.NoError(t, err)
	now := int64(1_700_000_000)
	code, err := totpCodeAt(secret, totpStep(now))
	require.NoError(t, err)
	assert.True(t, verifyTOTP(secret, code, now))
}

func TestVerifyTOTP_PreviousStepWithinSkewValid(t *testing.T) {
	secret, err := generateTOTPSecret()
	require.NoError(t, err)
	now := int64(1_700_000_000)
	prevStep := totpStep(now) - 1
	code, err := totpCodeAt(secret, prevStep)
	require.NoError(t, err)
	assert.True(t, verifyTOTP(secret, code, now))
}

func TestVerifyTOTP_NextStepWithinSkewValid(t *testing.T) {
	secret, err := generateTOTPSecret()
	require.NoError(t, err)
	now := int64(1_700_000_000)
	nextStep := totpStep(now) + 1
	code, err := totpCodeAt(secret, nextStep)
	require.NoError(t, err)
	assert.True(t, verifyTOTP(secret, code, now))
}

func TestVerifyTOTP_OutsideSkewRejected(t *testing.T) {
	secret, err := generateTOTPSecret()
	require.NoError(t, err)
	now := int64(1_700_000_000)
	farStep := totpStep(now) + 2
	code, err := totpCodeAt(secret, farStep)
	require.NoError(t, err)
	assert.False(t, verifyTOTP(secret, code, now))
}

func TestVerifyTOTP_WrongCodeRejected(t *testing.T) {
	secret, err := generateTOTPSecret()
	require.NoError(t, err)
	assert.False(t, verifyTOTP(secret, "000000", int64(1_700_000_000)))
}

func TestVerifyTOTP_NearEpochNoUnderflow(t *testing.T) {
	// step counter is 0 at unixTime < totpStepSeconds; delta=-1 must not underflow
	// the uint64 counter subtraction.
	secret, err := generateTOTPSecret()
	require.NoError(t, err)
	code, err := totpCodeAt(secret, 0)
	require.NoError(t, err)
	assert.True(t, verifyTOTP(secret, code, 10))
}

func TestVerifyTOTP_InvalidSecretRejected(t *testing.T) {
	assert.False(t, verifyTOTP("not-valid-base32!!!", "123456", 1_700_000_000))
}
