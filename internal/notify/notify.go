// Package notify sends order-event notifications to Discord.
// Call Init once on startup with the channel's webhook URL, then call
// SendOrderNotify for every confirmed PayOS payment. All sends are done in a
// background goroutine; on transient failures the send is retried up to
// maxRetries times with a fixed retryBackoff between attempts.
package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/pkg/discord"
)

const (
	maxRetries   = 10
	retryBackoff = 2 * time.Second
	orderColor   = "#0099FF"
)

var _notifier discord.INotifier

// Init creates the package-level Discord notifier for the Notify Order channel.
// webhookURL is the Discord webhook URL for "MocByDylan Notify Order".
// Call once from main before serving requests.
func Init(webhookURL string) {
	if webhookURL == "" {
		fmt.Println("[notify] DISCORD_NOTIFY_ORDER_WEBHOOK not set; Discord order notifications disabled")
		return
	}
	_notifier = discord.New(discord.Config{
		Username: "MocByDylan Order",
		Channels: map[discord.ChannelName]string{
			discord.ChannelNotifyOrder: webhookURL,
		},
	})
	fmt.Println("[notify] Discord order notifier initialised")
}

// OrderInfo holds every piece of data available after a successful PayOS payment.
type OrderInfo struct {
	// PayOS payment data
	OrderCode              int64
	Amount                 int64
	PaymentLinkID          string
	Reference              string
	TransactionDateTime    string
	AccountNumber          string
	CounterAccountBankName string
	CounterAccountName     string
	CounterAccountNumber   string

	// Cart payload (buyer info from Redis)
	BuyerName       string
	BuyerEmail      string
	BuyerPhone      string
	ShippingAddress string
	LineItems       []kv.LineItem

	// Shopify result
	ShopifyOrderID   int64
	ShopifyOrderName string
	ShopifyErr       string // non-empty when CreateOrder failed
}

// SendOrderNotify fires a Discord notification in the background.
// It retries up to maxRetries times with retryBackoff between each attempt.
func SendOrderNotify(info OrderInfo) {
	if _notifier == nil {
		return
	}

	notifier := _notifier // capture for goroutine
	go func() {
		title := buildTitle(info)
		fields := buildFields(info)

		var lastErr error
		for attempt := 1; attempt <= maxRetries; attempt++ {
			err := notifier.Send(discord.ChannelNotifyOrder, orderColor, title, fields...)
			if err == nil {
				return
			}
			lastErr = err
			fmt.Printf("[notify] discord send attempt %d/%d failed: %v\n", attempt, maxRetries, err)
			if attempt < maxRetries {
				time.Sleep(retryBackoff)
			}
		}
		fmt.Printf("[notify] discord SendOrderNotify gave up after %d retries: %v\n", maxRetries, lastErr)
	}()
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func buildTitle(info OrderInfo) string {
	label := info.ShopifyOrderName
	if label == "" {
		label = fmt.Sprintf("OrderCode #%d", info.OrderCode)
	}
	return fmt.Sprintf("🎉 Đơn hàng mới | %s | %s VND", label, fmtAmount(info.Amount))
}

func buildFields(info OrderInfo) []discord.EmbedFields {
	var fields []discord.EmbedFields

	add := func(name, value string, inline bool) {
		if strings.TrimSpace(value) != "" {
			fields = append(fields, discord.EmbedFields{Name: name, Value: value, Inline: inline})
		}
	}

	// ── Payment ──────────────────────────────────────────────────────────
	add("💰 Số tiền", fmtAmount(info.Amount)+" VND", true)
	add("📅 Thời gian TT", info.TransactionDateTime, true)
	add("🔖 Reference", "`"+info.Reference+"`", true)
	add("🆔 PaymentLinkID", "`"+info.PaymentLinkID+"`", false)

	// ── Customer ─────────────────────────────────────────────────────────
	add("👤 Khách hàng", info.BuyerName, true)
	add("📧 Email", info.BuyerEmail, true)
	add("📱 SĐT", info.BuyerPhone, true)
	add("🏠 Địa chỉ giao hàng", info.ShippingAddress, false)

	// ── Bank transfer details ─────────────────────────────────────────────
	if info.CounterAccountBankName != "" || info.CounterAccountNumber != "" {
		add("🏦 Ngân hàng", info.CounterAccountBankName, true)
		add("🔢 Số TK chuyển", info.CounterAccountNumber, true)
		add("🪪 Chủ TK", info.CounterAccountName, true)
	}

	// ── Line items ────────────────────────────────────────────────────────
	if len(info.LineItems) > 0 {
		add("🛒 Sản phẩm", fmtLineItems(info.LineItems), false)
	}

	// ── Shopify result ────────────────────────────────────────────────────
	switch {
	case info.ShopifyOrderName != "":
		add("🏪 Shopify Order", fmt.Sprintf("✅ %s (ID: `%d`)", info.ShopifyOrderName, info.ShopifyOrderID), false)
	case info.ShopifyErr != "":
		add("🏪 Shopify Order", "❌ "+info.ShopifyErr, false)
	}

	return fields
}

func fmtAmount(amount int64) string {
	s := fmt.Sprintf("%d", amount)
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	mod := n % 3
	if mod > 0 {
		b.WriteString(s[:mod])
	}
	for i := mod; i < n; i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

func fmtLineItems(items []kv.LineItem) string {
	var b strings.Builder
	for _, it := range items {
		priceVND := it.Price / 100
		b.WriteString(fmt.Sprintf("• %s × %d — %sđ\n", it.Title, it.Quantity, fmtAmount(priceVND)))
	}
	return strings.TrimRight(b.String(), "\n")
}
