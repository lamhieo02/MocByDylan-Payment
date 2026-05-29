package main

import (
	"context"
	"log"
	"net/http"
	"os"

	_ "github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/config"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/handlers"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/notify"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

func main() {
	notify.Init(os.Getenv("DISCORD_NOTIFY_ORDER_WEBHOOK"))

	// Auto-register the PayOS webhook URL on startup if configured.
	// Set PAYOS_WEBHOOK_URL to the public URL of this server, e.g.:
	//   https://your-app.railway.app/api/webhook
	// webHookURL := os.Getenv("PAYOS_WEBHOOK_URL")
	// // log debug
	// log.Printf("webHookURL: %s", webHookURL)
	// if webHookURL != "" {
	// 	if err := payos.RegisterWebhook(webHookURL); err != nil {
	// 		log.Printf("[payos] webhook registration failed: %v", err)
	// 	} else {
	// 		log.Printf("[payos] webhook registered: %s", webHookURL)
	// 	}
	// }

	// ── Inventory config validation (fail fast) ───────────────────────────────
	// Validate all inventory-specific env vars up front so the operator gets a
	// clear error instead of a cryptic Shopify API failure at request time.
	{
		shopifyToken := os.Getenv("SHOPIFY_ADMIN_API_TOKEN")
		switch {
		case os.Getenv("SHOPIFY_STORE_DOMAIN") == "":
			log.Fatal("[inventory] SHOPIFY_STORE_DOMAIN is required")
		case shopifyToken == "":
			log.Fatal("[inventory] SHOPIFY_ADMIN_API_TOKEN is required")
		case os.Getenv("INVENTORY_ALLOWED_EMAILS") == "":
			log.Fatal("[inventory] INVENTORY_ALLOWED_EMAILS is required — no emails configured means all inventory requests will be rejected")
		}
	}

	// ── Shopify token cache ───────────────────────────────────────────────────
	// Tokens are cached in Redis under shopify:access_token:{shop} (TTL = 30 days).
	// GetAccessToken checks Redis first; on cache miss it falls back to
	// SHOPIFY_ADMIN_API_TOKEN and back-fills Redis.
	tokenProvider := shopify.NewTokenCache(shopify.NewRedisAdapter(kv.Client()))

	mux := http.NewServeMux()

	mux.HandleFunc("/api/create-payment", handlers.CreatePayment)
	mux.HandleFunc("/api/webhook", handlers.Webhook)
	mux.HandleFunc("/api/payment-status", handlers.PaymentStatus)
	mux.HandleFunc("/api/health", handlers.Health)
	mux.HandleFunc("/health", handlers.Live)

	// Shopify OAuth flow — caches the resulting token in Redis
	authHandler := handlers.NewAuthHandler(tokenProvider)
	mux.HandleFunc("/auth", authHandler.Install)
	mux.HandleFunc("/auth/callback", authHandler.Callback)

	// Internal staff auth
	// OPTIONS preflight for validate — answered before auth runs.
	mux.HandleFunc("OPTIONS /api/internal/auth/validate", handlers.WithInventoryCORS(func(w http.ResponseWriter, r *http.Request) {}))
	mux.HandleFunc("POST /api/internal/auth/validate", handlers.WithInventoryCORS(handlers.ValidateEmail))

	// Internal staff inventory API (protected by email allow-list)
	invClient, err := shopify.NewInventoryClient(context.Background(), tokenProvider)
	if err != nil {
		log.Fatalf("[inventory] failed to initialise Shopify client: %v", err)
	}
	invHandler := handlers.NewInventoryHandler(invClient)
	// OPTIONS preflight for inventory — answered before auth runs.
	mux.HandleFunc("OPTIONS /api/internal/inventory", handlers.WithInventoryCORS(func(w http.ResponseWriter, r *http.Request) {}))
	mux.HandleFunc("OPTIONS /api/internal/inventory/{inventoryItemId}", handlers.WithInventoryCORS(func(w http.ResponseWriter, r *http.Request) {}))
	mux.HandleFunc("GET /api/internal/inventory", handlers.WithInventoryCORS(handlers.RequireAllowedEmail(invHandler.List)))
	mux.HandleFunc("PATCH /api/internal/inventory/{inventoryItemId}", handlers.WithInventoryCORS(handlers.RequireAllowedEmail(invHandler.Update)))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
