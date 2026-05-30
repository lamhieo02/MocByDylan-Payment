package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/db"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/mailer"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/notify"
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

	// Atomically claim the processing right before touching any external system.
	// Redis SET NX guarantees only one concurrent handler proceeds — eliminating
	// the race window that existed when the flag was set at the end of the handler.
	claimed, err := kv.TryMarkProcessed(data.PaymentLinkID)
	if err != nil {
		fmt.Printf("[webhook] TryMarkProcessed error for paymentLinkId=%s: %v\n", data.PaymentLinkID, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	if !claimed {
		fmt.Printf("[webhook] duplicate event for paymentLinkId=%s, skipping\n", data.PaymentLinkID)
		w.WriteHeader(http.StatusOK)
		return
	}

	payload, err := kv.GetCartPayload(data.PaymentLinkID)
	if err != nil {
		fmt.Printf("[webhook] KV error for paymentLinkId=%s: %v\n", data.PaymentLinkID, err)
	}
	if payload == nil {
		// Redis TTL (20 min) may have expired before the webhook arrived.
		// Fall back to PostgreSQL which has the same data persisted at link-creation time.
		fmt.Printf("[webhook] Redis miss for paymentLinkId=%s — trying DB fallback\n", data.PaymentLinkID)
		dbRec, dbErr := db.GetCartPayload(data.PaymentLinkID)
		if dbErr != nil {
			fmt.Printf("[webhook] DB fallback error for paymentLinkId=%s: %v\n", data.PaymentLinkID, dbErr)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if dbRec == nil {
			fmt.Printf("[webhook] no cart data found (redis+db miss) for paymentLinkId=%s\n", data.PaymentLinkID)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		converted, convErr := cartPayloadFromDB(dbRec)
		if convErr != nil {
			fmt.Printf("[webhook] DB record conversion error for paymentLinkId=%s: %v\n", data.PaymentLinkID, convErr)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		payload = converted
		fmt.Printf("[webhook] using DB fallback payload for paymentLinkId=%s\n", data.PaymentLinkID)
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
			SendReceipt:            false,
			SendFulfillmentReceipt: false,
			ShippingFeeVND:         shippingFeeVND,
		},
	}

	log.Printf("[webhook] orderReq (CreateOrder): %+v", orderReq)

	var shopifyOrderID int64
	var shopifyOrderName string
	var shopifyErrMsg string
	var dbNote string

	created, shopifyErr := shopify.CreateOrderGQL(orderReq)
	if shopifyErr != nil {
		// Payment is already successful — DB is source of truth for fulfillment.
		fmt.Printf("[webhook] Shopify CreateOrderGQL failed (bypass) paymentLinkId=%s: %v\n", data.PaymentLinkID, shopifyErr)
		shopifyErrMsg = shopifyErr.Error()
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
	}, nil
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
