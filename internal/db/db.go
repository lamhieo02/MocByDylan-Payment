// Package db manages PostgreSQL persistence for orders (trace by payment_link_id,
// customer info, line items, shipping — for fulfillment and support).
//
// Required env: DATABASE_URL. If unset, all operations are no-op (nil error).
package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

var pool *pgxpool.Pool

func init() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Println("db: DATABASE_URL not set — order persistence disabled")
		return
	}

	var err error
	pool, err = pgxpool.New(context.Background(), dsn)
	if err != nil {
		log.Printf("db: failed to create pool: %v", err)
		return
	}
	if err := pool.Ping(context.Background()); err != nil {
		log.Printf("db: ping failed: %v", err)
		pool = nil
		return
	}
	log.Println("db: connected to PostgreSQL")

	if err := migrate(); err != nil {
		log.Printf("db: migration failed: %v", err)
	}
}

func migrate() error {
	ctx := context.Background()
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS orders (
			id                  SERIAL PRIMARY KEY,
			payment_link_id     TEXT UNIQUE NOT NULL,
			order_code          BIGINT,
			shopify_order_id    BIGINT,
			shopify_order_name  TEXT,
			status              TEXT        NOT NULL DEFAULT 'pending',
			amount              BIGINT      NOT NULL,
			currency            TEXT        NOT NULL DEFAULT 'VND',
			description         TEXT,
			buyer_name          TEXT,
			buyer_email         TEXT,
			buyer_phone         TEXT,
			shipping_address    TEXT,
			line_items          JSONB,
			payos_reference     TEXT,
			payos_tx_datetime   TEXT,
			note                TEXT,
			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return fmt.Errorf("create orders table: %w", err)
	}
	// Older deployments: add column if missing
	_, _ = pool.Exec(ctx, `ALTER TABLE orders ADD COLUMN IF NOT EXISTS description TEXT`)
	log.Println("db: schema up-to-date")
	return nil
}

// OrderRecord is written when a PayOS payment link is created (status=pending).
type OrderRecord struct {
	PaymentLinkID   string
	OrderCode       int64
	Amount          int64
	Description     string // PayOS payment description (short)
	BuyerName       string
	BuyerEmail      string
	BuyerPhone      string
	ShippingAddress string
	LineItems       interface{} // JSON-serialisable (e.g. []kv.LineItem)
}

// SaveOrder inserts a pending row keyed by payment_link_id (idempotent on conflict).
func SaveOrder(rec OrderRecord) error {
	if pool == nil {
		return nil
	}

	lineItemsJSON, err := json.Marshal(rec.LineItems)
	if err != nil {
		return fmt.Errorf("db: marshal line_items: %w", err)
	}

	_, err = pool.Exec(context.Background(), `
		INSERT INTO orders (
			payment_link_id, order_code, amount, description,
			buyer_name, buyer_email, buyer_phone,
			shipping_address, line_items,
			status, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'pending',NOW(),NOW())
		ON CONFLICT (payment_link_id) DO NOTHING
	`,
		rec.PaymentLinkID,
		rec.OrderCode,
		rec.Amount,
		nullIfEmpty(rec.Description),
		nullIfEmpty(rec.BuyerName),
		nullIfEmpty(rec.BuyerEmail),
		nullIfEmpty(rec.BuyerPhone),
		nullIfEmpty(rec.ShippingAddress),
		lineItemsJSON,
	)
	if err != nil {
		return fmt.Errorf("db: insert order: %w", err)
	}
	return nil
}

func nullIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

// UpdateOrderPaid sets status=paid and PayOS settlement fields.
// Shopify fields: use orderID 0 and empty name when CreateOrder was skipped/failed.
// If note is non-empty, it is stored (e.g. Shopify error for ops); empty string leaves note unchanged.
func UpdateOrderPaid(paymentLinkID string, shopifyOrderID int64, shopifyOrderName, payosReference, payosTxDatetime, note string) error {
	if pool == nil {
		return nil
	}

	_, err := pool.Exec(context.Background(), `
		UPDATE orders SET
			status             = 'paid',
			shopify_order_id   = NULLIF($2::bigint, 0),
			shopify_order_name = NULLIF($3, ''),
			payos_reference    = $4,
			payos_tx_datetime  = $5,
			note               = COALESCE(NULLIF($6, ''), note),
			updated_at         = NOW()
		WHERE payment_link_id = $1
	`,
		paymentLinkID,
		shopifyOrderID,
		shopifyOrderName,
		payosReference,
		payosTxDatetime,
		note,
	)
	if err != nil {
		return fmt.Errorf("db: update order paid: %w", err)
	}
	return nil
}

// UpdateOrderFailed sets status=failed and stores the error in note.
func UpdateOrderFailed(paymentLinkID, errMsg string) error {
	if pool == nil {
		return nil
	}

	_, err := pool.Exec(context.Background(), `
		UPDATE orders SET
			status     = 'failed',
			note       = $2,
			updated_at = NOW()
		WHERE payment_link_id = $1
	`, paymentLinkID, errMsg)
	if err != nil {
		return fmt.Errorf("db: update order failed: %w", err)
	}
	return nil
}

// Ping checks DB connectivity (health). Fails if DATABASE_URL is unset.
func Ping(ctx context.Context) error {
	if pool == nil {
		return fmt.Errorf("db: not connected (DATABASE_URL not set)")
	}
	return pool.Ping(ctx)
}
