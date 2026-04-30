package s3

import (
	"crypto/sha1" //nolint:gosec // SHA1 used for data-integrity checking per S3 spec
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"net/http"
)

type checksumAlgorithm string

const (
	checksumCRC32     checksumAlgorithm = "CRC32"
	checksumCRC32C    checksumAlgorithm = "CRC32C"
	checksumSHA1      checksumAlgorithm = "SHA1"
	checksumSHA256    checksumAlgorithm = "SHA256"
	checksumCRC64NVME checksumAlgorithm = "CRC64NVME"
)

// checksumHeaderName maps each algorithm to its canonical HTTP header name.
var checksumHeaderName = map[checksumAlgorithm]string{
	checksumCRC32:     "X-Amz-Checksum-Crc32",
	checksumCRC32C:    "X-Amz-Checksum-Crc32c",
	checksumSHA1:      "X-Amz-Checksum-Sha1",
	checksumSHA256:    "X-Amz-Checksum-Sha256",
	checksumCRC64NVME: "X-Amz-Checksum-Crc64nvme",
}

func newChecksumHash(algo checksumAlgorithm) hash.Hash {
	switch algo {
	case checksumCRC32:
		return crc32.NewIEEE()
	case checksumCRC32C:
		return crc32.New(crc32.MakeTable(crc32.Castagnoli))
	case checksumSHA1:
		return sha1.New() //nolint:gosec // SHA1 used for data-integrity checking per S3 spec
	case checksumSHA256:
		return sha256.New()
	case checksumCRC64NVME:
		return newCRC64NVMEHash()
	default:
		return nil
	}
}

// parseChecksumHeaders reads x-amz-sdk-checksum-algorithm and the corresponding
// x-amz-checksum-* header. Returns (nil, nil, true) when the algorithm header is absent,
// (hash, expected, true) when both headers are present and valid, or (nil, nil, false)
// after writing an error response (unknown algorithm, missing checksum header, bad digest).
func parseChecksumHeaders(w http.ResponseWriter, r *http.Request) (hash.Hash, []byte, bool) {
	algoStr := r.Header.Get("X-Amz-Sdk-Checksum-Algorithm")
	if algoStr == "" {
		return nil, nil, true
	}
	algo := checksumAlgorithm(algoStr)
	headerName, known := checksumHeaderName[algo]
	if !known {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"Unsupported checksum algorithm: "+algoStr)
		return nil, nil, false
	}
	encoded := r.Header.Get(headerName)
	if encoded == "" {
		writeError(w, r, http.StatusBadRequest, "InvalidArgument",
			"Missing checksum header for algorithm: "+algoStr)
		return nil, nil, false
	}
	expected, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "InvalidDigest",
			"The checksum value you specified is invalid.")
		return nil, nil, false
	}
	h := newChecksumHash(algo)
	if len(expected) != h.Size() {
		writeError(w, r, http.StatusBadRequest, "InvalidDigest",
			"The checksum value you specified is invalid.")
		return nil, nil, false
	}
	return h, expected, true
}

// CRC-64/NVME: reflected polynomial 0x9A6C9329AC4BC9B5 (LSB-first) matches Go's crc64
// reflected algorithm and produces values identical to the AWS SDK Go v2 implementation.
// init=0xFFFFFFFFFFFFFFFF and xorout=0xFFFFFFFFFFFFFFFF are applied by crc64.Update's
// internal ^crc at start/end when called with crc=0.

var crc64NVMETable = crc64.MakeTable(0x9A6C9329AC4BC9B5)

type crc64NVMEHash struct {
	crc uint64
}

func newCRC64NVMEHash() hash.Hash {
	// crc64.Update applies ^crc internally, so we pass 0 to get effective init=0xFFFF...
	return &crc64NVMEHash{crc: 0}
}

func (h *crc64NVMEHash) Write(p []byte) (int, error) {
	h.crc = crc64.Update(h.crc, crc64NVMETable, p)
	return len(p), nil
}

func (h *crc64NVMEHash) Sum(b []byte) []byte {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], h.crc)
	return append(b, buf[:]...)
}

func (h *crc64NVMEHash) Reset()         { h.crc = 0 }
func (h *crc64NVMEHash) Size() int      { return 8 }
func (h *crc64NVMEHash) BlockSize() int { return 1 }
