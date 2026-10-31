package payments

// discount.go, TM-2: the early payment discount at cash application.
//
// An invoice sold on discount terms (2/10 net 30 and its kin) carries the two facts the terms gave
// it when the writer raised it: the terms code it was dated by and the last house day its discount
// runs. A payment applied to that invoice ON OR BEFORE that day takes the discount automatically:
// the terms' own percent of the money the application lands, in integer cents rounded half to even,
// posted DR discounts allowed / CR 1100 beside the receipt, in the same transaction as the
// application, so the invoice closes at the discounted payoff and the aging and the ledger move
// together.
//
// THE TERMS ARE THE INVOICE'S OWN, never the customer's of today. The percent is read from the
// master row the invoice's STORED code names (through this module's DiscountTermsReader seam, which
// rides the caller's transaction), and the window is the invoice's STORED discount date. A customer
// whose terms changed after the document was raised keeps the document's own discount: re-dating an
// old invoice by today's terms would silently reprice a sale that already happened.
//
// The window is judged on the DEALER'S HOUSE DAY. The comparison is house day against house day, so
// an application made in the evening between the UTC day change and the house midnight is still on
// the dealer's yesterday, and a discount date that is still open on the dealer's calendar does not
// close because Greenwich rolled over.

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// DiscountTerms is the one fact of a terms row the discount needs: the rate in basis points (100
// is one percent, 0 is no discount).
type DiscountTerms struct{ DiscountBps int }

// DiscountTermsReader is the terms master read the discount runs on: the percent one terms code
// carries. The customer module owns the master, so the read crosses as this seam and never as SQL
// on its table. The transaction is the caller's own: an application holds its transaction while it
// asks, and the read rides it (a second pool connection beside a held one is the starvation shape
// QD5 removed); a nil transaction reads on the pool (the preview's lock free read).
type DiscountTermsReader interface {
	DiscountTermsByCode(ctx context.Context, tx pgx.Tx, code string) (DiscountTerms, error)
}

// WithDiscountTerms binds the terms read the discount runs on. Unbound, a discount terms invoice
// discounts nothing: the discount needs the percent, and a rate the module cannot ask for is a rate
// it must not guess. Production binds the customer module's master at every mount that applies
// money to invoices.
func (r *Repository) WithDiscountTerms(src DiscountTermsReader) *Repository {
	r.discountTerms = src
	return r
}

// WithZoneSource binds the dealer's business day zone source the discount window is judged in,
// read live at each application (a change of zone is live at the next window judgement). Unbound,
// the Ontario dealer's zone is the answer, which is what a repository built without one has always
// meant.
func (r *Repository) WithZoneSource(src houseday.Source) *Repository { r.zone = src; return r }

// WithDiscountClock pins the clock the window is judged on (production: the wall clock). The legs
// pin the last minute of a discount date and the first minute after the house midnight that
// follows it.
func (r *Repository) WithDiscountClock(now func() time.Time) *Repository {
	if now == nil {
		now = time.Now
	}
	r.discountClock = now
	return r
}

// nowClock answers the clock the discount window is judged on.
func (r *Repository) nowClock() time.Time {
	if r.discountClock == nil {
		return time.Now()
	}
	return r.discountClock()
}

// roundHalfEvenCents answers base*bps/10000 in integer cents, halves to the even cent. Money is
// integer cents everywhere; the half is the only place a rate meets a cent and it is resolved the
// accountant's way, not the schoolbook's.
func roundHalfEvenCents(base int64, bps int64) int64 {
	if base <= 0 || bps <= 0 {
		return 0
	}
	q, rem := base*bps/10000, base*bps%10000
	switch twice := rem * 2; {
	case twice > 10000:
		return q + 1
	case twice == 10000:
		if q%2 == 1 {
			return q + 1
		}
		return q
	default:
		return q
	}
}

// applicationDiscount is THE discount rule, one spelling for every door that applies money to an
// invoice:
//
//   - Not eligible (no discount date, no rate, the window closed, an NSF fee): zero.
//   - The money settles the invoice (applied at or past the discounted payoff, payable - full):
//     the exact remainder, payable - applied, which is at most the terms' full percent of the
//     payable. A 2/10 payoff of 98.00 on a 100.00 invoice closes it with a 2.00 discount, and a
//     desk that sends the odd 99.00 closes it with the 1.00 remainder rather than overshooting the
//     debt.
//   - A partial payment: the terms' percent of the money applied, half to even. The discount
//     follows the money, because the discount is the price of early money and only the money that
//     arrived inside the window earned it; the rest of the invoice keeps its face for the next
//     application.
func applicationDiscount(payable, applied int64, bps int, eligible bool) int64 {
	if !eligible || bps <= 0 || payable <= 0 || applied <= 0 {
		return 0
	}
	full := roundHalfEvenCents(payable, int64(bps))
	if applied >= payable-full {
		d := payable - applied
		if d < 0 {
			return 0
		}
		if d > full {
			return full
		}
		return d
	}
	d := roundHalfEvenCents(applied, int64(bps))
	// The discount never overshoots the debt: the balance move is the money and the discount
	// together, and a rounding edge on a tiny payable cannot take the balance below zero on a
	// payment that does not close the document.
	if applied+d > payable {
		d = payable - applied
	}
	return d
}

