// Package payos — webhook signature verification per PayOS payment-requests docs:
// https://payos.vn/docs/tich-hop-webhook/kiem-tra-du-lieu-voi-signature/
package payos

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// VerifyPaymentWebhookSignature checks HMAC-SHA256 over the webhook `data` object.
// dataJSON must be the raw JSON of the "data" field (not the full webhook body).
// Algorithm matches PayOS samples: keys sorted alphabetically, key=value joined by &,
// null/undefined → empty string, arrays JSON-stringified with per-element object key sort.
func VerifyPaymentWebhookSignature(dataJSON []byte, receivedSignature string) bool {
	if len(bytes.TrimSpace(dataJSON)) == 0 || strings.TrimSpace(receivedSignature) == "" {
		return false
	}

	var data map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader(dataJSON))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		return false
	}

	sigInput := buildPaymentWebhookSignatureString(data)
	checksumKey := os.Getenv("PAYOS_CHECKSUM_KEY")
	if checksumKey == "" {
		return false
	}

	mac := hmac.New(sha256.New, []byte(checksumKey))
	mac.Write([]byte(sigInput))
	expected := fmt.Sprintf("%x", mac.Sum(nil))

	return hmac.Equal([]byte(strings.ToLower(expected)), []byte(strings.ToLower(strings.TrimSpace(receivedSignature))))
}

func buildPaymentWebhookSignatureString(data map[string]interface{}) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+formatWebhookFieldValue(data[k]))
	}
	return strings.Join(parts, "&")
}

func formatWebhookFieldValue(v interface{}) string {
	if v == nil {
		return ""
	}

	switch t := v.(type) {
	case string:
		if t == "null" || t == "undefined" {
			return ""
		}
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return t.String()
	case float64:
		if math.Trunc(t) == t && !math.IsInf(t, 0) && !math.IsNaN(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []interface{}:
		// PayOS: JSON.stringify(array.map(sort keys each object))
		sorted := make([]interface{}, len(t))
		for i, e := range t {
			if m, ok := e.(map[string]interface{}); ok {
				sorted[i] = sortMapStringKeysDeep(m)
			} else {
				sorted[i] = e
			}
		}
		return mustJSONCompact(sorted)
	case map[string]interface{}:
		return mustJSONCompact(sortMapStringKeysDeep(t))
	default:
		// Fallback — should not happen for normal PayOS JSON
		return fmt.Sprint(t)
	}
}

// sortMapStringKeysDeep returns a new map with same values; nested maps sorted for stable JSON.
func sortMapStringKeysDeep(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return nil
	}
	out := make(map[string]interface{}, len(m))
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = deepSortValue(m[k])
	}
	return out
}

func deepSortValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return sortMapStringKeysDeep(t)
	case []interface{}:
		out := make([]interface{}, len(t))
		for i, e := range t {
			out[i] = deepSortValue(e)
		}
		return out
	default:
		return v
	}
}

func mustJSONCompact(v interface{}) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return ""
	}
	s := strings.TrimSuffix(strings.TrimSpace(buf.String()), "\n")
	return s
}
