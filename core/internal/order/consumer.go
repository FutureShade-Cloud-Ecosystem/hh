// consumer.go, DIB-191 event-driven order-status drivers. Two NON-OWNING observers advance
// orders.status off the ERP's existing domain topics, which the GL posting consumer already OWNS:
//
//	order.fulfilled       → FULFILLED   (payload carries order_id;  emitted by pos on a COLLECT_NOW ring)
//	ar.invoice.finalized  → INVOICED    (payload carries invoice_id; resolved to order_id via a seam)
//
// They register with dispatcher.RegisterObserver (NOT Register) because those topics already have a
// primary owner (gl). Observer semantics fit a status advance exactly: they run only AFTER the GL owner
// settles the row, and an error is swallowed by the dispatcher, a status blip can never
// fail/retry/dead-letter a financial row. This mirrors the messaging EVENT-post observer on the same
// topics (notify.Dispatcher.RegisterObserver, "the sanctioned thin-relay seam"). The orders table is a
// display projection here, never the money source of truth (PRR P2).
//
// ORDER-INDEPENDENCE + IDEMPOTENCY (B2/R1, PRR P5). Both drivers advance through
// Service.AdvanceFromEvent, which grades every move against the forward-only ladder (statusRank) INSIDE
// the guarded write. Consequences:
//
//   - A redelivered event whose order is still at this driver's target is a no-op (ErrNoTransition), so
//     no second timeline row is written.
//   - An event that arrives AFTER a later driver already advanced the order past its target is a logged
//     no-op (ErrIllegalTransition) rather than a backward write.
//   - An event arriving on a CANCELLED or CLOSED order is a logged no-op, no resurrection.
//
// This is what makes a POS cash sale DETERMINISTIC. pos emits order.fulfilled and ar.invoice.finalized in
// ONE transaction, so both outbox rows share an identical occurred_at (now() is the transaction
// timestamp) and the dispatcher claims `ORDER BY occurred_at` with no tiebreaker, the drain order is
// genuinely arbitrary. Under the ladder both orders converge on INVOICED: fulfilled-then-invoiced walks
// CONFIRMED→FULFILLED→INVOICED, while invoiced-then-fulfilled walks CONFIRMED→INVOICED and then no-ops
// the FULFILLED event. Before this guard the same sale could settle FULFILLED or INVOICED at random
// (audit H1).
//
// The source outbox event id rides in the timeline row's reason for traceability.
package order

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

// InvoiceOrderResolver maps a finalized invoice to its owning order. The ar.invoice.finalized payload
// carries only invoice_id, so the INVOICED driver resolves the order through this narrow seam
// (satisfied by *billing.Repository in the composition root). Owning the interface here means the order
// module never imports billing or reads the invoices table itself (module boundary: order does not read
// invoices). A missing/unbound invoice yields "" and the driver no-ops.
type InvoiceOrderResolver interface {
	OrderIDForInvoice(ctx context.Context, invoiceID string) (orderID string, err error)
}

// observerRegistrar is the subset of *notify.Dispatcher the consumer needs (observer registration only),
// kept as an interface so the order package depends on the seam, not the dispatcher's whole surface, and
// so tests can drive RegisterHandlers with a fake.
type observerRegistrar interface {
	RegisterObserver(topic string, h notify.Handler)
}

// LifecycleConsumer drives event-sourced FULFILLED/INVOICED transitions through the order Service.
type LifecycleConsumer struct {
	svc      *Service
	invoices InvoiceOrderResolver
	log      *slog.Logger
}

// NewLifecycleConsumer wires the consumer. A nil invoices resolver disables ONLY the INVOICED driver
// (FULFILLED still works), so a partial wiring degrades gracefully rather than panicking.
func NewLifecycleConsumer(svc *Service, invoices InvoiceOrderResolver, log *slog.Logger) *LifecycleConsumer {
	return &LifecycleConsumer{svc: svc, invoices: invoices, log: log}
}

