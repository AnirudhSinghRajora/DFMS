// Package cache provides Redis client management and caching utilities.
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/AnirudhSinghRajora/DFMS/internal/config"
)

// Client wraps the go-redis client with DFMS-specific functionality.
type Client struct {
	rdb *redis.Client
}

// NewClient creates and validates a new Redis connection.
func NewClient(cfg config.RedisConfig) (*Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         cfg.Addr,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		MinIdleConns: 5,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
	})

	// Verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

// Redis returns the underlying go-redis client for direct access.
func (c *Client) Redis() *redis.Client {
	return c.rdb
}

// Close closes the Redis connection.
func (c *Client) Close() error {
	return c.rdb.Close()
}

// HealthCheck verifies the Redis connection is alive.
func (c *Client) HealthCheck(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// Set stores a key-value pair with a TTL.
func (c *Client) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	return c.rdb.Set(ctx, key, value, ttl).Err()
}

// Get retrieves a value by key. Returns redis.Nil if not found.
func (c *Client) Get(ctx context.Context, key string) (string, error) {
	return c.rdb.Get(ctx, key).Result()
}

// Delete removes one or more keys.
func (c *Client) Delete(ctx context.Context, keys ...string) error {
	return c.rdb.Del(ctx, keys...).Err()
}

// Exists checks if a key exists. Returns true if it does.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	result, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// SetNX sets a key only if it does not exist (distributed lock pattern).
// Returns true if the key was set, false if it already existed.
func (c *Client) SetNX(ctx context.Context, key string, value interface{}, ttl time.Duration) (bool, error) {
	return c.rdb.SetNX(ctx, key, value, ttl).Result()
}

// HSet sets a field in a Redis hash.
func (c *Client) HSet(ctx context.Context, key, field string, value interface{}) error {
	return c.rdb.HSet(ctx, key, field, value).Err()
}

// HGetAll returns all fields and values in a Redis hash.
func (c *Client) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return c.rdb.HGetAll(ctx, key).Result()
}

// Expire sets a TTL on an existing key.
func (c *Client) Expire(ctx context.Context, key string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, key, ttl).Err()
}

