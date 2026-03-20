package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/payos"
)

// PaymentStatus handles GET /api/payment-status?id={paymentLinkId}.
// The frontend polls this endpoint to detect PAID / CANCELLED / EXPIRED.
func PaymentStatus(w http.ResponseWriter, r *http.Request) {
	setCORSHeaders(w, r)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		jsonErr(w, "missing id query parameter", http.StatusBadRequest)
		return
	}

	statusResp, err := payos.GetPaymentStatus(id)
	if err != nil {
		jsonErr(w, "failed to fetch payment status: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": statusResp.Status,
		"amount": statusResp.Amount,
	})
}
