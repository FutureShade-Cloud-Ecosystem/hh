package order

// yard_pick.go, a short pick at the yard becomes a backorder on the order line (migration 0369).
//
// WHAT HAPPENS. The yard recorded fewer than the order line asked for. The line's quantity to
// fulfil drops to what was picked, the rest is held as a backorder on the same line, the line total
// is pro-rated at the agreed unit price (a short pick never reprices what was delivered), and the
// order's subtotal, tax and total follow. The invoice raised from the order (the agreed invoice, or
// the whole-order invoice) therefore bills what went out and never what did not.
//
// THE INVARIANT. Ordered = quantity to fulfil + backorder, always. The ordered values are frozen on
// order_line_shortfalls at the first short, so a correction (the yard finds the rest) restores the
// line and the money exactly, and a repeat of the same record is a no-op that reports Changed=false
// so nobody is told twice.
//
// WHAT THIS DOES NOT DO. It never raises a quantity above what was ordered (ErrAboveOrdered), never
// takes a line below what has already gone out (ErrLineInUse), and never moves money that has
// already been taken: an order with a live invoice keeps that invoice exactly as it is, and the
// outcome reports what the customer paid and what is now owed back. Issuing the credit is the
// office's act, not the yard's.
//
// The quantity unit is the order line's own (the ticket's own unit), the one the picker reads.
//
// NOTE FOR THE CLOSE PATH (a follow up, deliberately not built here). Today an order reaches
// FULFILLED, INVOICED and CLOSED with a backorder still on a line, and its reservation is released
// with it, so the owed goods would vanish from replenishment's sight. When the screens land, the close
// path should refuse (or demand an explicit decision for) an order with has_backorder: fulfil the
// backorder, cancel it with the credit it implies, or carry it onto a new order; and a backorder that
// is fulfilled later is a new pick against the line, which this model leaves room for (ordered =
// quantity + backorder) but does not yet do.

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// yardReasonOther is the reason code that needs a note. The code list itself belongs to the yard
// door (tasks); the order enforces only that a short has one and that OTHER explains itself.
const yardReasonOther = "OTHER"

// ErrAboveOrdered is a picked quantity above what the order line was ordered for. Mapped to 400.
var ErrAboveOrdered = errors.New("order: that is more than was ordered, the yard cannot raise a quantity")

// yardPickStatuses are the order statuses a short pick may land on: after confirmation and before
// the order is fulfilled. A line that has partly gone out is held by its own fulfilled quantity.
var yardPickStatuses = []string{"CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED"}

// YardPickInput is one recorded pick for one order.
type YardPickInput struct {
	OrderID string
	ActorID string
	Lines   []YardPickLineInput
}

// YardPickLineInput is one line: the picked quantity is a decimal string in the line's own unit.
type YardPickLineInput struct {
	LineNo    int
	QtyPicked string
	Reason    string
	Note      string
}

// YardPickOutcome is what changed, and the order's facts a notice needs.
type YardPickOutcome struct {
	Order YardOrderFacts
	Lines []YardLineOutcome
}

// YardLineOutcome is one line after the pick. Changed is false when the line already held exactly
// this record.
type YardLineOutcome struct {
	LineNo         int
	Description    string
	UOM            string
	QtyOrdered     string
	QtyPicked      string
	QtyBackordered string
	Changed        bool
	// Revision counts the changes of this line's shortfall (0 when it never was short), so a
	// notice's idempotency key tells one real change from a repeat of it.
	Revision int
	// Reason and Note are what the order holds for this line after the pick: the reason sent, or
	// the stored one a blank re-save keeps, empty on a full pick.
	Reason string
	Note   string
}

// YardOrderFacts is the order's side of a notice: whose order it is and how its money stands.
type YardOrderFacts struct {
	OrderID          string
	OrderNo          string
	CustomerID       string
	BranchID         string
	SalespersonID    string
	TotalBeforeCents int64
	TotalAfterCents  int64
	InvoiceNo        string
	InvoicedCents    int64
	PaidCents        int64
	// OwedBackCents is what the customer has paid beyond what the order now asks for; DueCents is
	// what the order now asks for beyond what the customer has paid, when something was paid.
	OwedBackCents int64
	DueCents      int64
}

// InvoiceStanding is an order's live invoice and how much of it is paid. Found is false when the
// order has no live invoice.
type InvoiceStanding struct {
	Found      bool
	InvoiceNo  string
	TotalCents int64
	PaidCents  int64
}

// InvoiceStandingReader is the consumer-declared billing port: the order module never reads the
// invoice tables itself.
type InvoiceStandingReader interface {
	InvoiceStanding(ctx context.Context, orderID string) (InvoiceStanding, error)
}

// WithInvoiceStanding injects the billing read the notice facts need. Without it the facts carry no
// invoice and nothing is reported as owed.
func (s *Service) WithInvoiceStanding(r InvoiceStandingReader) *Service { s.standing = r; return s }

// yardPickStore is the store capability this needs, asserted like the other capabilities.
type yardPickStore interface {
	// The precheck runs inside the write's transaction, after the order row
	// lock and the status window check, on the context's precheck tx (item 9
	// Opus P3-2): the hold read rides the write, not a pool read before it.
	ApplyYardPicksTx(ctx context.Context, in YardPickInput, precheck func(context.Context) error, audit yardPickAudit) (YardPickOutcome, error)
}

