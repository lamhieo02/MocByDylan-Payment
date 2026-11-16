package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/payos"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

type fakeQR struct {
	status      string
	cancelErr   error
	cancelled   int
	deleted     []int64
	marked      []string
	statusCalls int
}

func installFakeQR(t *testing.T, f *fakeQR, phone string) {
	t.Helper()
	oldGet, oldCancel, oldDel, oldMark, oldLookup, oldKV := payosGetStatus, payosCancel, deleteDraftOrder, markQRReplaced, lookupQRPayload, kvMarkProcessed
	t.Cleanup(func() {
		payosGetStatus, payosCancel, deleteDraftOrder, markQRReplaced, lookupQRPayload, kvMarkProcessed = oldGet, oldCancel, oldDel, oldMark, oldLookup, oldKV
	})
	payosGetStatus = func(id string) (*payos.PaymentStatus, error) {
		f.statusCalls++
		return &payos.PaymentStatus{Status: f.status}, nil
	}
	payosCancel = func(id, reason string) error {
		if f.cancelErr != nil {
			return f.cancelErr
		}
		f.cancelled++
		f.status = "CANCELLED"
		return nil
	}
	deleteDraftOrder = func(id int64) error { f.deleted = append(f.deleted, id); return nil }
	markQRReplaced = func(id, note string) error { f.marked = append(f.marked, note); return nil }
	kvMarkProcessed = func(id string) (bool, error) { return true, nil }
	lookupQRPayload = func(id string) (*qrPayload, error) {
		return &qrPayload{BuyerPhone: phone, DraftOrderID: 555, DraftOrderName: "#D53"}, nil
	}
}

func fakeShopify(t *testing.T, subtotal string) (*[]string, *string) {
	calls := []string{}
	var lastNote string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/draft_orders.json"):
			lastNote = string(b)
			io.WriteString(w, `{"draft_order":{"id":11,"name":"#D60","subtotal_price":"`+subtotal+`","line_items":[]}}`)
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/draft_orders/11.json"):
			calls = append(calls, "SHIP "+string(b))
			io.WriteString(w, `{"draft_order":{"id":11}}`)
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/draft_orders/11/complete.json"):
			io.WriteString(w, `{"draft_order":{"id":11,"order_id":99}}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/orders/99.json"):
			io.WriteString(w, `{"order":{"id":99,"name":"#1090","order_status_url":"https://x","total_price":"1"}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	shopify.HTTPClient = srv.Client()
	t.Setenv("SHOPIFY_STORE_DOMAIN", strings.TrimPrefix(srv.URL, "https://"))
	return &calls, &lastNote
}

func post(body string, ip string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/create-cod-order", strings.NewReader(body))
	req.Header.Set("X-Forwarded-For", ip)
	CreateCODOrder(rr, req)
	return rr
}

const baseBody = `"buyerName":"Nguyen Van A","buyerPhone":"0912345678","shippingAddress":"1 Le Loi, Phuong Ben Nghe, Thanh pho Ho Chi Minh","lineItems":[{"variantId":1,"quantity":1}]`

func TestReplaceQR_CancelsLinkAndDeletesDraft(t *testing.T) {
	f := &fakeQR{status: "PENDING"}
	installFakeQR(t, f, "0912345678")
	calls, note := fakeShopify(t, "162000.00")
	rr := post(`{`+baseBody+`,"replacesPaymentLinkId":"abc123def456"}`, "1.1.1.1")
	if rr.Code != 200 {
		t.Fatalf("code %d %s", rr.Code, rr.Body.String())
	}
	if f.cancelled != 1 || len(f.deleted) != 1 || f.deleted[0] != 555 || len(f.marked) != 1 {
		t.Fatalf("cancel=%d deleted=%v marked=%v", f.cancelled, f.deleted, f.marked)
	}
	if !strings.Contains(*note, "#D53") {
		t.Fatalf("note missing replacement info: %s", *note)
	}
	t.Logf("calls=%v marked=%v", *calls, f.marked)
}

func TestReplaceQR_AlreadyPaid_Rejects(t *testing.T) {
	f := &fakeQR{status: "PAID"}
	installFakeQR(t, f, "0912345678")
	calls, _ := fakeShopify(t, "162000.00")
	rr := post(`{`+baseBody+`,"replacesPaymentLinkId":"abc123def456"}`, "1.1.1.2")
	if rr.Code != http.StatusConflict {
		t.Fatalf("want 409 got %d", rr.Code)
	}
	if len(*calls) != 0 || f.cancelled != 0 {
		t.Fatalf("should not create order: %v", *calls)
	}
}

func TestReplaceQR_PhoneMismatch_Ignored(t *testing.T) {
	f := &fakeQR{status: "PENDING"}
	installFakeQR(t, f, "0999999999")
	fakeShopify(t, "162000.00")
	rr := post(`{`+baseBody+`,"replacesPaymentLinkId":"abc123def456"}`, "1.1.1.3")
	if rr.Code != 200 || f.cancelled != 0 || len(f.deleted) != 0 {
		t.Fatalf("code=%d cancel=%d deleted=%v", rr.Code, f.cancelled, f.deleted)
	}
}

func TestReplaceQR_CancelFails_StillCreatesWithWarning(t *testing.T) {
	f := &fakeQR{status: "PENDING", cancelErr: errors.New("timeout")}
	installFakeQR(t, f, "0912345678")
	_, note := fakeShopify(t, "162000.00")
	rr := post(`{`+baseBody+`,"replacesPaymentLinkId":"abc123def456"}`, "1.1.1.4")
	if rr.Code != 200 || len(f.deleted) != 0 || !strings.Contains(*note, "Chưa huỷ được link QR") {
		t.Fatalf("code=%d deleted=%v note=%s", rr.Code, f.deleted, *note)
	}
}

func TestFreeShippingLine(t *testing.T) {
	calls, _ := fakeShopify(t, "485000.00")
	rr := post(`{`+baseBody+`}`, "1.1.1.5")
	if rr.Code != 200 {
		t.Fatalf("code %d", rr.Code)
	}
	found := false
	for _, c := range *calls {
		if strings.HasPrefix(c, "SHIP ") && strings.Contains(c, "Miễn phí vận chuyển") && strings.Contains(c, `"price":"0"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no free shipping line: %v", *calls)
	}
}