// RegisterHandlers attaches the two drivers as observers. MUST be called AFTER the GL posting consumer
// registers its primary handlers for these topics, or the dispatcher panics at Run (an observer with no
// primary owner can never fire, validateObservers enforces this at boot).
func (c *LifecycleConsumer) RegisterHandlers(d observerRegistrar) {
	d.RegisterObserver("order.fulfilled", c.onFulfilled)
	d.RegisterObserver("ar.invoice.finalized", c.onInvoiceFinalized)
}

// onFulfilled advances the order to FULFILLED when its POS ring issues stock (order.fulfilled).
func (c *LifecycleConsumer) onFulfilled(ctx context.Context, e notify.Event) error {
	var p struct {
		OrderID string `json:"order_id"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		c.warn("order.fulfilled: bad payload", e, err)
		return nil // a malformed payload is not retryable; drop (the owner already settled the row)
	}
	if p.OrderID == "" {
		return nil
	}
	return c.advance(ctx, e, p.OrderID, "FULFILLED")
}

// onInvoiceFinalized advances the order to INVOICED when its invoice finalizes (ar.invoice.finalized).
func (c *LifecycleConsumer) onInvoiceFinalized(ctx context.Context, e notify.Event) error {
	if c.invoices == nil {
		return nil // INVOICED driver not wired (nil resolver), nothing to do
	}
	var p struct {
		InvoiceID string `json:"invoice_id"`
	}
	if err := json.Unmarshal(e.Payload, &p); err != nil {
		c.warn("ar.invoice.finalized: bad payload", e, err)
		return nil
	}
	if p.InvoiceID == "" {
		return nil
	}
	orderID, err := c.invoices.OrderIDForInvoice(ctx, p.InvoiceID)
	if err != nil {
		c.warn("ar.invoice.finalized: resolve order", e, err)
		return nil
	}
	if orderID == "" {
		return nil // invoice missing or not bound to an order, nothing to advance
	}
	return c.advance(ctx, e, orderID, "INVOICED")
}

// advance funnels the transition through AdvanceFromEvent, the STATE-GUARDED event writer (B2/R1) -
// as a SYSTEM actor (actor "" → NULL actor_id → renders as "system" on the timeline). The source outbox
// event id rides in the reason for traceability.
//
// Three outcomes are benign and swallowed to nil so a non-owning observer can never fail, retry, or
// dead-letter a financial row:
//
//	ErrNoTransition      the order is already at target, a redelivered event (idempotency, PRR P5)
//	ErrIllegalTransition the move is not forward: a later driver already advanced the order past this
//	                     event's target, or the order is CANCELLED/CLOSED. LOGGED, per R1, this is the
//	                     ordinary out-of-order-drain case for a POS sale, not an error.
//	ErrNotFound          the order is not visible (or was removed)
//
// Any other error is returned so the dispatcher logs it as a swallowed observer failure.
func (c *LifecycleConsumer) advance(ctx context.Context, e notify.Event, orderID, target string) error {
	err := c.svc.AdvanceFromEvent(ctx, orderID, target, e.EventID)
	switch {
	case err == nil, errors.Is(err, ErrNoTransition):
		return nil
	case errors.Is(err, ErrIllegalTransition):
		// Not a failure: the ladder refused a non-forward move. Logged so an unexpected pattern is
		// visible without polluting the error path.
		c.info("skipped non-forward advance to "+target, e, orderID)
		return nil
	case errors.Is(err, ErrNotFound):
		return nil
	default:
		c.warn("advance to "+target, e, err)
		return err
	}
}

func (c *LifecycleConsumer) info(msg string, e notify.Event, orderID string) {
	if c.log != nil {
		c.log.Info("order lifecycle: "+msg, "topic", e.Topic, "event_id", e.EventID, "order_id", orderID)
	}
}

func (c *LifecycleConsumer) warn(msg string, e notify.Event, err error) {
	if c.log != nil {
		c.log.Warn("order lifecycle: "+msg, "topic", e.Topic, "event_id", e.EventID, "err", err)
	}
}
