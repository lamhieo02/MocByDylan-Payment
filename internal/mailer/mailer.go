// Package mailer sends order notification emails.
//
// Priority:
//  1. RESEND_API_KEY — Resend HTTPS API (Railway-friendly, port 443).
//  2. SMTP_PASSWORD — SMTP (use provider hostname, never a raw IP — see Railway docs).
//
// SMTP env: SMTP_HOST (default smtp.gmail.com), SMTP_PORT (587 or 465),
// SMTP_USER (Gmail: full address; optional if parseable from MAIL_FROM <...>),
// SMTP_PASSWORD (app password). MAIL_FROM / MAIL_TO optional.
package mailer

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// resendAPIBase is the Resend API origin (override in tests via httptest).
var resendAPIBase = "https://api.resend.com"

// Defaults for Resend when MAIL_FROM / MAIL_TO unset.
const (
	defaultFromResend = "PayOS <onboarding@resend.dev>"
	defaultTo         = "lamfan011@gmail.com"
)

// Default SMTP target per provider docs (hostname only — do not use IP literals).
const (
	defaultSMTPHost = "smtp.gmail.com"
	defaultSMTPPort = "587" // or 465 for SSL
)

// mailHTTPClient is used for Resend API calls (replace in tests).
var mailHTTPClient = &http.Client{Timeout: 30 * time.Second}

// sendSMTPFunc performs SMTP delivery (replaced in tests).
var sendSMTPFunc = deliverSMTP

// LineItem is a single product in the order notification.
type LineItem struct {
	Title     string
	VariantID int64
	Quantity  int
	Price     int64 // VND
}

// Notification holds all data needed to compose the order email.
type Notification struct {
	ShopifyOrderName    string
	ShopifyOrderID      int64
	PaymentLinkID       string
	Reference           string
	TransactionDatetime string
	Amount              int64 // VND
	BuyerName           string
	BuyerEmail          string
	BuyerPhone          string
	ShippingAddress     string
	LineItems           []LineItem
}

// resendEmailPayload is the JSON body for POST https://api.resend.com/emails
type resendEmailPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// SendOrderNotification sends an HTML order summary email asynchronously.
func SendOrderNotification(n Notification) {
	go func() {
		if err := send(n); err != nil {
			log.Printf("mailer: failed to send notification for %s: %v", n.ShopifyOrderName, err)
			return
		}
		log.Printf("mailer: notification sent for %s (order_id=%d)", n.ShopifyOrderName, n.ShopifyOrderID)
	}()
}

func mailFromResend() string {
	if v := os.Getenv("MAIL_FROM"); v != "" {
		return v
	}
	return defaultFromResend
}

func mailTo() string {
	if v := os.Getenv("MAIL_TO"); v != "" {
		return v
	}
	return defaultTo
}

// mailFromSMTP returns the visible From header; defaults to PayOS <SMTP_USER> if MAIL_FROM unset.
func mailFromSMTP(smtpUser string) string {
	if v := os.Getenv("MAIL_FROM"); v != "" {
		return v
	}
	return fmt.Sprintf("PayOS <%s>", smtpUser)
}

func send(n Notification) error {
	htmlBody, err := buildHTML(n)
	if err != nil {
		return fmt.Errorf("build html: %w", err)
	}
	subject := fmt.Sprintf("[PayOS] Đơn hàng mới %s — %s", n.ShopifyOrderName, formatVND(n.Amount))

	if key := os.Getenv("RESEND_API_KEY"); key != "" {
		return sendResend(key, subject, htmlBody)
	}

	if pw := os.Getenv("SMTP_PASSWORD"); pw != "" {
		return sendSMTPFunc(subject, htmlBody, pw)
	}

	log.Println("mailer: neither RESEND_API_KEY nor SMTP_PASSWORD set — skipping email notification")
	return nil
}

