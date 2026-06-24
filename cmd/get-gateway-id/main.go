// get-gateway-id is a one-shot CLI tool to find the Shopify PaymentGateway GID
// for manual payment methods (e.g. "Bank Deposit") without needing the
// read_payment_gateways API scope.
//
// It reads the same env vars as the main service, prints every manual payment
// gateway name + GID, then tells you which env var to set.
//
// Usage:
//
//	SHOPIFY_STORE_DOMAIN=your-store.myshopify.com \
//	SHOPIFY_ADMIN_API_TOKEN=shpat_xxx \
//	go run ./cmd/get-gateway-id/main.go
//
// If you get HTTP 404, your token lacks read_payment_gateways.
// Quick fix (no code redeploy needed): see Option B below.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	domain := os.Getenv("SHOPIFY_STORE_DOMAIN")
	token := os.Getenv("SHOPIFY_ADMIN_API_TOKEN")
	if domain == "" || token == "" {
		fmt.Fprintln(os.Stderr, "Error: set SHOPIFY_STORE_DOMAIN and SHOPIFY_ADMIN_API_TOKEN")
		os.Exit(1)
	}

	url := fmt.Sprintf("https://%s/admin/api/2026-01/payment_gateways.json", domain)
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("X-Shopify-Access-Token", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 404 || resp.StatusCode == 401 || resp.StatusCode == 403 {
		fmt.Printf(`
HTTP %d — Token lacks read_payment_gateways scope.

Option A — Add scope (takes ~2 min):
  1. Shopify Admin → Apps → Develop apps → [Your custom app]
  2. Configuration → Admin API integration → API access scopes
  3. Tick "read_payment_gateways" → Save → Install app
  4. Copy the new token → update SHOPIFY_ADMIN_API_TOKEN
  5. Re-run this script

Option B — Get ID from browser (no token change needed):
  1. Open Chrome DevTools on admin.shopify.com/store/mocbydylan/settings/payments
  2. Network tab → filter "payment"
  3. Click "Edit" on "Bank Deposit"
  4. Find the request URL containing the gateway ID number

`, resp.StatusCode)
		os.Exit(1)
	}

	if resp.StatusCode != 200 {
		fmt.Fprintf(os.Stderr, "Unexpected HTTP %d: %s\n", resp.StatusCode, raw)
		os.Exit(1)
	}

	var result struct {
		PaymentGateways []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"payment_gateways"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		fmt.Fprintf(os.Stderr, "Parse error: %v\nRaw: %s\n", err, raw)
		os.Exit(1)
	}

	fmt.Println("Payment gateways found:")
	fmt.Println("─────────────────────────────────────────────────────────")
	for _, gw := range result.PaymentGateways {
		gid := fmt.Sprintf("gid://shopify/PaymentGateway/%d", gw.ID)
		fmt.Printf("  %-25s  %s\n", gw.Name, gid)
	}
	fmt.Println("─────────────────────────────────────────────────────────")
	fmt.Println("\nTo use 'Bank Deposit', set this env var in your deployment:")
	for _, gw := range result.PaymentGateways {
		if gw.Name == "Bank Deposit" {
			fmt.Printf("\n  SHOPIFY_PAYOS_GATEWAY_ID=gid://shopify/PaymentGateway/%d\n\n", gw.ID)
			return
		}
	}
	fmt.Println("\n  (Bank Deposit not found — check the Name column above and set SHOPIFY_PAYOS_GATEWAY_NAME accordingly)")
}
