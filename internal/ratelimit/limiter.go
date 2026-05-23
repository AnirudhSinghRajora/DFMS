// Package ratelimit provides distributed rate limiting backed by Redis.
package ratelimit

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	apierrors "github.com/AnirudhSinghRajora/DFMS/pkg/errors"
)

// slidingWindowScript is a Redis Lua script that implements a sliding window
// rate limiter. It uses a sorted set to track request timestamps within the window.
// This approach provides accurate per-window counting without boundary burst issues.
const slidingWindowScript = `
local key = KEYS[1]
local window = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local window_start = now - window

-- Remove expired entries
redis.call('ZREMRANGEBYSCORE', key, '-inf', window_start)

-- Count current entries
local count = redis.call('ZCARD', key)

if count < limit then
    -- Allow request: add timestamp
    redis.call('ZADD', key, now, now .. '-' .. math.random(1000000))
    redis.call('EXPIRE', key, window)
    return 1
else
    -- Reject request
    return 0
end
`

// Limiter provides distributed rate limiting using Redis.
type Limiter struct {
	rdb    *redis.Client
	script *redis.Script
}

// NewLimiter creates a new rate limiter backed by Redis.
func NewLimiter(rdb *redis.Client) *Limiter {
	return &Limiter{
		rdb:    rdb,
		script: redis.NewScript(slidingWindowScript),
	}
}

// Allow checks if a request should be allowed under the rate limit.
// key: the rate limit bucket identifier (e.g., "rl:user:uuid" or "rl:global")
// limit: maximum requests allowed within the window
// window: the time window duration
func (l *Limiter) Allow(c *gin.Context, key string, limit int, window time.Duration) bool {
	now := float64(time.Now().UnixMicro())
	windowMicros := float64(window.Microseconds())

	result, err := l.script.Run(c.Request.Context(), l.rdb, []string{key}, windowMicros, limit, now).Int()
	if err != nil {
		// On Redis failure, allow the request (fail-open)
		// This prevents Redis outages from blocking all traffic
		return true
	}

	return result == 1
}

// GlobalRateLimitMiddleware creates a Gin middleware that enforces a global
// rate limit across all users and endpoints.
func GlobalRateLimitMiddleware(limiter *Limiter, rpm int) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !limiter.Allow(c, "rl:global", rpm, time.Minute) {
			apiErr := apierrors.NewRateLimited("Global rate limit exceeded. Please try again later.")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		c.Next()
	}
}

// UserRateLimitMiddleware creates a Gin middleware that enforces per-user
// rate limits. Must be placed after authentication middleware.
func UserRateLimitMiddleware(limiter *Limiter, rpm int) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("user_id")
		if !exists {
			// No user context — skip per-user limiting
			c.Next()
			return
		}

		key := "rl:user:" + userID.(string)
		if !limiter.Allow(c, key, rpm, time.Minute) {
			apiErr := apierrors.NewRateLimited("Per-user rate limit exceeded. Please slow down.")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		c.Next()
	}
}

// EndpointRateLimitMiddleware creates a Gin middleware that enforces per-endpoint
// rate limits for a specific route pattern.
func EndpointRateLimitMiddleware(limiter *Limiter, rpm int) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("user_id")
		identifier := c.ClientIP()
		if exists {
			identifier = userID.(string)
		}

		key := "rl:ep:" + c.FullPath() + ":" + identifier
		if !limiter.Allow(c, key, rpm, time.Minute) {
			apiErr := apierrors.NewRateLimited("Endpoint rate limit exceeded. Please try again later.")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, apiErr.ToResponse(c.GetString("request_id")))
			return
		}
		c.Next()
	}
}
