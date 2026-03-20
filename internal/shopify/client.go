// Package shopify wraps the Shopify Admin REST API for order creation.
// Required env vars: SHOPIFY_STORE_DOMAIN, SHOPIFY_ADMIN_API_TOKEN
package shopify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

const apiVersion = "2024-10"

// LineItem is a Shopify order line item.
type LineItem struct {
	VariantID int64 `json:"variant_id"`
	Quantity  int   `json:"quantity"`
}

// Customer is a minimal Shopify customer object.
type Customer struct {
	Email     string `json:"email,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// ShippingAddress is a Shopify order shipping/billing address.
type ShippingAddress struct {
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Phone       string `json:"phone,omitempty"`
	Address1    string `json:"address1,omitempty"`
	City        string `json:"city,omitempty"`
	Province    string `json:"province,omitempty"`
	Country     string `json:"country,omitempty"`
	CountryCode string `json:"country_code,omitempty"`
	Zip         string `json:"zip,omitempty"`
}

// Transaction records the PayOS payment against the order.
type Transaction struct {
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	Amount        string `json:"amount"`
	Currency      string `json:"currency"`
	Gateway       string `json:"gateway"`
	Authorization string `json:"authorization"`
}

// OrderRequest is the top-level payload for POST /orders.json.
type OrderRequest struct {
	Order OrderBody `json:"order"`
}

// OrderBody contains the order fields.
type OrderBody struct {
	LineItems              []LineItem       `json:"line_items"`
	Customer               Customer         `json:"customer"`
	ShippingAddress        *ShippingAddress `json:"shipping_address,omitempty"`
	BillingAddress         *ShippingAddress `json:"billing_address,omitempty"`
	FinancialStatus        string           `json:"financial_status"`
	Currency               string           `json:"currency"`
	Transactions           []Transaction    `json:"transactions"`
	Note                   string           `json:"note,omitempty"`
	Tags                   string           `json:"tags,omitempty"`
	SendReceipt            bool             `json:"send_receipt"`
	SendFulfillmentReceipt bool             `json:"send_fulfillment_receipt"`
}

// OrderResponse holds the created order fields we return to the frontend.
type OrderResponse struct {
	ID              int64  `json:"id"`
	OrderNumber     int    `json:"order_number"`
	Name            string `json:"name"`
	OrderStatusURL  string `json:"order_status_url"`
	FinancialStatus string `json:"financial_status"`
}

// orderEnvelope wraps the Shopify order response.
type orderEnvelope struct {
	Order OrderResponse `json:"order"`
}

// adminURL builds the full Admin API URL for the given path.
func adminURL(path string) string {
	domain := os.Getenv("SHOPIFY_STORE_DOMAIN")
	return fmt.Sprintf("https://%s/admin/api/%s/%s", domain, apiVersion, path)
}

// CreateOrder creates a paid order via the Shopify Admin API.
// Returns the created order with its name and status URL.
func CreateOrder(req OrderRequest) (*OrderResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, adminURL("orders.json"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Shopify-Access-Token", os.Getenv("SHOPIFY_ADMIN_API_TOKEN"))

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shopify: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var env orderEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("shopify: cannot parse response: %w", err)
	}
	return &env.Order, nil
}

// ParseName splits "Nguyen Van A" into first + last name parts.
// Returns first, last. If only one word, it becomes first_name.
func ParseName(fullName string) (string, string) {
	parts := strings.Fields(fullName)
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}
