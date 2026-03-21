package shopify_test

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// shippingAddr is the shared address used in every test case, matching the
// pointer values from the original webhook payload.
var shippingAddr = &shopify.ShippingAddress{
	FirstName:   "Lâm",
	LastName:    "Nguyễn",
	Phone:       "3333",
	Address1:    "123 Test Street",
	Country:     "Vietnam",
	CountryCode: "VN",
}

// sampleOrderReq mirrors the exact OrderRequest printed by the webhook handler:
//
//	{Order:{LineItems:[{VariantID:44965664260278 Quantity:1}]
//	  Customer:{Email:lamlklk2002@gmail.com FirstName:Lâm LastName:Nguyễn Phone:3333}
//	  ShippingAddress:... BillingAddress:...
//	  FinancialStatus:paid Currency:VND
//	  Transactions:[{Kind:sale Status:success Amount:179000 Currency:VND
//	                 Gateway:payos Authorization:a4bdadfe8657413fbf2baa8412d4bd44}]
//	  Note:PayOS QR transfer. paymentLinkId: a4bdadfe... | ref: FT26080900SF
//	  Tags:payos,qr-transfer SendReceipt:true SendFulfillmentReceipt:true}}
func sampleOrderReq() shopify.OrderRequest {
	return shopify.OrderRequest{
		Order: shopify.OrderBody{
			LineItems: []shopify.LineItem{
				{VariantID: 44965664260278, Quantity: 1},
			},
			Customer: shopify.Customer{
				Email:     "lamlklk2002@gmail.com",
				FirstName: "Lâm",
				LastName:  "Nguyễn",
				Phone:     "3333",
			},
			ShippingAddress: shippingAddr,
			BillingAddress:  shippingAddr,
			FinancialStatus: "paid",
			Transactions: []shopify.Transaction{
				{
					Kind:          "sale",
					Status:        "success",
					Amount:        "179000.00",
					Currency:      "VND",
					Gateway:       "payos",
					Authorization: "a4bdadfe8657413fbf2baa8412d4bd44",
				},
			},
			Note:                   "PayOS QR transfer. paymentLinkId: a4bdadfe8657413fbf2baa8412d4bd44 | ref: FT26080900SF",
			Tags:                   "payos,qr-transfer",
			SendReceipt:            true,
			SendFulfillmentReceipt: true,
		},
	}
}

// setupMockServer starts a TLS test server with the given handler, injects it
// into the shopify package, and returns a cleanup function.
//
// We use httptest.NewTLSServer because adminURL always builds an https:// URL.
// srv.Client() already trusts the self-signed cert, so no extra TLS config is
// needed in the test.
func setupMockServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)

	// Point shopify package at the test server.
	t.Setenv("SHOPIFY_STORE_DOMAIN", srv.Listener.Addr().String())
	t.Setenv("SHOPIFY_ADMIN_API_TOKEN", "3333")

	log.Println("SHOPIFY_STORE_DOMAIN", "mocbydylan.myshopify.com")

	// Replace the HTTP client with one that trusts the test server's TLS cert.
	origClient := shopify.HTTPClient
	shopify.HTTPClient = srv.Client()

	t.Cleanup(func() {
		shopify.HTTPClient = origClient
		srv.Close()
	})
}

// ── tests ─────────────────────────────────────────────────────────────────────

// TestCreateOrder_Success verifies that a 200 response with a valid order JSON
// body is correctly parsed and returned.
func TestCreateOrder_Success(t *testing.T) {
	want := shopify.OrderResponse{
		ID:              987654321,
		OrderNumber:     1042,
		Name:            "#1042",
		OrderStatusURL:  "https://mocbydylan.myshopify.com/orders/987654321/authenticate?key=abc",
		FinancialStatus: "paid",
	}

	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		// Verify the endpoint and method.
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/admin/api/2026-01/orders.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Shopify-Access-Token") == "" {
			t.Errorf("missing X-Shopify-Access-Token header")
		}

		// Verify the request body contains expected fields.
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("cannot decode request body: %v", err)
		}
		order := body["order"].(map[string]any)
		if order["financial_status"] != "paid" {
			t.Errorf("expected financial_status=paid, got %v", order["financial_status"])
		}
		if _, hasCurrency := order["currency"]; hasCurrency {
			t.Errorf("currency should not be in request body (read-only field), but it was sent")
		}

		// Return a successful Shopify response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"order": want})
	})

	got, err := shopify.CreateOrder(sampleOrderReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ID != want.ID {
		t.Errorf("ID: got %d, want %d", got.ID, want.ID)
	}
	if got.Name != want.Name {
		t.Errorf("Name: got %q, want %q", got.Name, want.Name)
	}
	if got.FinancialStatus != want.FinancialStatus {
		t.Errorf("FinancialStatus: got %q, want %q", got.FinancialStatus, want.FinancialStatus)
	}
}

