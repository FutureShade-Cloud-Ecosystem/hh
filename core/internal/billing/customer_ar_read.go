package billing

// customer_ar_read.go, ONE account's AR position and ONE account's recent AR movements, as reads a
// consumer outside this module can call.
//
// -----------------------------------------------------------------------------------------------
// THE ARITHMETIC IS NOT RE-IMPLEMENTED HERE. THAT IS THE ENTIRE POINT OF THIS FILE.
// -----------------------------------------------------------------------------------------------
// CustomerAging calls Aging(), the AR console's own read, the one the month-end screen renders and
// the one the auto-hold sweep decides on, and picks the account out of it. It does not run a
// second, smaller, "equivalent" query.
//
// The temptation to write that query is strong and it is exactly the mistake to avoid. The buckets
// in agingSelect age from DUE DATE, not issue date; they read FINALIZED and PARTIALLY_PAID only;
// they exclude non-invoice AR movements because those have no due date to age from. Every one of
// those is a judgement recorded in credit_repository.go, and a second copy would agree with the
// first on the day it was written and drift the first time one of them changed. Two surfaces that
// disagree about what a customer owes is the failure mode the AR console was consolidated to end -
// and an assistant that quotes a different number to the person on the phone than the screen shows
// the person in the office is that failure in its most expensive form.
//
// The cost of reusing it honestly is that Aging() computes every account with an open balance and
// this read discards all but one. At this system's scale that is one indexed aggregate; it is a
// price worth paying for a number that cannot drift, and if it ever stops being worth paying the
// remedy is a single-customer predicate inside agingSelect, one query, still one definition, not
// a second query here. (Filed as a PARK in NOTES-FIX-431B.md.)
//
// -----------------------------------------------------------------------------------------------
// WHAT SCOPES THESE READS, AND WHAT DELIBERATELY DOES NOT
// -----------------------------------------------------------------------------------------------
// These take a customer id that the CALLER HAS ALREADY BEEN ALLOWED TO RESOLVE. The fence on "which
// accounts may I ask about" belongs to the account lookup (internal/customer), where it already
// exists and is already the rule the POS picker enforces; putting a second, different fence here
// would give the app two answers to one question.
//
// What these reads then return is the ACCOUNT'S WHOLE POSITION, not the caller's-branch slice of it,
// and that is a deliberate choice rather than an omission. A customer owes what a customer owes. An
// account balance filtered to one branch's invoices is a number that appears on no statement, ties
// to nothing in the GL, and would be quoted down a phone line as if it were the debt. The AR console
// shows the whole account for the same reason.

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ErrCustomerActivityNotConfigured marks a store without the activity read (a unit-test fake). A
// wiring state, not a failure, and named so a consumer can say so.
var ErrCustomerActivityNotConfigured = errors.New("billing: customer activity read not configured")

// CustomerAging returns one account's aging row and the instant the buckets were computed against.
//
// found=false means the account carries NO open balance, which is a true, good answer ("nothing
// outstanding"), not an absence and not an error. agingSelect's HAVING clause drops accounts whose
// balances net to zero, so an account in perfect standing simply is not in the worklist, and every
// consumer must read that as zero rather than as missing.
//
// asOf IS RETURNED RATHER THAN LEFT FOR THE CALLER TO GUESS. An aging figure is only meaningful with
// the date it was aged against; a consumer that stamped its own time.Now() onto this answer would be
// labelling one clock's number with another clock's instant, and the two disagree by however long
// the query took, which is exactly the sort of discrepancy that turns into an argument with a
// customer about whether an invoice was late "as of" the call.
func (s *CreditService) CustomerAging(ctx context.Context, customerID string) (AgingRow, time.Time, bool, error) {
	asOf := s.clock()
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return AgingRow{}, asOf, false, nil
	}
	// branchID "", the account's WHOLE position, for the reason in the file header. asOf is the
	// service clock, so a test can pin the buckets without pinning wall time.
	// THE ACCOUNT'S WHOLE POSITION, whatever branch the caller works: this is the counter's standing,
	// which warns the till off selling on credit to a held or past due account, and an account held in
	// another branch is held. It is the internal read (every branch), as the sweep's is; the register,
	// the aging worklist and the holds list are the reach fenced reads.
	pol, err := s.store.CreditPolicy(ctx, "")
	if err != nil {
		return AgingRow{}, asOf, false, err
	}
	rows, err := s.store.AgingRowsIn(ctx, asOf, pol.AgingBucketDays, "", AllBranches)
	if err != nil {
		return AgingRow{}, asOf, false, err
	}
	for _, r := range rows {
		if r.CustomerID == customerID {
			return r, asOf, true, nil
		}
	}
	return AgingRow{}, asOf, false, nil
}

// OpenCents is what the account owes right now: every open invoice balance, current and late.
func (a AgingRow) OpenCents() int64 { return a.TotalCents }

// OverdueCents is the part of the open balance that is PAST ITS DUE DATE, the sum of the four
// late C8 buckets.
//
// Derived from the buckets rather than as Total − Current, and the difference is not cosmetic: the
// buckets and the total are computed by separate FILTER aggregates over the same rows, so if one of
// them ever stops covering a case (a NULL due date, a status added to the invoice CHECK), a
// subtraction would silently absorb the gap into "overdue" and report money as late that nobody has
// aged. Adding the late buckets can only ever report what was actually bucketed as late.
func (a AgingRow) OverdueCents() int64 {
	return a.Days30Cents + a.Days60Cents + a.Days60To90Cents + a.Days90PlusCents
}

