// lineage.go, DIB-491's related-documents read model: a quote's DOCUMENT FAMILY, as normalized rows.
//
// D-097 sets the bar as "staff can see where a document came from". The family is already in the
// data, orders.quote_id (0008) links an order to its quote, invoices.order_id (0009) links an
// invoice to its order, and quote_accept_provenance (0162) says which numbered acceptance minted
// each order and at which door. What was missing is ONE read that walks the keys and answers as a
// flat list a surface can render without joining anything client-side.
//
// DELIBERATELY FLAT. The rows carry parent_id, so a consumer that wants a tree can build one, but
// the shipped UI is a compact list (the ruling: data correctness + a clean list now; visual polish
// is a later phase), and a flat, parent-annotated shape serves both without committing to either.
//
// TWO ENTRY POINTS, ONE FAMILY. Asking from the quote and asking from any of its orders returns the
// SAME rows (modulo which row is marked `self`), because the family is a property of the documents,
// not of where you happened to be standing when you asked.
package quote

import (
	"context"
	"errors"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// Document kinds a family may contain. Constants, so a consumer switching on kind cannot be broken
// by a respelling.
const (
	DocKindQuote   = "QUOTE"
	DocKindOrder   = "ORDER"
	DocKindInvoice = "INVOICE"
)

// RelatedDocument is one normalized row of a document family.
type RelatedDocument struct {
	Kind       string    `json:"kind"` // QUOTE | ORDER | INVOICE
	ID         string    `json:"id"`
	Number     string    `json:"number"` // the human number, "#Q-…", "#SO-…", "#INV-…"
	Status     string    `json:"status"`
	Date       time.Time `json:"date"` // when the document came to exist (created_at)
	TotalCents int64     `json:"total_cents"`
	// ParentID is the lineage edge: an order's parent is its quote, an invoice's parent is its
	// order. Empty on the family root.
	ParentID string `json:"parent_id,omitempty"`
	// Origin/AcceptSeq are the DIB-491 provenance facts, present on ORDER rows minted since 0162:
	// which door (PORTAL/COUNTER) and which numbered acceptance (1 = the original, 2+ = repeats).
	// Zero-valued on orders that predate the provenance table, absent is honest there.
	Origin    string `json:"origin,omitempty"`
	AcceptSeq int    `json:"accept_seq,omitempty"`
	// Self marks the document the caller asked FROM, so the list can say "this document" instead of
	// offering a link to the page the reader is already on.
	Self bool `json:"self"`
}

// ErrLineageNotConfigured, the store predates the lineage read (a fake, or a partial deploy).
// 503-shaped, so an operator reports a deploy problem rather than a business one.
var ErrLineageNotConfigured = errors.New("quote: the related-documents read is not configured on this server")

// lineageReader is the persistence seam. *Repository satisfies it; a store that does not leaves the
// surface answering 503 rather than guessing.
type lineageReader interface {
	// QuoteFamily returns the full family rooted at quoteID. selfID marks which member the caller
	// asked from (the quote itself, or one of its orders).
	QuoteFamily(ctx context.Context, quoteID, selfID string) ([]RelatedDocument, error)
	// OrderOnlyFamily returns the family of an order with NO quote behind it (a POS sale, a portal
	// reorder): the order and its downstream documents.
	OrderOnlyFamily(ctx context.Context, orderID string) ([]RelatedDocument, error)
	// ResolveOrderForLineage resolves an order id or visible number ("#SO-…"), branch-scoped exactly
	// like ResolveQuoteID, and hands back the two facts the authority ladder needs.
	ResolveOrderForLineage(ctx context.Context, idOrNo string, global bool, branchIDs []string) (orderID, branchID, quoteID string, err error)
}

// RelatedDocumentsForQuote answers the family for a quote, id or visible number. The authority
// ladder is StaffQuoteRead's, verbatim: authenticated → active → resolve (branch-scoped, no
// existence oracle) → branch reach.
func (s *StaffService) RelatedDocumentsForQuote(ctx context.Context, idOrNo string) ([]RelatedDocument, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	rGlobal, rBranches := callerBranchScope(caller)
	quoteID, err := s.store.ResolveQuoteID(ctx, idOrNo, rGlobal, rBranches)
	if err != nil {
		return nil, err
	}
	_, branchID, _, err := s.store.QuoteEditHeader(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	if !caller.CanAccessBranch(branchID) {
		return nil, ErrForbidden
	}
	lr, ok := s.store.(lineageReader)
	if !ok {
		return nil, ErrLineageNotConfigured
	}
	return lr.QuoteFamily(ctx, quoteID, quoteID)
}

// RelatedDocumentsForOrder answers the same family, asked from an order. An order born of a quote
// answers the quote's WHOLE family (siblings included, that is the point when multi-accept has
// minted duplicates); an order with no quote answers itself + its downstream documents.
func (s *StaffService) RelatedDocumentsForOrder(ctx context.Context, idOrNo string) ([]RelatedDocument, error) {
	caller, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	if caller.Status != identity.StatusActive {
		return nil, ErrForbidden
	}
	lr, ok := s.store.(lineageReader)
	if !ok {
		return nil, ErrLineageNotConfigured
	}
	rGlobal, rBranches := callerBranchScope(caller)
	orderID, branchID, quoteID, err := lr.ResolveOrderForLineage(ctx, idOrNo, rGlobal, rBranches)
	if err != nil {
		return nil, err
	}
	if !caller.CanAccessBranch(branchID) {
		return nil, ErrForbidden
	}
	if quoteID == "" {
		return lr.OrderOnlyFamily(ctx, orderID)
	}
	return lr.QuoteFamily(ctx, quoteID, orderID)
}
