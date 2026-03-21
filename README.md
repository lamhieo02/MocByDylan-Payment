# payos-backend

Go serverless backend for **Mộc by Dylan** PayOS payment integration on Vercel.

## Architecture

```
Shopify Cart (payos-qr.liquid)
  │
  ├─ POST /api/create-payment ──► PayOS API (/v2/payment-requests)
  │   └─ stores cartPayload in Vercel KV (TTL 20 min)
  │
  ├─ GET  /api/payment-status ──► PayOS API (/v2/payment-requests/{id})
  │   └─ polled every 4s by the QR modal
  │
  └─ POST /api/webhook (PayOS ──► this server)
      ├─ verify HMAC_SHA256 signature
      ├─ idempotency check (KV)
      ├─ lookup cartPayload from KV
      └─ POST /admin/api/.../orders.json (Shopify CreateOrder, optional — lỗi vẫn OK, DB là nguồn truth)
```

## Environment variables

Copy `.env.example` to `.env` and fill in all values before deploying.

| Variable | Where to find it |
|---|---|
| `PAYOS_CLIENT_ID` | my.payos.vn → Kênh thanh toán → API |
| `PAYOS_API_KEY` | my.payos.vn → Kênh thanh toán → API |
| `PAYOS_CHECKSUM_KEY` | my.payos.vn → Kênh thanh toán → API |
| `SHOPIFY_STORE_DOMAIN` | e.g. `mocbydylan.myshopify.com` (no `https://`) |
| `SHOPIFY_ADMIN_API_TOKEN` | Shopify Admin → Settings → Apps → Develop apps → API credentials |
| `KV_REST_API_URL` | Vercel project → Storage → KV → `.env.local` |
| `KV_REST_API_TOKEN` | Vercel project → Storage → KV → `.env.local` |
| `RESEND_API_KEY` | [Resend](https://resend.com) → API Keys (HTTPS). **Ưu tiên** nếu có. |
| `SMTP_HOST` / `SMTP_PORT` | Fallback SMTP: hostname (vd. `smtp.gmail.com`, `smtp.sendgrid.net`), **không dùng IP** |
| `SMTP_USER` / `SMTP_PASSWORD` | Gmail: user = full email + [App Password](https://myaccount.google.com/apppasswords); port `587` hoặc `465` |
| `MAIL_FROM` / `MAIL_TO` | Tuỳ chọn From/To (Resend hoặc SMTP) |
| `DATABASE_URL` | PostgreSQL (Railway, …) — lưu đơn để trace & giao hàng |

## Orders trong PostgreSQL (`DATABASE_URL`)

Sau **create-payment**: insert `pending` với `payment_link_id`, khách, địa chỉ, `line_items` (JSON), số tiền.  
Sau **webhook** thanh toán OK + Shopify: `paid` + `shopify_order_id` / `shopify_order_name` + PayOS ref.  
Lỗi Shopify: `failed` + `note`.

Ví dụ tra cứu để giao hàng:

```sql
SELECT payment_link_id, status, buyer_name, buyer_email, buyer_phone,
       shipping_address, line_items, amount, shopify_order_name, payos_reference, updated_at
FROM orders
WHERE payment_link_id = '...'
ORDER BY updated_at DESC LIMIT 20;
```

## Shopify app setup

1. Go to **Settings → Apps → Develop apps** in your Shopify admin.
2. Create a custom app named "PayOS Backend".
3. Under **Admin API access scopes**, enable:
   - `write_orders` — create orders
   - `read_products` — validate variants
4. Install the app and copy the **Admin API access token** → `SHOPIFY_ADMIN_API_TOKEN`.

## Vercel KV setup

1. In your Vercel project dashboard, go to **Storage → Create Database → KV**.
2. After creation, go to the **`.env.local`** tab to copy `KV_REST_API_URL` and `KV_REST_API_TOKEN`.
3. Add both to your Vercel project environment variables.

## Deploy to Vercel

```bash
# Install Vercel CLI (first time only)
npm i -g vercel

# Deploy
vercel --prod
```

After deploy, your webhook URL will be:
`https://your-project.vercel.app/api/webhook`

## Register the PayOS webhook

Run this once after deploying:

```bash
PAYOS_CLIENT_ID=xxx PAYOS_API_KEY=xxx \
  go run ./cmd/register-webhook/main.go https://your-project.vercel.app/api/webhook
```

## Shopify Admin API — Scopes required

The custom app needs `write_orders` scope. The order is created with:
- `financial_status: "paid"`
- A `transactions` entry with `gateway: "payos"` and `authorization: {paymentLinkId}`
- `send_receipt: true` — Shopify automatically sends the customer confirmation email.

## Local development

```bash
cp .env.example .env
# fill in .env values

# Run with vercel dev (requires Vercel CLI)
vercel dev
```

## Payment flow

1. Customer fills cart on Shopify storefront.
2. Clicks "Thanh toán QR / Chuyển khoản".
3. Frontend calls `POST /api/create-payment` with cart data.
4. Backend creates a PayOS payment link and stores `paymentLinkId → cartPayload` in KV.
5. Frontend displays the EMV QR code (rendered by `qrcode.js`).
6. Customer scans with any Vietnamese banking app.
7. PayOS sends `POST /api/webhook` after bank confirms transfer.
8. Backend verifies signature, checks idempotency, creates Shopify order.
9. Frontend polling detects `PAID` → redirects to `/pages/payment-result?status=success`.
10. Shopify sends order confirmation email to customer.
