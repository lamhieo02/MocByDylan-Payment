package shopify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

// DraftOrderDetail is a richer view of a draft order, used by the COD flow so
// the server can read Shopify's own prices instead of trusting the storefront.
type DraftOrderDetail struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	OrderID       int64  `json:"order_id"`
	Status        string `json:"status"`
	SubtotalPrice string `json:"subtotal_price"`
	TotalPrice    string `json:"total_price"`
	LineItems     []struct {
		VariantID int64  `json:"variant_id"`
		ProductID int64  `json:"product_id"`
		Title     string `json:"title"`
		Quantity  int    `json:"quantity"`
		Price     string `json:"price"`
	} `json:"line_items"`
}

// OrderSummary is the minimal order info returned to the storefront.
type OrderSummary struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	OrderStatusURL string `json:"order_status_url"`
	TotalPrice     string `json:"total_price"`
}

// codAdminDo sends one Admin REST request and decodes the JSON reply into out.
func codAdminDo(method, path string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, adminURL(path), body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Shopify-Access-Token", os.Getenv("SHOPIFY_ADMIN_API_TOKEN"))

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("shopify: HTTP %d: %s", resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("shopify: cannot parse response: %w", err)
		}
	}
	return nil
}

// CreateDraftOrderDetailed creates a draft order and returns it with prices.
func CreateDraftOrderDetailed(req DraftOrderRequest) (*DraftOrderDetail, error) {
	var env struct {
		DraftOrder DraftOrderDetail `json:"draft_order"`
	}
	if err := codAdminDo(http.MethodPost, "draft_orders.json", req, &env); err != nil {
		return nil, err
	}
	return &env.DraftOrder, nil
}

// SetDraftOrderShippingLine adds or replaces the shipping line of a draft order.
func SetDraftOrderShippingLine(draftOrderID int64, line DraftOrderShippingLine) (*DraftOrderDetail, error) {
	payload := map[string]any{
		"draft_order": map[string]any{
			"id":            draftOrderID,
			"shipping_line": line,
		},
	}
	var env struct {
		DraftOrder DraftOrderDetail `json:"draft_order"`
	}
	if err := codAdminDo(http.MethodPut, fmt.Sprintf("draft_orders/%d.json", draftOrderID), payload, &env); err != nil {
		return nil, err
	}
	return &env.DraftOrder, nil
}

// CompleteDraftOrderPaymentPending turns a draft order into a real order whose
// payment is still pending — the right state for cash on delivery.
func CompleteDraftOrderPaymentPending(draftOrderID int64) (*DraftOrderDetail, error) {
	var env struct {
		DraftOrder DraftOrderDetail `json:"draft_order"`
	}
	path := fmt.Sprintf("draft_orders/%d/complete.json?payment_pending=true", draftOrderID)
	if err := codAdminDo(http.MethodPut, path, nil, &env); err != nil {
		return nil, err
	}
	return &env.DraftOrder, nil
}

// GetOrderSummary fetches an order's name, status page URL and total.
func GetOrderSummary(orderID int64) (*OrderSummary, error) {
	var env struct {
		Order OrderSummary `json:"order"`
	}
	path := fmt.Sprintf("orders/%d.json?fields=id,name,order_status_url,total_price", orderID)
	if err := codAdminDo(http.MethodGet, path, nil, &env); err != nil {
		return nil, err
	}
	return &env.Order, nil
}

// DeleteDraftOrder removes a draft order (used to clean up a failed COD attempt).
func DeleteDraftOrder(draftOrderID int64) error {
	return codAdminDo(http.MethodDelete, fmt.Sprintf("draft_orders/%d.json", draftOrderID), nil, nil)
}
