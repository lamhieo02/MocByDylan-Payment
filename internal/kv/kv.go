// Package kv provides a Redis client for storing cart payloads and deduplication flags.
// Required env var: REDIS_URL (e.g. redis://default:password@host:6379)
// Falls back to REDIS_ADDR (host:port, default localhost:6379) when REDIS_URL is not set.
package kv

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
		// ping redis to test connectivity
		err = rdb.Ping(context.Background()).Err()
		if err != nil {
			panic(fmt.Sprintf("kv: failed to ping redis: %v", err))
		}
		log.Printf("kv: redis connected")
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
// The webhook handler reads this to complete the Shopify Draft Order.
type CartPayload struct {
	OrderCode       int64      `json:"orderCode"`
	Amount          int64      `json:"amount"` // VND, e.g. 150000
	BuyerName       string     `json:"buyerName"`
	BuyerEmail      string     `json:"buyerEmail"`
	BuyerPhone      string     `json:"buyerPhone"`
	ShippingAddress string     `json:"shippingAddress"` // full address string entered by buyer
	LineItems       []LineItem `json:"lineItems"`
	// DraftOrderID and DraftOrderName are set immediately after Draft Order
	// creation in create_payment. The webhook handler uses DraftOrderID to
	// complete the draft order instead of creating a new one.
	DraftOrderID   int64  `json:"draftOrderId,omitempty"`
	DraftOrderName string `json:"draftOrderName,omitempty"`
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

// TryMarkProcessed atomically claims the processing right for a paymentLinkId.
// It uses Redis SET NX (set-if-not-exists) so the operation is atomic — only
// one concurrent caller receives claimed=true and should proceed. Any subsequent
// caller (duplicate webhook delivery, retry) receives claimed=false and must skip.
// TTL: 24h, consistent with the previous MarkProcessed behaviour.
func TryMarkProcessed(paymentLinkID string) (claimed bool, err error) {
	ok, err := rdb.SetNX(context.Background(), "processed:"+paymentLinkID, "1", 24*time.Hour).Result()
	if err != nil {
		return false, fmt.Errorf("kv: SetNX processed:%s: %w", paymentLinkID, err)
	}
	return ok, nil
}

// Ping checks connectivity to Redis (used by health checks).
func Ping(ctx context.Context) error {
	return rdb.Ping(ctx).Err()
}

// Client returns the underlying *redis.Client.
// Use this to pass Redis access to other packages that need raw client operations.
func Client() *redis.Client {
	return rdb
}
