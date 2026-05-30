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

// ─── GraphQL orderCreate ─────────────────────────────────────────────────────

// graphqlURL returns the Admin GraphQL endpoint URL.
func graphqlURL() string {
	return fmt.Sprintf("https://%s/admin/api/%s/graphql.json",
		os.Getenv("SHOPIFY_STORE_DOMAIN"), apiVersion)
}

// doGraphQL sends a GraphQL request to the Shopify Admin API using the static
// SHOPIFY_ADMIN_API_TOKEN. The structure mirrors the inventory client's doGraphQL
// (internal/shopify/inventory.go) — same headers, same error-unwrap logic.
func doGraphQL(query string, variables map[string]any) (json.RawMessage, error) {
	payload := map[string]any{
		"query":     query,
		"variables": variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, graphqlURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Access-Token", os.Getenv("SHOPIFY_ADMIN_API_TOKEN"))

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shopify: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("shopify: cannot parse GraphQL response: %w", err)
	}
	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Message
		}
		return nil, fmt.Errorf("shopify: GraphQL errors: %s", strings.Join(msgs, "; "))
	}
	return result.Data, nil
}

// ─── GraphQL input / output types ────────────────────────────────────────────

type gqlMoneyInput struct {
	Amount       string `json:"amount"`
	CurrencyCode string `json:"currencyCode"`
}

type gqlMoneyBagInput struct {
	ShopMoney gqlMoneyInput `json:"shopMoney"`
}

// gqlTransactionInput records the PayOS payment.
// kind and status must be UPPER_CASE per the GraphQL schema (e.g. "SALE", "SUCCESS").
type gqlTransactionInput struct {
	Kind      string           `json:"kind"`
	Status    string           `json:"status"`
	AmountSet gqlMoneyBagInput `json:"amountSet"`
}

type gqlAddressInput struct {
	FirstName   string `json:"firstName,omitempty"`
	LastName    string `json:"lastName,omitempty"`
	Address1    string `json:"address1,omitempty"`
	Phone       string `json:"phone,omitempty"`
	CountryCode string `json:"countryCode,omitempty"`
}

