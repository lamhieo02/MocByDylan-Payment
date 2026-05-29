package handlers

// Shopify OAuth 2.0 flow
//
// Step 1 – GET /auth?shop=xxx.myshopify.com
//   Merchant clicks "Install" → Shopify redirects here.
//   We validate the shop domain, generate a cryptographic state token,
//   store it in memory, and redirect the browser to Shopify's authorize URL.
//
// Step 2 – GET /auth/callback?code=...&shop=...&state=...&hmac=...
//   Shopify redirects back here after the merchant approves.
//   We verify:
//     a. state token matches what we stored (CSRF protection)
//     b. hmac matches the HMAC-SHA256 we compute from the query params
//   Then we exchange the one-time code for a permanent access token and
//   cache it in Redis so all subsequent Shopify API calls skip the exchange.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

// stateStore holds nonces keyed by their hex value.
var stateStore sync.Map

// shopDomainRE validates a Shopify myshopify.com domain.
var shopDomainRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9\-]*\.myshopify\.com$`)

// AuthHandler handles the Shopify OAuth installation flow.
// It caches the resulting access token via the injected TokenProvider.
type AuthHandler struct {
	tokenProvider shopify.TokenProvider
}

// NewAuthHandler creates an AuthHandler with the given token provider.
func NewAuthHandler(tp shopify.TokenProvider) *AuthHandler {
	return &AuthHandler{tokenProvider: tp}
}

// ── Step 1: Initiate OAuth ────────────────────────────────────────────────────

// Install handles GET /auth?shop={shop_domain}.
// It validates the shop, creates a state nonce, and redirects the browser to
// Shopify's OAuth authorize endpoint.
func (h *AuthHandler) Install(w http.ResponseWriter, r *http.Request) {
	shop := strings.TrimSpace(r.URL.Query().Get("shop"))
	if !shopDomainRE.MatchString(shop) {
		jsonErr(w, "missing or invalid shop parameter", http.StatusBadRequest)
		return
	}

	stateBytes := make([]byte, 16)
	if _, err := rand.Read(stateBytes); err != nil {
		jsonErr(w, "failed to generate state", http.StatusInternalServerError)
		return
	}
	state := hex.EncodeToString(stateBytes)
	stateStore.Store(state, true)

	params := url.Values{
		"client_id":    {os.Getenv("SHOPIFY_CLIENT_ID")},
		"scope":        {os.Getenv("SHOPIFY_SCOPES")},
		"redirect_uri": {os.Getenv("SHOPIFY_REDIRECT_URI")},
		"state":        {state},
	}
	redirectURL := fmt.Sprintf("https://%s/admin/oauth/authorize?%s", shop, params.Encode())
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// ── Step 2: Handle callback ───────────────────────────────────────────────────

// Callback handles GET /auth/callback?code=...&shop=...&state=...&hmac=...
// It validates state + HMAC, exchanges the code for an access token, and
// caches the token in Redis (TTL = 30 days).
func (h *AuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	shop := q.Get("shop")
	state := q.Get("state")
	code := q.Get("code")
	receivedHMAC := q.Get("hmac")

	// (a) Validate state to prevent CSRF attacks.
	if _, ok := stateStore.LoadAndDelete(state); !ok {
		jsonErr(w, "invalid or expired state", http.StatusUnauthorized)
		return
	}

	// (b) Validate HMAC-SHA256 signature from Shopify.
	if !validateHMAC(q, receivedHMAC) {
		jsonErr(w, "HMAC validation failed", http.StatusUnauthorized)
		return
	}

	// (c) Exchange the temporary code for a permanent access token.
	token, err := exchangeCodeForToken(shop, code)
	if err != nil {
		log.Printf("[shopify oauth] OAuth exchange failure for %s: %v", shop, err)
		jsonErr(w, "failed to obtain access token", http.StatusBadGateway)
		return
	}
	log.Printf("[shopify oauth] OAuth exchange success for %s", shop)

	// (d) Cache the token in Redis so future requests skip the exchange.
	if cacheErr := h.tokenProvider.CacheToken(r.Context(), shop, token); cacheErr != nil {
		// Non-fatal: log and continue — the token is still returned to the caller.
		log.Printf("[shopify oauth] token cache error for %s: %v", shop, cacheErr)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"shop":         shop,
		"access_token": token,
	})
}

// ── HMAC Validation ───────────────────────────────────────────────────────────

func validateHMAC(q url.Values, receivedHMAC string) bool {
	pairs := make([]string, 0, len(q))
	for key, values := range q {
		if key == "hmac" {
			continue
		}
		pairs = append(pairs, key+"="+values[0])
	}
	sort.Strings(pairs)
	message := strings.Join(pairs, "&")

	secret := os.Getenv("SHOPIFY_CLIENT_SECRET")
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	expectedHMAC := hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedHMAC), []byte(receivedHMAC))
}

// ── Token Exchange ────────────────────────────────────────────────────────────

func exchangeCodeForToken(shop, code string) (string, error) {
	endpoint := fmt.Sprintf("https://%s/admin/oauth/access_token", shop)

	payload := url.Values{
		"client_id":     {os.Getenv("SHOPIFY_CLIENT_ID")},
		"client_secret": {os.Getenv("SHOPIFY_CLIENT_SECRET")},
		"code":          {code},
	}

	resp, err := http.PostForm(endpoint, payload)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("shopify returned HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse response: %w", err)
	}
	if result.AccessToken == "" {
		return "", fmt.Errorf("empty access_token in response: %s", string(body))
	}
	return result.AccessToken, nil
}
