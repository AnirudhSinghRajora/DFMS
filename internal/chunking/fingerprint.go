package chunking

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
)

// SHA256Hash computes the SHA-256 digest of the given data and returns
// it as a lowercase hex-encoded string. Used for chunk content addressing.
func SHA256Hash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// SHA256Stream computes the SHA-256 digest of data read from an io.Reader
// without buffering the entire content in memory. Returns the hex digest
// and the total number of bytes read.
func SHA256Stream(r io.Reader) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, r)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// FileChecksum computes the SHA-256 digest of an entire file from a reader.
// This is used to verify download integrity against the stored checksum.
func FileChecksum(r io.Reader) (string, error) {
	digest, _, err := SHA256Stream(r)
	return digest, err
}