// ApplyYardPicks applies a recorded pick to the order. See the file header.
func (s *Service) ApplyYardPicks(ctx context.Context, in YardPickInput) (YardPickOutcome, error) {
	if strings.TrimSpace(in.OrderID) == "" || !looksLikeUUID(in.OrderID) {
		return YardPickOutcome{}, ErrNotFound
	}
	if len(in.Lines) == 0 {
		return YardPickOutcome{}, httpx.BadRequestf("say which lines were picked")
	}
	st, ok := s.store.(yardPickStore)
	if !ok {
		return YardPickOutcome{}, ErrEditNotConfigured
	}
	// Item 9.3: a pick releases goods, so the pay before release hold guards
	// it before any line is touched (pick_hold.go). Item 9 Opus P3-2: the read
	// rides the write's own transaction, under the order row lock its writer
	// takes, as the writer's precheck.
	hold := func(ctx context.Context) error { return s.refuseHeldFromPicking(ctx, in.OrderID) }
	out, err := st.ApplyYardPicksTx(ctx, in, hold, s.yardPickAudit(in.ActorID, in.OrderID))
	if err != nil {
		return YardPickOutcome{}, err
	}
	if s.standing != nil {
		stand, err := s.standing.InvoiceStanding(ctx, in.OrderID)
		if err != nil {
			return YardPickOutcome{}, fmt.Errorf("order: invoice standing for %s: %w", in.OrderID, err)
		}
		out.Order = withStanding(out.Order, stand)
	}
	return out, nil
}

// withStanding folds the live invoice into the facts and states what is owed either way.
func withStanding(f YardOrderFacts, st InvoiceStanding) YardOrderFacts {
	if !st.Found {
		return f
	}
	f.InvoiceNo, f.InvoicedCents, f.PaidCents = st.InvoiceNo, st.TotalCents, st.PaidCents
	if st.PaidCents > f.TotalAfterCents {
		f.OwedBackCents = st.PaidCents - f.TotalAfterCents
	}
	if st.PaidCents > 0 && f.TotalAfterCents > st.PaidCents {
		f.DueCents = f.TotalAfterCents - st.PaidCents
	}
	return f
}

// yardPickAudit is the audit hook a repository call runs on its own transaction.
type yardPickAudit func(ctx context.Context, tx pgx.Tx, before, after LineSnapshot, totals OrderTotals, taxBefore, taxAfter int64) error

func (s *Service) yardPickAudit(actor, orderID string) yardPickAudit {
	return func(ctx context.Context, tx pgx.Tx, before, after LineSnapshot, totals OrderTotals, taxBefore, taxAfter int64) error {
		if err := s.recordLineChange(ctx, tx, actor, orderID, FieldLineQty, before, after, totals); err != nil {
			return err
		}
		return s.recordIntChange(ctx, tx, actor, orderID, 0, "tax_cents", &taxBefore, &taxAfter)
	}
}

// - the pure money and quantity arithmetic -

func ratOf(s string) (*big.Rat, bool) { return new(big.Rat).SetString(strings.TrimSpace(s)) }

// scaleBase is ordered_base * picked / ordered_entered at four places, half up: the base quantity
// that matches a picked quantity in the line's own unit.
func scaleBase(orderedBase, picked, orderedEntered *big.Rat) *big.Rat {
	r := new(big.Rat).Mul(orderedBase, picked)
	r.Quo(r, orderedEntered)
	return roundRat4(r)
}

func roundRat4(r *big.Rat) *big.Rat {
	x := new(big.Rat).Mul(r, big.NewRat(10000, 1))
	x.Add(x, big.NewRat(1, 2))
	q := new(big.Int).Quo(x.Num(), x.Denom()) // x is never negative here, so truncation is floor
	return new(big.Rat).SetFrac(q, big.NewInt(10000))
}

func rat4(r *big.Rat) string { return r.FloatString(4) }

// proratedTotal is the line total for picked of ordered at the agreed price: exact at the ends, the
// largest-remainder pro-ration in between (the same prorateShares the plan surface uses).
func proratedTotal(orderedTotal int64, picked, ordered *big.Rat) int64 {
	switch picked.Cmp(ordered) {
	case 0:
		return orderedTotal
	}
	if picked.Sign() == 0 {
		return 0
	}
	p, _ := picked.Float64()
	o, _ := ordered.Float64()
	return prorateShares(orderedTotal, []float64{p, o - p})[0]
}

// proratedTax is the header tax for a new subtotal, always from the frozen originals.
func proratedTax(basisSubtotal, basisTax, subtotal int64) int64 {
	switch {
	case basisSubtotal <= 0 || subtotal >= basisSubtotal:
		return basisTax
	case subtotal <= 0:
		return 0
	}
	return prorateShares(basisTax, []float64{float64(subtotal), float64(basisSubtotal - subtotal)})[0]
}

type yardLineRow struct {
	id                  string
	lineNo              int
	description, uom    string
	qtyEntered, qtyBase string
	backEntered, backB  string
	total               int64
	fulfilled           string
}

func sortYardLines(ls []YardPickLineInput) []YardPickLineInput {
	out := append([]YardPickLineInput(nil), ls...)
	sort.Slice(out, func(i, j int) bool { return out[i].LineNo < out[j].LineNo })
	return out
}
