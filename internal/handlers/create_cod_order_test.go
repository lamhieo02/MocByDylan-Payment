package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

func TestCreateCODOrderFlow(t *testing.T) {
	var calls []string
	var shippingBody string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		b, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/draft_orders.json"):
			io.WriteString(w, `{"draft_order":{"id":11,"name":"#D1","subtotal_price":"175000.00","line_items":[{"variant_id":1,"title":"Lovie","quantity":1,"price":"175000.00"}]}}`)
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/draft_orders/11.json"):
			shippingBody = string(b)
			io.WriteString(w, `{"draft_order":{"id":11}}`)
		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/draft_orders/11/complete.json"):
			io.WriteString(w, `{"draft_order":{"id":11,"order_id":99,"status":"completed"}}`)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/orders/99.json"):
			io.WriteString(w, `{"order":{"id":99,"name":"#1023","order_status_url":"https://x/status","total_price":"195000.00"}}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	shopify.HTTPClient = srv.Client()
	t.Setenv("SHOPIFY_STORE_DOMAIN", strings.TrimPrefix(srv.URL, "https://"))

	body := `{"buyerName":"Nguyen Van A","buyerPhone":"0912 345 678","shippingAddress":"1 Le Loi, Phuong Ben Nghe, Thanh pho Ho Chi Minh","lineItems":[{"variantId":1,"quantity":1}]}`
	rr := httptest.NewRecorder()
	CreateCODOrder(rr, httptest.NewRequest("POST", "/api/create-cod-order", strings.NewReader(body)))
	if rr.Code != 200 {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rr.Body.Bytes(), &out)
	t.Logf("resp=%v calls=%v", out, calls)
	if out["total"].(float64) != 195000 || out["shippingFee"].(float64) != 20000 || out["orderName"] != "#1023" {
		t.Fatalf("unexpected: %v", out)
	}
	if !strings.Contains(shippingBody, `"price":"20000"`) {
		t.Fatalf("shipping not set: %s", shippingBody)
	}
	if !strings.Contains(calls[2], "payment_pending=true") {
		t.Fatalf("not payment pending: %v", calls)
	}

	rr2 := httptest.NewRecorder()
	CreateCODOrder(rr2, httptest.NewRequest("POST", "/api/create-cod-order", strings.NewReader(`{"buyerName":"A B","buyerPhone":"123","shippingAddress":"1 Le Loi, HCM xx","lineItems":[{"variantId":1,"quantity":1}]}`)))
	if rr2.Code != 400 {
		t.Fatalf("bad phone should be 400, got %d", rr2.Code)
	}
}
