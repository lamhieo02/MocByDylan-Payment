package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/db"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/mailer"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/payos"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

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
		fmt.Printf("[webhook] signature mismatch for paymentLinkId=%s\n", data.PaymentLinkID)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	if !body.Success || body.Code != "00" {
		w.WriteHeader(http.StatusOK)
		return
	}

	processed, err := kv.IsProcessed(data.PaymentLinkID)
	if err != nil {
		fmt.Printf("[webhook] KV IsProcessed error: %v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if processed {
		fmt.Printf("[webhook] duplicate event for paymentLinkId=%s, skipping\n", data.PaymentLinkID)
		w.WriteHeader(http.StatusOK)
		return
	}

	payload, err := kv.GetCartPayload(data.PaymentLinkID)
	if err != nil || payload == nil {
		fmt.Printf("[webhook] no KV payload for paymentLinkId=%s, building minimal order\n", data.PaymentLinkID)
		// payload = &kv.CartPayload{
		// 	OrderCode:  data.OrderCode,
		// 	Amount:     data.Amount,
		// 	BuyerEmail: "",
		// }
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// validate payload
	if err := ValidateRequestDataWebhook(payload); err != nil {
		fmt.Printf("[webhook] invalid payload for paymentLinkId=%s: %v\n", data.PaymentLinkID, err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	log.Printf("[webhook] payload: %+v", payload)

	firstName, lastName := shopify.ParseName(payload.BuyerName)
	lineItems := toShopifyLineItems(payload.LineItems)

	// Shopify ignores shipping_address when either first_name or last_name is
	// empty, so fall back to first_name when the buyer has only one name.
	addrLastName := lastName
	if addrLastName == "" {
		addrLastName = firstName
	}

	var shippingAddr *shopify.ShippingAddress
	if payload.ShippingAddress != "" {
		shippingAddr = &shopify.ShippingAddress{
			FirstName:   firstName,
			LastName:    addrLastName,
			Phone:       payload.BuyerPhone,
			Address1:    payload.ShippingAddress,
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
			SendReceipt:            false,
			SendFulfillmentReceipt: false,
		},
	}

	log.Printf("[webhook] orderReq (CreateOrder): %+v", orderReq)

	var shopifyOrderID int64
	var shopifyOrderName string
	var dbNote string

	created, shopifyErr := shopify.CreateOrder(orderReq)
	if shopifyErr != nil {
		// Nice-to-have: payment is already successful — DB is source of truth for fulfillment.
		fmt.Printf("[webhook] Shopify CreateOrder failed (bypass) paymentLinkId=%s: %v\n", data.PaymentLinkID, shopifyErr)
		dbNote = "shopify CreateOrder: " + shopifyErr.Error()
	} else {
		shopifyOrderID = created.ID
		shopifyOrderName = created.Name
		fmt.Printf("[webhook] Shopify order created: %s (order_id=%d) for paymentLinkId=%s\n",
			shopifyOrderName, shopifyOrderID, data.PaymentLinkID)
	}

	if err := db.UpdateOrderPaid(data.PaymentLinkID, shopifyOrderID, shopifyOrderName, data.Reference, data.TransactionDateTime, dbNote); err != nil {
		fmt.Printf("[webhook] DB UpdateOrderPaid error: %v\n", err)
	}

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

	if err := kv.MarkProcessed(data.PaymentLinkID); err != nil {
		fmt.Printf("[webhook] KV MarkProcessed error: %v\n", err)
	}

	w.WriteHeader(http.StatusOK)
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
