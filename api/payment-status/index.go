package handler

import (
	"encoding/json"
	"net/http"

	"github.com/mocbydylan/payos-backend/internal/payos"
)

// Handler proxies GET /api/payment-status?id={paymentLinkId} to PayOS.
// The frontend polls this every 4 seconds to detect PAID / CANCELLED / EXPIRED.
func Handler(w http.ResponseWriter, r *http.Request) {
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

func setCORSHeaders(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if origin != "" {
		w.Header().Set("Access-Control-Allow-Origin", origin)
	} else {
		w.Header().Set("Access-Control-Allow-Origin", "*")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

func jsonErr(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
