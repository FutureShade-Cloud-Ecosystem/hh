package order

// deposit_release.go, the deposit release emission (the item after 9.5): when
// the special order a supplier deposit secures is FULFILLED, the order tells
// the outbox, one deposit.released event per live deposit document, and the GL
// posting consumer (the owner of every financial topic) posts the release,
// DR 2300 Customer Deposits / CR revenue, idempotent per document.
//
// WHERE THE EMISSION SITS, AND WHY. Every fulfilment road in the tree funnels
// into Service.AdvanceFromEvent to FULFILLED: the dispatch handoff after the
// order's last live stop is delivered, the will-call collection, and the pos
// ring's order.fulfilled event through the lifecycle observer. That choke
// point is where the release fires.
//
// THE POS RING'S BEAT. An on-account sale's own invoice finalises at the ring,
// and its ar.invoice.finalized event advances the order to INVOICED before the
// goods leave (the consumer's INVOICED driver). The yard's MarkFulfilled is
// then declined by the forward-only ladder, exactly the case dispatch's
// handoff comment names ("a POS ring beat us to it"). The physical fact still
// stands, only the yard calls this seam once the material has left, so the
// release fires on that decline too, when the order sits at INVOICED. A
// CANCELLED or CLOSED order never releases: the deposit of an order that will
// not be fulfilled is the reversal follow up's money, not revenue.
//
// A PARTIAL FULFILMENT RELEASES NOTHING. The roads that reach this emission
// fire only on the whole order (the dispatch handoff checks every live stop
// delivered before it calls; a will-call collection is the whole pickup), and
// the house does not recognise revenue per shipment for a POS ring sale (its
// one invoice posts at the ring), so there is no per-shipment release to
// follow.
//
// THE POSTURE. The emission runs beside the status move, never inside it, the
// completeCounterInvoiced posture: the hand over has committed and must not be
// failed by what follows. A failure or an unreadable read is LOGGED with the
// order id and the event id, never raised, and a redelivered fulfilment
// re-reaches the emission (the healing path); the ledger's journal key is what
// keeps the release exactly once, the house's own dedupe for redelivered money
// events. A delivered order whose deposit never released is a queryable gap:
// FULFILLED or INVOICED with a live SUPPLIER_DEPOSIT document and no
// deposit.released journal.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// depositReleasedTopic is the outbox topic the emission writes and the GL
// posting consumer owns (module.go PostedTopics). A raw literal, the pos
// emitter's precedent: the join between the two literals is proven end to end
// by the order package's integration leg.
const depositReleasedTopic = "deposit.released"

// depositReleaseStore is the capability the emission needs from the store: the
// order's live deposit documents, and the outbox write. Satisfied by the real
// *Repository; a store without it (the unit fake unless a test adds the lane)
// simply emits nothing.
type depositReleaseStore interface {
	// DepositDocumentIDs lists the order's live SUPPLIER_DEPOSIT documents
	// (source_kind SUPPLIER_DEPOSIT, not DRAFT or VOID), the same predicate
	// the pos owing read carries.
	DepositDocumentIDs(ctx context.Context, orderID string) ([]string, error)
	// EmitEvent writes one outbox row on the store's own transaction.
	EmitEvent(ctx context.Context, topic string, payload any) error
}

// releaseSupplierDeposits tells the outbox the order's deposit documents
// released, once per document. Best effort and loudly logged, see the file
// header; never raises to the caller.
func (s *Service) releaseSupplierDeposits(ctx context.Context, orderID, eventID string) {
	rs, ok := s.store.(depositReleaseStore)
	if !ok {
		return
	}
	ids, err := rs.DepositDocumentIDs(ctx, orderID)
	if err != nil {
		slog.Default().Warn("order lifecycle: deposit release: document read failed",
			"order_id", orderID, "event_id", eventID, "err", err)
		return
	}
	for _, id := range ids {
		if err := rs.EmitEvent(ctx, depositReleasedTopic, map[string]any{
			"invoice_id": id, "order_id": orderID,
		}); err != nil {
			slog.Default().Warn("order lifecycle: deposit release: emit failed",
				"order_id", orderID, "event_id", eventID, "invoice_id", id, "err", err)
			return
		}
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

// compile-time proof the real repository satisfies the deposit release lane.
var _ depositReleaseStore = (*Repository)(nil)

// DepositDocumentIDs lists the order's live deposit documents. Reading the
// invoices table from here is the same narrow exception OrderHasCounterInvoice
// (invoice_source.go) already carries: rows this order's own sale wrote, keyed
// by the order, a lookup no invoice-shaped billing seam exists to answer.
func (r *Repository) DepositDocumentIDs(ctx context.Context, orderID string) ([]string, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text FROM invoices
		 WHERE order_id = $1 AND doc_type = 'INVOICE'
		   AND source_kind = 'SUPPLIER_DEPOSIT' AND status NOT IN ('DRAFT','VOID')
		 ORDER BY created_at, id`, orderID)
	if err != nil {
		return nil, fmt.Errorf("order: deposit documents for %s: %w", orderID, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("order: scan deposit document: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// EmitEvent writes one outbox row, the pos emitter's insert on the store's own
// transaction (events_outbox is the notify spine's shared table, not a peer
// module's domain table).
func (r *Repository) EmitEvent(ctx context.Context, topic string, payload any) error {
	if r.pool == nil {
		return errNoDB
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := r.pool.Exec(ctx, `INSERT INTO events_outbox (topic, payload) VALUES ($1, $2)`, topic, b); err != nil {
		return fmt.Errorf("order: emit %s: %w", topic, err)
	}
	return nil
}
