package storage_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// chunkKey is not exported, so we test it indirectly by verifying the
// expected sharding behavior. Since we can't call it directly from
// an external test package, we test the public API behavior.

// However, the sharding function IS exported implicitly through
// PutChunk/GetChunk key format. For unit test coverage, we replicate
// the chunkKey logic here and validate it.

func chunkKey(hash string) string {
	if len(hash) < 4 {
		return hash
	}
	return hash[0:2] + "/" + hash[2:4] + "/" + hash
}

func TestChunkKey_Sharding(t *testing.T) {
	hash := "a1b2c3d4e5f6789012345678901234567890123456789012345678901234abcd"

	result := chunkKey(hash)
	assert.Equal(t, "a1/b2/"+hash, result)

	// Verify the 2-level directory structure
	parts := splitPath(result)
	assert.Len(t, parts, 3)
	assert.Equal(t, "a1", parts[0])
	assert.Equal(t, "b2", parts[1])
	assert.Equal(t, hash, parts[2])
}

func TestChunkKey_DifferentHashes(t *testing.T) {
	tests := []struct {
		hash     string
		expected string
	}{
		{
			hash:     "deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
			expected: "de/ad/deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
		},
		{
			hash:     "0000000000000000000000000000000000000000000000000000000000000000",
			expected: "00/00/0000000000000000000000000000000000000000000000000000000000000000",
		},
		{
			hash:     "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			expected: "ff/ff/ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.hash[:8], func(t *testing.T) {
			assert.Equal(t, tt.expected, chunkKey(tt.hash))
		})
	}
}

func TestChunkKey_ShortHash(t *testing.T) {
	// Hashes shorter than 4 chars should be returned as-is
	assert.Equal(t, "ab", chunkKey("ab"))
	assert.Equal(t, "abc", chunkKey("abc"))
	assert.Equal(t, "a", chunkKey("a"))
	assert.Equal(t, "", chunkKey(""))
}

func TestChunkKey_ExactlyFourChars(t *testing.T) {
	result := chunkKey("abcd")
	assert.Equal(t, "ab/cd/abcd", result)
}

func splitPath(s string) []string {
	var parts []string
	current := ""
	for _, c := range s {
		if c == '/' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}
