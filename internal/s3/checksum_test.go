package s3

import (
	"crypto/sha1" //nolint:gosec // SHA1 used for data-integrity checking per S3 spec
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCRC64NVME(t *testing.T) {
	t.Run("deterministic: same input yields same output", func(t *testing.T) {
		data := []byte("123456789")
		h1 := newCRC64NVMEHash()
		_, err := h1.Write(data)
		require.NoError(t, err)
		h2 := newCRC64NVMEHash()
		_, err = h2.Write(data)
		require.NoError(t, err)
		assert.Equal(t, h1.Sum(nil), h2.Sum(nil))
	})

	t.Run("different inputs yield different outputs", func(t *testing.T) {
		h1 := newCRC64NVMEHash()
		_, _ = h1.Write([]byte("hello"))
		h2 := newCRC64NVMEHash()
		_, _ = h2.Write([]byte("world"))
		assert.NotEqual(t, h1.Sum(nil), h2.Sum(nil))
	})

	t.Run("Reset clears state", func(t *testing.T) {
		h := newCRC64NVMEHash()
		_, err := h.Write([]byte("some data"))
		require.NoError(t, err)
		h.Reset()
		_, err = h.Write([]byte("123456789"))
		require.NoError(t, err)

		fresh := newCRC64NVMEHash()
		_, err = fresh.Write([]byte("123456789"))
		require.NoError(t, err)
		assert.Equal(t, fresh.Sum(nil), h.Sum(nil))
	})

	t.Run("empty input produces 8 bytes", func(t *testing.T) {
		h := newCRC64NVMEHash()
		_, err := h.Write([]byte{})
		require.NoError(t, err)
		assert.Len(t, h.Sum(nil), 8)
	})

	t.Run("Size is 8", func(t *testing.T) {
		assert.Equal(t, 8, newCRC64NVMEHash().Size())
	})

	t.Run("BlockSize is 1", func(t *testing.T) {
		assert.Equal(t, 1, newCRC64NVMEHash().BlockSize())
	})
}

func TestNewChecksumHash(t *testing.T) {
	data := []byte("hello world")

	t.Run("CRC32", func(t *testing.T) {
		h := newChecksumHash(checksumCRC32)
		require.NotNil(t, h)
		_, err := h.Write(data)
		require.NoError(t, err)
		want := crc32.ChecksumIEEE(data)
		got := binary.BigEndian.Uint32(h.Sum(nil))
		assert.Equal(t, want, got)
	})

	t.Run("CRC32C", func(t *testing.T) {
		h := newChecksumHash(checksumCRC32C)
		require.NotNil(t, h)
		_, err := h.Write(data)
		require.NoError(t, err)
		want := crc32.Checksum(data, crc32.MakeTable(crc32.Castagnoli))
		got := binary.BigEndian.Uint32(h.Sum(nil))
		assert.Equal(t, want, got)
	})

	t.Run("SHA1", func(t *testing.T) {
		h := newChecksumHash(checksumSHA1)
		require.NotNil(t, h)
		_, err := h.Write(data)
		require.NoError(t, err)
		want := sha1.Sum(data) //nolint:gosec
		assert.Equal(t, want[:], h.Sum(nil))
	})

	t.Run("SHA256", func(t *testing.T) {
		h := newChecksumHash(checksumSHA256)
		require.NotNil(t, h)
		_, err := h.Write(data)
		require.NoError(t, err)
		want := sha256.Sum256(data)
		assert.Equal(t, want[:], h.Sum(nil))
	})

	t.Run("CRC64NVME", func(t *testing.T) {
		h := newChecksumHash(checksumCRC64NVME)
		require.NotNil(t, h)
		assert.Equal(t, 8, h.Size())
	})

	t.Run("unknown algorithm returns nil", func(t *testing.T) {
		assert.Nil(t, newChecksumHash("UNKNOWN"))
	})
}

func TestParseChecksumHeaders(t *testing.T) {
	newReq := func(algo, value string) *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		if algo != "" {
			req.Header.Set("X-Amz-Sdk-Checksum-Algorithm", algo)
		}
		if value != "" {
			header := checksumHeaderName[checksumAlgorithm(algo)]
			req.Header.Set(header, value)
		}
		return req
	}

	crc32Sum := func(data []byte) string {
		h := newChecksumHash(checksumCRC32)
		_, _ = h.Write(data)
		return base64.StdEncoding.EncodeToString(h.Sum(nil))
	}

	t.Run("absent header returns nil hash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		w := httptest.NewRecorder()
		h, expected, ok := parseChecksumHeaders(w, req)
		require.True(t, ok)
		assert.Nil(t, h)
		assert.Nil(t, expected)
	})

	t.Run("unknown algorithm returns 400 InvalidArgument", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("X-Amz-Sdk-Checksum-Algorithm", "MD5")
		w := httptest.NewRecorder()
		_, _, ok := parseChecksumHeaders(w, req)
		assert.False(t, ok)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidArgument")
	})

	t.Run("algorithm present but no checksum header returns nil", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/", nil)
		req.Header.Set("X-Amz-Sdk-Checksum-Algorithm", "CRC32")
		w := httptest.NewRecorder()
		h, expected, ok := parseChecksumHeaders(w, req)
		require.True(t, ok)
		assert.Nil(t, h)
		assert.Nil(t, expected)
	})

	t.Run("valid CRC32 checksum header returns hash and expected bytes", func(t *testing.T) {
		data := []byte("hello")
		req := newReq("CRC32", crc32Sum(data))
		w := httptest.NewRecorder()
		h, expected, ok := parseChecksumHeaders(w, req)
		require.True(t, ok)
		assert.NotNil(t, h)
		assert.Len(t, expected, 4)
	})

	t.Run("invalid base64 returns 400 InvalidDigest", func(t *testing.T) {
		req := newReq("CRC32", "!!!not-base64!!!")
		w := httptest.NewRecorder()
		_, _, ok := parseChecksumHeaders(w, req)
		assert.False(t, ok)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidDigest")
	})

	t.Run("wrong length for algorithm returns 400 InvalidDigest", func(t *testing.T) {
		// CRC32 expects 4 bytes; provide 8 bytes (CRC64NVME size).
		req := newReq("CRC32", base64.StdEncoding.EncodeToString(make([]byte, 8)))
		w := httptest.NewRecorder()
		_, _, ok := parseChecksumHeaders(w, req)
		assert.False(t, ok)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "InvalidDigest")
	})
}
