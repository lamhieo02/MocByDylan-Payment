package shopify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// InventoryItem represents a product variant with its current available quantity.
type InventoryItem struct {
	InventoryItemID string `json:"inventoryItemId"`
	Title           string `json:"title"`
	SKU             string `json:"sku"`
	Image           string `json:"image"`
	Quantity        int    `json:"quantity"`
}

// ShopifyClient is the abstraction for inventory operations against Shopify Admin GraphQL.
type ShopifyClient interface {
	ListInventory(ctx context.Context, search string) ([]InventoryItem, error)
	UpdateInventory(ctx context.Context, inventoryItemID string, quantity int) error
}

type shopifyInventoryClient struct {
	httpClient    *http.Client
	store         string
	version       string
	locationID    string // fetched once at startup
	tokenProvider TokenProvider
}

// NewInventoryClient creates a ShopifyClient, fetching and caching the first active
// Shopify location on startup. Token lookup on every GraphQL request is handled by tp.
//
// Reads from env:
//
//	SHOPIFY_STORE_DOMAIN  (shared with the payment client)
//	SHOPIFY_API_VERSION   (defaults to "2026-04")
func NewInventoryClient(ctx context.Context, tp TokenProvider) (ShopifyClient, error) {
	store := os.Getenv("SHOPIFY_STORE_DOMAIN")
	if store == "" {
		return nil, fmt.Errorf("shopify: SHOPIFY_STORE_DOMAIN is not configured")
	}

	version := os.Getenv("SHOPIFY_API_VERSION")
	if version == "" {
		version = "2026-04"
	}
	c := &shopifyInventoryClient{
		httpClient:    HTTPClient,
		store:         store,
		version:       version,
		tokenProvider: tp,
	}
	locID, err := c.fetchFirstLocation(ctx)
	if err != nil {
		return nil, fmt.Errorf("shopify: fetch location: %w", err)
	}
	c.locationID = locID
	return c, nil
}

// graphqlURL returns the Shopify Admin GraphQL endpoint for this client.
func (c *shopifyInventoryClient) graphqlURL() string {
	return fmt.Sprintf("https://%s/admin/api/%s/graphql.json", c.store, c.version)
}

// doGraphQL executes a GraphQL request against the Shopify Admin API.
// The access token is obtained via the TokenProvider on every call so that
// token rotations (e.g. after a re-install) are picked up without a restart.
func (c *shopifyInventoryClient) doGraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	token, err := c.tokenProvider.GetAccessToken(ctx, c.store)
	if err != nil {
		return nil, fmt.Errorf("shopify: get access token: %w", err)
	}

	payload := map[string]any{
		"query":     query,
		"variables": variables,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.graphqlURL(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Shopify-Access-Token", token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shopify: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	var result struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("shopify: cannot parse GraphQL response: %w", err)
	}
	if len(result.Errors) > 0 {
		msgs := make([]string, len(result.Errors))
		for i, e := range result.Errors {
			msgs[i] = e.Message
		}
		return nil, fmt.Errorf("shopify: GraphQL errors: %s", strings.Join(msgs, "; "))
	}
	return result.Data, nil
}

// stripGID extracts the numeric ID from a Shopify GID.
// "gid://shopify/InventoryItem/123" → "123"
func stripGID(gid string) string {
	parts := strings.Split(gid, "/")
	if len(parts) == 0 {
		return gid
	}
	return parts[len(parts)-1]
}

// toGID wraps a numeric ID as a fully-qualified Shopify GID of the given type.
func toGID(typeName, id string) string {
	if strings.HasPrefix(id, "gid://") {
		return id
	}
	return fmt.Sprintf("gid://shopify/%s/%s", typeName, id)
}

// ─── Location ────────────────────────────────────────────────────────────────

const locationsQuery = `
query {
  locations(first: 1) {
    edges {
      node {
        id
        name
      }
    }
  }
}
`

// fetchFirstLocation retrieves the first active Shopify location and returns its GID.
func (c *shopifyInventoryClient) fetchFirstLocation(ctx context.Context) (string, error) {
	data, err := c.doGraphQL(ctx, locationsQuery, nil)
	if err != nil {
		return "", err
	}

	var resp struct {
		Locations struct {
			Edges []struct {
				Node struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"node"`
			} `json:"edges"`
		} `json:"locations"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("shopify: cannot parse locations response: %w", err)
	}
	if len(resp.Locations.Edges) == 0 {
		return "", fmt.Errorf("shopify: no locations found in store")
	}
	return resp.Locations.Edges[0].Node.ID, nil
}

// ─── List inventory ───────────────────────────────────────────────────────────

// listInventoryQuery fetches active products with per-variant inventory quantities
// filtered to the specific location ($locationId) that UpdateInventory writes to.
// Using inventoryLevel(locationId:) ensures the displayed quantity always matches
// the location being updated — critical for multi-location stores.
const listInventoryQuery = `
query ListInventory($query: String, $cursor: String, $locationId: ID!) {
  products(first: 50, query: $query, after: $cursor, sortKey: TITLE) {
    pageInfo {
      hasNextPage
      endCursor
    }
    edges {
      node {
        id
        title
        status
        featuredImage {
          url
        }
        variants(first: 100) {
          edges {
            node {
              sku
              inventoryItem {
                id
                inventoryLevel(locationId: $locationId) {
                  quantities(names: ["available"]) {
                    name
                    quantity
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}
`

type gqlProductsResponse struct {
	Products struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Edges []struct {
			Node struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Status string `json:"status"`
				FeaturedImage *struct {
					URL string `json:"url"`
				} `json:"featuredImage"`
				Variants struct {
					Edges []struct {
						Node struct {
							SKU           string `json:"sku"`
							InventoryItem struct {
								ID             string `json:"id"`
								// InventoryLevel is nil when the item is not stocked at this location.
								InventoryLevel *struct {
									Quantities []struct {
										Name     string `json:"name"`
										Quantity int    `json:"quantity"`
									} `json:"quantities"`
								} `json:"inventoryLevel"`
							} `json:"inventoryItem"`
						} `json:"node"`
					} `json:"edges"`
				} `json:"variants"`
			} `json:"node"`
		} `json:"edges"`
	} `json:"products"`
}