// TestCreateOrder_ShopifyReturnsError verifies that a non-2xx HTTP status from
// Shopify is surfaced as an error containing the status code.
func TestCreateOrder_ShopifyReturnsError(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"errors":{"variant_id":["is invalid"]}}`))
	})

	_, err := shopify.CreateOrder(sampleOrderReq())
	if err == nil {
		t.Fatal("expected an error for HTTP 422, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "422") {
		t.Errorf("error should mention status 422, got: %s", errMsg)
	}
}

// TestCreateOrder_MalformedJSONResponse verifies that a 200 response with
// invalid JSON is returned as a parse error.
func TestCreateOrder_MalformedJSONResponse(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not-json`))
	})

	_, err := shopify.CreateOrder(sampleOrderReq())
	if err == nil {
		t.Fatal("expected a parse error, got nil")
	}
}

// TestCreateOrder_RequestPayload verifies that the JSON body sent to Shopify
// contains all required fields from the sample webhook data.
func TestCreateOrder_RequestPayload(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		var envelope struct {
			Order struct {
				LineItems []struct {
					VariantID int64 `json:"variant_id"`
					Quantity  int   `json:"quantity"`
				} `json:"line_items"`
				Customer struct {
					Email     string `json:"email"`
					FirstName string `json:"first_name"`
					LastName  string `json:"last_name"`
					Phone     string `json:"phone"`
				} `json:"customer"`
				Transactions []struct {
					Authorization string `json:"authorization"`
					Gateway       string `json:"gateway"`
					Amount        string `json:"amount"`
				} `json:"transactions"`
				Note                   string `json:"note"`
				Tags                   string `json:"tags"`
				SendReceipt            bool   `json:"send_receipt"`
				SendFulfillmentReceipt bool   `json:"send_fulfillment_receipt"`
			} `json:"order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode: %v", err)
		}
		o := envelope.Order

		assertEq(t, "variant_id", int64(44965664260278), o.LineItems[0].VariantID)
		assertEq(t, "quantity", 1, o.LineItems[0].Quantity)
		assertEq(t, "email", "lamlklk2002@gmail.com", o.Customer.Email)
		assertEq(t, "first_name", "Lâm", o.Customer.FirstName)
		assertEq(t, "last_name", "Nguyễn", o.Customer.LastName)
		assertEq(t, "phone", "3333", o.Customer.Phone)
		assertEq(t, "authorization", "a4bdadfe8657413fbf2baa8412d4bd44", o.Transactions[0].Authorization)
		assertEq(t, "gateway", "payos", o.Transactions[0].Gateway)
		assertEq(t, "amount", "179000.00", o.Transactions[0].Amount)
		assertEq(t, "tags", "payos,qr-transfer", o.Tags)
		if !o.SendReceipt {
			t.Error("send_receipt should be true")
		}
		if !o.SendFulfillmentReceipt {
			t.Error("send_fulfillment_receipt should be true")
		}
		if !strings.Contains(o.Note, "a4bdadfe8657413fbf2baa8412d4bd44") {
			t.Errorf("note should contain paymentLinkId, got: %s", o.Note)
		}

		// Return a minimal valid response so CreateOrder doesn't error.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"order": map[string]any{"id": 1, "name": "#1001", "financial_status": "paid"},
		})
	})

	if _, err := shopify.CreateOrder(sampleOrderReq()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCreateOrder_MissingDomain verifies that an empty SHOPIFY_STORE_DOMAIN
// results in an HTTP error (not a panic).
func TestCreateOrder_MissingDomain(t *testing.T) {
	orig := os.Getenv("SHOPIFY_STORE_DOMAIN")
	os.Setenv("SHOPIFY_STORE_DOMAIN", "")
	defer os.Setenv("SHOPIFY_STORE_DOMAIN", orig)

	_, err := shopify.CreateOrder(sampleOrderReq())
	if err == nil {
		t.Fatal("expected error when domain is empty, got nil")
	}
}

// ── assertion helpers ─────────────────────────────────────────────────────────

func assertEq[T comparable](t *testing.T, field string, want, got T) {
	t.Helper()
	if got != want {
		t.Errorf("%s: want %v, got %v", field, want, got)
	}
}

// ── draft order helpers ───────────────────────────────────────────────────────

// sampleDraftReq is the DraftOrderRequest equivalent of the webhook payload,
// mirroring what webhook.go now sends to Shopify.
func sampleDraftReq() shopify.DraftOrderRequest {
	return shopify.DraftOrderRequest{
		DraftOrder: shopify.DraftOrderBody{
			LineItems: []shopify.LineItem{
				{VariantID: 44965664260278, Quantity: 1},
			},
			Customer: &shopify.Customer{
				Email:     "lamlklk2002@gmail.com",
				FirstName: "Lâm",
				LastName:  "Nguyễn",
				Phone:     "3333",
			},
			Email:           "lamlklk2002@gmail.com",
			ShippingAddress: shippingAddr,
			BillingAddress:  shippingAddr,
			Note:            "PayOS QR transfer. paymentLinkId: a4bdadfe8657413fbf2baa8412d4bd44 | ref: FT26080900SF",
			Tags:            "payos,qr-transfer",
		},
	}
}

// draftOrderCreatedJSON is a minimal Shopify draft order response (status: open).
const draftOrderCreatedJSON = `{
	"draft_order": {
		"id": 1069920487,
		"name": "#D3",
		"status": "open",
		"order_id": 0,
		"invoice_url": "https://mocbydylan.myshopify.com/548380009/invoices/abc123"
	}
}`

// draftOrderCompletedJSON is a minimal response after PUT …/complete.json.
const draftOrderCompletedJSON = `{
	"draft_order": {
		"id": 1069920487,
		"name": "#D3",
		"status": "completed",
		"order_id": 987654321,
		"invoice_url": "https://mocbydylan.myshopify.com/548380009/invoices/abc123"
	}
}`

// ── CreateDraftOrder tests ────────────────────────────────────────────────────

// TestCreateDraftOrder_Success verifies a 201 response is correctly parsed.
func TestCreateDraftOrder_Success(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/admin/api/2026-01/draft_orders.json" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("X-Shopify-Access-Token") == "" {
			t.Error("missing X-Shopify-Access-Token header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(draftOrderCreatedJSON))
	})

	got, err := shopify.CreateDraftOrder(sampleDraftReq())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEq(t, "ID", int64(1069920487), got.ID)
	assertEq(t, "Name", "#D3", got.Name)
	assertEq(t, "Status", "open", got.Status)
	assertEq(t, "OrderID", int64(0), got.OrderID)
}

// TestCreateDraftOrder_ShopifyReturnsError verifies non-2xx is surfaced as an error.
func TestCreateDraftOrder_ShopifyReturnsError(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"errors":{"line_items":["is invalid"]}}`))
	})

	_, err := shopify.CreateDraftOrder(sampleDraftReq())
	if err == nil {
		t.Fatal("expected error for HTTP 422, got nil")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error should mention status 422, got: %s", err.Error())
	}
}

