package mailer

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
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

// ── TestSend_NoConfig ─────────────────────────────────────────────────────────

func TestSend_NoConfig(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("SMTP_PASSWORD", "")
	if err := send(sampleNotification()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// ── TestSend_ResendSuccess ───────────────────────────────────────────────────

func TestSend_ResendSuccess(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("SMTP_PASSWORD", "") // Resend wins
	t.Setenv("MAIL_FROM", "")
	t.Setenv("MAIL_TO", "")

	var gotAuth string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"abc"}`))
	}))
	defer srv.Close()

	origClient := mailHTTPClient
	origBase := resendAPIBase
	resendAPIBase = srv.URL
	mailHTTPClient = srv.Client()
	mailHTTPClient.Timeout = 5 * time.Second
	defer func() {
		mailHTTPClient = origClient
		resendAPIBase = origBase
	}()

	if err := send(sampleNotification()); err != nil {
		t.Fatalf("send: %v", err)
	}
	if gotAuth != "Bearer re_test_key" {
		t.Errorf("Authorization = %q, want Bearer re_test_key", gotAuth)
	}

	var p resendEmailPayload
	if err := json.Unmarshal(gotBody, &p); err != nil {
		t.Fatalf("payload json: %v", err)
	}
	if p.From != defaultFromResend {
		t.Errorf("From = %q, want %q", p.From, defaultFromResend)
	}
	if len(p.To) != 1 || p.To[0] != defaultTo {
		t.Errorf("To = %v, want [%q]", p.To, defaultTo)
	}
	if !strings.Contains(p.Subject, "#D42") || !strings.Contains(p.Subject, "179,000 VNĐ") {
		t.Errorf("Subject = %q", p.Subject)
	}
	if !strings.Contains(p.HTML, "Lâm Nguyễn") {
		t.Error("HTML missing buyer name")
	}
}

// ── TestSend_ResendCustomFromTo ───────────────────────────────────────────────

func TestSend_ResendCustomFromTo(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_test_key")
	t.Setenv("SMTP_PASSWORD", "")
	t.Setenv("MAIL_FROM", "PayOS <notify@example.com>")
	t.Setenv("MAIL_TO", "other@example.com")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var p resendEmailPayload
		_ = json.Unmarshal(body, &p)
		if p.From != "PayOS <notify@example.com>" {
			t.Errorf("From = %q", p.From)
		}
		if len(p.To) != 1 || p.To[0] != "other@example.com" {
			t.Errorf("To = %v", p.To)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origClient := mailHTTPClient
	origBase := resendAPIBase
	resendAPIBase = srv.URL
	mailHTTPClient = srv.Client()
	defer func() {
		mailHTTPClient = origClient
		resendAPIBase = origBase
	}()

	if err := send(sampleNotification()); err != nil {
		t.Fatal(err)
	}
}

// ── TestSend_ResendAPIError ───────────────────────────────────────────────────

func TestSend_ResendAPIError(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "re_bad")
	t.Setenv("SMTP_PASSWORD", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Invalid API key"}`))
	}))
	defer srv.Close()

	origClient := mailHTTPClient
	origBase := resendAPIBase
	resendAPIBase = srv.URL
	mailHTTPClient = srv.Client()
	defer func() {
		mailHTTPClient = origClient
		resendAPIBase = origBase
	}()

	err := send(sampleNotification())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention 401: %v", err)
	}
}

// ── TestSend_SMTPFallbackMocked ─────────────────────────────────────────────

// Resend unset → SMTP path; sendSMTPFunc mocked.
func TestSend_SMTPFallbackMocked(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("SMTP_PASSWORD", "app-password")
	t.Setenv("SMTP_HOST", "smtp.gmail.com")
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USER", "sender@gmail.com")
	t.Setenv("MAIL_FROM", "")
	t.Setenv("MAIL_TO", "recv@example.com")

	var gotSubject, gotHTML string
	orig := sendSMTPFunc
	sendSMTPFunc = func(subject, html, _ string) error {
		gotSubject = subject
		gotHTML = html
		return nil
	}
	defer func() { sendSMTPFunc = orig }()

	if err := send(sampleNotification()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotSubject, "#D42") {
		t.Errorf("subject %q", gotSubject)
	}
	if !strings.Contains(gotHTML, "Lâm Nguyễn") {
		t.Error("missing html")
	}
}

