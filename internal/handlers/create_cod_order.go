package handlers

import (
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/notify"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

// createCODReq is the body sent by the storefront payment popup for a
// cash-on-delivery order. Prices are NOT accepted from the client: the server
// reads them from the Shopify draft order and computes shipping itself.
type createCODReq struct {
	BuyerName       string        `json:"buyerName"`
	BuyerPhone      string        `json:"buyerPhone"`
	BuyerEmail      string        `json:"buyerEmail"`
	ShippingAddress string        `json:"shippingAddress"`
	Note            string        `json:"note"`
	LineItems       []codLineItem `json:"lineItems"`
	// ReplacesPaymentLinkID is set when the buyer switches from the QR modal to
	// COD; that PayOS link is cancelled and its draft order removed.
	ReplacesPaymentLinkID string `json:"replacesPaymentLinkId"`
}

type codLineItem struct {
	VariantID int64 `json:"variantId"`
	Quantity  int   `json:"quantity"`
}

var vnPhoneRe = regexp.MustCompile(`^0\d{9}$`)

// normalizeVNPhone strips separators and turns +84/84 into a leading 0.
func normalizeVNPhone(p string) string {
	r := strings.NewReplacer(" ", "", ".", "", "-", "", "(", "", ")", "")
	p = r.Replace(strings.TrimSpace(p))
	switch {
	case strings.HasPrefix(p, "+84"):
		p = "0" + p[3:]
	case strings.HasPrefix(p, "84") && len(p) == 11:
		p = "0" + p[2:]
	}
	return p
}

func envInt64(key string, def int64) int64 {
	if v, err := strconv.ParseInt(os.Getenv(key), 10, 64); err == nil && v >= 0 {
		return v
	}
	return def
}

func parseVND(s string) int64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0
	}
	return int64(math.Round(f))
}

// ── Simple in-memory rate limit: max 5 COD orders per IP per 10 minutes ──────
var codLimiter = struct {
	sync.Mutex
	hits map[string][]time.Time
}{hits: map[string][]time.Time{}}

