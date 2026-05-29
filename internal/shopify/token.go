package shopify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

const tokenTTL = 30 * 24 * time.Hour // 30 days

// tokenKey returns the Redis key for a shop's access token.
func tokenKey(shop string) string {
	return "shopify:access_token:" + shop
}

// ─── Redis abstraction ────────────────────────────────────────────────────────

// RedisCache is the minimal Redis interface required for token caching.
// The narrow interface makes the tokenCache easy to test with a mock.
type RedisCache interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
}

// redisAdapter wraps *redis.Client to satisfy RedisCache.
// redis.Nil (key-not-found) is translated to ("", nil) so callers can treat
// an empty string as a cache miss without handling a sentinel error.
type redisAdapter struct {
	client *redis.Client
}

// NewRedisAdapter wraps a *redis.Client as a RedisCache.
func NewRedisAdapter(client *redis.Client) RedisCache {
	return &redisAdapter{client: client}
}

func (a *redisAdapter) Get(ctx context.Context, key string) (string, error) {
	val, err := a.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", nil // cache miss — not an error
	}
	return val, err
}

func (a *redisAdapter) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	return a.client.Set(ctx, key, value, ttl).Err()
}

// ─── TokenProvider ────────────────────────────────────────────────────────────

// TokenProvider manages Shopify access tokens with Redis caching.
// Implementations must be safe for concurrent use.
type TokenProvider interface {
	// GetAccessToken returns a valid access token for the given shop.
	// It checks Redis first, then falls back to the SHOPIFY_ADMIN_API_TOKEN
	// environment variable.
	GetAccessToken(ctx context.Context, shop string) (string, error)

	// CacheToken stores an access token in Redis under the shop key.
	// Call this after a successful OAuth token exchange so future requests
	// skip the exchange and read from the cache.
	CacheToken(ctx context.Context, shop, token string) error
}

// ─── tokenCache ───────────────────────────────────────────────────────────────

// tokenCache implements TokenProvider backed by a RedisCache.
type tokenCache struct {
	redis RedisCache
}

// NewTokenCache returns a TokenProvider that caches tokens in the given RedisCache.
func NewTokenCache(r RedisCache) TokenProvider {
	return &tokenCache{redis: r}
}

// GetAccessToken returns the access token for the given shop.
//
// Resolution order:
//  1. Redis key  shopify:access_token:{shop}  (cache hit)
//  2. Env var    SHOPIFY_ADMIN_API_TOKEN
//     The env-var value is then written back to Redis (TTL = 30 days) so that
//     the next call is a cache hit.
//
// If Redis is unavailable the error is logged and the lookup falls through to
// the env var — the feature degrades gracefully rather than failing hard.
func (tc *tokenCache) GetAccessToken(ctx context.Context, shop string) (string, error) {
	key := tokenKey(shop)

	// 1. Try Redis.
	token, err := tc.redis.Get(ctx, key)
	if err != nil {
		log.Printf("[shopify] token cache error for %s: %v (falling back to env)", shop, err)
	} else if token != "" {
		log.Printf("[shopify] token cache hit for %s", shop)
		return token, nil
	} else {
		log.Printf("[shopify] token cache miss for %s", shop)
	}

	// 2. Fall back to environment variable.
	token = os.Getenv("SHOPIFY_ADMIN_API_TOKEN")
	if token == "" {
		return "", fmt.Errorf("shopify: access token not configured for shop %s (set SHOPIFY_ADMIN_API_TOKEN)", shop)
	}

	// 3. Back-fill the cache so the next request is a hit.
	if setErr := tc.redis.Set(ctx, key, token, tokenTTL); setErr != nil {
		log.Printf("[shopify] token cache set failed for %s: %v", shop, setErr)
	} else {
		log.Printf("[shopify] token cached for %s (sourced from env)", shop)
	}

	return token, nil
}

// CacheToken stores a newly-obtained OAuth access token in Redis.
// Call this from the OAuth callback handler immediately after a successful
// token exchange.
func (tc *tokenCache) CacheToken(ctx context.Context, shop, token string) error {
	if err := tc.redis.Set(ctx, tokenKey(shop), token, tokenTTL); err != nil {
		log.Printf("[shopify] token cache set failed for %s: %v", shop, err)
		return fmt.Errorf("shopify: token cache: %w", err)
	}
	log.Printf("[shopify] token cached for %s", shop)
	return nil
}
