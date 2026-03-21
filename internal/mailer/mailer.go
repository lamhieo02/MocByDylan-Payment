// Package mailer sends order notification emails via Gmail SMTP.
// Required env var: SMTP_PASSWORD (Gmail app password for the sender account).
// If SMTP_PASSWORD is not set, all sends are silently skipped.
package mailer

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/smtp"
	"os"

	gomail "github.com/jordan-wright/email"
)

const (
	smtpHost = "smtp.gmail.com"
	smtpPort = "587"
	fromAddr = "lamlklk2002@gmail.com"
	toAddr   = "lamfan011@gmail.com"
)

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

// sendFn is the underlying send implementation; replaced in tests to avoid real SMTP calls.
var sendFn = func(e *gomail.Email, addr string, a smtp.Auth) error {
	return e.Send(addr, a)
}

// SendOrderNotification sends an HTML order summary email asynchronously.
// Errors are logged but never returned so the webhook response is never blocked.
func SendOrderNotification(n Notification) {
	go func() {
		if err := send(n); err != nil {
			log.Printf("mailer: failed to send notification for %s: %v", n.ShopifyOrderName, err)
			return
		}
		log.Printf("mailer: notification sent for %s (order_id=%d)", n.ShopifyOrderName, n.ShopifyOrderID)
	}()
}

func send(n Notification) error {
	password := os.Getenv("SMTP_PASSWORD")
	if password == "" {
		log.Println("mailer: SMTP_PASSWORD not set — skipping email notification")
		return nil
	}

	htmlBody, err := buildHTML(n)
	if err != nil {
		return fmt.Errorf("build html: %w", err)
	}

	e := gomail.NewEmail()
	e.From = "PayOS Backend Notifier <" + fromAddr + ">"
	e.To = []string{toAddr}
	e.Subject = fmt.Sprintf("[PayOS] Đơn hàng mới %s — %s", n.ShopifyOrderName, formatVND(n.Amount))
	e.HTML = []byte(htmlBody)

	smptAuthAddress := smtpHost + ":" + smtpPort

	smtpAuth := smtp.PlainAuth("", fromAddr, password, smtpHost)
	return e.Send(smptAuthAddress, smtpAuth)

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
