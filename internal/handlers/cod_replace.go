package handlers

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/db"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/kv"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/payos"
	"github.com/mocbydylan/shopify-mocbydylan-payos-payment/internal/shopify"
)

// When a buyer opens the QR modal and then switches to cash on delivery, the
// QR flow has already created a PayOS payment link and a Shopify draft order
// (tag payos). If both stay alive, a later bank transfer could complete the
// draft and produce a second order. qrReplacement cancels the link before the
// COD order is created and removes the draft afterwards.

// Seams for tests.
var (
	payosGetStatus   = payos.GetPaymentStatus
	payosCancel      = payos.CancelPayment
	kvMarkProcessed  = kv.TryMarkProcessed
	deleteDraftOrder = shopify.DeleteDraftOrder
	markQRReplaced   = func(paymentLinkID, note string) error { return db.UpdateOrderFailed(paymentLinkID, note) }
	lookupQRPayload  = defaultLookupQRPayload
)

var paymentLinkIDRe = regexp.MustCompile(`^[A-Za-z0-9_-]{6,64}$`)

type qrPayload struct {
	BuyerPhone     string
	DraftOrderID   int64
	DraftOrderName string
}

func defaultLookupQRPayload(paymentLinkID string) (*qrPayload, error) {
	if p, err := kv.GetCartPayload(paymentLinkID); err == nil && p != nil {
		return &qrPayload{BuyerPhone: p.BuyerPhone, DraftOrderID: p.DraftOrderID, DraftOrderName: p.DraftOrderName}, nil
	}
	rec, err := db.GetCartPayload(paymentLinkID)
	if err != nil || rec == nil {
		return nil, err
	}
	return &qrPayload{BuyerPhone: rec.BuyerPhone, DraftOrderID: rec.DraftOrderID, DraftOrderName: rec.DraftOrderName}, nil
}

type qrReplacement struct {
	PaymentLinkID  string
	DraftOrderID   int64
	DraftOrderName string
	Cancelled      bool
	Warning        string
}

// errQRAlreadyPaid is returned when the buyer has in fact paid the QR already.
var errQRAlreadyPaid = fmt.Errorf("qr already paid")

// prepareQRReplacement cancels the PayOS link the buyer is switching away from.
// It returns (nil, nil) when there is nothing to replace or the link does not
// belong to this buyer, and errQRAlreadyPaid when the QR was paid.
func prepareQRReplacement(paymentLinkID, phone string) (*qrReplacement, error) {
	paymentLinkID = strings.TrimSpace(paymentLinkID)
	if paymentLinkID == "" {
		return nil, nil
	}
	if !paymentLinkIDRe.MatchString(paymentLinkID) {
		log.Printf("[cod] replace: ignoring malformed paymentLinkId %q", paymentLinkID)
		return nil, nil
	}

	payload, err := lookupQRPayload(paymentLinkID)
	if err != nil || payload == nil {
		log.Printf("[cod] replace: no QR payload for %s (err=%v), creating COD without replacement", paymentLinkID, err)
		return nil, nil
	}
	if normalizeVNPhone(payload.BuyerPhone) != phone {
		log.Printf("[cod] replace: phone mismatch for %s, ignoring replacement", paymentLinkID)
		return nil, nil
	}

	rep := &qrReplacement{
		PaymentLinkID:  paymentLinkID,
		DraftOrderID:   payload.DraftOrderID,
		DraftOrderName: payload.DraftOrderName,
	}

	if st, err := payosGetStatus(paymentLinkID); err == nil && st != nil && st.Status == "PAID" {
		return nil, errQRAlreadyPaid
	}

	if err := payosCancel(paymentLinkID, "Khach doi sang COD"); err != nil {
		// Cancel can fail because the link is already cancelled/expired (fine)
		// or because it was just paid (must stop).
		st, sErr := payosGetStatus(paymentLinkID)
		switch {
		case sErr == nil && st != nil && st.Status == "PAID":
			return nil, errQRAlreadyPaid
		case sErr == nil && st != nil && (st.Status == "CANCELLED" || st.Status == "EXPIRED"):
			rep.Cancelled = true
		default:
			rep.Warning = "Chưa huỷ được link QR PayOS (" + err.Error() + "). Kiểm tra PayOS trước khi giao hàng."
			log.Printf("[cod] replace: cancel %s FAILED: %v", paymentLinkID, err)
		}
	} else {
		rep.Cancelled = true
	}

	if rep.Cancelled {
		// Make any late webhook for this link a no-op.
		if _, err := kvMarkProcessed(paymentLinkID); err != nil {
			log.Printf("[cod] replace: TryMarkProcessed %s: %v", paymentLinkID, err)
		}
	}
	return rep, nil
}

// noteLine is appended to the COD order note so staff can see what happened.
func (rep *qrReplacement) noteLine() string {
	if rep == nil {
		return ""
	}
	s := "\nThay cho đơn chờ chuyển khoản QR"
	if rep.DraftOrderName != "" {
		s += " " + rep.DraftOrderName
	}
	s += " (paymentLinkId " + rep.PaymentLinkID + ")."
	if rep.Warning != "" {
		s += "\n⚠️ " + rep.Warning
	}
	return s
}

// finalize removes the QR draft order once the COD order exists.
func (rep *qrReplacement) finalize(codOrderName string) {
	if rep == nil {
		return
	}
	if rep.Cancelled && rep.DraftOrderID > 0 {
		if err := deleteDraftOrder(rep.DraftOrderID); err != nil {
			log.Printf("[cod] replace: delete draft %d FAILED: %v", rep.DraftOrderID, err)
		} else {
			log.Printf("[cod] replace: deleted QR draft %s (%d) -> COD %s", rep.DraftOrderName, rep.DraftOrderID, codOrderName)
		}
	}
	if err := markQRReplaced(rep.PaymentLinkID, "Khách đổi sang COD – đơn "+codOrderName); err != nil {
		log.Printf("[cod] replace: db mark %s: %v", rep.PaymentLinkID, err)
	}
}
