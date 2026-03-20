package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mocbydylan/payos-backend/internal/kv"
	"github.com/mocbydylan/payos-backend/internal/payos"
)

// CreatePaymentReq is the body sent from the Shopify storefront JS.
type CreatePaymentReq struct {
	OrderCode   int64       `json:"orderCode"`
	Amount      int64       `json:"amount"`      // VND (e.g. 150000)
	Description string      `json:"description"` // ≤9 chars
	BuyerName   string      `json:"buyerName"`
	BuyerEmail  string      `json:"buyerEmail"`
	BuyerPhone  string      `json:"buyerPhone"`
	LineItems   []LineItem  `json:"lineItems"`
}

// LineItem mirrors what the Shopify cart JS provides.
type LineItem struct {
	VariantID int64  `json:"variantId"`
	ProductID int64  `json:"productId"`
	Title     string `json:"title"`
	Quantity  int    `json:"quantity"`
	Price     int64  `json:"price"` // Shopify internal units (× 100)
}

func Handler(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req CreatePaymentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Amount <= 0 {
		jsonErr(w, "amount must be positive", http.StatusBadRequest)
		return
	}

	// Guarantee a unique int64 order code.
	if req.OrderCode == 0 {
		req.OrderCode = time.Now().UnixMilli()
	}

	// PayOS description is max 9 chars for non-linked bank accounts.
	desc := req.Description
	if len(desc) > 9 {
		desc = desc[:9]
	}
	if desc == "" {
		desc = fmt.Sprintf("M%d", req.OrderCode%100000)
	}

	storeDomain := os.Getenv("SHOPIFY_STORE_DOMAIN")
	cancelURL := fmt.Sprintf("https://%s/pages/payment-result?status=cancelled", storeDomain)
	returnURL := fmt.Sprintf("https://%s/pages/payment-result?status=success", storeDomain)

	// Build and sign the PayOS payload.
	sigInput := fmt.Sprintf(
		"amount=%d&cancelUrl=%s&description=%s&orderCode=%d&returnUrl=%s",
		req.Amount, cancelURL, desc, req.OrderCode, returnURL,
	)
	checksumKey := os.Getenv("PAYOS_CHECKSUM_KEY")
	mac := hmac.New(sha256.New, []byte(checksumKey))
	mac.Write([]byte(sigInput))
	signature := fmt.Sprintf("%x", mac.Sum(nil))

	// Map local line items to PayOS items.
	payosItems := make([]payos.Item, 0, len(req.LineItems))
	for _, li := range req.LineItems {
		payosItems = append(payosItems, payos.Item{
			Name:     li.Title,
			Quantity: li.Quantity,
			Price:    li.Price / 100, // convert Shopify internal units → VND
		})
	}

	// Call PayOS to create the payment link.
	payosResp, err := payos.CreatePaymentLink(payos.CreatePaymentRequest{
		OrderCode:   req.OrderCode,
		Amount:      req.Amount,
		Description: desc,
		BuyerName:   req.BuyerName,
		BuyerEmail:  req.BuyerEmail,
		BuyerPhone:  req.BuyerPhone,
		CancelURL:   cancelURL,
		ReturnURL:   returnURL,
		Signature:   signature,
		Items:       payosItems,
	})
	if err != nil {
		jsonErr(w, "failed to create payment link: "+err.Error(), http.StatusBadGateway)
		return
	}

	// Store the cart payload in KV so the webhook handler can create the Shopify order.
	kvPayload := kv.CartPayload{
		OrderCode:  req.OrderCode,
		Amount:     req.Amount,
		BuyerName:  req.BuyerName,
		BuyerEmail: req.BuyerEmail,
		BuyerPhone: req.BuyerPhone,
		LineItems:  toKVItems(req.LineItems),
	}
	if err := kv.Set(payosResp.PaymentLinkID, kvPayload, 20*60); err != nil {
		// Non-fatal: log and continue. Webhook will still process (albeit without buyer info).
		fmt.Printf("[payos-be] KV set error for %s: %v\n", payosResp.PaymentLinkID, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"qrCode":        payosResp.QRCode,
		"checkoutUrl":   payosResp.CheckoutURL,
		"paymentLinkId": payosResp.PaymentLinkID,
		"amount":        req.Amount,
	})
}

// toKVItems converts the handler's LineItem slice to kv.LineItem slice.
func toKVItems(items []LineItem) []kv.LineItem {
	out := make([]kv.LineItem, len(items))
	for i, it := range items {
		out[i] = kv.LineItem{
			VariantID: it.VariantID,
			ProductID: it.ProductID,
			Title:     it.Title,
			Quantity:  it.Quantity,
			Price:     it.Price,
		}
	}
	return out
}

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	// Allow requests from Shopify storefronts only.
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	} else {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
