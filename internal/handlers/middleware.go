package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/allowlist"
)

// WithInventoryCORS adds CORS response headers required for browser-based staff
// dashboards (e.g. a Shopify theme served from a different origin).
//
// The X-Staff-Email custom header is explicitly allowed so browsers pass it in
// credentialed requests. OPTIONS pre-flight is answered with 204 before any
// authentication middleware runs — this is intentional and safe.
func WithInventoryCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}
		w.Header().Set("Access-Control-Allow-Methods", "GET, PATCH, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Staff-Email")
		w.Header().Set("Access-Control-Max-Age", "86400")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next(w, r)
	}
}

// RequireStaffAccess protects internal staff routes by checking the X-Staff-Token
// request header against the STAFF_SECRET environment variable.
//
// If STAFF_SECRET is not configured the endpoint is always blocked, preventing
// accidental public exposure. Set STAFF_SECRET to a long random value in the
// deployment environment.
// func RequireStaffAccess(next http.HandlerFunc) http.HandlerFunc {
// 	return func(w http.ResponseWriter, r *http.Request) {
// 		secret := os.Getenv("STAFF_SECRET")
// 		if secret == "" || r.Header.Get("X-Staff-Token") != secret {
// 			jsonErr(w, "unauthorized", http.StatusUnauthorized)
// 			return
// 		}
// 		next(w, r)
// 	}
// }

// RequireAllowedEmail protects inventory routes by checking the X-Staff-Email
// request header against the INVENTORY_ALLOWED_EMAILS allow-list.
//
// The email is normalised (trimmed + lowercased) before the lookup.
// Returns HTTP 401 if the header is absent, empty, or not in the allow-list.
func RequireAllowedEmail(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Staff-Email")))
		if email == "" || !allowlist.Allowed(email) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{
				"success": false,
				"message": "Unauthorized",
			})
			return
		}
		next(w, r)
	}
}
