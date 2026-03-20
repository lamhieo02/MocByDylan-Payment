// Package payos wraps the PayOS API (https://api-merchant.payos.vn).
// Required env vars: PAYOS_CLIENT_ID, PAYOS_API_KEY
package payos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

const baseURL = "https://api-merchant.payos.vn"

// CreatePaymentRequest is the payload sent to POST /v2/payment-requests.
type CreatePaymentRequest struct {
	OrderCode   int64   `json:"orderCode"`
	Amount      int64   `json:"amount"`
	Description string  `json:"description"`
	BuyerName   string  `json:"buyerName,omitempty"`
	BuyerEmail  string  `json:"buyerEmail,omitempty"`
	BuyerPhone  string  `json:"buyerPhone,omitempty"`
	CancelURL   string  `json:"cancelUrl"`
	ReturnURL   string  `json:"returnUrl"`
	Signature   string  `json:"signature"`
	ExpiredAt   *int64  `json:"expiredAt,omitempty"`
	Items       []Item  `json:"items,omitempty"`
}

// Item represents a product line in the PayOS payment request.
type Item struct {
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
	Price    int64  `json:"price"`
}

// CreatePaymentResponse holds the fields we care about from PayOS.
type CreatePaymentResponse struct {
	QRCode        string `json:"qrCode"`
	CheckoutURL   string `json:"checkoutUrl"`
	PaymentLinkID string `json:"paymentLinkId"`
	AccountNumber string `json:"accountNumber"`
	AccountName   string `json:"accountName"`
	Bin           string `json:"bin"`
	Amount        int64  `json:"amount"`
	Status        string `json:"status"`
}

// PaymentStatus holds the status fields from GET /v2/payment-requests/{id}.
type PaymentStatus struct {
	Status string `json:"status"` // PENDING | PAID | CANCELLED | EXPIRED
	Amount int64  `json:"amount"`
	ID     string `json:"id"`
}

// payosEnvelope is the outer wrapper for every PayOS API response.
type payosEnvelope struct {
	Code string          `json:"code"`
	Desc string          `json:"desc"`
	Data json.RawMessage `json:"data"`
}

// authHeaders adds the required PayOS authentication headers.
func authHeaders(req *http.Request) {
	req.Header.Set("x-client-id", os.Getenv("PAYOS_CLIENT_ID"))
	req.Header.Set("x-api-key", os.Getenv("PAYOS_API_KEY"))
	req.Header.Set("Content-Type", "application/json")
}

// CreatePaymentLink calls POST /v2/payment-requests and returns the data object.
func CreatePaymentLink(payload CreatePaymentRequest) (*CreatePaymentResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v2/payment-requests", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	authHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var env payosEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("payos: bad response: %s", string(raw))
	}
	if env.Code != "00" {
		return nil, fmt.Errorf("payos: error %s: %s", env.Code, env.Desc)
	}

	var data CreatePaymentResponse
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("payos: cannot parse data: %w", err)
	}
	return &data, nil
}

// GetPaymentStatus calls GET /v2/payment-requests/{id}.
func GetPaymentStatus(id string) (*PaymentStatus, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/v2/payment-requests/"+id, nil)
	if err != nil {
		return nil, err
	}
	authHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)

	var env payosEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("payos: bad response: %s", string(raw))
	}
	if env.Code != "00" {
		return nil, fmt.Errorf("payos: error %s: %s", env.Code, env.Desc)
	}

	var data PaymentStatus
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("payos: cannot parse status: %w", err)
	}
	return &data, nil
}

// RegisterWebhook calls POST /confirm-webhook to register or update the
// webhook URL for the payment channel. Should be called once at server startup.
// Required env vars: PAYOS_CLIENT_ID, PAYOS_API_KEY
func RegisterWebhook(webhookURL string) error {
	payload := map[string]string{"webhookUrl": webhookURL}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, baseURL+"/confirm-webhook", bytes.NewReader(body))
	if err != nil {
		return err
	}
	authHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var env payosEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("payos: bad response: %s", string(raw))
	}
	if env.Code != "00" {
		return fmt.Errorf("payos: register webhook error %s: %s", env.Code, env.Desc)
	}
	return nil
}

// CancelPayment calls POST /v2/payment-requests/{id}/cancel.
func CancelPayment(id, reason string) error {
	payload := map[string]string{"cancellationReason": reason}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequest(http.MethodPost, baseURL+"/v2/payment-requests/"+id+"/cancel", bytes.NewReader(body))
	if err != nil {
		return err
	}
	authHeaders(req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	var env payosEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("payos: bad response: %s", string(raw))
	}
	if env.Code != "00" {
		return fmt.Errorf("payos: cancel error %s: %s", env.Code, env.Desc)
	}
	return nil
}
