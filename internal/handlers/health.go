package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/db"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
)

type healthResponse struct {
	Status    string `json:"status"`
	Redis     string `json:"redis"`
	Postgres  string `json:"postgres,omitempty"` // "ok" | "error" | omitted if DATABASE_URL unset
}

// Live handles GET /health — liveness for Railway/load balancers.
// Does not call Redis so deploy succeeds even if Redis is still starting
// or REDIS_URL is misconfigured (use /api/health to verify Redis).
func Live(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintln(w, "ok")
}

// Health handles GET /api/health — readiness: pings Redis.
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

	resp := healthResponse{Status: "ok", Redis: "ok"}
	if os.Getenv("DATABASE_URL") != "" {
		if err := db.Ping(ctx); err != nil {
			log.Printf("[health] postgres ping failed: %v", err)
			resp.Status = "unhealthy"
			resp.Postgres = "error"
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		resp.Postgres = "ok"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
