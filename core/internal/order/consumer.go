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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// InvoiceOrderResolver maps a finalized invoice to its owning order. The ar.invoice.finalized payload
// carries only invoice_id, so the INVOICED driver resolves the order through this narrow seam
// (satisfied by *billing.Repository in the composition root). Owning the interface here means the order
// module never imports billing or reads the invoices table itself (module boundary: order does not read
// invoices). A missing/unbound invoice yields "" and the driver no-ops.
type InvoiceOrderResolver interface {
	OrderIDForInvoice(ctx context.Context, invoiceID string) (orderID string, err error)
}

// counterInvoiceSource is the "source" an ar.invoice.finalized payload carries when the invoice was
// raised at the counter for an order that has not left the yard (QD-5). It spells billing's
// EventSourceCounter: the order module never imports billing, and a test pins the two equal.
const counterInvoiceSource = "COUNTER"

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
	// The ring's fulfilment is a fact of delivery: the explicit road, which releases the deposits.
	return c.settle(e, p.OrderID, "FULFILLED", c.svc.AdvanceFromDelivery(ctx, p.OrderID, e.EventID))
}

// onInvoiceFinalized advances the order to INVOICED when its invoice finalizes (ar.invoice.finalized).
func (c *LifecycleConsumer) onInvoiceFinalized(ctx context.Context, e notify.Event) error {
	if c.invoices == nil {
		return nil // INVOICED driver not wired (nil resolver), nothing to do
	}
	var p struct {
		InvoiceID string `json:"invoice_id"`
		Source    string `json:"source"`
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
	// Item 9 Opus P2-2, the INVOICED driver's own ruling (the supervisor's
	// default, pending the principal's answer): an order that owes under a
	// pay before release shape stays where it is when its invoice finalizes
	// (CONFIRMED, in process) instead of jumping to INVOICED past the whole
	// ladder; once paid in full the order moves on as any order does, through
	// the normal doors, which all carry the hold. The status writer's own
	// guard (holdGuardedTarget) refuses the move anyway; this skip is the
	// decision itself, kept here so it can be reverted alone.
	if h := c.svc.payHoldView(ctx, orderID, ""); h != nil && h.HeldFromPicking {
		c.info("invoice finalized on an order that owes, it stays where it is until the balance is paid", e, orderID)
		return nil
	}
	if p.Source == counterInvoiceSource {
		// QD-5: an invoice raised at the counter bills goods that may still be in the yard. It moves
		// the order only when the goods have already left (FULFILLED); otherwise the order stays
		// where the yard has it, with its holds, and the fulfilment road completes the move.
		return c.settle(e, orderID, "INVOICED", c.svc.AdvanceCounterInvoiced(ctx, orderID, e.EventID))
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
	return c.settle(e, orderID, target, c.svc.AdvanceFromEvent(ctx, orderID, target, e.EventID))
}

// settle grades the outcome of one guarded move the way advance's header describes.
func (c *LifecycleConsumer) settle(e notify.Event, orderID, target string, err error) error {
	switch {
	case err == nil, errors.Is(err, ErrNoTransition):
		return nil
	case errors.Is(err, ErrHeldFromPicking):
		// Item 9 Opus P2-2: the order owes money under a pay before release
		// shape, so it stays where it is until the balance is paid. Not an
		// error: a non-owning observer must never fail or retry a financial
		// row on the hold, and once paid the order moves on as any order
		// does, through the normal doors (which all carry the hold).
		c.info("held, stayed put on the "+target+" advance until the balance is paid", e, orderID)
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

// - the counter's invoice (QD-5) -

// AdvanceCounterInvoiced is the move an invoice raised at the counter makes: FULFILLED to INVOICED
// and nothing else. An order still DRAFT, CONFIRMED, PICKING or READY is not moved (the goods are in
// the yard and the holds stay), which the guarded write answers as ErrIllegalTransition, a logged
// no-op for the consumer. The holds are not touched here either: they were consumed at FULFILLED, and
// the INVOICED backstop finds none ACTIVE.
func (s *Service) AdvanceCounterInvoiced(ctx context.Context, orderID, eventID string) error {
	if orderID == "" {
		return httpx.BadRequestf("order id is required")
	}
	return s.setStatusGuarded(ctx, "", orderID, "INVOICED", eventID, []string{"FULFILLED"}, nil)
}

// counterInvoiceStore is the store capability the fulfilment completion needs: whether the order
// already has an invoice raised at the counter. A store that cannot answer completes nothing.
type counterInvoiceStore interface {
	OrderHasCounterInvoice(ctx context.Context, orderID string) (bool, error)
}

// completeCounterInvoiced is the fulfilment road finding the counter's invoice already there: the
// order has just reached FULFILLED (its holds consumed once, in that move), and an invoice raised at
// the counter before the goods left is the very document that would have moved it on, so the move
// is completed here and no second invoice is raised. Best effort by design: the hand over has
// committed and must not be failed by what follows; an order left FULFILLED beside its paid invoice
// is the same state any order is in before its invoice event arrives.
//
// A failure is never silent: a lookup or a move that fails is logged with the order id and the event
// id, so an order left FULFILLED beside its paid invoice can be found. The completion is idempotent (a
// guarded FULFILLED to INVOICED move, a benign no-op once the order is past it), which is why
// AdvanceFromEvent runs it again when a redelivered hand over finds the order already FULFILLED.
func (s *Service) completeCounterInvoiced(ctx context.Context, orderID, eventID string) {
	cs, ok := s.store.(counterInvoiceStore)
	if !ok {
		return
	}
	has, err := cs.OrderHasCounterInvoice(ctx, orderID)
	if err != nil {
		slog.Default().Warn("order lifecycle: counter invoice completion: lookup failed",
			"order_id", orderID, "event_id", eventID, "err", err)
		return
	}
	if !has {
		return
	}
	if err := s.AdvanceCounterInvoiced(ctx, orderID, eventID); err != nil &&
		!errors.Is(err, ErrNoTransition) && !errors.Is(err, ErrIllegalTransition) {
		slog.Default().Warn("order lifecycle: counter invoice completion: move to INVOICED failed",
			"order_id", orderID, "event_id", eventID, "err", err)
	}
}
