package main

import (
	"log"
	"net/http"
	"os"

	_ "github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/config"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/handlers"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/payos"
)

func main() {
	// Auto-register the PayOS webhook URL on startup if configured.
	// Set PAYOS_WEBHOOK_URL to the public URL of this server, e.g.:
	//   https://your-app.railway.app/api/webhook
	if webhookURL := os.Getenv("PAYOS_WEBHOOK_URL"); webhookURL != "" {
		if err := payos.RegisterWebhook(webhookURL); err != nil {
			log.Printf("[payos] webhook registration failed: %v", err)
		} else {
			log.Printf("[payos] webhook registered: %s", webhookURL)
		}
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/api/create-payment", handlers.CreatePayment)
	mux.HandleFunc("/api/webhook", handlers.Webhook)
	mux.HandleFunc("/api/payment-status", handlers.PaymentStatus)
	mux.HandleFunc("/api/health", handlers.Health)
	// mux.HandleFunc("/health", handlers.Health)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, mux); err != nil {
		log.Fatal(err)
	}
}
