// Package db manages the PostgreSQL connection and order persistence.
// // Required env var: DATABASE_URL (postgres connection string)
// // If DATABASE_URL is not set, all operations are silently skipped (no-op).
package db

// import (
// 	"context"
// 	"encoding/json"
// 	"fmt"
// 	"log"
// 	"os"

// 	"github.com/jackc/pgx/v5/pgxpool"
// )

// var pool *pgxpool.Pool

// func init() {
// 	dsn := os.Getenv("DATABASE_URL")
// 	if dsn == "" {
// 		log.Println("db: DATABASE_URL not set — order persistence disabled")
// 		return
// 	}

// 	var err error
// 	pool, err = pgxpool.New(context.Background(), dsn)
// 	if err != nil {
// 		log.Printf("db: failed to create pool: %v", err)
// 		return
// 	}
// 	if err := pool.Ping(context.Background()); err != nil {
// 		log.Printf("db: ping failed: %v", err)
// 		pool = nil
// 		return
// 	}
// 	log.Println("db: connected to PostgreSQL")

// 	if err := migrate(); err != nil {
// 		log.Printf("db: migration failed: %v", err)
// 	}
// }

// // migrate creates the orders table if it does not already exist.
// func migrate() error {
// 	_, err := pool.Exec(context.Background(), `
// 		CREATE TABLE IF NOT EXISTS orders (
// 			id                  SERIAL PRIMARY KEY,
// 			payment_link_id     TEXT UNIQUE NOT NULL,
// 			order_code          BIGINT,
// 			shopify_order_id    BIGINT,
// 			shopify_order_name  TEXT,
// 			status              TEXT        NOT NULL DEFAULT 'pending',
// 			amount              BIGINT      NOT NULL,
// 			currency            TEXT        NOT NULL DEFAULT 'VND',
// 			buyer_name          TEXT,
// 			buyer_email         TEXT,
// 			buyer_phone         TEXT,
// 			shipping_address    TEXT,
// 			line_items          JSONB,
// 			payos_reference     TEXT,
// 			payos_tx_datetime   TEXT,
// 			note                TEXT,
// 			created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
// 			updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
// 		)
// 	`)
// 	if err != nil {
// 		return fmt.Errorf("create orders table: %w", err)
// 	}
// 	log.Println("db: schema up-to-date")
// 	return nil
// }

// // OrderRecord holds the data needed to insert a new pending order.
// type OrderRecord struct {
// 	PaymentLinkID   string
// 	OrderCode       int64
// 	Amount          int64
// 	BuyerName       string
// 	BuyerEmail      string
// 	BuyerPhone      string
// 	ShippingAddress string
// 	LineItems       interface{} // any JSON-serialisable value
// }

// // SaveOrder inserts a new order with status=pending.
// // If the payment_link_id already exists it is silently ignored (idempotent).
// // Returns nil when DATABASE_URL is not configured.
// func SaveOrder(rec OrderRecord) error {
// 	if pool == nil {
// 		return nil
// 	}

// 	lineItemsJSON, err := json.Marshal(rec.LineItems)
// 	if err != nil {
// 		return fmt.Errorf("db: marshal line_items: %w", err)
// 	}

// 	_, err = pool.Exec(context.Background(), `
// 		INSERT INTO orders (
// 			payment_link_id, order_code, amount,
// 			buyer_name, buyer_email, buyer_phone,
// 			shipping_address, line_items,
// 			status, created_at, updated_at
// 		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'pending',NOW(),NOW())
// 		ON CONFLICT (payment_link_id) DO NOTHING
// 	`,
// 		rec.PaymentLinkID,
// 		rec.OrderCode,
// 		rec.Amount,
// 		rec.BuyerName,
// 		rec.BuyerEmail,
// 		rec.BuyerPhone,
// 		rec.ShippingAddress,
// 		string(lineItemsJSON),
// 	)
// 	if err != nil {
// 		return fmt.Errorf("db: insert order: %w", err)
// 	}
// 	return nil
// }

// // UpdateOrderPaid marks an order as paid and stores Shopify + PayOS details.
// // Returns nil when DATABASE_URL is not configured.
// func UpdateOrderPaid(paymentLinkID string, shopifyOrderID int64, shopifyOrderName, payosReference, payosTxDatetime string) error {
// 	if pool == nil {
// 		return nil
// 	}

// 	_, err := pool.Exec(context.Background(), `
// 		UPDATE orders SET
// 			status             = 'paid',
// 			shopify_order_id   = $2,
// 			shopify_order_name = $3,
// 			payos_reference    = $4,
// 			payos_tx_datetime  = $5,
// 			updated_at         = NOW()
// 		WHERE payment_link_id = $1
// 	`,
// 		paymentLinkID,
// 		shopifyOrderID,
// 		shopifyOrderName,
// 		payosReference,
// 		payosTxDatetime,
// 	)
// 	if err != nil {
// 		return fmt.Errorf("db: update order paid: %w", err)
// 	}
// 	return nil
// }

// // UpdateOrderFailed marks an order as failed and stores the error message.
// // Returns nil when DATABASE_URL is not configured.
// func UpdateOrderFailed(paymentLinkID, errMsg string) error {
// 	if pool == nil {
// 		return nil
// 	}

// 	_, err := pool.Exec(context.Background(), `
// 		UPDATE orders SET
// 			status     = 'failed',
// 			note       = $2,
// 			updated_at = NOW()
// 		WHERE payment_link_id = $1
// 	`, paymentLinkID, errMsg)
// 	if err != nil {
// 		return fmt.Errorf("db: update order failed: %w", err)
// 	}
// 	return nil
// }

// // Ping checks database connectivity. Used by health checks.
// func Ping(ctx context.Context) error {
// 	if pool == nil {
// 		return fmt.Errorf("db: not connected (DATABASE_URL not set)")
// 	}
// 	return pool.Ping(ctx)
// }