func sendResend(apiKey, subject, htmlBody string) error {
	payload := resendEmailPayload{
		From:    mailFromResend(),
		To:      []string{mailTo()},
		Subject: subject,
		HTML:    htmlBody,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal resend payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, resendAPIBase+"/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := mailHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("resend http: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("resend: HTTP %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

func smtpHost() string {
	h := strings.TrimSpace(os.Getenv("SMTP_HOST"))
	if h == "" {
		return defaultSMTPHost
	}
	return h
}

func smtpPort() string {
	p := strings.TrimSpace(os.Getenv("SMTP_PORT"))
	if p == "" {
		return defaultSMTPPort
	}
	return p
}

// validateSMTPHost rejects IP literals so we always use DNS hostnames (Railway / provider guidance).
func validateSMTPHost(host string) error {
	if host == "" {
		return fmt.Errorf("empty SMTP host")
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil {
		return fmt.Errorf("mailer: use SMTP hostname (e.g. smtp.gmail.com, smtp.sendgrid.net), not IP %q", host)
	}
	return nil
}

// smtpAuthUser resolves the account for PlainAuth: SMTP_USER, or email inside MAIL_FROM <...>.
func smtpAuthUser() string {
	if u := strings.TrimSpace(os.Getenv("SMTP_USER")); u != "" {
		return u
	}
	mf := os.Getenv("MAIL_FROM")
	if i := strings.Index(mf, "<"); i >= 0 {
		if j := strings.Index(mf[i:], ">"); j > 0 {
			return strings.TrimSpace(mf[i+1 : i+j])
		}
	}
	return ""
}

func deliverSMTP(subject, htmlBody, password string) error {
	host := smtpHost()
	if err := validateSMTPHost(host); err != nil {
		return err
	}
	port := smtpPort()
	user := smtpAuthUser()
	if user == "" {
		return fmt.Errorf("mailer: set SMTP_USER or MAIL_FROM with angle brackets e.g. Name <you@gmail.com>")
	}

	fromHeader := mailFromSMTP(user)
	to := mailTo()
	msg := buildRFC822Message(fromHeader, to, subject, htmlBody)

	// Envelope sender: use auth user email for Gmail.
	auth := smtp.PlainAuth("", user, password, host)
	addr := net.JoinHostPort(host, port)

	switch port {
	case "465":
		return sendSMTP465(host, addr, auth, user, []string{to}, msg)
	default:
		return smtp.SendMail(addr, auth, user, []string{to}, msg)
	}
}

func buildRFC822Message(from, to, subject, html string) []byte {
	var b strings.Builder
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(html)
	return []byte(b.String())
}

// sendSMTP465 uses implicit TLS (SMTPS), e.g. smtp.gmail.com:465.
func sendSMTP465(host, addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	tlsCfg := &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("smtp tls dial: %w", err)
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer c.Close()

	if err := c.Auth(auth); err != nil {
		return fmt.Errorf("smtp auth: %w", err)
	}
	if err := c.Mail(from); err != nil {
		return fmt.Errorf("smtp mail: %w", err)
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}
	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// ── helpers ───────────────────────────────────────────────────────────────────

func formatVND(amount int64) string {
	s := fmt.Sprintf("%d", amount)
	n := len(s)
	var out bytes.Buffer
	for i, ch := range s {
		if i > 0 && (n-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(ch)
	}
	return out.String() + " VNĐ"
}

// ── HTML template ─────────────────────────────────────────────────────────────

const emailHTML = `<!DOCTYPE html>
<html lang="vi">
<head>
<meta charset="UTF-8">
<style>
  body { font-family: Arial, sans-serif; background: #f5f5f5; margin: 0; padding: 20px; }
  .card { background: #fff; border-radius: 8px; max-width: 600px; margin: 0 auto;
          box-shadow: 0 2px 8px rgba(0,0,0,.08); overflow: hidden; }
  .header { background: #1a73e8; color: #fff; padding: 24px 28px; }
  .header h1 { margin: 0; font-size: 20px; }
  .header p  { margin: 4px 0 0; font-size: 13px; opacity: .85; }
  .body { padding: 24px 28px; }
  h2 { font-size: 14px; color: #444; border-bottom: 1px solid #eee;
       padding-bottom: 6px; margin-bottom: 12px; text-transform: uppercase; letter-spacing: .5px; }
  table { width: 100%; border-collapse: collapse; margin-bottom: 20px; font-size: 14px; }
  th { text-align: left; color: #888; font-weight: normal; padding: 4px 0; }
  td { padding: 4px 0; color: #222; }
  td.val { font-weight: 500; }
  .items td, .items th { border-bottom: 1px solid #f0f0f0; padding: 8px 4px; }
  .items tr:last-child td { border-bottom: none; }
  .amount-row td { font-size: 18px; font-weight: bold; color: #1a73e8; padding-top: 12px; }
  .footer { background: #f9f9f9; padding: 14px 28px; font-size: 12px; color: #999;
            border-top: 1px solid #eee; }
</style>
</head>
<body>
<div class="card">
  <div class="header">
    <h1>💰 Thanh toán thành công</h1>
    <p>Đơn hàng Shopify <strong>{{.ShopifyOrderName}}</strong> đã được tạo</p>
  </div>
  <div class="body">

    <h2>Thông tin khách hàng</h2>
    <table>
      <tr><th width="40%">Họ tên</th>    <td class="val">{{.BuyerName}}</td></tr>
      <tr><th>Email</th>                  <td class="val">{{.BuyerEmail}}</td></tr>
      <tr><th>Số điện thoại</th>          <td class="val">{{if .BuyerPhone}}{{.BuyerPhone}}{{else}}—{{end}}</td></tr>
      <tr><th>Địa chỉ giao hàng</th>      <td class="val">{{if .ShippingAddress}}{{.ShippingAddress}}{{else}}—{{end}}</td></tr>
    </table>

    <h2>Sản phẩm</h2>
    <table class="items">
      <tr>
        <th>Sản phẩm</th>
        <th style="text-align:center">SL</th>
        <th style="text-align:right">Đơn giá</th>
      </tr>
      {{range .LineItems}}
      <tr>
        <td>{{if .Title}}{{.Title}}{{else}}Variant #{{.VariantID}}{{end}}</td>
        <td style="text-align:center">{{.Quantity}}</td>
        <td style="text-align:right">{{formatVND .Price}}</td>
      </tr>
      {{end}}
      <tr class="amount-row">
        <td colspan="2">Tổng thanh toán</td>
        <td style="text-align:right">{{formatVND .Amount}}</td>
      </tr>
    </table>

    <h2>Thông tin thanh toán</h2>
    <table>
      <tr><th width="40%">Shopify Order ID</th> <td class="val">{{.ShopifyOrderID}}</td></tr>
      <tr><th>PayOS Payment Link</th>            <td class="val">{{.PaymentLinkID}}</td></tr>
      <tr><th>Mã tham chiếu</th>                 <td class="val">{{.Reference}}</td></tr>
      <tr><th>Thời gian giao dịch</th>           <td class="val">{{.TransactionDatetime}}</td></tr>
    </table>

  </div>
  <div class="footer">Email tự động từ hệ thống PayOS – Shopify Integration</div>
</div>
</body>
</html>`

var emailTmpl = template.Must(
	template.New("order").Funcs(template.FuncMap{
		"formatVND": func(amount int64) string { return formatVND(amount) },
	}).Parse(emailHTML),
)

func buildHTML(n Notification) (string, error) {
	var buf bytes.Buffer
	if err := emailTmpl.Execute(&buf, n); err != nil {
		return "", err
	}
	return buf.String(), nil
}
