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

// makeProductWithCollections builds a fake product node that includes a collections block.
func makeProductWithCollections(id, title string, variants []map[string]any, collections []map[string]any) map[string]any {
	varEdges := make([]map[string]any, len(variants))
	for i, v := range variants {
		varEdges[i] = map[string]any{"node": v}
	}
	colEdges := make([]map[string]any, len(collections))
	for i, c := range collections {
		colEdges[i] = map[string]any{"node": c}
	}
	return map[string]any{
		"id":            id,
		"title":         title,
		"status":        "ACTIVE",
		"featuredImage": map[string]any{"url": "https://cdn.shopify.com/image.jpg"},
		"collections":   map[string]any{"edges": colEdges},
		"variants":      map[string]any{"edges": varEdges},
	}
}

// makeProduct builds a fake product node with no collections (backward-compatible helper).
func makeProduct(id, title string, variants []map[string]any) map[string]any {
	return makeProductWithCollections(id, title, variants, nil)
}

// makeVariantFull builds a fake variant node with title, selectedOptions, and inventory.
// Pass qty=-1 to simulate a variant not stocked at the queried location (null level).
func makeVariantFull(sku, variantTitle, invItemGID string, qty int, opts []map[string]any) map[string]any {
	var invLevel any
	if qty >= 0 {
		invLevel = map[string]any{
			"quantities": []map[string]any{
				{"name": "available", "quantity": qty},
			},
		}
	}
	if opts == nil {
		opts = []map[string]any{}
	}
	return map[string]any{
		"sku":             sku,
		"title":           variantTitle,
		"selectedOptions": opts,
		"inventoryItem": map[string]any{
			"id":             invItemGID,
			"inventoryLevel": invLevel,
		},
	}
}

// makeVariant is a convenience wrapper around makeVariantFull for tests that
// don't care about variant title or options (backward-compatible helper).
func makeVariant(sku, invItemGID string, qty int) map[string]any {
	return makeVariantFull(sku, "", invItemGID, qty, nil)
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
	// Text filtering is client-side; the Shopify query always uses status:ACTIVE only.
	queryStr, _ := capturedVars["query"].(string)
	assert.Equal(t, "status:ACTIVE", queryStr)
	// locationId must be passed so the query filters to the correct location.
	assert.Equal(t, "gid://shopify/Location/1", capturedVars["locationId"])
}

func TestListInventory_VariantOptions(t *testing.T) {
	opts := []map[string]any{
		{"name": "Size", "value": "12mm"},
		{"name": "Color", "value": "Forest"},
	}
	product := makeProduct(
		"gid://shopify/Product/1",
		"Forest Bracelet",
		[]map[string]any{makeVariantFull("FOREST-12", "12mm", "gid://shopify/InventoryItem/1", 8, opts)},
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

	item := items[0]
	assert.Equal(t, "Forest Bracelet", item.Title)
	assert.Equal(t, "12mm", item.VariantTitle)
	assert.Equal(t, "FOREST-12", item.SKU)
	assert.Equal(t, 8, item.Quantity)
	require.Len(t, item.SelectedOptions, 2)
	assert.Equal(t, "Size", item.SelectedOptions[0].Name)
	assert.Equal(t, "12mm", item.SelectedOptions[0].Value)
	assert.Equal(t, "Color", item.SelectedOptions[1].Name)
	assert.Equal(t, "Forest", item.SelectedOptions[1].Value)
	assert.Equal(t, "12mm", item.DisplayVariant) // Size wins
	assert.Equal(t, "12mm", item.Size)
}

func TestListInventory_SearchMatchesOptionValue(t *testing.T) {
	opts12 := []map[string]any{{"name": "Size", "value": "12mm"}}
	opts8 := []map[string]any{{"name": "Size", "value": "8mm"}}
	product := makeProduct(
		"gid://shopify/Product/1",
		"Forest Bracelet",
		[]map[string]any{
			makeVariantFull("FOREST-12", "12mm", "gid://shopify/InventoryItem/1", 8, opts12),
			makeVariantFull("FOREST-8", "8mm", "gid://shopify/InventoryItem/2", 5, opts8),
		},
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(productsResp(false, "", []map[string]any{product})))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/1")

	// Search "12mm" — only the 12mm variant should be returned.
	items, err := client.ListInventory(context.Background(), "12mm")
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "12mm", items[0].Size)
	assert.Equal(t, "FOREST-12", items[0].SKU)
}

func TestListInventory_SearchMatchesProductTitle(t *testing.T) {
	opts := []map[string]any{{"name": "Size", "value": "12mm"}}
	product := makeProduct(
		"gid://shopify/Product/1",
		"Moon Charm",
		[]map[string]any{
			makeVariantFull("MOON-12", "12mm", "gid://shopify/InventoryItem/1", 3, opts),
			makeVariantFull("MOON-8", "8mm", "gid://shopify/InventoryItem/2", 7, []map[string]any{{"name": "Size", "value": "8mm"}}),
		},
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(productsResp(false, "", []map[string]any{product})))
	}))
	defer server.Close()

	client := newClientForTest(&http.Client{Transport: &redirectTransport{server.URL}}, "gid://shopify/Location/1")

	// Searching the product title returns ALL variants of that product.
	items, err := client.ListInventory(context.Background(), "moon")
	require.NoError(t, err)
	assert.Len(t, items, 2)
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
	// buildSearchQuery always returns status:ACTIVE — text filtering is applied
	// client-side in ListInventory so that variant title and option value searches work.
	assert.Equal(t, "status:ACTIVE", buildSearchQuery(""))
	assert.Equal(t, "status:ACTIVE", buildSearchQuery("moon"))
	assert.Equal(t, "status:ACTIVE", buildSearchQuery("12mm"))
}

