package handlers

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
)

type healthResponse struct {
	Status string `json:"status"`
	Redis  string `json:"redis"`
}

// Health handles GET /health and GET /api/health.
// Returns 200 + JSON when Redis is reachable; 503 otherwise.
func Health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := kv.Ping(ctx); err != nil {
		log.Printf("[health] redis ping failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(healthResponse{Status: "unhealthy", Redis: "error"})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthResponse{Status: "ok", Redis: "ok"})
}
