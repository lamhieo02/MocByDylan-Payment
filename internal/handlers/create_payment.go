package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/db"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/payos"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

// createPaymentReq is the body sent from the Shopify storefront JS.
type createPaymentReq struct {
	OrderCode       int64      `json:"orderCode"`
	Amount          int64      `json:"amount"`      // VND (e.g. 150000)
	Description     string     `json:"description"` // ≤9 chars
	BuyerName       string     `json:"buyerName"`
	BuyerEmail      string     `json:"buyerEmail"`
	BuyerPhone      string     `json:"buyerPhone"`
	ShippingAddress string     `json:"shippingAddress"` // full address string
	LineItems       []lineItem `json:"lineItems"`
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

	// ── Build Shopify Draft Order immediately ─────────────────────────────────
	// Creating the draft order here (before payment) means Shopify reserves
	// inventory and has the order ready. When the PayOS webhook fires, we
	// complete the draft order instead of creating a new one — ensuring no
	// duplicate orders and enabling Shopify's automatic Order Confirmation email.
	kvItems := toKVItems(req.LineItems)
	draftOrderID, draftOrderName := createDraftOrder(req, payosResp.PaymentLinkID)

	kvPayload := kv.CartPayload{
		OrderCode:       req.OrderCode,
		Amount:          req.Amount,
		BuyerName:       req.BuyerName,
		BuyerEmail:      req.BuyerEmail,
		BuyerPhone:      req.BuyerPhone,
		ShippingAddress: req.ShippingAddress,
		LineItems:       kvItems,
		DraftOrderID:    draftOrderID,
		DraftOrderName:  draftOrderName,
	}
	if err := kv.Set(payosResp.PaymentLinkID, kvPayload, 20*60); err != nil {
		log.Printf("[create-payment] KV set error for %s: %v", payosResp.PaymentLinkID, err)
		jsonErr(w, "failed to set KV: "+err.Error(), http.StatusBadGateway)
		return
	}

	if err := db.SaveOrder(db.OrderRecord{
		PaymentLinkID:   payosResp.PaymentLinkID,
		OrderCode:       req.OrderCode,
		Amount:          req.Amount,
		Description:     desc,
		BuyerName:       req.BuyerName,
		BuyerEmail:      req.BuyerEmail,
		BuyerPhone:      req.BuyerPhone,
		ShippingAddress: req.ShippingAddress,
		LineItems:       kvPayload.LineItems,
	}); err != nil {
		log.Printf("[create-payment] DB SaveOrder error for %s: %v", payosResp.PaymentLinkID, err)
	}

	// Persist draft order IDs to DB (separate update so SaveOrder remains idempotent).
	if draftOrderID > 0 {
		if err := db.UpdateDraftOrder(payosResp.PaymentLinkID, draftOrderID, draftOrderName); err != nil {
			log.Printf("[create-payment] DB UpdateDraftOrder error for %s: %v", payosResp.PaymentLinkID, err)
		}
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

// createDraftOrder creates a Shopify Draft Order for the given payment request.
// It returns the draft order ID and name. On failure it logs the error and
// returns (0, "") — the caller falls back to creating a new order at webhook time.
func createDraftOrder(req createPaymentReq, paymentLinkID string) (id int64, name string) {
	firstName, lastName := shopify.ParseName(req.BuyerName)

	// Fallback: when buyer has a single name, use it as both first and last so
	// Shopify accepts the shipping address (requires non-empty last_name).
	addrLastName := lastName
	if addrLastName == "" {
		addrLastName = firstName
	}

	// Derive shipping fee: PayOS amount − sum of line item prices.
	var lineSubtotalVND int64
	for _, it := range req.LineItems {
		lineSubtotalVND += (it.Price / 100) * int64(it.Quantity)
	}
	shippingFeeVND := req.Amount - lineSubtotalVND
	if shippingFeeVND < 0 {
		shippingFeeVND = 0
	}

	// Extract city from the last comma-separated segment of "street, ward, province".
	shippingCity := ""
	if req.ShippingAddress != "" {
		parts := strings.Split(req.ShippingAddress, ", ")
		if len(parts) > 0 {
			shippingCity = strings.TrimSpace(parts[len(parts)-1])
		}
	}

	lineItems := make([]shopify.LineItem, 0, len(req.LineItems))
	for _, it := range req.LineItems {
		if it.VariantID > 0 {
			lineItems = append(lineItems, shopify.LineItem{
				VariantID: it.VariantID,
				Quantity:  it.Quantity,
			})
		}
	}

	var shippingAddr *shopify.ShippingAddress
	if req.ShippingAddress != "" {
		shippingAddr = &shopify.ShippingAddress{
			FirstName:   firstName,
			LastName:    addrLastName,
			Phone:       req.BuyerPhone,
			Address1:    req.ShippingAddress,
			City:        shippingCity,
			Country:     "Vietnam",
			CountryCode: "VN",
		}
	}

	var shippingLine *shopify.DraftOrderShippingLine
	if shippingFeeVND > 0 {
		shippingLine = &shopify.DraftOrderShippingLine{
			Title: "Giao hàng tiêu chuẩn",
			Price: fmt.Sprintf("%d", shippingFeeVND),
		}
	}

	noteText := fmt.Sprintf("PayOS QR payment link created. paymentLinkId: %s", paymentLinkID)

	draftReq := shopify.DraftOrderRequest{
		DraftOrder: shopify.DraftOrderBody{
			LineItems: lineItems,
			Customer: &shopify.Customer{
				Email:     req.BuyerEmail,
				FirstName: firstName,
				LastName:  lastName,
				Phone:     req.BuyerPhone,
			},
			Email:           req.BuyerEmail,
			ShippingAddress: shippingAddr,
			BillingAddress:  shippingAddr,
			ShippingLine:    shippingLine,
			Note:            noteText,
			Tags:            "payos,qr-transfer,draft",
		},
	}

	draft, err := shopify.CreateDraftOrder(draftReq)
	if err != nil {
		log.Printf("[create-payment] Shopify CreateDraftOrder FAILED | paymentLinkId=%s | error=%v", paymentLinkID, err)
		return 0, ""
	}

	log.Printf("[create-payment] Draft Order Created | DraftID=%d | DraftName=%s | Email=%s | PaymentLinkID=%s",
		draft.ID, draft.Name, req.BuyerEmail, paymentLinkID)
	return draft.ID, draft.Name
}
