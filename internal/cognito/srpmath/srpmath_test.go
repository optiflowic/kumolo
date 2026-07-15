package srpmath

import (
	"math/big"
	"testing"

	"github.com/stretchr/testify/assert"
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
		{"already even length, high bit set", big.NewInt(0xFF14), []byte{0x00, 0xFF, 0x14}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, PadHex(tt.n))
		})
	}
}

func TestPadHex_NegativePanics(t *testing.T) {
	assert.Panics(t, func() {
		PadHex(big.NewInt(-1))
	})
}

func TestSHA256Concat(t *testing.T) {
	a := SHA256Concat([]byte("foo"), []byte("bar"))
	b := SHA256Concat([]byte("foobar"))
	assert.Equal(t, b, a, "concatenating parts must hash identically to a single pre-joined slice")
}

func TestDeriveSessionKey(t *testing.T) {
	u := big.NewInt(123)
	s := big.NewInt(456)

	key1 := DeriveSessionKey(u, s)
	assert.Len(t, key1, 16)

	// Deterministic: same inputs must derive the same key.
	key2 := DeriveSessionKey(u, s)
	assert.Equal(t, key1, key2)

	// Different inputs must derive a different key.
	key3 := DeriveSessionKey(big.NewInt(789), s)
	assert.NotEqual(t, key1, key3)
}

func TestGroupParameters(t *testing.T) {
	assert.NotNil(t, N)
	assert.NotNil(t, G)
	assert.NotNil(t, K)
	assert.Equal(t, 0, G.Cmp(big.NewInt(2)))
	assert.Equal(t, 3072, N.BitLen())
}
