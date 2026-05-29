package shopify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ─── mockRedisCache ───────────────────────────────────────────────────────────

type setCaptured struct {
	key   string
	value string
	ttl   time.Duration
}

// mockRedisCache is a simple in-memory RedisCache for tests.
type mockRedisCache struct {
	data     map[string]string
	getErr   error
	setErr   error
	setCalls []setCaptured
}

func newMockRedis(initial map[string]string) *mockRedisCache {
	if initial == nil {
		initial = make(map[string]string)
	}
	return &mockRedisCache{data: initial}
}

func (m *mockRedisCache) Get(_ context.Context, key string) (string, error) {
	if m.getErr != nil {
		return "", m.getErr
	}
	return m.data[key], nil
}

func (m *mockRedisCache) Set(_ context.Context, key, value string, ttl time.Duration) error {
	if m.setErr != nil {
		return m.setErr
	}
	m.data[key] = value
	m.setCalls = append(m.setCalls, setCaptured{key: key, value: value, ttl: ttl})
	return nil
}

// ─── GetAccessToken tests ─────────────────────────────────────────────────────

func TestGetAccessToken_CacheHit(t *testing.T) {
	r := newMockRedis(map[string]string{
		"shopify:access_token:test.myshopify.com": "cached-token",
	})
	tc := &tokenCache{redis: r}

	token, err := tc.GetAccessToken(context.Background(), "test.myshopify.com")
	require.NoError(t, err)
	assert.Equal(t, "cached-token", token)
	assert.Empty(t, r.setCalls, "must not write to Redis on a cache hit")
}

func TestGetAccessToken_CacheMiss_FallsBackToEnv(t *testing.T) {
	t.Setenv("SHOPIFY_ADMIN_API_TOKEN", "env-token")

	r := newMockRedis(nil)
	tc := &tokenCache{redis: r}

	token, err := tc.GetAccessToken(context.Background(), "test.myshopify.com")
	require.NoError(t, err)
	assert.Equal(t, "env-token", token)

	// The env-var token must be written back to Redis.
	require.Len(t, r.setCalls, 1)
	assert.Equal(t, "shopify:access_token:test.myshopify.com", r.setCalls[0].key)
	assert.Equal(t, "env-token", r.setCalls[0].value)
	assert.Equal(t, tokenTTL, r.setCalls[0].ttl)
}

func TestGetAccessToken_NoTokenConfigured(t *testing.T) {
	t.Setenv("SHOPIFY_ADMIN_API_TOKEN", "")

	r := newMockRedis(nil)
	tc := &tokenCache{redis: r}

	_, err := tc.GetAccessToken(context.Background(), "test.myshopify.com")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "access token not configured")
}

func TestGetAccessToken_RedisGetError_FallsBackToEnv(t *testing.T) {
	t.Setenv("SHOPIFY_ADMIN_API_TOKEN", "env-token")

	r := newMockRedis(nil)
	r.getErr = errors.New("redis: connection refused")
	tc := &tokenCache{redis: r}

	// Redis failing must not block the request — fall back to env var.
	token, err := tc.GetAccessToken(context.Background(), "test.myshopify.com")
	require.NoError(t, err)
	assert.Equal(t, "env-token", token)
}

func TestGetAccessToken_RedisSetError_StillReturnsToken(t *testing.T) {
	t.Setenv("SHOPIFY_ADMIN_API_TOKEN", "env-token")

	r := newMockRedis(nil)
	r.setErr = errors.New("redis: write timeout")
	tc := &tokenCache{redis: r}

	// A failed cache write must not prevent the token from being returned.
	token, err := tc.GetAccessToken(context.Background(), "test.myshopify.com")
	require.NoError(t, err)
	assert.Equal(t, "env-token", token)
}

func TestGetAccessToken_CacheHit_DoesNotCallEnv(t *testing.T) {
	// Even if the env var would return a different token, the cached one wins.
	t.Setenv("SHOPIFY_ADMIN_API_TOKEN", "env-token")

	r := newMockRedis(map[string]string{
		"shopify:access_token:test.myshopify.com": "redis-token",
	})
	tc := &tokenCache{redis: r}

	token, err := tc.GetAccessToken(context.Background(), "test.myshopify.com")
	require.NoError(t, err)
	assert.Equal(t, "redis-token", token)
}

// ─── CacheToken tests ─────────────────────────────────────────────────────────

func TestCacheToken_Success(t *testing.T) {
	r := newMockRedis(nil)
	tc := &tokenCache{redis: r}

	err := tc.CacheToken(context.Background(), "test.myshopify.com", "new-token")
	require.NoError(t, err)

	require.Len(t, r.setCalls, 1)
	assert.Equal(t, "shopify:access_token:test.myshopify.com", r.setCalls[0].key)
	assert.Equal(t, "new-token", r.setCalls[0].value)
	assert.Equal(t, tokenTTL, r.setCalls[0].ttl)
	assert.Equal(t, "new-token", r.data["shopify:access_token:test.myshopify.com"])
}

func TestCacheToken_RedisError(t *testing.T) {
	r := newMockRedis(nil)
	r.setErr = errors.New("redis: write failed")
	tc := &tokenCache{redis: r}

	err := tc.CacheToken(context.Background(), "test.myshopify.com", "new-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "write failed")
}

// ─── Helper unit tests ────────────────────────────────────────────────────────

func TestTokenKey(t *testing.T) {
	assert.Equal(t, "shopify:access_token:mocbydylan.myshopify.com", tokenKey("mocbydylan.myshopify.com"))
}

func TestTokenTTL_Is30Days(t *testing.T) {
	assert.Equal(t, 30*24*time.Hour, tokenTTL)
}
