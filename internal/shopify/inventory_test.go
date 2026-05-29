package shopify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// redirectTransport rewrites every outbound request to hit the target test server.
type redirectTransport struct{ target string }

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r2 := req.Clone(req.Context())
	r2.URL.Scheme = "http"
	host := rt.target
	if strings.HasPrefix(host, "http://") {
		host = host[7:]
	}
	r2.URL.Host = host
	return http.DefaultTransport.RoundTrip(r2)
}

// overrideHTTPClient replaces the package-level HTTPClient for the duration of a test.
func overrideHTTPClient(t *testing.T, server *httptest.Server) {
	t.Helper()
	orig := HTTPClient
	HTTPClient = &http.Client{Transport: &redirectTransport{target: server.URL}}
	t.Cleanup(func() { HTTPClient = orig })
}

// staticTokenProvider is a TokenProvider that always returns the same token.
// Used in tests to bypass Redis without mocking the full token lifecycle.
type staticTokenProvider struct{ token string }

func (s *staticTokenProvider) GetAccessToken(_ context.Context, _ string) (string, error) {
	return s.token, nil
}
func (s *staticTokenProvider) CacheToken(_ context.Context, _, _ string) error { return nil }

// newClientForTest creates a shopifyInventoryClient with a pre-set locationID,
// bypassing the startup location fetch. Only accessible within this package's tests.
func newClientForTest(httpClient *http.Client, locationID string) *shopifyInventoryClient {
	return &shopifyInventoryClient{
		httpClient:    httpClient,
		store:         "test.myshopify.com",
		version:       "2026-04",
		locationID:    locationID,
		tokenProvider: &staticTokenProvider{token: "test-token"},
	}
}

// ─── Helpers for building fake GraphQL responses ──────────────────────────────

func locationsResp(locationGID string) string {
	b, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"locations": map[string]any{
				"edges": []map[string]any{
					{"node": map[string]any{"id": locationGID, "name": "Main"}},
				},
			},
		},
	})
	return string(b)
}

func productsResp(hasNextPage bool, endCursor string, products []map[string]any) string {
	edges := make([]map[string]any, len(products))
	for i, p := range products {
		edges[i] = map[string]any{"node": p}
	}
	b, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"products": map[string]any{
				"pageInfo": map[string]any{
					"hasNextPage": hasNextPage,
					"endCursor":   endCursor,
				},
				"edges": edges,
			},
		},
	})
	return string(b)
}

func makeProduct(id, title string, variants []map[string]any) map[string]any {
	varEdges := make([]map[string]any, len(variants))
	for i, v := range variants {
		varEdges[i] = map[string]any{"node": v}
	}
	return map[string]any{
		"id":    id,
		"title": title,
		"status": "ACTIVE",
		"featuredImage": map[string]any{"url": "https://cdn.shopify.com/image.jpg"},
		"variants":      map[string]any{"edges": varEdges},
	}
}

// makeVariant builds a fake variant node using the inventoryLevel(locationId:) shape.
// Pass qty=-1 to simulate a variant not stocked at the queried location (null level).
func makeVariant(sku, invItemGID string, qty int) map[string]any {
	var invLevel any
	if qty >= 0 {
		invLevel = map[string]any{
			"quantities": []map[string]any{
				{"name": "available", "quantity": qty},
			},
		}
	} // nil → JSON null → InventoryLevel pointer is nil in the parsed struct

	return map[string]any{
		"sku": sku,
		"inventoryItem": map[string]any{
			"id":             invItemGID,
			"inventoryLevel": invLevel,
		},
	}
}

func setQuantitiesResp(userErrors []map[string]any) string {
	if userErrors == nil {
		userErrors = []map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{
		"data": map[string]any{
			"inventorySetQuantities": map[string]any{
				"userErrors": userErrors,
				"inventoryAdjustmentGroup": map[string]any{
					"reason":  "correction",
					"changes": []map[string]any{},
				},
			},
		},
	})
	return string(b)
}

// ─── NewInventoryClient tests ─────────────────────────────────────────────────

func TestNewInventoryClient_FetchesLocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(locationsResp("gid://shopify/Location/999")))
	}))
	defer server.Close()

	overrideHTTPClient(t, server)
	t.Setenv("SHOPIFY_STORE_DOMAIN", "test.myshopify.com")

	tp := &staticTokenProvider{token: "test-token"}
	client, err := NewInventoryClient(context.Background(), tp)
	require.NoError(t, err)
	require.NotNil(t, client)

	c := client.(*shopifyInventoryClient)
	assert.Equal(t, "gid://shopify/Location/999", c.locationID)
}

func TestNewInventoryClient_NoLocations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := json.Marshal(map[string]any{
			"data": map[string]any{
				"locations": map[string]any{"edges": []any{}},
			},
		})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	}))
	defer server.Close()

	overrideHTTPClient(t, server)
	t.Setenv("SHOPIFY_STORE_DOMAIN", "test.myshopify.com")

	tp := &staticTokenProvider{token: "test-token"}
	_, err := NewInventoryClient(context.Background(), tp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no locations found")
}

func TestNewInventoryClient_MissingStore(t *testing.T) {
	t.Setenv("SHOPIFY_STORE_DOMAIN", "")

	tp := &staticTokenProvider{token: "test-token"}
	_, err := NewInventoryClient(context.Background(), tp)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SHOPIFY_STORE_DOMAIN is not configured")
}

// ─── ListInventory tests ──────────────────────────────────────────────────────

