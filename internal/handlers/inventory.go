package handlers

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

// InventoryHandler handles internal staff inventory endpoints.
type InventoryHandler struct {
	client shopify.ShopifyClient
}

// NewInventoryHandler creates an InventoryHandler with the provided Shopify client.
func NewInventoryHandler(client shopify.ShopifyClient) *InventoryHandler {
	return &InventoryHandler{client: client}
}

// listInventoryResponse is the response body for GET /api/internal/inventory.
type listInventoryResponse struct {
	Items []shopify.InventoryItem `json:"items"`
}

// List handles GET /api/internal/inventory.
// Accepts an optional ?search= query parameter to filter by product title or SKU.
func (h *InventoryHandler) List(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	search := strings.TrimSpace(r.URL.Query().Get("search"))

	items, err := h.client.ListInventory(r.Context(), search)
	if err != nil {
		jsonErr(w, "failed to fetch inventory: "+err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(listInventoryResponse{Items: items})
}

// updateInventoryRequest is the request body for PATCH /api/internal/inventory/:inventoryItemId.
type updateInventoryRequest struct {
	Quantity *int `json:"quantity"`
}

// updateInventoryResponse is the response body for PATCH /api/internal/inventory/:inventoryItemId.
type updateInventoryResponse struct {
	Success  bool `json:"success"`
	Quantity int  `json:"quantity"`
}

// Update handles PATCH /api/internal/inventory/{inventoryItemId}.
// Sets the available quantity for the given inventory item.
func (h *InventoryHandler) Update(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		jsonErr(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	inventoryItemID := strings.TrimSpace(r.PathValue("inventoryItemId"))
	if inventoryItemID == "" {
		jsonErr(w, "inventoryItemId is required", http.StatusBadRequest)
		return
	}

	var req updateInventoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonErr(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.Quantity == nil {
		jsonErr(w, "quantity is required", http.StatusBadRequest)
		return
	}
	if *req.Quantity < 0 {
		jsonErr(w, "quantity must be >= 0", http.StatusBadRequest)
		return
	}

	if err := h.client.UpdateInventory(r.Context(), inventoryItemID, *req.Quantity); err != nil {
		errMsg := err.Error()
		status := http.StatusBadGateway
		if strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "invalid") {
			status = http.StatusBadRequest
		}
		jsonErr(w, "failed to update inventory: "+errMsg, status)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updateInventoryResponse{
		Success:  true,
		Quantity: *req.Quantity,
	})
}