// discountContext is the discount facts one application resolves once, before its money moves: the
// dealer's house day the windows are judged on, the percent each terms code the named documents
// carry, and (when the desk asked) the manager's late discount approval. Built on the transaction
// the application holds and handed to the walk, the settle and the preview, so all three answer
// one question with one clock: a walk that judged the window on one tick of the clock and a settle
// that judged it on another could disagree across a house midnight.
type discountContext struct {
	today string
	bps   map[string]int
	late  *DiscountApproval
}

// DiscountApproval is a manager's approval to take an invoice's early payment discount OUTSIDE its
// window: the words the manager gave and the manager's own id, stamped by the route that checked
// the caller is one the manager gate admits. The approval is recorded on every invoice whose
// discount it let through (0472's invoice_discount_approvals) with the amount taken.
type DiscountApproval struct {
	ApprovedBy string
	Reason     string
	// Manager is the route's own judgement that the caller passes the manager gate (the same
	// predicate RequireManager runs, the write off route's tier). The walk never re-derives it.
	Manager bool
}

// windowOpen answers whether the house day of the application sits inside the document's own
// discount window.
func (c *discountContext) windowOpen(discountDay string) bool {
	return discountDay != "" && c.today <= discountDay
}

// eligible answers the discount terms and whether the discount may be taken at all on one
// document: inside its own window automatically, outside it only under a manager's approval with
// words. bps 0 means the document carries no discount terms (no code, no discount date, or a term
// that discounts nothing), and then nothing can discount it.
func (c *discountContext) eligible(discountDay, termsCode string) (bps int, allowed bool) {
	if discountDay == "" || termsCode == "" {
		return 0, false
	}
	rate := c.bps[termsCode]
	if rate <= 0 {
		return 0, false
	}
	if c.today <= discountDay {
		return rate, true
	}
	if c.late != nil && c.late.Manager && c.late.Reason != "" {
		return rate, true
	}
	return rate, false
}

// fullDiscountOn is the terms' own percent of the whole payable, half to even, when the document
// is eligible: the discount a payment that closes the document takes, and therefore the money the
// document needs to close (payable less it).
func (c *discountContext) fullDiscountOn(payable int64, discountDay, termsCode string) int64 {
	bps, allowed := c.eligible(discountDay, termsCode)
	if !allowed {
		return 0
	}
	return roundHalfEvenCents(payable, int64(bps))
}

// discountOn is applicationDiscount over one document's own facts, with the window and the late
// approval already folded in by eligible.
func (c *discountContext) discountOn(payable, applied int64, discountDay, termsCode string) int64 {
	bps, allowed := c.eligible(discountDay, termsCode)
	if !allowed {
		return 0
	}
	return applicationDiscount(payable, applied, bps, true)
}

// takesLate answers whether one document's discount, if any is taken at all, would ride the late
// approval rather than the window: the approval rows are written for exactly these.
func (c *discountContext) takesLate(discountDay, termsCode string) bool {
	if discountDay == "" || termsCode == "" || c.today <= discountDay {
		return false
	}
	if c.late == nil || !c.late.Manager || c.late.Reason == "" {
		return false
	}
	return c.bps[termsCode] > 0
}

// newDiscountContextTx resolves the context on the caller's transaction: the house day off the
// repository's zone and clock, and each distinct terms code's percent through the seam, read on
// the transaction the application holds. With no seam bound the context answers no rates, which
// discounts nothing: a percent the module cannot ask for is a percent it must not guess.
func (r *Repository) newDiscountContextTx(ctx context.Context, tx pgx.Tx, rows []applyInvoiceRow, late *DiscountApproval) (*discountContext, error) {
	// The dealer's own zone, read on the caller's transaction when there is one: the application
	// holds its connection, and a pool read beside it waits on a second one.
	zone, err := houseday.ResolveTx(ctx, r.zone, tx)
	if err != nil {
		return nil, err
	}
	c := &discountContext{
		today: zone.Today(r.nowClock),
		bps:   map[string]int{},
		late:  late,
	}
	if r.discountTerms == nil {
		return c, nil
	}
	seen := map[string]bool{}
	for _, row := range rows {
		if row.IsNSFFee || row.TermsCode == "" || row.DiscountDate == nil || seen[row.TermsCode] {
			continue
		}
		seen[row.TermsCode] = true
		t, err := r.discountTerms.DiscountTermsByCode(ctx, tx, row.TermsCode)
		if err != nil {
			return nil, fmt.Errorf("payments: read the discount rate of terms %q: %w", row.TermsCode, err)
		}
		c.bps[row.TermsCode] = t.DiscountBps
	}
	return c, nil
}

// discountDayOf answers one row's stored discount day as ISO text, "" when it carries none.
func discountDayOf(row applyInvoiceRow) string {
	if row.DiscountDate == nil || row.IsNSFFee {
		return ""
	}
	return row.DiscountDate.Format("2006-01-02")
}
