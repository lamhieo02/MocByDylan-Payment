package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/allowlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reloadAllowlist sets INVENTORY_ALLOWED_EMAILS and re-parses the allow-list
// so each test gets a clean, predictable state.
func reloadAllowlist(t *testing.T, emails string) {
	t.Helper()
	t.Setenv("INVENTORY_ALLOWED_EMAILS", emails)
	allowlist.Reload()
	t.Cleanup(allowlist.Reload) // restore after test
}

func postValidate(body any) (*httptest.ResponseRecorder, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req := httptest.NewRequest(http.MethodPost, "/api/internal/auth/validate", bytes.NewReader(b))
	w := httptest.NewRecorder()
	ValidateEmail(w, req)
	return w, nil
}

func TestValidateEmail_Allowed(t *testing.T) {
	reloadAllowlist(t, "staff@mocbydylan.com,owner@mocbydylan.com")

	w, err := postValidate(map[string]string{"email": "staff@mocbydylan.com"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
}

func TestValidateEmail_AllowedCaseInsensitive(t *testing.T) {
	reloadAllowlist(t, "staff@mocbydylan.com")

	w, err := postValidate(map[string]string{"email": "  STAFF@MOCBYDYLAN.COM  "})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestValidateEmail_NotAllowed(t *testing.T) {
	reloadAllowlist(t, "staff@mocbydylan.com")

	w, err := postValidate(map[string]string{"email": "intruder@evil.com"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"])
	assert.Equal(t, "Unauthorized", resp["message"])
}

func TestValidateEmail_EmptyList(t *testing.T) {
	reloadAllowlist(t, "")

	w, err := postValidate(map[string]string{"email": "staff@mocbydylan.com"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestValidateEmail_MissingEmailField(t *testing.T) {
	w, err := postValidate(map[string]string{"other": "value"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "email is required")
}

func TestValidateEmail_InvalidFormat(t *testing.T) {
	w, err := postValidate(map[string]string{"email": "notanemail"})
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "invalid email format")
}

func TestValidateEmail_InvalidBody(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/internal/auth/validate", bytes.NewBufferString("not-json"))
	w := httptest.NewRecorder()
	ValidateEmail(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestValidateEmail_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/internal/auth/validate", nil)
	w := httptest.NewRecorder()
	ValidateEmail(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

// ─── RequireAllowedEmail middleware tests ─────────────────────────────────────

func TestRequireAllowedEmail_Authorized(t *testing.T) {
	reloadAllowlist(t, "staff@mocbydylan.com")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Staff-Email", "staff@mocbydylan.com")
	w := httptest.NewRecorder()

	RequireAllowedEmail(next).ServeHTTP(w, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestRequireAllowedEmail_AuthorizedCaseInsensitive(t *testing.T) {
	reloadAllowlist(t, "staff@mocbydylan.com")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Staff-Email", "STAFF@MOCBYDYLAN.COM")
	w := httptest.NewRecorder()

	RequireAllowedEmail(next).ServeHTTP(w, req)

	assert.True(t, called)
}

func TestRequireAllowedEmail_NotInList(t *testing.T) {
	reloadAllowlist(t, "staff@mocbydylan.com")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Staff-Email", "intruder@evil.com")
	w := httptest.NewRecorder()

	RequireAllowedEmail(next).ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"])
	assert.Equal(t, "Unauthorized", resp["message"])
}

func TestRequireAllowedEmail_MissingHeader(t *testing.T) {
	reloadAllowlist(t, "staff@mocbydylan.com")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	RequireAllowedEmail(next).ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAllowedEmail_EmptyHeader(t *testing.T) {
	reloadAllowlist(t, "staff@mocbydylan.com")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Staff-Email", "")
	w := httptest.NewRecorder()

	RequireAllowedEmail(next).ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAllowedEmail_EmptyAllowList(t *testing.T) {
	reloadAllowlist(t, "")

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true })

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Staff-Email", "staff@mocbydylan.com")
	w := httptest.NewRecorder()

	RequireAllowedEmail(next).ServeHTTP(w, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
