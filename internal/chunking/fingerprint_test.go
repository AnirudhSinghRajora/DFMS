package chunking_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/chunking"
)

// Known SHA-256 test vectors from NIST
func TestSHA256Hash_KnownVector(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "",
			expected: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		},
		{
			input:    "hello",
			expected: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
		},
		{
			input:    "abc",
			expected: "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := chunking.SHA256Hash([]byte(tt.input))
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSHA256Hash_Length(t *testing.T) {
	// SHA-256 should always produce 64 hex characters
	result := chunking.SHA256Hash([]byte("any data"))
	assert.Len(t, result, 64)
	// All lowercase hex
	assert.Equal(t, result, strings.ToLower(result))
}

func TestSHA256Hash_Deterministic(t *testing.T) {
	data := []byte("deterministic test data")
	h1 := chunking.SHA256Hash(data)
	h2 := chunking.SHA256Hash(data)
	assert.Equal(t, h1, h2)
}

func TestSHA256Stream_MatchesHash(t *testing.T) {
	data := []byte("stream test data that could be large")

	// Compute hash via direct function
	directHash := chunking.SHA256Hash(data)

	// Compute hash via stream
	streamHash, n, err := chunking.SHA256Stream(bytes.NewReader(data))
	require.NoError(t, err)

	assert.Equal(t, directHash, streamHash)
	assert.Equal(t, int64(len(data)), n)
}

func TestSHA256Stream_LargeData(t *testing.T) {
	// 1MB of zeros
	data := make([]byte, 1024*1024)
	directHash := chunking.SHA256Hash(data)

	streamHash, n, err := chunking.SHA256Stream(bytes.NewReader(data))
	require.NoError(t, err)

	assert.Equal(t, directHash, streamHash)
	assert.Equal(t, int64(1024*1024), n)
}

func TestSHA256Stream_Empty(t *testing.T) {
	hash, n, err := chunking.SHA256Stream(bytes.NewReader(nil))
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hash)
}

func TestFileChecksum(t *testing.T) {
	data := []byte("file checksum test data")
	expected := chunking.SHA256Hash(data)

	checksum, err := chunking.FileChecksum(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, expected, checksum)
}
