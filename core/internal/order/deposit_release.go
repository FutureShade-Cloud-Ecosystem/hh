package order

// deposit_release.go, the deposit release trigger: when the special order a
// supplier deposit secures is delivered, the order asks billing to release the
// order's live deposit documents (billing emits one deposit.released event per
// document), and the GL posting consumer (the owner of every financial topic)
// posts the release, DR 2300 Customer Deposits / CR revenue, idempotent per
// document.
//
// A FACT OF DELIVERY, NEVER A STATUS INFERENCE. The release fires only from
// AdvanceFromDelivery, the explicit road the fulfilment roads take: the
// dispatch handoff after the order's last live stop is delivered, the will
// call collection, and the pos ring's order.fulfilled observer. The plain
// event writer (AdvanceFromEvent) never releases: a configurable task rule can
// reach it, and a rule that moves an INVOICED order to FULFILLED is not the
// goods leaving. The delivery is also recorded on the order (MarkDelivered),
// so the billing sweep has a fact to find that does not depend on the status.
//
// THE POS RING'S BEAT. An on-account sale's own invoice finalises at the ring,
// and its ar.invoice.finalized event advances the order to INVOICED before the
// goods leave. The yard's delivery is then declined by the forward-only
// ladder, exactly the case dispatch's handoff comment names ("a POS ring beat
// us to it"). The delivery is still a fact, so it records and releases when
// the decline finds the order at INVOICED. A CANCELLED or CLOSED decline never
// does: the deposit of an order that will not be fulfilled is the reversal
// follow up's money, not revenue.
//
// A PARTIAL FULFILMENT RELEASES NOTHING. The roads fire only on the whole
// order (the dispatch handoff checks every live stop delivered before it
// calls; a will call collection is the whole pickup).
//
// THE POSTURE. The release runs beside the status move, never inside it, the
// completeCounterInvoiced posture: the hand over has committed and must not be
// failed by what follows. A failure is LOGGED with the order id and the event
// id, never raised, and it is never lost: the delivery record stays, and the
// billing sweep (billing.ReleaseDeliveredDeposits, run by the nightly
// reconciliation job) finds a delivered order whose deposit document has no
// release journal and releases it. The ledger's journal key keeps every path
// exactly once.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// DepositReleaser is billing's seam: it reads the order's live SUPPLIER_DEPOSIT
// documents and emits one deposit.released event for each, continuing past a
// failed document. Wired in the composition root; nil means no release.
type DepositReleaser interface {
	ReleaseDeposits(ctx context.Context, orderID string) error
}

// WithDepositReleaser injects the billing seam the delivery road calls.
func (s *Service) WithDepositReleaser(r DepositReleaser) *Service { s.depositReleaser = r; return s }

// deliveryFactStore is the capability the delivery record needs from the
// store: write the fact on the order's own row, and read it back for a batch.
// Satisfied by the real *Repository; a store without it records nothing.
type deliveryFactStore interface {
	MarkDelivered(ctx context.Context, orderID string) error
	DeliveredOrderIDs(ctx context.Context, orderIDs []string) ([]string, error)
}

// AdvanceFromDelivery is the fulfilment roads' door: the goods have left. It
// moves the order to FULFILLED through the guarded event writer, then records
// the delivery and asks billing to release the order's deposits. The result is
// AdvanceFromEvent's, unchanged: ErrNoTransition for a redelivery,
// ErrIllegalTransition for a move the ladder declines.
func (s *Service) AdvanceFromDelivery(ctx context.Context, orderID, eventID string) error {
	err := s.AdvanceFromEvent(ctx, orderID, "FULFILLED", eventID)
	switch {
	case err == nil || errors.Is(err, ErrNoTransition):
		_ = s.fulfilOrderOnDelivery(ctx, orderID)
		s.recordDeliveryAndRelease(ctx, orderID, eventID) // a redelivery reaches it too, the healing path
	case errors.Is(err, ErrIllegalTransition) && s.orderSitsAtInvoiced(ctx, orderID):
		// The ring's invoice beat the yard to INVOICED; the goods still left.
		_ = s.fulfilOrderOnDelivery(ctx, orderID)
		s.recordDeliveryAndRelease(ctx, orderID, eventID)
	}
	return err
}

// DeliveredOrderIDs answers which of the orders had a delivery recorded, the
// billing sweep's read of the order module's own fact.
func (s *Service) DeliveredOrderIDs(ctx context.Context, orderIDs []string) ([]string, error) {
	ds, ok := s.store.(deliveryFactStore)
	if !ok {
		return nil, nil
	}
	return ds.DeliveredOrderIDs(ctx, orderIDs)
}

// recordDeliveryAndRelease writes the delivery fact, then asks billing to
// release. Each failure is logged and never raised, see the file header.
func (s *Service) recordDeliveryAndRelease(ctx context.Context, orderID, eventID string) {
	if ds, ok := s.store.(deliveryFactStore); ok {
		if err := ds.MarkDelivered(ctx, orderID); err != nil {
			slog.Default().Warn("order lifecycle: deposit release: delivery record failed",
				"order_id", orderID, "event_id", eventID, "err", err)
		}
	}
	if s.depositReleaser == nil {
		return
	}
	if err := s.depositReleaser.ReleaseDeposits(ctx, orderID); err != nil {
		slog.Default().Warn("order lifecycle: deposit release: release failed, the sweep will retry",
			"order_id", orderID, "event_id", eventID, "err", err)
	}
}

// orderSitsAtInvoiced reports whether the order's current status is INVOICED,
// the beat the pos ring's invoice event writes before the goods leave. Used to
// tell that decline apart from CANCELLED/CLOSED, which must not release.
func (s *Service) orderSitsAtInvoiced(ctx context.Context, orderID string) bool {
	status, _, err := s.store.OrderHeader(ctx, orderID)
	if err != nil {
		return false // an unreadable header does not release; the decline stands as the answer
	}
	return status == "INVOICED"
}

// - the repository half -

// compile-time proof the real repository satisfies the delivery fact lane.
var _ deliveryFactStore = (*Repository)(nil)

// MarkDelivered stamps the delivery on the order's own row (orders.metadata).
// Idempotent: the first stamp keeps its time.
func (r *Repository) MarkDelivered(ctx context.Context, orderID string) error {
	if r.pool == nil {
		return errNoDB
	}
	if !looksLikeUUID(orderID) {
		return ErrNotFound
	}
	if _, err := r.pool.Exec(ctx, `
		UPDATE orders
		   SET metadata = metadata || jsonb_build_object('delivered_at', now()::text)
		 WHERE id = $1 AND NOT (metadata ? 'delivered_at')`, orderID); err != nil {
		return fmt.Errorf("order: mark delivered %s: %w", orderID, err)
	}
	return nil
}

// DeliveredOrderIDs lists, of the given orders, those with a delivery stamp.
func (r *Repository) DeliveredOrderIDs(ctx context.Context, orderIDs []string) ([]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if len(orderIDs) == 0 {
		return nil, nil
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text FROM orders
		 WHERE id = ANY($1::uuid[]) AND metadata ? 'delivered_at'
		 ORDER BY id`, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("order: delivered orders: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("order: scan delivered order: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
