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

const apiVersion = "2026-01"

// HTTPClient is the HTTP client used for all Shopify API calls.
// Override in tests to inject a mock server.
var HTTPClient *http.Client = http.DefaultClient

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

	resp, err := HTTPClient.Do(httpReq)
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

// ─── Draft Order ────────────────────────────────────────────────────────────

// DraftOrderRequest is the top-level payload for POST /draft_orders.json.
type DraftOrderRequest struct {
	DraftOrder DraftOrderBody `json:"draft_order"`
}

// DraftOrderBody contains the fields accepted by POST /draft_orders.json.
// Unlike OrderBody, financial_status and transactions are not sent here;
// payment is recorded by calling CompleteDraftOrder after creation.
type DraftOrderBody struct {
	LineItems       []LineItem       `json:"line_items"`
	Customer        *Customer        `json:"customer,omitempty"`
	Email           string           `json:"email,omitempty"`
	ShippingAddress *ShippingAddress `json:"shipping_address,omitempty"`
	BillingAddress  *ShippingAddress `json:"billing_address,omitempty"`
	Note            string           `json:"note,omitempty"`
	Tags            string           `json:"tags,omitempty"`
}

// DraftOrderResponse holds the fields returned by the Shopify DraftOrder API.
// After CompleteDraftOrder, OrderID contains the resulting Shopify order ID.
type DraftOrderResponse struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	OrderID    int64  `json:"order_id"`
	Status     string `json:"status"`
	InvoiceURL string `json:"invoice_url"`
}

type draftOrderEnvelope struct {
	DraftOrder DraftOrderResponse `json:"draft_order"`
}

// CreateDraftOrder creates a new draft order via the Shopify Admin API.
// Call CompleteDraftOrder afterwards to convert it into a paid order.
func CreateDraftOrder(req DraftOrderRequest) (*DraftOrderResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequest(http.MethodPost, adminURL("draft_orders.json"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Shopify-Access-Token", os.Getenv("SHOPIFY_ADMIN_API_TOKEN"))

	resp, err := HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shopify: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var env draftOrderEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("shopify: cannot parse draft order response: %w", err)
	}
	return &env.DraftOrder, nil
}

// CompleteDraftOrder marks the draft order as paid and converts it into a
// regular Shopify order. The returned DraftOrderResponse.OrderID contains
// the ID of the resulting order.
func CompleteDraftOrder(draftOrderID int64) (*DraftOrderResponse, error) {
	url := adminURL(fmt.Sprintf("draft_orders/%d/complete.json", draftOrderID))
	httpReq, err := http.NewRequest(http.MethodPut, url, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("X-Shopify-Access-Token", os.Getenv("SHOPIFY_ADMIN_API_TOKEN"))

	resp, err := HTTPClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shopify: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var env draftOrderEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("shopify: cannot parse complete draft order response: %w", err)
	}
	return &env.DraftOrder, nil
}

// ─── Utilities ───────────────────────────────────────────────────────────────

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
