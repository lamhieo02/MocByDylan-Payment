package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockShopifyClient implements shopify.ShopifyClient for handler tests.
type mockShopifyClient struct {
	listFn   func(ctx context.Context, search string) ([]shopify.InventoryItem, error)
	updateFn func(ctx context.Context, inventoryItemID string, quantity int) error
}

func (m *mockShopifyClient) ListInventory(ctx context.Context, search string) ([]shopify.InventoryItem, error) {
	return m.listFn(ctx, search)
}

func (m *mockShopifyClient) UpdateInventory(ctx context.Context, inventoryItemID string, quantity int) error {
	return m.updateFn(ctx, inventoryItemID, quantity)
}

// newListRequest builds a GET request for the inventory list endpoint.
func newListRequest(search string) *http.Request {
	url := "/api/internal/inventory"
	if search != "" {
		url += "?search=" + search
	}
	return httptest.NewRequest(http.MethodGet, url, nil)
}

// newUpdateRequest builds a PATCH request for the inventory update endpoint.
func newUpdateRequest(inventoryItemID string, body any) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch, "/api/internal/inventory/"+inventoryItemID, bytes.NewReader(b))
	req.SetPathValue("inventoryItemId", inventoryItemID)
	return req
}

// ─── List handler tests ────────────────────────────────────────────────────────

func TestListHandler_Success(t *testing.T) {
	expectedItems := []shopify.InventoryItem{
		{InventoryItemID: "123", Title: "Moon Charm", SKU: "MOON-001", Image: "https://cdn.shopify.com/image.jpg", Quantity: 18},
	}
	client := &mockShopifyClient{
		listFn: func(_ context.Context, search string) ([]shopify.InventoryItem, error) {
			assert.Equal(t, "", search)
			return expectedItems, nil
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.List(w, newListRequest(""))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp listInventoryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Items, 1)
	assert.Equal(t, "123", resp.Items[0].InventoryItemID)
	assert.Equal(t, "Moon Charm", resp.Items[0].Title)
	assert.Equal(t, 18, resp.Items[0].Quantity)
}

func TestListHandler_ResponseShape(t *testing.T) {
	client := &mockShopifyClient{
		listFn: func(_ context.Context, _ string) ([]shopify.InventoryItem, error) {
			return []shopify.InventoryItem{
				{InventoryItemID: "123", Title: "T", SKU: "S", Image: "I", Quantity: 5},
			}, nil
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.List(w, newListRequest(""))

	// Verify camelCase JSON keys in response.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	items := raw["items"].([]any)
	item := items[0].(map[string]any)
	assert.Contains(t, item, "inventoryItemId", "response must use camelCase key")
	assert.NotContains(t, item, "inventory_item_id", "response must not use snake_case key")
}

func TestListHandler_WithSearch(t *testing.T) {
	var capturedSearch string
	client := &mockShopifyClient{
		listFn: func(_ context.Context, search string) ([]shopify.InventoryItem, error) {
			capturedSearch = search
			return []shopify.InventoryItem{}, nil
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.List(w, newListRequest("moon"))

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "moon", capturedSearch)
}

func TestListHandler_EmptyItems(t *testing.T) {
	client := &mockShopifyClient{
		listFn: func(_ context.Context, _ string) ([]shopify.InventoryItem, error) {
			return []shopify.InventoryItem{}, nil
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.List(w, newListRequest(""))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp listInventoryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotNil(t, resp.Items)
	assert.Empty(t, resp.Items)
}

func TestListHandler_ShopifyError(t *testing.T) {
	client := &mockShopifyClient{
		listFn: func(_ context.Context, _ string) ([]shopify.InventoryItem, error) {
			return nil, errors.New("shopify: GraphQL errors: Access denied")
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.List(w, newListRequest(""))

	assert.Equal(t, http.StatusBadGateway, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "Access denied")
}

func TestListHandler_MethodNotAllowed(t *testing.T) {
	h := NewInventoryHandler(&mockShopifyClient{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/inventory", nil)
	h.List(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ─── Update handler tests ──────────────────────────────────────────────────────

func TestUpdateHandler_Success(t *testing.T) {
	client := &mockShopifyClient{
		updateFn: func(_ context.Context, inventoryItemID string, quantity int) error {
			assert.Equal(t, "123", inventoryItemID)
			assert.Equal(t, 25, quantity)
			return nil
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.Update(w, newUpdateRequest("123", map[string]int{"quantity": 25}))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp updateInventoryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, 25, resp.Quantity)
}

func TestUpdateHandler_ResponseShape(t *testing.T) {
	client := &mockShopifyClient{
		updateFn: func(_ context.Context, _ string, _ int) error { return nil },
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.Update(w, newUpdateRequest("123", map[string]int{"quantity": 10}))

	// Response must be {"success": true, "quantity": 10} — no inventory_item_id field.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.Equal(t, true, raw["success"])
	assert.EqualValues(t, 10, raw["quantity"])
	assert.NotContains(t, raw, "inventory_item_id")
	assert.NotContains(t, raw, "inventoryItemId")
}

func TestUpdateHandler_ZeroQuantity(t *testing.T) {
	client := &mockShopifyClient{
		updateFn: func(_ context.Context, _ string, qty int) error {
			assert.Equal(t, 0, qty)
			return nil
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.Update(w, newUpdateRequest("123", map[string]int{"quantity": 0}))

	assert.Equal(t, http.StatusOK, w.Code)

	var resp updateInventoryResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.Equal(t, 0, resp.Quantity)
}

func TestUpdateHandler_NegativeQuantity(t *testing.T) {
	h := NewInventoryHandler(&mockShopifyClient{})
	w := httptest.NewRecorder()
	h.Update(w, newUpdateRequest("123", map[string]int{"quantity": -1}))

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], ">= 0")
}

func TestUpdateHandler_MissingQuantityField(t *testing.T) {
	h := NewInventoryHandler(&mockShopifyClient{})
	w := httptest.NewRecorder()
	h.Update(w, newUpdateRequest("123", map[string]string{"other": "field"}))

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "quantity is required")
}

func TestUpdateHandler_InvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPatch, "/api/internal/inventory/123", bytes.NewBufferString("not-json"))
	req.SetPathValue("inventoryItemId", "123")

	h := NewInventoryHandler(&mockShopifyClient{})
	w := httptest.NewRecorder()
	h.Update(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateHandler_ShopifyNotFoundError(t *testing.T) {
	client := &mockShopifyClient{
		updateFn: func(_ context.Context, _ string, _ int) error {
			return errors.New("shopify: inventory update failed: Inventory item not found")
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.Update(w, newUpdateRequest("999", map[string]int{"quantity": 10}))

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "not found")
}

func TestUpdateHandler_ShopifyGatewayError(t *testing.T) {
	client := &mockShopifyClient{
		updateFn: func(_ context.Context, _ string, _ int) error {
			return errors.New("shopify: HTTP 503: service unavailable")
		},
	}

	h := NewInventoryHandler(client)
	w := httptest.NewRecorder()
	h.Update(w, newUpdateRequest("123", map[string]int{"quantity": 5}))

	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestUpdateHandler_MethodNotAllowed(t *testing.T) {
	h := NewInventoryHandler(&mockShopifyClient{})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/internal/inventory/123", nil)
	req.SetPathValue("inventoryItemId", "123")
	h.Update(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
