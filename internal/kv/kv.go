// Package kv provides a thin client for Vercel KV (Upstash Redis REST API).
// Required env vars: KV_REST_API_URL, KV_REST_API_TOKEN
package kv

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
)

// LineItem mirrors a Shopify cart line item needed to create an order.
type LineItem struct {
	VariantID int64  `json:"variantId"`
	ProductID int64  `json:"productId"`
	Title     string `json:"title"`
	Quantity  int    `json:"quantity"`
	Price     int64  `json:"price"` // Shopify internal units (price × 100)
}

// CartPayload is stored against the paymentLinkId key in KV.
// The webhook handler reads this to create the Shopify order.
type CartPayload struct {
	OrderCode  int64      `json:"orderCode"`
	Amount     int64      `json:"amount"` // VND, e.g. 150000
	BuyerName  string     `json:"buyerName"`
	BuyerEmail string     `json:"buyerEmail"`
	BuyerPhone string     `json:"buyerPhone"`
	LineItems  []LineItem `json:"lineItems"`
}

// upstashResponse is the envelope returned by every Upstash REST command.
type upstashResponse struct {
	Result interface{} `json:"result"`
	Error  string      `json:"error"`
}

// restURL returns the base Upstash REST URL from env.
func restURL() string { return os.Getenv("KV_REST_API_URL") }

// token returns the Upstash bearer token from env.
func token() string { return os.Getenv("KV_REST_API_TOKEN") }

// exec sends a single Redis command to the Upstash REST endpoint.
// cmd is a JSON array like ["SET", "key", "value", "EX", "1200"].
func exec(cmd []interface{}) (interface{}, error) {
	body, _ := json.Marshal(cmd)

	req, err := http.NewRequest(http.MethodPost, restURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token())
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var up upstashResponse
	if err := json.Unmarshal(raw, &up); err != nil {
		return nil, fmt.Errorf("kv: bad response: %s", string(raw))
	}
	if up.Error != "" {
		return nil, errors.New("kv: " + up.Error)
	}
	return up.Result, nil
}

// Set stores value under key with a TTL (seconds). Value is JSON-encoded.
func Set(key string, value interface{}, ttlSeconds int) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = exec([]interface{}{"SET", key, string(encoded), "EX", strconv.Itoa(ttlSeconds)})
	return err
}

// GetCartPayload retrieves a CartPayload by key. Returns nil, nil when not found.
func GetCartPayload(key string) (*CartPayload, error) {
	result, err := exec([]interface{}{"GET", key})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	str, ok := result.(string)
	if !ok {
		return nil, fmt.Errorf("kv: unexpected type for GET result")
	}
	var payload CartPayload
	if err := json.Unmarshal([]byte(str), &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// MarkProcessed sets a small flag key so duplicate webhooks are ignored.
// Uses a 24-hour TTL.
func MarkProcessed(paymentLinkID string) error {
	_, err := exec([]interface{}{"SET", "processed:" + paymentLinkID, "1", "EX", "86400"})
	return err
}

// IsProcessed returns true when the paymentLinkId has already been handled.
func IsProcessed(paymentLinkID string) (bool, error) {
	result, err := exec([]interface{}{"GET", "processed:" + paymentLinkID})
	if err != nil {
		return false, err
	}
	return result != nil, nil
}
