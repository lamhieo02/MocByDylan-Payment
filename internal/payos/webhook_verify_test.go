package payos

import (
	"bytes"
	"encoding/json"
	"testing"
)

// Sample from https://payos.vn/docs/tich-hop-webhook/kiem-tra-du-lieu-voi-signature/
const payosDocDataJSON = `{
  "orderCode": 123,
  "amount": 3000,
  "description": "VQRIO123",
  "accountNumber": "12345678",
  "reference": "TF230204212323",
  "transactionDateTime": "2023-02-04 18:25:00",
  "currency": "VND",
  "paymentLinkId": "124c33293c43417ab7879e14c8d9eb18",
  "code": "00",
  "desc": "Thành công",
  "counterAccountBankId": "",
  "counterAccountBankName": "",
  "counterAccountName": "",
  "counterAccountNumber": "",
  "virtualAccountName": "",
  "virtualAccountNumber": ""
}`

func TestVerifyPaymentWebhookSignature_PayOSDocSample(t *testing.T) {
	t.Setenv("PAYOS_CHECKSUM_KEY", "1a54716c8f0efb2744fb28b6e38b25da7f67a925d98bc1c18bd8faaecadd7675")

	var data map[string]interface{}
	dec := json.NewDecoder(bytes.NewReader([]byte(payosDocDataJSON)))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil {
		t.Fatal(err)
	}
	built := buildPaymentWebhookSignatureString(data)
	t.Logf("built signature string: %s", built)

	// Doc lists two different signatures in different sections; one must match.
	sigA := "412e915d2871504ed31be63c8f62a149a4410d34c4c42affc9006ef9917eaa03"
	sigB := "8d8640d802576397a1ce45ebda7f835055768ac7ad2e0bfb77f9b8f12cca4c7f"

	if !VerifyPaymentWebhookSignature([]byte(payosDocDataJSON), sigA) &&
		!VerifyPaymentWebhookSignature([]byte(payosDocDataJSON), sigB) {
		t.Fatalf("computed string did not match either PayOS doc example signature; string was: %q", built)
	}
}