type gqlCustomerUpsertInput struct {
	Email     string `json:"email,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
	Phone     string `json:"phone,omitempty"`
}

// gqlCustomerInput wraps the customer in "toUpsert" so Shopify creates a new
// customer record or merges with an existing one by email/phone match.
type gqlCustomerInput struct {
	ToUpsert *gqlCustomerUpsertInput `json:"toUpsert,omitempty"`
}

type gqlLineItemInput struct {
	VariantID string `json:"variantId"` // GID: "gid://shopify/ProductVariant/{id}"
	Quantity  int    `json:"quantity"`
}

type gqlOrderInput struct {
	LineItems       []gqlLineItemInput    `json:"lineItems"`
	Customer        *gqlCustomerInput     `json:"customer,omitempty"`
	ShippingAddress *gqlAddressInput      `json:"shippingAddress,omitempty"`
	BillingAddress  *gqlAddressInput      `json:"billingAddress,omitempty"`
	FinancialStatus string                `json:"financialStatus,omitempty"` // must be UPPER_CASE
	Transactions    []gqlTransactionInput `json:"transactions,omitempty"`
	Note            string                `json:"note,omitempty"`
	Tags            []string              `json:"tags,omitempty"`
	Currency        string                `json:"currency,omitempty"`
}

type gqlOptionsInput struct {
	SendReceipt            bool `json:"sendReceipt"`
	SendFulfillmentReceipt bool `json:"sendFulfillmentReceipt"`
}

// orderCreateMutation is the GraphQL mutation sent to /admin/api/{version}/graphql.json.
// legacyResourceId returns the plain integer Shopify order ID (same as the REST id field).
const orderCreateMutation = `
mutation orderCreate($order: OrderCreateOrderInput!, $options: OrderCreateOptionsInput) {
  orderCreate(order: $order, options: $options) {
    userErrors {
      field
      message
    }
    order {
      id
      name
      legacyResourceId
      displayFinancialStatus
    }
  }
}`

type orderCreateResponseData struct {
	OrderCreate struct {
		UserErrors []struct {
			Field   []string `json:"field"`
			Message string   `json:"message"`
		} `json:"userErrors"`
		Order *struct {
			ID                     string `json:"id"`
			Name                   string `json:"name"`
			LegacyResourceID       string `json:"legacyResourceId"`
			DisplayFinancialStatus string `json:"displayFinancialStatus"`
		} `json:"order"`
	} `json:"orderCreate"`
}

// CreateOrderGQL creates a paid Shopify order via the Admin GraphQL API
// (orderCreate mutation — https://shopify.dev/docs/api/admin-graphql/latest/mutations/orderCreate).
//
// It accepts the same OrderRequest as CreateOrder (REST) and returns the same
// OrderResponse, so it is a drop-in replacement. Differences from REST:
//   - variantId is sent as a GID ("gid://shopify/ProductVariant/{id}")
//   - financialStatus and transaction kind/status are UPPER_CASE
//   - transaction amount uses amountSet.shopMoney instead of a plain amount string
//   - comma-separated tags are split into a []string
//   - customer uses toUpsert (create-or-match by email/phone) instead of inline embed
func CreateOrderGQL(req OrderRequest) (*OrderResponse, error) {
	// Line items → GID format.
	gqlItems := make([]gqlLineItemInput, 0, len(req.Order.LineItems))
	for _, li := range req.Order.LineItems {
		gqlItems = append(gqlItems, gqlLineItemInput{
			VariantID: fmt.Sprintf("gid://shopify/ProductVariant/%d", li.VariantID),
			Quantity:  li.Quantity,
		})
	}

	// Customer: toUpsert creates or merges by email/phone.
	var customer *gqlCustomerInput
	c := req.Order.Customer
	if c.Email != "" || c.FirstName != "" || c.LastName != "" || c.Phone != "" {
		customer = &gqlCustomerInput{
			ToUpsert: &gqlCustomerUpsertInput{
				Email:     c.Email,
				FirstName: c.FirstName,
				LastName:  c.LastName,
				Phone:     c.Phone,
			},
		}
	}

	// REST ShippingAddress → GraphQL address input.
	toGQLAddr := func(a *ShippingAddress) *gqlAddressInput {
		if a == nil {
			return nil
		}
		return &gqlAddressInput{
			FirstName:   a.FirstName,
			LastName:    a.LastName,
			Address1:    a.Address1,
			Phone:       a.Phone,
			CountryCode: a.CountryCode,
		}
	}

	// Transactions — GraphQL requires UPPER_CASE kind/status and amountSet wrapper.
	gqlTxns := make([]gqlTransactionInput, 0, len(req.Order.Transactions))
	for _, tx := range req.Order.Transactions {
		gqlTxns = append(gqlTxns, gqlTransactionInput{
			Kind:   strings.ToUpper(tx.Kind),
			Status: strings.ToUpper(tx.Status),
			AmountSet: gqlMoneyBagInput{
				ShopMoney: gqlMoneyInput{
					Amount:       tx.Amount,
					CurrencyCode: tx.Currency,
				},
			},
		})
	}

	// Tags: REST sends "payos,qr-transfer"; GraphQL needs []string.
	var tags []string
	for _, t := range strings.Split(req.Order.Tags, ",") {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			tags = append(tags, trimmed)
		}
	}

	variables := map[string]any{
		"order": gqlOrderInput{
			LineItems:       gqlItems,
			Customer:        customer,
			ShippingAddress: toGQLAddr(req.Order.ShippingAddress),
			BillingAddress:  toGQLAddr(req.Order.BillingAddress),
			FinancialStatus: strings.ToUpper(req.Order.FinancialStatus),
			Transactions:    gqlTxns,
			Note:            req.Order.Note,
			Tags:            tags,
			Currency:        "VND",
		},
		"options": gqlOptionsInput{
			SendReceipt:            req.Order.SendReceipt,
			SendFulfillmentReceipt: req.Order.SendFulfillmentReceipt,
		},
	}

	data, err := doGraphQL(orderCreateMutation, variables)
	if err != nil {
		return nil, err
	}

	var result orderCreateResponseData
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("shopify: cannot parse orderCreate data: %w", err)
	}

	if len(result.OrderCreate.UserErrors) > 0 {
		msgs := make([]string, len(result.OrderCreate.UserErrors))
		for i, ue := range result.OrderCreate.UserErrors {
			msgs[i] = fmt.Sprintf("%s: %s", strings.Join(ue.Field, "."), ue.Message)
		}
		return nil, fmt.Errorf("shopify: orderCreate userErrors: %s", strings.Join(msgs, "; "))
	}

	if result.OrderCreate.Order == nil {
		return nil, fmt.Errorf("shopify: orderCreate returned no order")
	}

	// legacyResourceId is the plain decimal integer string (e.g. "5678901234").
	var numericID int64
	fmt.Sscanf(result.OrderCreate.Order.LegacyResourceID, "%d", &numericID)

	return &OrderResponse{
		ID:              numericID,
		Name:            result.OrderCreate.Order.Name,
		FinancialStatus: result.OrderCreate.Order.DisplayFinancialStatus,
	}, nil
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