// TestCreateDraftOrder_MalformedJSONResponse verifies a parse error is returned.
func TestCreateDraftOrder_MalformedJSONResponse(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`not-json`))
	})

	_, err := shopify.CreateDraftOrder(sampleDraftReq())
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

// TestCreateDraftOrder_RequestPayload verifies the JSON body sent to Shopify
// contains all required fields and does NOT include financial_status or transactions.
func TestCreateDraftOrder_RequestPayload(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		var envelope struct {
			DraftOrder struct {
				LineItems []struct {
					VariantID int64 `json:"variant_id"`
					Quantity  int   `json:"quantity"`
				} `json:"line_items"`
				Customer struct {
					Email     string `json:"email"`
					FirstName string `json:"first_name"`
					LastName  string `json:"last_name"`
					Phone     string `json:"phone"`
				} `json:"customer"`
				Email           string `json:"email"`
				ShippingAddress struct {
					Address1    string `json:"address1"`
					CountryCode string `json:"country_code"`
				} `json:"shipping_address"`
				Note string `json:"note"`
				Tags string `json:"tags"`
			} `json:"draft_order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&envelope); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		d := envelope.DraftOrder

		assertEq(t, "variant_id", int64(44965664260278), d.LineItems[0].VariantID)
		assertEq(t, "quantity", 1, d.LineItems[0].Quantity)
		assertEq(t, "customer.email", "lamlklk2002@gmail.com", d.Customer.Email)
		assertEq(t, "customer.first_name", "Lâm", d.Customer.FirstName)
		assertEq(t, "customer.last_name", "Nguyễn", d.Customer.LastName)
		assertEq(t, "customer.phone", "3333", d.Customer.Phone)
		assertEq(t, "email", "lamlklk2002@gmail.com", d.Email)
		assertEq(t, "shipping_address.address1", "123 Test Street", d.ShippingAddress.Address1)
		assertEq(t, "shipping_address.country_code", "VN", d.ShippingAddress.CountryCode)
		assertEq(t, "tags", "payos,qr-transfer", d.Tags)
		if !strings.Contains(d.Note, "a4bdadfe8657413fbf2baa8412d4bd44") {
			t.Errorf("note should contain paymentLinkId, got: %s", d.Note)
		}

		// Draft order must NOT contain financial_status or transactions.
		var raw map[string]any
		// re-marshal the draft_order sub-object from what we already decoded
		rawBytes, _ := json.Marshal(d)
		json.Unmarshal(rawBytes, &raw) //nolint:errcheck
		if _, ok := raw["financial_status"]; ok {
			t.Error("draft_order must not contain financial_status")
		}
		if _, ok := raw["transactions"]; ok {
			t.Error("draft_order must not contain transactions")
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(draftOrderCreatedJSON))
	})

	if _, err := shopify.CreateDraftOrder(sampleDraftReq()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCreateDraftOrder_MissingDomain verifies that an empty SHOPIFY_STORE_DOMAIN
// results in an error rather than a panic.
func TestCreateDraftOrder_MissingDomain(t *testing.T) {
	orig := os.Getenv("SHOPIFY_STORE_DOMAIN")
	os.Setenv("SHOPIFY_STORE_DOMAIN", "")
	defer os.Setenv("SHOPIFY_STORE_DOMAIN", orig)

	_, err := shopify.CreateDraftOrder(sampleDraftReq())
	if err == nil {
		t.Fatal("expected error when domain is empty, got nil")
	}
}

// ── CompleteDraftOrder tests ──────────────────────────────────────────────────

// TestCompleteDraftOrder_Success verifies that completing a draft order returns
// the updated DraftOrderResponse with OrderID populated.
func TestCompleteDraftOrder_Success(t *testing.T) {
	const draftID int64 = 1069920487

	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/admin/api/2026-01/draft_orders/1069920487/complete.json"
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if r.URL.Path != wantPath {
			t.Errorf("path: want %s, got %s", wantPath, r.URL.Path)
		}
		if r.Header.Get("X-Shopify-Access-Token") == "" {
			t.Error("missing X-Shopify-Access-Token header")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(draftOrderCompletedJSON))
	})

	got, err := shopify.CompleteDraftOrder(draftID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertEq(t, "ID", draftID, got.ID)
	assertEq(t, "Status", "completed", got.Status)
	assertEq(t, "OrderID", int64(987654321), got.OrderID)
	assertEq(t, "Name", "#D3", got.Name)
}

// TestCompleteDraftOrder_ShopifyReturnsError verifies non-2xx is surfaced.
func TestCompleteDraftOrder_ShopifyReturnsError(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"errors":"draft order already completed"}`))
	})

	_, err := shopify.CompleteDraftOrder(1069920487)
	if err == nil {
		t.Fatal("expected error for HTTP 422, got nil")
	}
	if !strings.Contains(err.Error(), "422") {
		t.Errorf("error should mention 422, got: %s", err.Error())
	}
}