// buildSearchQuery constructs a Shopify product search query string.
func buildSearchQuery(search string) string {
	base := "status:ACTIVE"
	if search == "" {
		return base
	}
	s := strings.ReplaceAll(search, `"`, ``)
	return fmt.Sprintf(`%s AND (title:*%s* OR sku:*%s*)`, base, s, s)
}

// ListInventory returns all active products with inventory quantities.
// Pass a non-empty search to filter by product title or SKU.
func (c *shopifyInventoryClient) ListInventory(ctx context.Context, search string) ([]InventoryItem, error) {
	var items []InventoryItem
	var cursor *string
	queryStr := buildSearchQuery(search)

	for {
		vars := map[string]any{
			"query":      queryStr,
			"locationId": c.locationID, // filter inventory to the same location UpdateInventory writes to
		}
		if cursor != nil {
			vars["cursor"] = *cursor
		}

		data, err := c.doGraphQL(ctx, listInventoryQuery, vars)
		if err != nil {
			return nil, err
		}

		var resp gqlProductsResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("shopify: cannot parse products response: %w", err)
		}

		for _, pe := range resp.Products.Edges {
			p := pe.Node
			if p.Status != "ACTIVE" {
				continue
			}

			imageURL := ""
			if p.FeaturedImage != nil {
				imageURL = p.FeaturedImage.URL
			}

			for _, ve := range p.Variants.Edges {
				v := ve.Node

				quantity := 0
				if v.InventoryItem.InventoryLevel != nil {
					for _, q := range v.InventoryItem.InventoryLevel.Quantities {
						if q.Name == "available" {
							quantity = q.Quantity
							break
						}
					}
				}

				items = append(items, InventoryItem{
					InventoryItemID: stripGID(v.InventoryItem.ID),
					Title:           p.Title,
					SKU:             v.SKU,
					Image:           imageURL,
					Quantity:        quantity,
				})
			}
		}

		if !resp.Products.PageInfo.HasNextPage {
			break
		}
		end := resp.Products.PageInfo.EndCursor
		cursor = &end
	}

	if items == nil {
		items = []InventoryItem{}
	}
	return items, nil
}

// ─── Update inventory ─────────────────────────────────────────────────────────

const setQuantitiesMutation = `
mutation SetQuantities($input: InventorySetQuantitiesInput!) {
  inventorySetQuantities(input: $input) {
    userErrors {
      field
      message
      code
    }
    inventoryAdjustmentGroup {
      reason
      changes {
        name
        delta
        quantityAfterChange
      }
    }
  }
}
`

type gqlSetQuantitiesResponse struct {
	InventorySetQuantities struct {
		UserErrors []struct {
			Field   []string `json:"field"`
			Message string   `json:"message"`
			Code    string   `json:"code"`
		} `json:"userErrors"`
		InventoryAdjustmentGroup *struct {
			Changes []struct {
				Name                string `json:"name"`
				Delta               int    `json:"delta"`
				QuantityAfterChange int    `json:"quantityAfterChange"`
			} `json:"changes"`
		} `json:"inventoryAdjustmentGroup"`
	} `json:"inventorySetQuantities"`
}

// UpdateInventory sets the available quantity for an inventory item at the cached location.
func (c *shopifyInventoryClient) UpdateInventory(ctx context.Context, inventoryItemID string, quantity int) error {
	vars := map[string]any{
		"input": map[string]any{
			"name":   "available",
			"reason": "correction",
			"quantities": []map[string]any{
				{
					"inventoryItemId": toGID("InventoryItem", inventoryItemID),
					"locationId":      c.locationID,
					"quantity":        quantity,
				},
			},
		},
	}

	data, err := c.doGraphQL(ctx, setQuantitiesMutation, vars)
	if err != nil {
		return err
	}

	var resp gqlSetQuantitiesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("shopify: cannot parse set quantities response: %w", err)
	}

	if errs := resp.InventorySetQuantities.UserErrors; len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Message
		}
		return fmt.Errorf("shopify: inventory update failed: %s", strings.Join(msgs, "; "))
	}
	return nil
}
