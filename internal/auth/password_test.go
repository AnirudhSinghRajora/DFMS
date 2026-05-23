package auth_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/auth"
)

func TestHashPassword_Success(t *testing.T) {
	hash, err := auth.HashPassword("validpass123")
	require.NoError(t, err)

	assert.NotEmpty(t, hash)
	assert.NotEqual(t, "validpass123", hash)
	// bcrypt hashes always start with "$2" prefix
	assert.Regexp(t, `^\$2[aby]?\$`, hash)
}

func TestHashPassword_TooShort(t *testing.T) {
	_, err := auth.HashPassword("short")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at least 8 characters")
}

func TestHashPassword_ExactlyMinLength(t *testing.T) {
	hash, err := auth.HashPassword("12345678")
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestHashPassword_TooLong(t *testing.T) {
	// 73 characters exceeds bcrypt's 72-byte limit
	longPass := make([]byte, 73)
	for i := range longPass {
		longPass[i] = 'a'
	}
	_, err := auth.HashPassword(string(longPass))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "at most 72")
}

func TestHashPassword_ExactlyMaxLength(t *testing.T) {
	maxPass := make([]byte, 72)
	for i := range maxPass {
		maxPass[i] = 'b'
	}
	hash, err := auth.HashPassword(string(maxPass))
	require.NoError(t, err)
	assert.NotEmpty(t, hash)
}

func TestHashPassword_DifferentHashesForSameInput(t *testing.T) {
	// bcrypt uses random salt, so same password → different hashes
	h1, err := auth.HashPassword("samepassword")
	require.NoError(t, err)
	h2, err := auth.HashPassword("samepassword")
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2)
}

func TestVerifyPassword_Correct(t *testing.T) {
	password := "correctpassword"
	hash, err := auth.HashPassword(password)
	require.NoError(t, err)

	err = auth.VerifyPassword(hash, password)
	assert.NoError(t, err)
}

func TestVerifyPassword_Wrong(t *testing.T) {
	hash, err := auth.HashPassword("rightpassword")
	require.NoError(t, err)

	err = auth.VerifyPassword(hash, "wrongpassword")
	assert.Error(t, err)
}

func TestVerifyPassword_EmptyHash(t *testing.T) {
	err := auth.VerifyPassword("", "password")
	assert.Error(t, err)
}