// TestCompleteDraftOrder_MalformedJSONResponse verifies parse errors are returned.
func TestCompleteDraftOrder_MalformedJSONResponse(t *testing.T) {
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{broken json`))
	})

	_, err := shopify.CompleteDraftOrder(1069920487)
	if err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

// ── full draft-order flow test ────────────────────────────────────────────────

// TestDraftOrderFlow_CreateThenComplete exercises the two-step flow used by
// the webhook handler: create a draft order, then complete it to get a paid order.
func TestDraftOrderFlow_CreateThenComplete(t *testing.T) {
	const wantDraftID int64 = 1069920487
	const wantOrderID int64 = 987654321

	// Single TLS server handles both endpoints by routing on method+path.
	setupMockServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost &&
			r.URL.Path == "/admin/api/2026-01/draft_orders.json":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(draftOrderCreatedJSON))

		case r.Method == http.MethodPut &&
			r.URL.Path == "/admin/api/2026-01/draft_orders/1069920487/complete.json":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(draftOrderCompletedJSON))

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// Step 1 — create.
	draft, err := shopify.CreateDraftOrder(sampleDraftReq())
	if err != nil {
		t.Fatalf("CreateDraftOrder: %v", err)
	}
	assertEq(t, "draft.ID", wantDraftID, draft.ID)
	assertEq(t, "draft.Status", "open", draft.Status)

	// Step 2 — complete.
	completed, err := shopify.CompleteDraftOrder(draft.ID)
	if err != nil {
		t.Fatalf("CompleteDraftOrder: %v", err)
	}
	assertEq(t, "completed.Status", "completed", completed.Status)
	assertEq(t, "completed.OrderID", wantOrderID, completed.OrderID)
}
