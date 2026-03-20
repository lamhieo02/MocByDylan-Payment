package handlers

import (
	"errors"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
)

func ValidateRequestDataWebhook(payload *kv.CartPayload) error {
	if payload.Amount == 0 {
		return errors.New("amount is required")
	}

	if payload.BuyerPhone == "" {
		return errors.New("buyerPhone is required")
	}

	if payload.ShippingAddress == "" {
		return errors.New("shippingAddress is required")
	}

	if len(payload.LineItems) == 0 {
		return errors.New("lineItems is required")
	}

	return nil
}
