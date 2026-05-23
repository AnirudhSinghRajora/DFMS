package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/auth"
	"github.com/AnirudhSinghRajora/DFMS/tests/testutil"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupRouter(jwtSvc *auth.JWTService) *gin.Engine {
	r := gin.New()
	r.Use(auth.JWTAuthMiddleware(jwtSvc))
	r.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"user_id": c.GetString("user_id"),
			"email":   c.GetString("user_email"),
			"role":    c.GetString("user_role"),
		})
	})
	return r
}

func TestJWTMiddleware_ValidToken(t *testing.T) {
	svc := testutil.NewTestJWTService(t)
	router := setupRouter(svc)

	pair, err := svc.GenerateTokenPair("user-mid-1", "mid@example.com", "user")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "user-mid-1")
	assert.Contains(t, w.Body.String(), "mid@example.com")
	assert.Contains(t, w.Body.String(), "user")
}

func TestJWTMiddleware_MissingHeader(t *testing.T) {
	svc := testutil.NewTestJWTService(t)
	router := setupRouter(svc)

	req := httptest.NewRequest("GET", "/protected", nil)
	// No Authorization header
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_TOKEN_MISSING")
}

func TestJWTMiddleware_InvalidScheme(t *testing.T) {
	svc := testutil.NewTestJWTService(t)
	router := setupRouter(svc)

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "AUTH_TOKEN_INVALID")
}

func TestJWTMiddleware_ExpiredToken(t *testing.T) {
	svc := testutil.NewTestJWTService(t)
	router := setupRouter(svc)

	// Pass a malformed/invalid token to trigger 401
	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer eyJhbGciOiJFUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.garbage")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestJWTMiddleware_EmptyBearer(t *testing.T) {
	svc := testutil.NewTestJWTService(t)
	router := setupRouter(svc)

	req := httptest.NewRequest("GET", "/protected", nil)
	req.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireRole_Allowed(t *testing.T) {
	svc := testutil.NewTestJWTService(t)

	r := gin.New()
	r.Use(auth.JWTAuthMiddleware(svc))
	r.Use(auth.RequireRole("admin"))
	r.GET("/admin", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	pair, err := svc.GenerateTokenPair("admin-user", "admin@example.com", "admin")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireRole_Forbidden(t *testing.T) {
	svc := testutil.NewTestJWTService(t)

	r := gin.New()
	r.Use(auth.JWTAuthMiddleware(svc))
	r.Use(auth.RequireRole("admin"))
	r.GET("/admin", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	pair, err := svc.GenerateTokenPair("normal-user", "user@example.com", "user")
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/admin", nil)
	req.Header.Set("Authorization", "Bearer "+pair.AccessToken)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestGetUserID(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("user_id", "test-user-id")

	assert.Equal(t, "test-user-id", auth.GetUserID(c))
}

func TestGetUserID_Missing(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.Equal(t, "", auth.GetUserID(c))
}

func TestGetUserEmail(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("user_email", "test@example.com")

	assert.Equal(t, "test@example.com", auth.GetUserEmail(c))
}
