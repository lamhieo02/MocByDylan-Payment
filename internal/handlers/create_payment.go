package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/payos"
)

// createPaymentReq is the body sent from the Shopify storefront JS.
type createPaymentReq struct {
	OrderCode   int64      `json:"orderCode"`
	Amount      int64      `json:"amount"`      // VND (e.g. 150000)
	Description string     `json:"description"` // ≤9 chars
	BuyerName   string     `json:"buyerName"`
	BuyerEmail  string     `json:"buyerEmail"`
	BuyerPhone  string     `json:"buyerPhone"`
	LineItems   []lineItem `json:"lineItems"`
}

// lineItem mirrors what the Shopify cart JS provides.
type lineItem struct {
	VariantID int64  `json:"variantId"`
	ProductID int64  `json:"productId"`
	Title     string `json:"title"`
	Quantity  int    `json:"quantity"`
	Price     int64  `json:"price"` // Shopify internal units (× 100)
}

// CreatePayment handles POST /api/create-payment.
func CreatePayment(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req createPaymentReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Amount <= 0 {
		jsonErr(w, "amount must be positive", http.StatusBadRequest)
		return
	}

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

	sigInput := fmt.Sprintf(
		"amount=%d&cancelUrl=%s&description=%s&orderCode=%d&returnUrl=%s",
		req.Amount, cancelURL, desc, req.OrderCode, returnURL,
	)
	checksumKey := os.Getenv("PAYOS_CHECKSUM_KEY")
	mac := hmac.New(sha256.New, []byte(checksumKey))
	mac.Write([]byte(sigInput))
	signature := fmt.Sprintf("%x", mac.Sum(nil))

	payosItems := make([]payos.Item, 0, len(req.LineItems))
	for _, li := range req.LineItems {
		payosItems = append(payosItems, payos.Item{
			Name:     li.Title,
			Quantity: li.Quantity,
			Price:    li.Price / 100,
		})
	}

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

	kvPayload := kv.CartPayload{
		OrderCode:  req.OrderCode,
		Amount:     req.Amount,
		BuyerName:  req.BuyerName,
		BuyerEmail: req.BuyerEmail,
		BuyerPhone: req.BuyerPhone,
		LineItems:  toKVItems(req.LineItems),
	}
	if err := kv.Set(payosResp.PaymentLinkID, kvPayload, 20*60); err != nil {
		fmt.Printf("[create-payment] KV set error for %s: %v\n", payosResp.PaymentLinkID, err)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"qrCode":        payosResp.QRCode,
		"checkoutUrl":   payosResp.CheckoutURL,
		"paymentLinkId": payosResp.PaymentLinkID,
		"amount":        req.Amount,
	})
}

func toKVItems(items []lineItem) []kv.LineItem {
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
