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
	// ShippingFeeVND is the shipping fee in VND derived from PayOS amount − line subtotal.
	// It is ignored by the REST CreateOrder (json:"-") and used only by CreateOrderGQL.
	ShippingFeeVND int64 `json:"-"`
}

// OrderResponse holds the created order fields we return to the frontend.
type OrderResponse struct {
	ID              int64  `json:"id"`
	OrderNumber     int    `json:"order_number"`
	Name            string `json:"name"`
	OrderStatusURL  string `json:"order_status_url"`
	FinancialStatus string `json:"financial_status"`
	// Email is the order-level email returned by Shopify after creation.
	// Used to confirm the address Shopify sent the receipt to.
	Email           string `json:"email,omitempty"`
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

// DraftOrderShippingLine sets the shipping cost on the draft order.
// Using a custom title + price (no handle) lets us record the PayOS-derived
// shipping fee without needing a pre-configured Shopify shipping rate.
type DraftOrderShippingLine struct {
	Title string `json:"title"`
	Price string `json:"price"` // decimal string in store currency, e.g. "35000"
}

// DraftOrderBody contains the fields accepted by POST /draft_orders.json.
// Unlike OrderBody, financial_status and transactions are not sent here;
// payment is recorded by calling CompleteDraftOrderGQL after creation.
type DraftOrderBody struct {
	LineItems       []LineItem              `json:"line_items"`
	Customer        *Customer              `json:"customer,omitempty"`
	// Email is the order-level email address. Shopify sends the Order Confirmation
	// email to this address automatically when the draft order is completed.
	Email           string                 `json:"email,omitempty"`
	ShippingAddress *ShippingAddress       `json:"shipping_address,omitempty"`
	BillingAddress  *ShippingAddress       `json:"billing_address,omitempty"`
	ShippingLine    *DraftOrderShippingLine `json:"shipping_line,omitempty"`
	Note            string                 `json:"note,omitempty"`
	Tags            string                 `json:"tags,omitempty"`
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
// regular Shopify order via the REST API. The returned DraftOrderResponse.OrderID
// contains the ID of the resulting order.
// Prefer CompleteDraftOrderGQL which returns the full order details in one call.
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

// ─── GraphQL draftOrderComplete ───────────────────────────────────────────────

// draftOrderCompleteMutation completes a draft order and returns the resulting
// Shopify order. The order email and name are requested so we can log the
// address Shopify sends the Order Confirmation email to.
const draftOrderCompleteMutation = `
mutation draftOrderComplete($id: ID!) {
  draftOrderComplete(id: $id) {
    userErrors {
      field
      message
    }
    draftOrder {
      id
      order {
        id
        name
        legacyResourceId
        displayFinancialStatus
        email
      }
    }
  }
}`

type draftOrderCompleteResponseData struct {
	DraftOrderComplete struct {
		UserErrors []struct {
			Field   []string `json:"field"`
			Message string   `json:"message"`
		} `json:"userErrors"`
		DraftOrder *struct {
			ID    string `json:"id"`
			Order *struct {
				ID                     string `json:"id"`
				Name                   string `json:"name"`
				LegacyResourceID       string `json:"legacyResourceId"`
				DisplayFinancialStatus string `json:"displayFinancialStatus"`
				Email                  string `json:"email"`
			} `json:"order"`
		} `json:"draftOrder"`
	} `json:"draftOrderComplete"`
}

// CompleteDraftOrderGQL completes a Shopify draft order via the Admin GraphQL API
// (draftOrderComplete mutation). It converts the draft order into a paid regular
// order and returns the resulting order details as an OrderResponse.
//
// When the draft order was created with an email address, Shopify automatically
// sends the Order Confirmation email to that address upon completion — no
// additional sendReceipt flag is required.
func CompleteDraftOrderGQL(draftOrderID int64) (*OrderResponse, error) {
	gid := fmt.Sprintf("gid://shopify/DraftOrder/%d", draftOrderID)
	variables := map[string]any{"id": gid}

	data, err := doGraphQL(draftOrderCompleteMutation, variables)
	if err != nil {
		return nil, err
	}

	var result draftOrderCompleteResponseData
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("shopify: cannot parse draftOrderComplete data: %w", err)
	}

	if len(result.DraftOrderComplete.UserErrors) > 0 {
		msgs := make([]string, len(result.DraftOrderComplete.UserErrors))
		for i, ue := range result.DraftOrderComplete.UserErrors {
			msgs[i] = fmt.Sprintf("%s: %s", strings.Join(ue.Field, "."), ue.Message)
		}
		return nil, fmt.Errorf("shopify: draftOrderComplete userErrors: %s", strings.Join(msgs, "; "))
	}

	if result.DraftOrderComplete.DraftOrder == nil || result.DraftOrderComplete.DraftOrder.Order == nil {
		return nil, fmt.Errorf("shopify: draftOrderComplete returned no order")
	}

	order := result.DraftOrderComplete.DraftOrder.Order
	var numericID int64
	fmt.Sscanf(order.LegacyResourceID, "%d", &numericID)

	return &OrderResponse{
		ID:              numericID,
		Name:            order.Name,
		FinancialStatus: order.DisplayFinancialStatus,
		Email:           order.Email,
	}, nil
}

// ─── Order Transaction ────────────────────────────────────────────────────────

// TransactionRequest is the REST payload for POST /orders/{id}/transactions.json.
// Used to record the PayOS payment against the completed Shopify order.
type TransactionRequest struct {
	Transaction TransactionBody `json:"transaction"`
}

// TransactionBody holds the payment details.
type TransactionBody struct {
	Kind          string `json:"kind"`          // "sale"
	Status        string `json:"status"`        // "success"
	Amount        string `json:"amount"`        // decimal string e.g. "350000"
	Currency      string `json:"currency"`      // "VND"
	Gateway       string `json:"gateway"`       // "payos"
	Authorization string `json:"authorization"` // paymentLinkId
}

// AddOrderTransaction posts a payment transaction to an existing Shopify order.
// This records the PayOS settlement against the order created via draftOrderComplete,
// providing a full audit trail inside Shopify's Timeline.
func AddOrderTransaction(shopifyOrderID int64, req TransactionRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	url := adminURL(fmt.Sprintf("orders/%d/transactions.json", shopifyOrderID))
	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Shopify-Access-Token", os.Getenv("SHOPIFY_ADMIN_API_TOKEN"))

	resp, err := HTTPClient.Do(httpReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("shopify: AddOrderTransaction HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return nil
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
	City        string `json:"city,omitempty"`
	Phone       string `json:"phone,omitempty"`
	CountryCode string `json:"countryCode,omitempty"`
}

// gqlShippingLineInput adds a shipping cost line to the Shopify order.
type gqlShippingLineInput struct {
	Title    string           `json:"title"`
	PriceSet gqlMoneyBagInput `json:"priceSet"`
}

// gqlCustomerUpsertInput identifies a customer by email only.
// Phone is intentionally excluded: Shopify enforces phone uniqueness per customer
// and returns a userError ("phone has already been taken") if the phone belongs to
// a different existing customer. Phone is captured separately via shippingAddress.
type gqlCustomerUpsertInput struct {
	Email     string `json:"email,omitempty"`
	FirstName string `json:"firstName,omitempty"`
	LastName  string `json:"lastName,omitempty"`
}

// gqlCustomerInput wraps the customer in "toUpsert" so Shopify creates a new
// customer record or merges with an existing one matched by email.
type gqlCustomerInput struct {
	ToUpsert *gqlCustomerUpsertInput `json:"toUpsert,omitempty"`
}

type gqlLineItemInput struct {
	VariantID        string `json:"variantId"`        // GID: "gid://shopify/ProductVariant/{id}"
	Quantity         int    `json:"quantity"`
	RequiresShipping bool   `json:"requiresShipping"` // explicit true overrides variant default
}

type gqlOrderInput struct {
	LineItems       []gqlLineItemInput     `json:"lineItems"`
	// Email is the order-level email address (OrderCreateOrderInput.email).
	// This is required for sendReceipt:true to deliver the Shopify order
	// confirmation email to the customer. It is distinct from customer.toUpsert.email.
	Email           string                 `json:"email,omitempty"`
	Customer        *gqlCustomerInput      `json:"customer,omitempty"`
	ShippingAddress *gqlAddressInput       `json:"shippingAddress,omitempty"`
	BillingAddress  *gqlAddressInput       `json:"billingAddress,omitempty"`
	FinancialStatus string                 `json:"financialStatus,omitempty"` // must be UPPER_CASE
	Transactions    []gqlTransactionInput  `json:"transactions,omitempty"`
	ShippingLines   []gqlShippingLineInput `json:"shippingLines,omitempty"`
	Note            string                 `json:"note,omitempty"`
	Tags            []string               `json:"tags,omitempty"`
	Currency        string                 `json:"currency,omitempty"`
}

type gqlOptionsInput struct {
	// InventoryBehaviour controls how inventory is adjusted when the order is created.
	// DECREMENT_OBEYING_POLICY respects the variant's inventory management settings.
	InventoryBehaviour     string `json:"inventoryBehaviour,omitempty"`
	SendReceipt            bool   `json:"sendReceipt"`
	SendFulfillmentReceipt bool   `json:"sendFulfillmentReceipt"`
}

// orderCreateMutation is the GraphQL mutation sent to /admin/api/{version}/graphql.json.
// legacyResourceId returns the plain integer Shopify order ID (same as the REST id field).
// email is returned so we can log and confirm the address Shopify will send the receipt to.
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
      email
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
			Email                  string `json:"email"`
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
//   - customer uses toUpsert matched by email only (phone excluded to avoid uniqueness conflicts)
func CreateOrderGQL(req OrderRequest) (*OrderResponse, error) {
	// Line items → GID format.
	// RequiresShipping: true is set explicitly so Shopify treats every item as a
	// physical product regardless of the variant's default setting. Without this,
	// Shopify may inherit requires_shipping=false from the variant, which silently
	// suppresses the shipping address, shipping lines, and inventory deduction.
	gqlItems := make([]gqlLineItemInput, 0, len(req.Order.LineItems))
	for _, li := range req.Order.LineItems {
		gqlItems = append(gqlItems, gqlLineItemInput{
			VariantID:        fmt.Sprintf("gid://shopify/ProductVariant/%d", li.VariantID),
			Quantity:         li.Quantity,
			RequiresShipping: true,
		})
	}

	// Customer: only set when email is present so Shopify has a unique identifier
	// to create-or-merge on. Without email, toUpsert has no matching key and
	// Shopify may error. Name and phone are always captured in shippingAddress.
	var customer *gqlCustomerInput
	c := req.Order.Customer
	if c.Email != "" {
		customer = &gqlCustomerInput{
			ToUpsert: &gqlCustomerUpsertInput{
				Email:     c.Email,
				FirstName: c.FirstName,
				LastName:  c.LastName,
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
			City:        a.City,
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

	// Shipping line: PayOS amount is the exact amount transferred by the customer.
	// If it exceeds the sum of line item prices, the difference is the shipping fee.
	var shippingLines []gqlShippingLineInput
	if req.Order.ShippingFeeVND > 0 {
		shippingLines = []gqlShippingLineInput{{
			Title: "Phí vận chuyển",
			PriceSet: gqlMoneyBagInput{
				ShopMoney: gqlMoneyInput{
					Amount:       fmt.Sprintf("%d", req.Order.ShippingFeeVND),
					CurrencyCode: "VND",
				},
			},
		}}
	}

	variables := map[string]any{
		"order": gqlOrderInput{
			LineItems: gqlItems,
			// order.email is the top-level email on OrderCreateOrderInput.
			// Shopify uses this address when options.sendReceipt=true to
			// deliver the official Order Confirmation notification. This is
			// separate from customer.toUpsert.email (customer matching).
			Email:           req.Order.Customer.Email,
			Customer:        customer,
			ShippingAddress: toGQLAddr(req.Order.ShippingAddress),
			BillingAddress:  toGQLAddr(req.Order.BillingAddress),
			FinancialStatus: strings.ToUpper(req.Order.FinancialStatus),
			Transactions:    gqlTxns,
			ShippingLines:   shippingLines,
			Note:            req.Order.Note,
			Tags:            tags,
			Currency:        "VND",
		},
		"options": gqlOptionsInput{
			InventoryBehaviour:     "DECREMENT_OBEYING_POLICY",
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
		Email:           result.OrderCreate.Order.Email,
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
