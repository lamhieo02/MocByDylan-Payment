package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/db"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/mailer"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/notify"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/payos"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

// waitDraftRetry is a variable so tests can replace it with a no-op to avoid
// sleeping during unit tests.
var waitDraftRetry = func(seconds int) { time.Sleep(time.Duration(seconds) * time.Second) }

// ── Payment gateway resolution ────────────────────────────────────────────────
// Resolution order:
//  1. SHOPIFY_PAYOS_GATEWAY_ID  — explicit GID, no API call (fastest, recommended)
//  2. SHOPIFY_PAYOS_GATEWAY_NAME — auto-lookup via REST (requires read_payment_gateways scope)
//
// To get the GID without adding a scope, run once:
//
//	go run ./cmd/get-gateway-id/main.go
//
// then set: SHOPIFY_PAYOS_GATEWAY_ID=gid://shopify/PaymentGateway/123456789
var (
	resolvedGatewayID  string
	gatewayResolveOnce sync.Once
)

func payosGatewayID() string {
	gatewayResolveOnce.Do(func() {
		// Explicit GID takes priority — no API call.
		if id := os.Getenv("SHOPIFY_PAYOS_GATEWAY_ID"); id != "" {
			resolvedGatewayID = id
			log.Printf("[webhook] payment gateway GID (env): %s", id)
			return
		}
		// Auto-lookup via REST; requires read_payment_gateways scope on the token.
		name := os.Getenv("SHOPIFY_PAYOS_GATEWAY_NAME")
		if name == "" {
			name = "Bank Deposit"
		}
		id, err := shopify.FetchPaymentGatewayID(name)
		if err != nil {
			log.Printf("[webhook] WARNING: cannot resolve gateway %q: %v\n"+
				"  → Payment label will show as \"Manual\".\n"+
				"  → Fix: run `go run ./cmd/get-gateway-id/main.go` and set SHOPIFY_PAYOS_GATEWAY_ID",
				name, err)
			return
		}
		resolvedGatewayID = id
		log.Printf("[webhook] payment gateway %q resolved → %s", name, id)
	})
	return resolvedGatewayID
}

type webhookBody struct {
	Code      string          `json:"code"`
	Desc      string          `json:"desc"`
	Success   bool            `json:"success"`
	Data      json.RawMessage `json:"data"`
	Signature string          `json:"signature"`
}

type webhookData struct {
	OrderCode              int64  `json:"orderCode"`
	Amount                 int64  `json:"amount"`
	Description            string `json:"description"`
	AccountNumber          string `json:"accountNumber"`
	Reference              string `json:"reference"`
	TransactionDateTime    string `json:"transactionDateTime"`
	Currency               string `json:"currency"`
	PaymentLinkID          string `json:"paymentLinkId"`
	Code                   string `json:"code"`
	Desc                   string `json:"desc"`
	CounterAccountBankID   string `json:"counterAccountBankId"`
	CounterAccountBankName string `json:"counterAccountBankName"`
	CounterAccountName     string `json:"counterAccountName"`
	CounterAccountNumber   string `json:"counterAccountNumber"`
	VirtualAccountName     string `json:"virtualAccountName"`
	VirtualAccountNumber   string `json:"virtualAccountNumber"`
}

