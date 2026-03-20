// register-webhook is a one-time CLI tool to register (or update) the PayOS
// webhook URL for the payment channel.
//
// Usage:
//
//	PAYOS_CLIENT_ID=xxx PAYOS_API_KEY=xxx \
//	  go run ./cmd/register-webhook/main.go https://your-backend.vercel.app/api/webhook
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: register-webhook <webhook_url>")
		fmt.Fprintln(os.Stderr, "Example: register-webhook https://mocbydylan-payos-be.vercel.app/api/webhook")
		os.Exit(1)
	}

	webhookURL := os.Args[1]

	clientID := os.Getenv("PAYOS_CLIENT_ID")
	apiKey := os.Getenv("PAYOS_API_KEY")
	if clientID == "" || apiKey == "" {
		fmt.Fprintln(os.Stderr, "PAYOS_CLIENT_ID and PAYOS_API_KEY must be set")
		os.Exit(1)
	}

	body, _ := json.Marshal(map[string]string{"webhookUrl": webhookURL})
	req, err := http.NewRequest(http.MethodPost, "https://api-merchant.payos.vn/confirm-webhook", bytes.NewReader(body))
	if err != nil {
		fmt.Fprintf(os.Stderr, "request error: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", clientID)
	req.Header.Set("x-api-key", apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "HTTP error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	fmt.Printf("PayOS response (HTTP %d):\n%s\n", resp.StatusCode, string(raw))

	if resp.StatusCode == 200 {
		fmt.Println("\n✓ Webhook registered successfully.")
	} else {
		fmt.Fprintf(os.Stderr, "\n✗ Registration failed (see response above).\n")
		os.Exit(1)
	}
}