func TestListInventory_NoSearch(t *testing.T) {
	product := makeProduct(
		"gid://shopify/Product/456",
		"Moon Charm",
		[]map[string]any{makeVariant("MOON-001", "gid://shopify/InventoryItem/123", 18)},
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(productsResp(false, "", []map[string]any{product})))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/1")

	items, err := client.ListInventory(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "123", items[0].InventoryItemID)
	assert.Equal(t, "Moon Charm", items[0].Title)
	assert.Equal(t, "MOON-001", items[0].SKU)
	assert.Equal(t, "https://cdn.shopify.com/image.jpg", items[0].Image)
	assert.Equal(t, 18, items[0].Quantity)
}

func TestListInventory_WithSearch(t *testing.T) {
	var capturedVars map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedVars, _ = body["variables"].(map[string]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(productsResp(false, "", nil)))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/1")

	items, err := client.ListInventory(context.Background(), "moon")
	require.NoError(t, err)
	assert.Empty(t, items)

	require.NotNil(t, capturedVars)
	queryStr, _ := capturedVars["query"].(string)
	assert.Contains(t, queryStr, "moon")
	assert.Contains(t, queryStr, "status:ACTIVE")
	// locationId must be passed so the query filters to the correct location.
	assert.Equal(t, "gid://shopify/Location/1", capturedVars["locationId"])
}

func TestListInventory_NullInventoryLevel(t *testing.T) {
	// A variant not stocked at the queried location returns null inventoryLevel.
	// ListInventory must treat this as quantity=0 rather than panicking.
	product := makeProduct(
		"gid://shopify/Product/1",
		"Unstocked Product",
		[]map[string]any{makeVariant("NO-STOCK", "gid://shopify/InventoryItem/999", -1)},
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(productsResp(false, "", []map[string]any{product})))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/1")

	items, err := client.ListInventory(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 0, items[0].Quantity)
}

func TestListInventory_Pagination(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var resp string
		if callCount == 1 {
			p := makeProduct("gid://shopify/Product/1", "A", []map[string]any{makeVariant("SKU-A", "gid://shopify/InventoryItem/1", 5)})
			resp = productsResp(true, "cursor1", []map[string]any{p})
		} else {
			p := makeProduct("gid://shopify/Product/2", "B", []map[string]any{makeVariant("SKU-B", "gid://shopify/InventoryItem/2", 10)})
			resp = productsResp(false, "", []map[string]any{p})
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(resp))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/1")

	items, err := client.ListInventory(context.Background(), "")
	require.NoError(t, err)
	assert.Len(t, items, 2)
	assert.Equal(t, 2, callCount)
}

func TestListInventory_EmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(productsResp(false, "", nil)))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/1")

	items, err := client.ListInventory(context.Background(), "")
	require.NoError(t, err)
	assert.NotNil(t, items)
	assert.Empty(t, items)
}

func TestListInventory_GraphQLError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":[{"message":"Access denied"}]}`))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/1")

	_, err := client.ListInventory(context.Background(), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Access denied")
}

// ─── UpdateInventory tests ────────────────────────────────────────────────────

func TestUpdateInventory_Success(t *testing.T) {
	var capturedVars map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedVars, _ = body["variables"].(map[string]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(setQuantitiesResp(nil)))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/999")

	err := client.UpdateInventory(context.Background(), "123", 25)
	require.NoError(t, err)

	// Verify the mutation received the correct GIDs and quantity.
	require.NotNil(t, capturedVars)
	input, _ := capturedVars["input"].(map[string]any)
	quantities, _ := input["quantities"].([]any)
	require.Len(t, quantities, 1)
	q0, _ := quantities[0].(map[string]any)
	assert.Equal(t, "gid://shopify/InventoryItem/123", q0["inventoryItemId"])
	assert.Equal(t, "gid://shopify/Location/999", q0["locationId"])
	assert.EqualValues(t, 25, q0["quantity"])
	assert.Equal(t, "available", input["name"])
}

func TestUpdateInventory_UserError(t *testing.T) {
	userErrors := []map[string]any{
		{"field": []string{"inventoryItemId"}, "message": "Inventory item not found", "code": "INVALID"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(setQuantitiesResp(userErrors)))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/999")

	err := client.UpdateInventory(context.Background(), "999", 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Inventory item not found")
}

func TestUpdateInventory_GIDPassthroughUnchanged(t *testing.T) {
	var capturedVars map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedVars, _ = body["variables"].(map[string]any)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(setQuantitiesResp(nil)))
	}))
	defer server.Close()

	// Pass a full GID — it must not be double-wrapped.
	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/999")
	err := client.UpdateInventory(context.Background(), "gid://shopify/InventoryItem/123", 5)
	require.NoError(t, err)

	input, _ := capturedVars["input"].(map[string]any)
	quantities, _ := input["quantities"].([]any)
	q0, _ := quantities[0].(map[string]any)
	assert.Equal(t, "gid://shopify/InventoryItem/123", q0["inventoryItemId"])
}

// ─── Helper unit tests ────────────────────────────────────────────────────────

func TestStripGID(t *testing.T) {
	assert.Equal(t, "123", stripGID("gid://shopify/InventoryItem/123"))
	assert.Equal(t, "456", stripGID("gid://shopify/Product/456"))
	assert.Equal(t, "plain", stripGID("plain"))
}

func TestToGID(t *testing.T) {
	assert.Equal(t, "gid://shopify/InventoryItem/123", toGID("InventoryItem", "123"))
	// Already a GID — must not be re-wrapped.
	assert.Equal(t, "gid://shopify/InventoryItem/123", toGID("InventoryItem", "gid://shopify/InventoryItem/123"))
}

func TestBuildSearchQuery(t *testing.T) {
	assert.Equal(t, "status:ACTIVE", buildSearchQuery(""))
	q := buildSearchQuery("moon")
	assert.Contains(t, q, "status:ACTIVE")
	assert.Contains(t, q, "moon")
}
