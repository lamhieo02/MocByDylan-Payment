package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/mocbydylan/payos-backend/internal/kv"
	"github.com/mocbydylan/payos-backend/internal/shopify"
)

// webhookBody is the full PayOS webhook payload.
type webhookBody struct {
	Code      string          `json:"code"`
	Desc      string          `json:"desc"`
	Success   bool            `json:"success"`
	Data      json.RawMessage `json:"data"`
	Signature string          `json:"signature"`
}

// webhookData contains the payment data nested inside the webhook body.
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

// Handler receives PayOS payment webhooks, verifies the HMAC signature,
// and creates a paid Shopify order when payment is confirmed.
func Handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read the full body (needed for signature verification and parsing).
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

	// Parse the nested data so we can verify the signature.
	var data webhookData
	if err := json.Unmarshal(body.Data, &data); err != nil {
		http.Error(w, "invalid data field", http.StatusBadRequest)
		return
	}

	// Verify HMAC signature over the sorted data fields.
	if !verifyWebhookSignature(data, body.Signature) {
		fmt.Printf("[webhook] signature mismatch for paymentLinkId=%s\n", data.PaymentLinkID)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	// Only act on successful payment events.
	if !body.Success || body.Code != "00" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Idempotency: skip if already processed.
	processed, err := kv.IsProcessed(data.PaymentLinkID)
	if err != nil {
		fmt.Printf("[webhook] KV IsProcessed error: %v\n", err)
		// Continue — better to risk duplicate than to drop a payment.
	}
	if processed {
		fmt.Printf("[webhook] duplicate event for paymentLinkId=%s, skipping\n", data.PaymentLinkID)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Retrieve the cart payload stored during create-payment.
	payload, err := kv.GetCartPayload(data.PaymentLinkID)
	if err != nil || payload == nil {
		// Cart data expired or was never stored. Build a minimal order.
		fmt.Printf("[webhook] no KV payload for paymentLinkId=%s, building minimal order\n", data.PaymentLinkID)
		payload = &kv.CartPayload{
			OrderCode:  data.OrderCode,
			Amount:     data.Amount,
			BuyerEmail: "",
		}
	}

	// Build the Shopify order.
	firstName, lastName := shopify.ParseName(payload.BuyerName)
	lineItems := toShopifyLineItems(payload.LineItems)

	amountStr := fmt.Sprintf("%d", data.Amount)
	currency := "VND"

	orderReq := shopify.OrderRequest{
		Order: shopify.OrderBody{
			LineItems: lineItems,
			Customer: shopify.Customer{
				Email:     payload.BuyerEmail,
				Phone:     payload.BuyerPhone,
				FirstName: firstName,
				LastName:  lastName,
			},
			FinancialStatus: "paid",
			Currency:        currency,
			Transactions: []shopify.Transaction{
				{
					Kind:          "sale",
					Status:        "success",
					Amount:        amountStr,
					Currency:      currency,
					Gateway:       "payos",
					Authorization: data.PaymentLinkID,
				},
			},
			Note:                   fmt.Sprintf("PayOS QR transfer. paymentLinkId: %s | ref: %s", data.PaymentLinkID, data.Reference),
			Tags:                   "payos,qr-transfer",
			SendReceipt:            true,
			SendFulfillmentReceipt: true,
		},
	}

	order, err := shopify.CreateOrder(orderReq)
	if err != nil {
		fmt.Printf("[webhook] Shopify order creation failed for paymentLinkId=%s: %v\n", data.PaymentLinkID, err)
		// Return 500 so PayOS will retry.
		http.Error(w, "order creation failed", http.StatusInternalServerError)
		return
	}

	fmt.Printf("[webhook] Shopify order created: %s (id=%d) for paymentLinkId=%s\n",
		order.Name, order.ID, data.PaymentLinkID)

	// Mark as processed to prevent duplicate order creation on retry.
	if err := kv.MarkProcessed(data.PaymentLinkID); err != nil {
		fmt.Printf("[webhook] KV MarkProcessed error: %v\n", err)
	}

	w.WriteHeader(http.StatusOK)
}

// verifyWebhookSignature builds the sorted data string and compares the HMAC.
// PayOS signs all fields in the data object sorted alphabetically.
func verifyWebhookSignature(data webhookData, receivedSig string) bool {
	fields := map[string]string{
		"accountNumber":          data.AccountNumber,
		"amount":                 fmt.Sprintf("%d", data.Amount),
		"code":                   data.Code,
		"counterAccountBankId":   data.CounterAccountBankID,
		"counterAccountBankName": data.CounterAccountBankName,
		"counterAccountName":     data.CounterAccountName,
		"counterAccountNumber":   data.CounterAccountNumber,
		"currency":               data.Currency,
		"description":            data.Description,
		"orderCode":              fmt.Sprintf("%d", data.OrderCode),
		"paymentLinkId":          data.PaymentLinkID,
		"reference":              data.Reference,
		"transactionDateTime":    data.TransactionDateTime,
		"virtualAccountName":     data.VirtualAccountName,
		"virtualAccountNumber":   data.VirtualAccountNumber,
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + fields[k]
	}
	sigInput := strings.Join(parts, "&")

	checksumKey := os.Getenv("PAYOS_CHECKSUM_KEY")
	mac := hmac.New(sha256.New, []byte(checksumKey))
	mac.Write([]byte(sigInput))
	expected := fmt.Sprintf("%x", mac.Sum(nil))

	return hmac.Equal([]byte(expected), []byte(receivedSig))
}

// toShopifyLineItems converts kv.LineItem to shopify.LineItem.
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
	// Guard: if no valid line items, Shopify will reject the order.
	// Return empty slice; caller logs the warning.
	return out
}
