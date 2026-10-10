package quote

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5"
)

// ErrConvertKeyReused: the key was already used for a different conversion (another fulfilment).
// Mapped to 409, in words. It never carries the first order's facts beyond what the refusal says.
var ErrConvertKeyReused = errors.New("quote: this idempotency key was used for a different conversion; use a new key for a new conversion")

// The key rides the QUOTE_CHANGED audit row the conversion writes on its OWN transaction (no migration,
// the same pattern as the acceptance key in share_idempotency.go): field status, before the standing
// status, after CONVERTED, the allowlisted client_id for the key and source_order_id for the order it
// made. The lookup runs under the quote row lock Convert takes, BEFORE the status checks, so two requests
// with one key serialize and the second always sees the first's committed row, and it answers the first
// order whatever the dealer's multi accept switch says (decision row 744 for accepts, FROM-PRINCIPAL 121
// for converts).

// findKeyedConversion returns the order an earlier conversion of this quote under the key made, or
// found=false. A found order whose fulfilment differs from the one asked for now is ErrConvertKeyReused.
// An unspecified fulfilment means the database default, PICKUP.
func (r *Repository) findKeyedConversion(ctx context.Context, tx pgx.Tx, quoteID, key, fulfillment string) (ConvertResult, bool, error) {
	var out ConvertResult
	var stored string
	err := tx.QueryRow(ctx, `
		SELECT o.id::text, o.order_no::text, o.fulfillment
		  FROM audit_log al
		  JOIN orders o ON o.id = (al.changes->>'source_order_id')::uuid
		 WHERE al.entity_type = 'quote' AND al.entity_id = $1::uuid AND al.action = $2
		   AND al.changes->>'client_id' = $3
		 ORDER BY al.seq
		 LIMIT 1`, quoteID, audit.ActQuoteChanged, key).Scan(&out.OrderID, &out.OrderNo, &stored)
	if errors.Is(err, pgx.ErrNoRows) {
		return ConvertResult{}, false, nil
	}
	if err != nil {
		return ConvertResult{}, false, fmt.Errorf("look up conversion by idempotency key: %w", err)
	}
	want := fulfillment
	if want == "" {
		want = FulfillPickup
	}
	if stored != want {
		return ConvertResult{}, false, ErrConvertKeyReused
	}
	out.replayed = true
	return out, true, nil
}

// recordKeyedConversion writes the key row on the conversion's transaction. A key with no audit trail
// to carry it could not be replayed safely, so that is an error, never a silent unkeyed conversion.
func recordKeyedConversion(ctx context.Context, tx pgx.Tx, opt ConvertOptions, quoteID, priorStatus string, res ConvertResult) error {
	if opt.keyAudit == nil {
		return errors.New("quote convert: an idempotency key needs the audit trail")
	}
	return opt.keyAudit.RecordTx(ctx, tx, audit.SensitiveAccess{
		Action: audit.ActQuoteChanged, EntityType: "quote", EntityID: quoteID,
		Purpose: "STAFF_QUOTE_CONVERT", Reason: "status",
		Extra: map[string]any{
			"field": "status", "before": priorStatus, "after": "CONVERTED",
			"client_id": opt.IdempotencyKey, "source_order_id": res.OrderID,
		},
	})
}