// OldestOverdueBucket names how late the OLDEST overdue money on this account is, in the aging
// screens' own vocabulary ("30" / "60" / "90+"). Empty when nothing is overdue.
//
// It reads the same four buckets OverdueCents adds, and for the same reason: a bucket label
// derived from a subtraction, or from PastBucketCents (which the aging window may or may not fold
// into 90+ depending on the dealer's bucket_days), could name a bucket that holds no money. A label
// on a counter screen has to be a fact about a number the customer can be shown.
func (a AgingRow) OldestOverdueBucket() string {
	switch {
	case a.Days90PlusCents > 0 || a.Days60To90Cents > 0:
		return "90+"
	case a.Days60Cents > 0:
		return "60"
	case a.Days30Cents > 0:
		return "30"
	default:
		return ""
	}
}

// ARActivity is one movement on an account's AR ledger, an invoice raised, a payment received, a
// credit memo, an adjustment, an NSF reversal, a write-off.
//
// Kind is the customer_transactions.txn_type verbatim. Reference is the human-visible document
// number (invoice_no / payment_no) when the row points at one, and "" when it does not, an
// ADJUSTMENT has no document, and inventing a placeholder for it would put a reference on screen
// that a user could not find anywhere else in the system.
type ARActivity struct {
	Kind        string    `json:"kind"`
	Reference   string    `json:"reference"`
	AmountCents int64     `json:"amount_cents"`
	BalanceLeft int64     `json:"balance_after_cents"`
	BranchID    string    `json:"branch_id"`
	Memo        string    `json:"memo"`
	OccurredAt  time.Time `json:"occurred_at"`
}

// customerActivityStore is the optional persistence seam for the activity read, a separate
// interface reached by type assertion, so creditStore (implemented by fakes in this package's tests)
// is not widened.
type customerActivityStore interface {
	CustomerActivity(ctx context.Context, customerID string, limit int, withPayments bool) ([]ARActivity, error)
}

// CustomerActivity returns the account's most recent AR movements, newest first.
//
// An account with no movements returns an EMPTY slice and no error. A brand-new account that has
// never been invoiced is an ordinary state, and reporting it as a fault is the defect DIB-443 was
// filed for one ticket earlier.
func (s *CreditService) CustomerActivity(ctx context.Context, customerID string, limit int, withPayments bool) ([]ARActivity, error) {
	customerID = strings.TrimSpace(customerID)
	if customerID == "" {
		return nil, nil
	}
	as, ok := s.store.(customerActivityStore)
	if !ok {
		return nil, ErrCustomerActivityNotConfigured
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	return as.CustomerActivity(ctx, customerID, limit, withPayments)
}

// CustomerActivity is the pgx read behind it.
//
// It reads customer_transactions, the AR ledger the statement run and the aging tie-out both read -
// rather than assembling a timeline from orders and payments separately. One source means the
// assistant's "recent activity" and the customer's statement cannot tell different stories about the
// same week.
//
// The document number is resolved by two LEFT JOINs on source_ref_id rather than by a UNION of
// per-type queries: source_ref_id is a plain uuid with no FK (0009), so a row whose document has
// since been voided or removed still appears, with an empty reference, instead of vanishing from the
// ledger, a missing ledger line is a much worse answer than a nameless one.
//
// withPayments admits the rows that name a payment (the payment, and the NSF reversal of one): they are
// the payment trail ruling 87 holds to money desk or counter till WORK, so a caller the role gate does
// not admit to the payment reads asks without them and the rows are absent, not blanked. The limit
// counts only the rows the caller may see.
func (r *Repository) CustomerActivity(ctx context.Context, customerID string, limit int, withPayments bool) ([]ARActivity, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	if !looksLikeUUIDish(customerID) {
		// customer_id is a uuid column; a non-uuid would reach the driver as an uncastable literal
		// and come back as an internal error for what is really "no such account" (the DIB-443 grade).
		return nil, nil
	}
	if limit <= 0 {
		limit = 10
	}
	rows, err := r.pool.Query(ctx, `
		SELECT ct.txn_type,
		       COALESCE(i.invoice_no::text, p.payment_no::text, ''),
		       ct.amount_cents, ct.balance_after,
		       COALESCE(ct.branch_id::text, ''), ct.memo, ct.occurred_at
		  FROM customer_transactions ct
		  LEFT JOIN invoices i ON i.id = ct.source_ref_id
		  LEFT JOIN payments p ON p.id = ct.source_ref_id
		 WHERE ct.customer_id = $1::uuid
		   AND ($3::bool OR ct.txn_type NOT IN ('PAYMENT','NSF'))
		 ORDER BY ct.occurred_at DESC, ct.created_at DESC
		 LIMIT $2`, customerID, limit, withPayments)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ARActivity, 0, limit)
	for rows.Next() {
		var a ARActivity
		if err := rows.Scan(&a.Kind, &a.Reference, &a.AmountCents, &a.BalanceLeft,
			&a.BranchID, &a.Memo, &a.OccurredAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// looksLikeUUIDish grades a candidate id in Go, before it can reach a uuid column as an uncastable
// literal. Length + alphabet only: this is a "do not hand SQL something it will choke on" guard, not
// a validator, and the query is the thing that decides whether the row exists.
func looksLikeUUIDish(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
