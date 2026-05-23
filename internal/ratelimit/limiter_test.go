package ratelimit_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/AnirudhSinghRajora/DFMS/internal/ratelimit"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() { rdb.Close() })

	return mr, rdb
}

func TestAllow_UnderLimit(t *testing.T) {
	_, rdb := setupRedis(t)
	limiter := ratelimit.NewLimiter(rdb)

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		allowed := limiter.Allow(c, "rl:test", 10, time.Minute)
		if allowed {
			c.Status(http.StatusOK)
		} else {
			c.Status(http.StatusTooManyRequests)
		}
	})

	// 5 requests with limit 10 → all should pass
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "Request %d should be allowed", i+1)
	}
}

func TestAllow_ExactLimit(t *testing.T) {
	_, rdb := setupRedis(t)
	limiter := ratelimit.NewLimiter(rdb)

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		allowed := limiter.Allow(c, "rl:exact", 5, time.Minute)
		if allowed {
			c.Status(http.StatusOK)
		} else {
			c.Status(http.StatusTooManyRequests)
		}
	})

	// Exactly 5 requests should pass
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code, "Request %d should be allowed", i+1)
	}

	// 6th request should be rejected
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code, "6th request should be rejected")
}

func TestAllow_SlidingWindow(t *testing.T) {
	_, rdb := setupRedis(t)
	limiter := ratelimit.NewLimiter(rdb)

	// Use a very short window (100ms) so we can test real expiry
	window := 100 * time.Millisecond

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		allowed := limiter.Allow(c, "rl:sliding", 2, window)
		if allowed {
			c.Status(http.StatusOK)
		} else {
			c.Status(http.StatusTooManyRequests)
		}
	})

	// Fill up the limit
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	// Next request should be blocked
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// Wait for the window to expire
	time.Sleep(150 * time.Millisecond)

	// Should be allowed again
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAllow_RedisDown_FailOpen(t *testing.T) {
	mr, rdb := setupRedis(t)
	limiter := ratelimit.NewLimiter(rdb)

	// Close Redis to simulate failure
	mr.Close()

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		allowed := limiter.Allow(c, "rl:down", 1, time.Minute)
		if allowed {
			c.Status(http.StatusOK)
		} else {
			c.Status(http.StatusTooManyRequests)
		}
	})

	// Should fail-open (allow request even though Redis is down)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGlobalMiddleware_Blocks(t *testing.T) {
	_, rdb := setupRedis(t)
	limiter := ratelimit.NewLimiter(rdb)

	router := gin.New()
	router.Use(ratelimit.GlobalRateLimitMiddleware(limiter, 3))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// 3 requests should pass
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	// 4th should be blocked
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
	assert.Contains(t, w.Body.String(), "rate limit")
}

func TestUserMiddleware_PerUser(t *testing.T) {
	_, rdb := setupRedis(t)
	limiter := ratelimit.NewLimiter(rdb)

	router := gin.New()
	// Simulate auth middleware setting user_id
	router.Use(func(c *gin.Context) {
		c.Set("user_id", c.GetHeader("X-User-ID"))
		c.Next()
	})
	router.Use(ratelimit.UserRateLimitMiddleware(limiter, 2))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// User A: 2 requests → pass
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-User-ID", "user-a")
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	// User A: 3rd → blocked
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-User-ID", "user-a")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)

	// User B: still allowed (separate bucket)
	w = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("X-User-ID", "user-b")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestUserMiddleware_NoUser(t *testing.T) {
	_, rdb := setupRedis(t)
	limiter := ratelimit.NewLimiter(rdb)

	router := gin.New()
	// No auth middleware → no user_id in context
	router.Use(ratelimit.UserRateLimitMiddleware(limiter, 1))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	// Should skip per-user limiting when no user context
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/test", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}
}

func TestEndpointMiddleware(t *testing.T) {
	_, rdb := setupRedis(t)
	limiter := ratelimit.NewLimiter(rdb)

	router := gin.New()
	router.Use(ratelimit.EndpointRateLimitMiddleware(limiter, 2))
	router.GET("/api/upload", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/upload", nil)
		router.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	}

	// 3rd request → blocked
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/upload", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}
