// cancel_seam.go, B2/R2: the reads the manual CANCELLED transition needs in order to PROVE that no
// money is attached to an order before it may be cancelled.
//
// Audit H2: a manual cancel from CONFIRMED orphaned money state on a transacted POS sale, the invoice,
// payment, stock move and GL journals all stood while the order flipped to CANCELLED, with no link to
// the returns flow. The supervisor ruling is that CANCELLED is permitted only when the order has NO
// finalized invoice and NO committed pos_sale; anything else must go through the POS returns flow.
//
// The order module owns these interfaces but reads NEITHER table itself (module boundary: order does not
// read invoices or pos_sale). They are satisfied structurally in the composition root by
// *billing.Repository and *pos.Repository, the same shape as InvoiceOrderResolver (consumer.go) ←
// *billing.Repository. Two separate interfaces rather than one, because the two facts live in two
// different modules and neither repository could satisfy a combined interface alone.
//
// FAIL CLOSED (ruled): the predicate's job is to PROVE the absence of money. A nil reader cannot prove
// it, so a cancel ATTEMPT is refused with ErrCancelUnverifiable (503) naming the unwired seam, never a
// silent allow, and never a generic 500. main.go additionally asserts both are wired at boot, so this is
// unreachable in a correct deploy. Note the offer side is softer: see canOfferCancel.
package order

import "context"

// FinalizedInvoiceReader reports whether an order has an invoice that has left DRAFT, i.e. one whose
// finalize transaction has already run (FINALIZED / PARTIALLY_PAID / PAID). A VOID invoice does NOT
// count: it has been unwound, so it is not money still attached to the order.
type FinalizedInvoiceReader interface {
	OrderHasFinalizedInvoice(ctx context.Context, orderID string) (bool, error)
}

// CommittedSaleReader reports whether an order has a COMMITTED pos_sale. A VOIDED sale does not count.
type CommittedSaleReader interface {
	OrderHasCommittedPOSSale(ctx context.Context, orderID string) (bool, error)
}

// WithCancelGuards injects the two cancel-predicate readers (billingRepo + posRepo in main.go).
func (s *Service) WithCancelGuards(inv FinalizedInvoiceReader, sales CommittedSaleReader) *Service {
	s.cancelInvoices = inv
	s.cancelSales = sales
	return s
}

// CancelGuardsWired reports whether both cancel-predicate readers are present. main.go asserts this at
// boot (fail fast) so a dropped wiring can never reach runtime in a deploy.
func (s *Service) CancelGuardsWired() bool {
	return s.cancelInvoices != nil && s.cancelSales != nil
}

// assertCancellable proves no money is attached, or explains why it cannot.
//
//	nil seam            → ErrCancelUnverifiable (503), cannot prove, so refuse
//	finalized invoice   → ErrCancelBlocked       (409), go through the POS returns flow
//	committed pos_sale  → ErrCancelBlocked       (409)
//	read error          → surfaced as-is; an unreadable predicate is not an implicit allow
func (s *Service) assertCancellable(ctx context.Context, orderID string) error {
	if !s.CancelGuardsWired() {
		return ErrCancelUnverifiable
	}
	hasInvoice, err := s.cancelInvoices.OrderHasFinalizedInvoice(ctx, orderID)
	if err != nil {
		return err
	}
	if hasInvoice {
		return ErrCancelBlocked
	}
	hasSale, err := s.cancelSales.OrderHasCommittedPOSSale(ctx, orderID)
	if err != nil {
		return err
	}
	if hasSale {
		return ErrCancelBlocked
	}
	return nil
}

// canOfferCancel reports whether CANCELLED should be OFFERED in legal_actions (handler.go's order read).
//
// This is the softer, offer-side counterpart to assertCancellable (supervisor C2): an unwired seam, a
// blocked order, or a failed read all mean "do not offer the button", but NONE of them may break the
// order view, the read still returns 200 with CANCELLED simply absent. The 503 belongs exclusively to
// an actual cancel attempt.
func (s *Service) canOfferCancel(ctx context.Context, orderID string) bool {
	return s.assertCancellable(ctx, orderID) == nil
}
