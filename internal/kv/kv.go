// Package kv provides a Redis client for storing cart payloads and deduplication flags.
// Required env var: REDIS_URL (e.g. redis://default:password@host:6379)
// Falls back to REDIS_ADDR (host:port, default localhost:6379) when REDIS_URL is not set.
package kv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
)

var rdb *redis.Client

func init() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL != "" {
		opt, err := redis.ParseURL(redisURL)
		if err != nil {
			panic(fmt.Sprintf("kv: invalid REDIS_URL: %v", err))
		}
		rdb = redis.NewClient(opt)
	} else {
		addr := os.Getenv("REDIS_ADDR")
		if addr == "" {
			addr = "localhost:6379"
		}
		rdb = redis.NewClient(&redis.Options{Addr: addr})
	}
}

// LineItem mirrors a Shopify cart line item needed to create an order.
type LineItem struct {
	VariantID int64  `json:"variantId"`
	ProductID int64  `json:"productId"`
	Title     string `json:"title"`
	Quantity  int    `json:"quantity"`
	Price     int64  `json:"price"` // Shopify internal units (price × 100)
}

// CartPayload is stored against the paymentLinkId key in Redis.
// The webhook handler reads this to create the Shopify order.
type CartPayload struct {
	OrderCode       int64      `json:"orderCode"`
	Amount          int64      `json:"amount"` // VND, e.g. 150000
	BuyerName       string     `json:"buyerName"`
	BuyerEmail      string     `json:"buyerEmail"`
	BuyerPhone      string     `json:"buyerPhone"`
	ShippingAddress string     `json:"shippingAddress"` // full address string entered by buyer
	LineItems       []LineItem `json:"lineItems"`
}

// Set stores value under key with a TTL (seconds). Value is JSON-encoded.
func Set(key string, value interface{}, ttlSeconds int) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return rdb.Set(context.Background(), key, string(encoded), time.Duration(ttlSeconds)*time.Second).Err()
}

// GetCartPayload retrieves a CartPayload by key. Returns nil, nil when not found.
func GetCartPayload(key string) (*CartPayload, error) {
	val, err := rdb.Get(context.Background(), key).Result()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("kv: GET %s: %w", key, err)
	}
	var payload CartPayload
	if err := json.Unmarshal([]byte(val), &payload); err != nil {
		return nil, fmt.Errorf("kv: unmarshal: %w", err)
	}
	return &payload, nil
}

// MarkProcessed sets a flag key so duplicate webhooks are ignored. TTL: 24h.
func MarkProcessed(paymentLinkID string) error {
	return rdb.Set(context.Background(), "processed:"+paymentLinkID, "1", 24*time.Hour).Err()
}

// IsProcessed returns true when the paymentLinkId has already been handled.
func IsProcessed(paymentLinkID string) (bool, error) {
	val, err := rdb.Get(context.Background(), "processed:"+paymentLinkID).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("kv: GET processed:%s: %w", paymentLinkID, err)
	}
	return val != "", nil
}

// Ping checks connectivity to Redis (used by health checks).
func Ping(ctx context.Context) error {
	return rdb.Ping(ctx).Err()
}
