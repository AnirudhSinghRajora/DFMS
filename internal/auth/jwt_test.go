package auth_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/auth"
	"github.com/AnirudhSinghRajora/DFMS/internal/config"
	"github.com/AnirudhSinghRajora/DFMS/tests/testutil"
)

func newTestService(t *testing.T) *auth.JWTService {
	t.Helper()
	return testutil.NewTestJWTService(t)
}

func TestGenerateTokenPair_Success(t *testing.T) {
	svc := newTestService(t)

	pair, err := svc.GenerateTokenPair("user-123", "test@example.com", "user")
	require.NoError(t, err)

	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, "Bearer", pair.TokenType)
	assert.Greater(t, pair.ExpiresIn, int64(0))
	// Access and refresh tokens must be different
	assert.NotEqual(t, pair.AccessToken, pair.RefreshToken)
}

func TestValidateAccessToken_Valid(t *testing.T) {
	svc := newTestService(t)

	pair, err := svc.GenerateTokenPair("user-456", "alice@example.com", "admin")
	require.NoError(t, err)

	claims, err := svc.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)

	assert.Equal(t, "user-456", claims.UserID)
	assert.Equal(t, "alice@example.com", claims.Email)
	assert.Equal(t, "admin", claims.Role)
	assert.Equal(t, "dfms-test", claims.Issuer)
	assert.Contains(t, claims.Audience, "dfms-api")
}

func TestValidateAccessToken_Expired(t *testing.T) {
	privKey, pubKey := testutil.GenerateTestKeyPair(t)
	svc := auth.NewJWTServiceFromKeys(privKey, pubKey, config.JWTConfig{
		AccessTTL:  -1 * time.Hour, // Already expired
		RefreshTTL: 168 * time.Hour,
		Issuer:     "dfms-test",
	})

	pair, err := svc.GenerateTokenPair("user-789", "bob@example.com", "user")
	require.NoError(t, err)

	_, err = svc.ValidateAccessToken(pair.AccessToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token is expired")
}

func TestValidateAccessToken_WrongAlgorithm(t *testing.T) {
	svc := newTestService(t)

	// Create an HMAC-signed token (algorithm confusion attack)
	hmacToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":   "attacker",
		"email": "evil@example.com",
		"role":  "admin",
		"iss":   "dfms-test",
		"aud":   []string{"dfms-api"},
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	tokenStr, err := hmacToken.SignedString([]byte("secret"))
	require.NoError(t, err)

	_, err = svc.ValidateAccessToken(tokenStr)
	assert.Error(t, err)
}

func TestValidateAccessToken_TamperedPayload(t *testing.T) {
	svc := newTestService(t)

	pair, err := svc.GenerateTokenPair("user-legit", "legit@example.com", "user")
	require.NoError(t, err)

	// Tamper: change a character in the payload section (2nd segment)
	token := pair.AccessToken
	if len(token) > 50 {
		runes := []rune(token)
		// Find the first dot (end of header), then modify a character in payload
		dotCount := 0
		for i, r := range runes {
			if r == '.' {
				dotCount++
			}
			if dotCount == 1 && r != '.' {
				// Flip a character in the payload
				if runes[i] == 'A' {
					runes[i] = 'B'
				} else {
					runes[i] = 'A'
				}
				break
			}
		}
		token = string(runes)
	}

	_, err = svc.ValidateAccessToken(token)
	assert.Error(t, err)
}

func TestValidateRefreshToken_Valid(t *testing.T) {
	svc := newTestService(t)

	pair, err := svc.GenerateTokenPair("user-refresh", "refresh@example.com", "user")
	require.NoError(t, err)

	claims, err := svc.ValidateRefreshToken(pair.RefreshToken)
	require.NoError(t, err)

	assert.Equal(t, "user-refresh", claims.UserID)
	assert.NotEmpty(t, claims.TokenFamily)
	assert.Equal(t, "dfms-test", claims.Issuer)
}

func TestValidateRefreshToken_Expired(t *testing.T) {
	privKey, pubKey := testutil.GenerateTestKeyPair(t)
	svc := auth.NewJWTServiceFromKeys(privKey, pubKey, config.JWTConfig{
		AccessTTL:  15 * time.Minute,
		RefreshTTL: -1 * time.Hour, // Already expired
		Issuer:     "dfms-test",
	})

	pair, err := svc.GenerateTokenPair("user-exp", "exp@example.com", "user")
	require.NoError(t, err)

	_, err = svc.ValidateRefreshToken(pair.RefreshToken)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "token is expired")
}

func TestValidateAccessToken_WrongKey(t *testing.T) {
	svc1 := newTestService(t)
	svc2 := newTestService(t) // Different key pair

	pair, err := svc1.GenerateTokenPair("user-wrong", "wrong@example.com", "user")
	require.NoError(t, err)

	// Try to validate with a different service (different public key)
	_, err = svc2.ValidateAccessToken(pair.AccessToken)
	assert.Error(t, err)
}

func TestNewJWTService_LoadFromFiles(t *testing.T) {
	privPath, pubPath := testutil.WriteTestKeys(t)

	svc, err := auth.NewJWTService(config.JWTConfig{
		PrivateKeyPath: privPath,
		PublicKeyPath:  pubPath,
		AccessTTL:      15 * time.Minute,
		RefreshTTL:     168 * time.Hour,
		Issuer:         "dfms-test",
	})
	require.NoError(t, err)

	pair, err := svc.GenerateTokenPair("file-user", "file@example.com", "user")
	require.NoError(t, err)

	claims, err := svc.ValidateAccessToken(pair.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, "file-user", claims.UserID)
}

func TestNewJWTService_BadPrivateKeyPath(t *testing.T) {
	_, err := auth.NewJWTService(config.JWTConfig{
		PrivateKeyPath: "/nonexistent/private.pem",
		PublicKeyPath:  "/nonexistent/public.pem",
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "private key")
}

func TestNewJWTServiceFromKeys(t *testing.T) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	svc := auth.NewJWTServiceFromKeys(privKey, &privKey.PublicKey, config.JWTConfig{
		AccessTTL:  5 * time.Minute,
		RefreshTTL: 24 * time.Hour,
		Issuer:     "test",
	})

	pair, err := svc.GenerateTokenPair("direct-key", "dk@example.com", "admin")
	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)
}