func codAllow(ip string) bool {
	const window = 10 * time.Minute
	const max = 5
	now := time.Now()
	codLimiter.Lock()
	defer codLimiter.Unlock()
	kept := codLimiter.hits[ip][:0]
	for _, t := range codLimiter.hits[ip] {
		if now.Sub(t) < window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= max {
		codLimiter.hits[ip] = kept
		return false
	}
	codLimiter.hits[ip] = append(kept, now)
	return true
}

func requestIP(r *http.Request) string {
	if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
		return strings.TrimSpace(strings.Split(xf, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// CreateCODOrder handles POST /api/create-cod-order.
// It creates a real Shopify order with payment pending (cash on delivery).
func CreateCODOrder(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req createCODReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "Dữ liệu không hợp lệ", http.StatusBadRequest)
		return
	}

	name := strings.TrimSpace(req.BuyerName)
	phone := normalizeVNPhone(req.BuyerPhone)
	email := strings.TrimSpace(req.BuyerEmail)
	address := strings.TrimSpace(req.ShippingAddress)

	switch {
	case len([]rune(name)) < 2 || len([]rune(name)) > 100:
		jsonErr(w, "Vui lòng nhập họ tên", http.StatusBadRequest)
		return
	case !vnPhoneRe.MatchString(phone):
		jsonErr(w, "Số điện thoại chưa đúng (10 số, bắt đầu bằng 0)", http.StatusBadRequest)
		return
	case len([]rune(address)) < 8 || len([]rune(address)) > 300:
		jsonErr(w, "Vui lòng nhập địa chỉ giao hàng đầy đủ", http.StatusBadRequest)
		return
	case len(req.LineItems) == 0 || len(req.LineItems) > 20:
		jsonErr(w, "Giỏ hàng trống hoặc quá nhiều sản phẩm", http.StatusBadRequest)
		return
	}

	lineItems := make([]shopify.LineItem, 0, len(req.LineItems))
	for _, it := range req.LineItems {
		if it.VariantID <= 0 || it.Quantity < 1 || it.Quantity > 20 {
			jsonErr(w, "Sản phẩm trong giỏ không hợp lệ", http.StatusBadRequest)
			return
		}
		lineItems = append(lineItems, shopify.LineItem{VariantID: it.VariantID, Quantity: it.Quantity})
	}

	if !codAllow(requestIP(r)) {
		jsonErr(w, "Bạn đặt hàng quá nhanh, vui lòng thử lại sau ít phút hoặc nhắn Zalo cho Mộc", http.StatusTooManyRequests)
		return
	}

	rep, err := prepareQRReplacement(req.ReplacesPaymentLinkID, phone)
	if err == errQRAlreadyPaid {
		jsonErr(w, "Đơn của bạn đã được thanh toán qua QR rồi nên không cần đặt COD nữa. Mộc sẽ liên hệ xác nhận đơn nhé!", http.StatusConflict)
		return
	}

	firstName, lastName := shopify.ParseName(name)
	addrLastName := lastName
	if addrLastName == "" {
		addrLastName = firstName
	}
	city := ""
	if parts := strings.Split(address, ", "); len(parts) > 0 {
		city = strings.TrimSpace(parts[len(parts)-1])
	}
	shippingAddr := &shopify.ShippingAddress{
		FirstName:   firstName,
		LastName:    addrLastName,
		Phone:       phone,
		Address1:    address,
		City:        city,
		Country:     "Vietnam",
		CountryCode: "VN",
	}

	note := "Đơn COD đặt từ website (popup thanh toán)."
	if n := strings.TrimSpace(req.Note); n != "" {
		if len([]rune(n)) > 500 {
			n = string([]rune(n)[:500])
		}
		note += "\nGhi chú của khách: " + n
	}
	note += rep.noteLine()

	draft, err := shopify.CreateDraftOrderDetailed(shopify.DraftOrderRequest{
		DraftOrder: shopify.DraftOrderBody{
			LineItems: lineItems,
			Customer: &shopify.Customer{
				Email:     email,
				FirstName: firstName,
				LastName:  lastName,
				Phone:     "+84" + phone[1:],
			},
			Email:           email,
			ShippingAddress: shippingAddr,
			BillingAddress:  shippingAddr,
			Note:            note,
			Tags:            "cod,web-popup",
		},
	})
	if err != nil {
		log.Printf("[cod] CreateDraftOrder FAILED | phone=%s | error=%v", phone, err)
		jsonErr(w, "Không tạo được đơn hàng, vui lòng thử lại hoặc nhắn Zalo cho Mộc", http.StatusBadGateway)
		return
	}

	threshold := envInt64("FREE_SHIPPING_THRESHOLD_VND", 399000)
	fee := envInt64("SHIPPING_FEE_VND", 20000)
	subtotal := parseVND(draft.SubtotalPrice)
	shippingFee := int64(0)
	if subtotal < threshold {
		shippingFee = fee
	}
	shipTitle := "Giao hàng tiêu chuẩn"
	if shippingFee == 0 {
		shipTitle = "Miễn phí vận chuyển"
	}
	if _, err := shopify.SetDraftOrderShippingLine(draft.ID, shopify.DraftOrderShippingLine{
		Title: shipTitle,
		Price: fmt.Sprintf("%d", shippingFee),
	}); err != nil {
		log.Printf("[cod] SetDraftOrderShippingLine FAILED | draft=%d | fee=%d | error=%v", draft.ID, shippingFee, err)
		// A missing free-shipping label is cosmetic; a missing fee is not.
		if shippingFee > 0 {
			_ = shopify.DeleteDraftOrder(draft.ID)
			jsonErr(w, "Không tạo được đơn hàng, vui lòng thử lại", http.StatusBadGateway)
			return
		}
	}

	done, err := shopify.CompleteDraftOrderPaymentPending(draft.ID)
	if err != nil {
		log.Printf("[cod] CompleteDraftOrder FAILED | draft=%d | error=%v", draft.ID, err)
		jsonErr(w, "Không hoàn tất được đơn hàng, vui lòng thử lại hoặc nhắn Zalo cho Mộc", http.StatusBadGateway)
		return
	}

	var orderName, statusURL string
	if done.OrderID > 0 {
		if sum, err := shopify.GetOrderSummary(done.OrderID); err == nil {
			orderName = sum.Name
			statusURL = sum.OrderStatusURL
		} else {
			log.Printf("[cod] GetOrderSummary failed | order=%d | error=%v", done.OrderID, err)
		}
	}

	total := subtotal + shippingFee
	rep.finalize(orderName)
	log.Printf("[cod] Order created | order=%s (%d) | total=%d | phone=%s", orderName, done.OrderID, total, phone)

	notifyItems := make([]kv.LineItem, 0, len(draft.LineItems))
	for _, li := range draft.LineItems {
		notifyItems = append(notifyItems, kv.LineItem{
			VariantID: li.VariantID,
			ProductID: li.ProductID,
			Title:     li.Title,
			Quantity:  li.Quantity,
			Price:     parseVND(li.Price) * 100,
		})
	}
	notify.SendOrderNotify(notify.OrderInfo{
		Amount:           total,
		PaymentLinkID:    "COD",
		BuyerName:        name,
		BuyerEmail:       email,
		BuyerPhone:       phone,
		ShippingAddress:  address,
		LineItems:        notifyItems,
		ShopifyOrderID:   done.OrderID,
		ShopifyOrderName: orderName,
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":             true,
		"orderId":        done.OrderID,
		"orderName":      orderName,
		"orderStatusUrl": statusURL,
		"subtotal":       subtotal,
		"shippingFee":    shippingFee,
		"total":          total,
	})
}