// Webhook handles POST /api/webhook — receives PayOS payment notifications,
// verifies the HMAC signature, and creates a paid Shopify order.
func Webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "cannot read body", http.StatusBadRequest)
		return
	}

	var body webhookBody
	if err := json.Unmarshal(raw, &body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	var data webhookData
	if err := json.Unmarshal(body.Data, &data); err != nil {
		http.Error(w, "invalid data field", http.StatusBadRequest)
		return
	}

	// Signature must use raw `data` JSON so all keys match PayOS (including new fields).
	if !payos.VerifyPaymentWebhookSignature(body.Data, body.Signature) {
		log.Printf("[webhook] signature mismatch for paymentLinkId=%s", data.PaymentLinkID)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	if !body.Success || body.Code != "00" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Atomically claim the processing right before touching any external system.
	// Redis SET NX guarantees only one concurrent handler proceeds — eliminating
	// the race window that existed when the flag was set at the end of the handler.
	//
	// If Redis is unavailable, we send a critical Discord alert so ops can
	// manually recover the order. Returning 500 causes PayOS to retry, but if
	// Redis stays down beyond the PayOS retry window the payment would be lost
	// without this alert.
	claimed, err := kv.TryMarkProcessed(data.PaymentLinkID)
	if err != nil {
		log.Printf("[webhook] TryMarkProcessed error for paymentLinkId=%s: %v", data.PaymentLinkID, err)
		notify.SendOrderNotify(notify.OrderInfo{
			OrderCode:     data.OrderCode,
			Amount:        data.Amount,
			PaymentLinkID: data.PaymentLinkID,
			Reference:     data.Reference,
			ShopifyErr:    "CRITICAL: Redis unavailable — idempotency check failed. Order NOT processed. Manual recovery required.",
		})
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !claimed {
		log.Printf("[webhook] duplicate event for paymentLinkId=%s, skipping", data.PaymentLinkID)
		w.WriteHeader(http.StatusOK)
		return
	}

	payload, err := kv.GetCartPayload(data.PaymentLinkID)
	if err != nil {
		log.Printf("[webhook] KV error for paymentLinkId=%s: %v", data.PaymentLinkID, err)
	}
	if payload == nil {
		// Redis TTL (20 min) may have expired before the webhook arrived.
		// Fall back to PostgreSQL which has the same data persisted at link-creation time.
		log.Printf("[webhook] Redis miss for paymentLinkId=%s — trying DB fallback", data.PaymentLinkID)
		dbRec, dbErr := db.GetCartPayload(data.PaymentLinkID)
		if dbErr != nil {
			log.Printf("[webhook] DB fallback error for paymentLinkId=%s: %v", data.PaymentLinkID, dbErr)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if dbRec == nil {
			log.Printf("[webhook] no cart data found (redis+db miss) for paymentLinkId=%s", data.PaymentLinkID)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		converted, convErr := cartPayloadFromDB(dbRec)
		if convErr != nil {
			log.Printf("[webhook] DB record conversion error for paymentLinkId=%s: %v", data.PaymentLinkID, convErr)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		payload = converted
		log.Printf("[webhook] using DB fallback payload for paymentLinkId=%s", data.PaymentLinkID)
	}

	// validate payload
	if err := ValidateRequestDataWebhook(payload); err != nil {
		log.Printf("[webhook] invalid payload for paymentLinkId=%s: %v", data.PaymentLinkID, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Amount sanity check: PayOS guarantees data.Amount == the linked amount,
	// but log a warning if they diverge (e.g. PayOS config bug, partial payment edge case).
	if payload.Amount > 0 && data.Amount != payload.Amount {
		log.Printf("[webhook] WARNING: amount mismatch for paymentLinkId=%s — expected %d VND, PayOS reported %d VND. Proceeding with actual received amount.",
			data.PaymentLinkID, payload.Amount, data.Amount)
	}

	// Warn when buyer email is missing: Shopify cannot send the Order Confirmation
	// email without a recipient address. The order will still be created, but no
	// receipt will be delivered even if sendReceipt=true.
	if payload.BuyerEmail == "" {
		log.Printf("[webhook] WARNING: buyerEmail is empty for paymentLinkId=%s — Shopify Order Confirmation email will NOT be sent", data.PaymentLinkID)
	}

	log.Printf("[webhook] Webhook Received | PaymentLinkID=%s | Amount=%d | BuyerEmail=%s | DraftOrderID=%d | DraftOrderName=%s",
		data.PaymentLinkID, data.Amount, payload.BuyerEmail, payload.DraftOrderID, payload.DraftOrderName)

	firstName, lastName := shopify.ParseName(payload.BuyerName)
	lineItems := toShopifyLineItems(payload.LineItems)

	// Shopify ignores shipping_address when either first_name or last_name is
	// empty, so fall back to first_name when the buyer has only one name.
	addrLastName := lastName
	if addrLastName == "" {
		addrLastName = firstName
	}

	// Shipping fee = PayOS transfer amount − sum of line item prices.
	// PayOS receives the exact amount the customer transferred, which already
	// includes the shipping fee baked in by the storefront.
	var lineSubtotalVND int64
	for _, it := range payload.LineItems {
		lineSubtotalVND += (it.Price / 100) * int64(it.Quantity)
	}
	shippingFeeVND := data.Amount - lineSubtotalVND
	if shippingFeeVND < 0 {
		shippingFeeVND = 0
	}

	// Extract the city/province from the last comma-separated segment of the
	// address string (format: "street, ward, province").
	shippingCity := ""
	if payload.ShippingAddress != "" {
		parts := strings.Split(payload.ShippingAddress, ", ")
		if len(parts) > 0 {
			shippingCity = strings.TrimSpace(parts[len(parts)-1])
		}
	}

	var shippingAddr *shopify.ShippingAddress
	if payload.ShippingAddress != "" {
		shippingAddr = &shopify.ShippingAddress{
			FirstName:   firstName,
			LastName:    addrLastName,
			Phone:       payload.BuyerPhone,
			Address1:    payload.ShippingAddress,
			City:        shippingCity,
			Country:     "Vietnam",
			CountryCode: "VN",
		}
	}

	amountStr := fmt.Sprintf("%.2f", float64(data.Amount))
	orderNote := fmt.Sprintf("PayOS QR transfer. paymentLinkId: %s | ref: %s", data.PaymentLinkID, data.Reference)

	orderReq := shopify.OrderRequest{
		Order: shopify.OrderBody{
			LineItems: lineItems,
			Customer: shopify.Customer{
				Email:     payload.BuyerEmail,
				Phone:     payload.BuyerPhone,
				FirstName: firstName,
				LastName:  lastName,
			},
			ShippingAddress:        shippingAddr,
			BillingAddress:         shippingAddr,
			FinancialStatus:        "paid",
			Transactions: []shopify.Transaction{{
				Kind:          "sale",
				Status:        "success",
				Amount:        amountStr,
				Currency:      "VND",
				Gateway:       "payos",
				Authorization: data.PaymentLinkID,
			}},
		Note:                   orderNote,
		Tags:                   "payos,qr-transfer",
		// sendReceipt=true instructs Shopify to send the official Order Confirmation
		// email (via the store's Notification template) to the order.email address.
		// order.email is populated from payload.BuyerEmail in the GQL input layer.
		SendReceipt:            true,
		SendFulfillmentReceipt: false,
		ShippingFeeVND:         shippingFeeVND,
		},
	}

	var shopifyOrderID int64
	var shopifyOrderName string
	var shopifyErrMsg string
	var dbNote string

	// ── Path A: Complete existing Draft Order ─────────────────────────────────
	// Primary path: when a Draft Order was created at payment-link time, complete
	// it now. draftOrderComplete triggers Shopify's standard order notification
	// pipeline, which automatically sends the Order Confirmation email to the
	// address on the draft order — no sendReceipt flag required.
	if payload.DraftOrderID > 0 {
		log.Printf("[webhook] Draft Completion Started | DraftID=%d | DraftName=%s | PaymentLinkID=%s",
			payload.DraftOrderID, payload.DraftOrderName, data.PaymentLinkID)

		completed, completeErr := completeDraftWithRetry(payload.DraftOrderID, data.PaymentLinkID, payosGatewayID())
		if completeErr != nil {
			// Draft completion failed after all retries. Payment is already confirmed
			// by PayOS — fall through to Path B (GQL fallback) to avoid a lost order.
			log.Printf("[webhook] CRITICAL: Draft completion failed after retries | DraftID=%d | PaymentLinkID=%s | error=%v",
				payload.DraftOrderID, data.PaymentLinkID, completeErr)
			shopifyErrMsg = "draft completion failed: " + completeErr.Error()
			dbNote = shopifyErrMsg
		} else {
			shopifyOrderID = completed.ID
			shopifyOrderName = completed.Name
			log.Printf("[webhook] Draft Completed Successfully | DraftID=%d | OrderID=%d | OrderNumber=%s | Email=%s | FinancialStatus=%s | PaymentLinkID=%s",
				payload.DraftOrderID,
				shopifyOrderID,
				shopifyOrderName,
				completed.Email,
				completed.FinancialStatus,
				data.PaymentLinkID,
			)
			if completed.Email == "" {
				log.Printf("[webhook] WARNING: Shopify returned empty email for order %s (ID=%d) — Order Confirmation email may not be sent",
					shopifyOrderName, shopifyOrderID)
			}

		// Update the order note with full PayOS settlement details.
		// Note: adding a new "sale" transaction via REST is rejected (HTTP 422)
		// because draftOrderComplete already records the payment internally.
		// Updating the note is the correct audit trail for the draft-order path.
		payosNote := fmt.Sprintf(
			"PayOS QR transfer confirmed.\npaymentLinkId: %s\nref: %s\namount: %d VND\ntxDatetime: %s",
			data.PaymentLinkID, data.Reference, data.Amount, data.TransactionDateTime,
		)
		if noteErr := shopify.UpdateOrderNote(shopifyOrderID, payosNote); noteErr != nil {
			log.Printf("[webhook] WARNING: UpdateOrderNote failed for order %d: %v", shopifyOrderID, noteErr)
		} else {
			log.Printf("[webhook] PayOS note written to order | OrderID=%d | Ref=%s", shopifyOrderID, data.Reference)
		}
		}
	}

	// ── Path B: GQL fallback ──────────────────────────────────────────────────
	// Triggered when: (1) no draft order exists (draft creation failed at
	// payment time or pre-dates this architecture), or (2) draft completion
	// failed above. Keeps backward-compatibility and ensures no paid order is lost.
	if shopifyOrderID == 0 {
		log.Printf("[webhook] calling CreateOrderGQL (fallback) | paymentLinkId=%s | buyerEmail=%s | sendReceipt=%v | amount=%d | lineItems=%d",
			data.PaymentLinkID, payload.BuyerEmail, orderReq.Order.SendReceipt, data.Amount, len(lineItems))

		created, createErr := shopify.CreateOrderGQL(orderReq)
		if createErr != nil {
			log.Printf("[webhook] Shopify CreateOrderGQL FAILED | paymentLinkId=%s | error=%v", data.PaymentLinkID, createErr)
			if shopifyErrMsg == "" {
				shopifyErrMsg = createErr.Error()
			}
			dbNote = "shopify CreateOrder: " + createErr.Error()
		} else {
			shopifyOrderID = created.ID
			shopifyOrderName = created.Name
			receiptWillSend := created.Email != ""
			log.Printf("[webhook] Order Created via GQL (fallback) | ID=%d | OrderNumber=%s | Email=%s | PaymentLinkID=%s | SendReceipt=%v | FinancialStatus=%s",
				shopifyOrderID,
				shopifyOrderName,
				created.Email,
				data.PaymentLinkID,
				receiptWillSend,
				created.FinancialStatus,
			)
			if !receiptWillSend {
				log.Printf("[webhook] WARNING: Shopify returned empty email for order %s (ID=%d) — Order Confirmation email may not be sent", shopifyOrderName, shopifyOrderID)
			}
		}
	}

	if err := db.UpdateOrderPaid(data.PaymentLinkID, shopifyOrderID, shopifyOrderName, data.Reference, data.TransactionDateTime, dbNote); err != nil {
		log.Printf("[webhook] DB UpdateOrderPaid error: %v", err)
	}

	notify.SendOrderNotify(notify.OrderInfo{
		OrderCode:              data.OrderCode,
		Amount:                 data.Amount,
		PaymentLinkID:          data.PaymentLinkID,
		Reference:              data.Reference,
		TransactionDateTime:    data.TransactionDateTime,
		AccountNumber:          data.AccountNumber,
		CounterAccountBankName: data.CounterAccountBankName,
		CounterAccountName:     data.CounterAccountName,
		CounterAccountNumber:   data.CounterAccountNumber,
		BuyerName:              payload.BuyerName,
		BuyerEmail:             payload.BuyerEmail,
		BuyerPhone:             payload.BuyerPhone,
		ShippingAddress:        payload.ShippingAddress,
		LineItems:              payload.LineItems,
		ShopifyOrderID:         shopifyOrderID,
		ShopifyOrderName:       shopifyOrderName,
		ShopifyErr:             shopifyErrMsg,
	})

	// Build line items for the email notification.
	mailItems := make([]mailer.LineItem, 0, len(payload.LineItems))
	for _, it := range payload.LineItems {
		mailItems = append(mailItems, mailer.LineItem{
			Title:     it.Title,
			VariantID: it.VariantID,
			Quantity:  it.Quantity,
			Price:     it.Price / 100, // convert Shopify units → VND
		})
	}
	mailOrderName := shopifyOrderName
	if mailOrderName == "" {
		mailOrderName = "— (chưa tạo trên Shopify — tra DB)"
	}
	mailer.SendOrderNotification(mailer.Notification{
		ShopifyOrderName:    mailOrderName,
		ShopifyOrderID:      shopifyOrderID,
		PaymentLinkID:       data.PaymentLinkID,
		Reference:           data.Reference,
		TransactionDatetime: data.TransactionDateTime,
		Amount:              data.Amount,
		BuyerName:           payload.BuyerName,
		BuyerEmail:          payload.BuyerEmail,
		BuyerPhone:          payload.BuyerPhone,
		ShippingAddress:     payload.ShippingAddress,
		LineItems:           mailItems,
	})

	w.WriteHeader(http.StatusOK)
}

// cartPayloadFromDB converts a db.CartPayloadRecord (PostgreSQL fallback) into the
// kv.CartPayload structure expected by the rest of the webhook handler.
// DraftOrderID and DraftOrderName are forwarded so the draft completion path works
// even when the Redis key has expired and we fall back to the database.
func cartPayloadFromDB(rec *db.CartPayloadRecord) (*kv.CartPayload, error) {
	var items []kv.LineItem
	if len(rec.LineItemsJSON) > 0 {
		if err := json.Unmarshal(rec.LineItemsJSON, &items); err != nil {
			return nil, fmt.Errorf("unmarshal line_items from db: %w", err)
		}
	}
	return &kv.CartPayload{
		OrderCode:       rec.OrderCode,
		Amount:          rec.Amount,
		BuyerName:       rec.BuyerName,
		BuyerEmail:      rec.BuyerEmail,
		BuyerPhone:      rec.BuyerPhone,
		ShippingAddress: rec.ShippingAddress,
		LineItems:       items,
		DraftOrderID:    rec.DraftOrderID,
		DraftOrderName:  rec.DraftOrderName,
	}, nil
}

// completeDraftWithRetry attempts to complete the Shopify Draft Order up to
// maxDraftRetries times with exponential backoff. It is called from the PayOS
// webhook handler after a successful payment confirmation.
//
// Why retry: Draft Order completion can transiently fail due to Shopify API
// rate-limits or network hiccups. Since the PayOS payment is already confirmed,
// we must not give up silently — every retry keeps the order recovery window open.
// If all retries fail, the caller falls back to CreateOrderGQL to avoid data loss.
//
// Fail-fast: Shopify userErrors (validation failures) are non-retriable — the
// same request will always fail. We detect them by the "userErrors" keyword and
// return immediately instead of burning through all retry slots.
//
// paymentGatewayID is the Shopify GID of a Manual Payment Method set via
// SHOPIFY_PAYOS_GATEWAY_ID. When the gateway is invalid, a single attempt fails
// with "Invalid payment gateway" (userError) and we bail out immediately.
func completeDraftWithRetry(draftOrderID int64, paymentLinkID string, paymentGatewayID string) (*shopify.OrderResponse, error) {
	const maxDraftRetries = 10
	// Attempts 1-3: quick backoff (2s, 5s, 10s).
	// Attempts 4-10: fixed 25s — gives Shopify time to recover from rate-limits
	// or transient outages while keeping the total window under ~3.5 minutes.
	backoff := []int{2, 5, 10, 25, 25, 25, 25, 25, 25, 25}

	var lastErr error
	for attempt := 1; attempt <= maxDraftRetries; attempt++ {
		order, err := shopify.CompleteDraftOrderGQL(draftOrderID, paymentGatewayID)
		if err == nil {
			return order, nil
		}
		lastErr = err
		log.Printf("[webhook] Draft completion attempt %d/%d FAILED | DraftID=%d | PaymentLinkID=%s | error=%v",
			attempt, maxDraftRetries, draftOrderID, paymentLinkID, err)

		// Shopify userErrors are permanent validation failures (e.g. "Invalid payment
		// gateway", "Draft order already completed"). Retrying is pointless — bail out
		// immediately so the caller can fall back to Path B without delay.
		if strings.Contains(err.Error(), "userErrors") {
			log.Printf("[webhook] Non-retriable userError detected — aborting retry loop | DraftID=%d", draftOrderID)
			return nil, fmt.Errorf("non-retriable: %w", lastErr)
		}

		if attempt < maxDraftRetries {
			sleepSec := backoff[attempt-1]
			log.Printf("[webhook] Retrying draft completion in %ds | DraftID=%d", sleepSec, draftOrderID)
			waitDraftRetry(sleepSec)
		}
	}
	return nil, fmt.Errorf("all %d attempts failed: %w", maxDraftRetries, lastErr)
}

func toShopifyLineItems(items []kv.LineItem) []shopify.LineItem {
	out := make([]shopify.LineItem, 0, len(items))
	for _, it := range items {
		if it.VariantID > 0 {
			out = append(out, shopify.LineItem{
				VariantID: it.VariantID,
				Quantity:  it.Quantity,
			})
		}
	}
	return out
}
