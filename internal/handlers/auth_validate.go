package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/allowlist"
)

// validateEmailRequest is the body for POST /api/internal/auth/validate.
type validateEmailRequest struct {
	Email string `json:"email"`
}

// ValidateEmail handles POST /api/internal/auth/validate.
// It checks whether the provided email is on the INVENTORY_ALLOWED_EMAILS list.
// No token or session is issued — callers are expected to pass X-Staff-Email on
// subsequent requests and this endpoint is used purely for a client-side gate check.
func ValidateEmail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req validateEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	if email == "" {
		jsonErr(w, "email is required", http.StatusBadRequest)
		return
	}
	if !strings.Contains(email, "@") {
		jsonErr(w, "invalid email format", http.StatusBadRequest)
		return
	}

	if !allowlist.Allowed(email) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "Unauthorized",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true})
}