func TestResolveDisplayVariant(t *testing.T) {
	opts := func(pairs ...string) []SelectedOption {
		out := make([]SelectedOption, 0, len(pairs)/2)
		for i := 0; i+1 < len(pairs); i += 2 {
			out = append(out, SelectedOption{Name: pairs[i], Value: pairs[i+1]})
		}
		return out
	}
	assert.Equal(t, "12mm", resolveDisplayVariant(opts("Size", "12mm"), "12mm"))
	assert.Equal(t, "Red", resolveDisplayVariant(opts("Color", "Red"), "Red"))
	// Size wins over Color.
	assert.Equal(t, "12mm", resolveDisplayVariant(opts("Color", "Red", "Size", "12mm"), "12mm / Red"))
	// Falls back to variant title when no Size/Color option.
	assert.Equal(t, "Default Title", resolveDisplayVariant(opts("Title", "Default Title"), "Default Title"))
	// Empty variant title → empty string.
	assert.Equal(t, "", resolveDisplayVariant(nil, ""))
}

func TestResolveSize(t *testing.T) {
	assert.Equal(t, "12mm", resolveSize([]SelectedOption{{Name: "Size", Value: "12mm"}}))
	// Case-insensitive name match.
	assert.Equal(t, "L", resolveSize([]SelectedOption{{Name: "size", Value: "L"}}))
	assert.Equal(t, "", resolveSize([]SelectedOption{{Name: "Color", Value: "Red"}}))
	assert.Equal(t, "", resolveSize(nil))
}

func TestVariantMatchesSearch(t *testing.T) {
	opts := []SelectedOption{{Name: "Size", Value: "12mm"}, {Name: "Color", Value: "Forest"}}
	assert.True(t, variantMatchesSearch("Forest Bracelet", "12mm", "FOREST-12", opts, ""))
	assert.True(t, variantMatchesSearch("Forest Bracelet", "12mm", "FOREST-12", opts, "forest"))
	assert.True(t, variantMatchesSearch("Forest Bracelet", "12mm", "FOREST-12", opts, "12mm"))
	assert.True(t, variantMatchesSearch("Forest Bracelet", "12mm", "FOREST-12", opts, "FOREST-12"))
	assert.True(t, variantMatchesSearch("Forest Bracelet", "12mm", "FOREST-12", opts, "Forest")) // option value
	assert.False(t, variantMatchesSearch("Forest Bracelet", "12mm", "FOREST-12", opts, "moon"))
}

// ─── resolveCollectionType tests ──────────────────────────────────────────────

func TestResolveCollectionType_KnownCollection(t *testing.T) {
	for _, title := range []string{"Mộc Wear", "Mộc Care", "Mộc Figure", "Mộc Charm", "Mộc Living"} {
		cols := []Collection{{ID: "1", Title: title, Handle: "moc-x"}}
		assert.Equal(t, title, resolveCollectionType(cols), "expected %q", title)
	}
}

func TestResolveCollectionType_Priority(t *testing.T) {
	// When a product belongs to multiple recognised collections, the first in
	// mocCollectionPriority wins (Mộc Wear before Mộc Charm).
	cols := []Collection{
		{Title: "Mộc Charm"},
		{Title: "Mộc Wear"},
	}
	assert.Equal(t, "Mộc Wear", resolveCollectionType(cols))
}

func TestResolveCollectionType_Other(t *testing.T) {
	assert.Equal(t, "Other", resolveCollectionType(nil))
	assert.Equal(t, "Other", resolveCollectionType([]Collection{{Title: "Sale"}}))
}

func TestResolveCollectionType_CaseInsensitive(t *testing.T) {
	cols := []Collection{{Title: "mộc charm"}}
	assert.Equal(t, "mộc charm", resolveCollectionType(cols))
}

// ─── ListInventory collection integration tests ───────────────────────────────

func TestListInventory_Collections(t *testing.T) {
	cols := []map[string]any{
		{"id": "gid://shopify/Collection/10", "title": "Mộc Charm", "handle": "moc-charm"},
	}
	product := makeProductWithCollections(
		"gid://shopify/Product/1",
		"Moon Charm",
		[]map[string]any{makeVariant("MOON-001", "gid://shopify/InventoryItem/123", 5)},
		cols,
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

	item := items[0]
	require.Len(t, item.Collections, 1)
	assert.Equal(t, "10", item.Collections[0].ID) // GID stripped
	assert.Equal(t, "Mộc Charm", item.Collections[0].Title)
	assert.Equal(t, "moc-charm", item.Collections[0].Handle)
	assert.Equal(t, "Mộc Charm", item.CollectionType)
}

func TestListInventory_NoCollections_ReturnsOther(t *testing.T) {
	product := makeProduct(
		"gid://shopify/Product/2",
		"Mystery Item",
		[]map[string]any{makeVariant("MYS-001", "gid://shopify/InventoryItem/999", 1)},
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
	assert.Empty(t, items[0].Collections)
	assert.Equal(t, "Other", items[0].CollectionType)
}
