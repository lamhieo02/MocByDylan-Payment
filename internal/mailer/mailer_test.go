package mailer

import (
	"errors"
	"net/smtp"
	"os"
	"strings"
	"testing"

	gomail "github.com/jordan-wright/email"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func sampleNotification() Notification {
	return Notification{
		ShopifyOrderName:    "#D42",
		ShopifyOrderID:      987654321,
		PaymentLinkID:       "a4bdadfe8657413fbf2baa8412d4bd44",
		Reference:           "FT26080900SF",
		TransactionDatetime: "2026-03-21T10:00:00+07:00",
		Amount:              179000,
		BuyerName:           "Lâm Nguyễn",
		BuyerEmail:          "lamlklk2002@gmail.com",
		BuyerPhone:          "0901234567",
		ShippingAddress:     "123 Nguyễn Huệ, Quận 1, TP.HCM",
		LineItems: []LineItem{
			{Title: "Áo thun nam", VariantID: 44965664260278, Quantity: 2, Price: 89500},
			{Title: "", VariantID: 44965664260279, Quantity: 1, Price: 89500},
		},
	}
}

// capturedSend records what was passed to the fake sendFn.
type capturedSend struct {
	email *gomail.Email
	addr  string
}

// stubSend replaces sendFn and captures the call. Returns the supplied error.
func stubSend(returnErr error) (restore func(), cap *capturedSend) {
	cap = &capturedSend{}
	orig := sendFn
	sendFn = func(e *gomail.Email, addr string, _ smtp.Auth) error {
		cap.email = e
		cap.addr = addr
		return returnErr
	}
	return func() { sendFn = orig }, cap
}

// ── TestSend_Success ──────────────────────────────────────────────────────────

// send() must call sendFn with the correct SMTP address when SMTP_PASSWORD is set.
func TestSend_Success(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "kzag yiig mbxz gapy")

	restore, cap := stubSend(nil)
	defer restore()

	if err := send(sampleNotification()); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	wantAddr := smtpHost + ":" + smtpPort
	if cap.addr != wantAddr {
		t.Errorf("addr = %q, want %q", cap.addr, wantAddr)
	}
}

// ── TestSend_EmailEnvelope ────────────────────────────────────────────────────

// The email struct must have the correct From and To fields.
func TestSend_EmailEnvelope(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "kzagyiigmbxzgapy")

	restore, cap := stubSend(nil)
	defer restore()

	_ = send(sampleNotification())

	wantFrom := "PayOS Backend Notifier <" + fromAddr + ">"
	if cap.email.From != wantFrom {
		t.Errorf("From = %q, want %q", cap.email.From, wantFrom)
	}
	if len(cap.email.To) != 1 || cap.email.To[0] != toAddr {
		t.Errorf("To = %v, want [%q]", cap.email.To, toAddr)
	}
}

// ── TestSend_SubjectContainsOrderName ────────────────────────────────────────

// Subject must contain the Shopify order name and formatted amount.
func TestSend_SubjectContainsOrderName(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "test-app-password")

	restore, cap := stubSend(nil)
	defer restore()

	n := sampleNotification()
	_ = send(n)

	if !strings.Contains(cap.email.Subject, n.ShopifyOrderName) {
		t.Errorf("Subject %q missing order name %q", cap.email.Subject, n.ShopifyOrderName)
	}
	if !strings.Contains(cap.email.Subject, "179,000 VNĐ") {
		t.Errorf("Subject %q missing formatted amount", cap.email.Subject)
	}
}

// ── TestSend_HTMLBodyContainsFields ──────────────────────────────────────────

// The HTML body must contain all key order fields.
func TestSend_HTMLBodyContainsFields(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "test-app-password")

	restore, cap := stubSend(nil)
	defer restore()

	n := sampleNotification()
	_ = send(n)

	body := string(cap.email.HTML)
	wantSubstrings := []string{
		n.BuyerName,
		n.BuyerEmail,
		n.BuyerPhone,
		n.ShippingAddress,
		n.ShopifyOrderName,
		"179,000 VNĐ",
		n.Reference,
		n.PaymentLinkID,
		"Áo thun nam",
		"Variant #44965664260279",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(body, want) {
			t.Errorf("HTML body missing %q", want)
		}
	}
}

// ── TestSend_SMTPError ────────────────────────────────────────────────────────

// When sendFn returns an error, send() must propagate it.
func TestSend_SMTPError(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "test-app-password")

	want := errors.New("smtp: connection refused")
	restore, _ := stubSend(want)
	defer restore()

	err := send(sampleNotification())
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
}

// ── TestFormatVND ─────────────────────────────────────────────────────────────

func TestFormatVND(t *testing.T) {
	cases := []struct {
		amount int64
		want   string
	}{
		{0, "0 VNĐ"},
		{999, "999 VNĐ"},
		{1000, "1,000 VNĐ"},
		{179000, "179,000 VNĐ"},
		{1000000, "1,000,000 VNĐ"},
		{12345678, "12,345,678 VNĐ"},
	}

	for _, c := range cases {
		got := formatVND(c.amount)
		if got != c.want {
			t.Errorf("formatVND(%d) = %q, want %q", c.amount, got, c.want)
		}
	}
}

// ── TestBuildHTML_ContainsAllFields ───────────────────────────────────────────

func TestBuildHTML_ContainsAllFields(t *testing.T) {
	n := sampleNotification()
	html, err := buildHTML(n)
	if err != nil {
		t.Fatalf("buildHTML error: %v", err)
	}

	checks := []string{
		n.BuyerName,
		n.BuyerEmail,
		n.BuyerPhone,
		n.ShippingAddress,
		n.ShopifyOrderName,
		"179,000 VNĐ",
		n.Reference,
		n.PaymentLinkID,
		n.TransactionDatetime[:19],
		"987654321",
		"Áo thun nam",
		"Variant #44965664260279",
	}

	for _, want := range checks {
		if !strings.Contains(html, want) {
			t.Errorf("HTML missing %q", want)
		}
	}
}

// ── TestBuildHTML_NoPhoneOrAddress ────────────────────────────────────────────

func TestBuildHTML_NoPhoneOrAddress(t *testing.T) {
	n := sampleNotification()
	n.BuyerPhone = ""
	n.ShippingAddress = ""

	html, err := buildHTML(n)
	if err != nil {
		t.Fatalf("buildHTML error: %v", err)
	}

	count := strings.Count(html, "—")
	if count < 2 {
		t.Errorf("expected at least 2 '—' fallbacks, got %d", count)
	}
}

// ── TestSend_NoLineItems ──────────────────────────────────────────────────────

func TestSend_NoLineItems(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "test-app-password")

	restore, _ := stubSend(nil)
	defer restore()

	n := sampleNotification()
	n.LineItems = nil

	if err := send(n); err != nil {
		t.Fatalf("unexpected error with empty line items: %v", err)
	}
}

// ── TestSend_RealEmail ────────────────────────────────────────────────────────

// Integration test: actually sends an email via Gmail SMTP.
// Run manually: SMTP_PASSWORD=xxxx go test -run TestSend_RealEmail -v ./internal/mailer/
func TestSend_RealEmail(t *testing.T) {
	t.Setenv("SMTP_PASSWORD", "")
	if os.Getenv("SMTP_PASSWORD") == "" {
		t.Skip("SMTP_PASSWORD not set — skipping real send test")
	}
	if err := send(sampleNotification()); err != nil {
		t.Fatalf("send failed: %v", err)
	}
}