// ── TestSend_SMTPRejectIPHost ─────────────────────────────────────────────────

func TestSend_SMTPRejectIPHost(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("SMTP_PASSWORD", "x")
	t.Setenv("SMTP_HOST", "64.233.170.109")
	t.Setenv("SMTP_USER", "a@b.com")

	err := send(sampleNotification())
	if err == nil {
		t.Fatal("expected error for IP SMTP host")
	}
	if !strings.Contains(err.Error(), "hostname") {
		t.Errorf("want hostname hint: %v", err)
	}
}

// ── TestSend_SMTPMissingUser ──────────────────────────────────────────────────

func TestSend_SMTPMissingUser(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("SMTP_PASSWORD", "x")
	t.Setenv("SMTP_HOST", "smtp.gmail.com")
	t.Setenv("SMTP_USER", "")
	t.Setenv("MAIL_FROM", "No Angle Brackets")

	err := send(sampleNotification())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "SMTP_USER") {
		t.Errorf("got %v", err)
	}
}

// ── TestValidateSMTPHost ─────────────────────────────────────────────────────

func TestValidateSMTPHost(t *testing.T) {
	for _, host := range []string{"smtp.gmail.com", "smtp.sendgrid.net"} {
		if err := validateSMTPHost(host); err != nil {
			t.Errorf("%q: %v", host, err)
		}
	}
	for _, host := range []string{"127.0.0.1", "::1", "192.168.1.1"} {
		if err := validateSMTPHost(host); err == nil {
			t.Errorf("%q should be rejected", host)
		}
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
	t.Setenv("RESEND_API_KEY", "re_x")
	t.Setenv("SMTP_PASSWORD", "")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	origClient := mailHTTPClient
	origBase := resendAPIBase
	resendAPIBase = srv.URL
	mailHTTPClient = srv.Client()
	defer func() {
		mailHTTPClient = origClient
		resendAPIBase = origBase
	}()

	n := sampleNotification()
	n.LineItems = nil
	if err := send(n); err != nil {
		t.Fatal(err)
	}
}

// ── TestSend_RealEmail_Resend ─────────────────────────────────────────────────

// Run: RESEND_API_KEY=re_xxx go test -run TestSend_RealEmail_Resend -v ./internal/mailer/
func TestSend_RealEmail_Resend(t *testing.T) {
	if os.Getenv("RESEND_API_KEY") == "" {
		t.Skip("RESEND_API_KEY not set — skipping real Resend test")
	}
	t.Setenv("SMTP_PASSWORD", "") // force Resend
	if err := send(sampleNotification()); err != nil {
		t.Fatalf("send failed: %v", err)
	}
}

// ── TestSend_RealEmail_SMTP ───────────────────────────────────────────────────

// Run: SMTP_PASSWORD=apppass SMTP_USER=you@gmail.com go test -run TestSend_RealEmail_SMTP -v ./internal/mailer/
func TestSend_RealEmail_SMTP(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	os.Setenv("SMTP_PASSWORD", "333")
	if os.Getenv("SMTP_PASSWORD") == "" {
		t.Skip("SMTP_PASSWORD not set — skipping real SMTP test")
	}
	// if os.Getenv("SMTP_USER") == "" && !strings.Contains(os.Getenv("MAIL_FROM"), "@") {
	// 	t.Skip("set SMTP_USER or MAIL_FROM with <email@...>")
	// }
	os.Setenv("SMTP_USER", "lamlklk2002@gmail.com")
	os.Setenv("MAIL_FROM", "PayOS Backend Notifier <lamlklk2002@gmail.com>")
	os.Setenv("MAIL_TO", "lamfan011@gmail.com")
	if err := send(sampleNotification()); err != nil {
		t.Fatalf("send failed: %v", err)
	}
}

// ── TestSendSMTPFuncErrorPropagates ───────────────────────────────────────────

func TestSendSMTPFuncErrorPropagates(t *testing.T) {
	t.Setenv("RESEND_API_KEY", "")
	t.Setenv("SMTP_PASSWORD", "x")
	t.Setenv("SMTP_USER", "a@b.com")

	want := errors.New("smtp down")
	orig := sendSMTPFunc
	sendSMTPFunc = func(_, _, _ string) error { return want }
	defer func() { sendSMTPFunc = orig }()

	err := send(sampleNotification())
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}
