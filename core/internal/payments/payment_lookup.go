package payments

// payment_lookup.go, "find me that payment", in the three shapes a person actually asks it.
//
// WHY THREE SHAPES AND NOT A SEARCH BOX. Every existing read in this module is keyed by
// customer_id, because every existing read serves the PORTAL, where the customer is the session.
// The question this file answers is asked at a counter or over a phone by staff, and the thing in
// their hand is one of exactly three facts:
//
//	a reference  , off a remittance advice or a bank line. Matches the payment number OR the
//	                gateway reference, because the person holding it does not know which one it is.
//	an amount    , the cheque on the desk. EXACT, in cents.
//	a customer   , "did the Northgate money land?"
//
// They COMBINE. An amount plus a customer is the most common real question and is far more precise
// than either alone.
//
// THE AMOUNT MATCH IS EXACT AND MUST STAY EXACT. Not a range, not a rounding, not a tolerance. A
// person looking for $1,240.00 who is shown $1,240.50 has been told the money arrived when it did
// not, and a near-miss is the single most expensive kind of wrong answer this module can give: it
// ends an investigation that should have continued. If a fuzzy search is ever wanted it is a
// different, separately named question with its own consumer, and its results have to SAY they are
// approximate. (Mutation M2 in NOTES-FIX-431B.md widens this to a tolerance; the exact-amount test
// dies.)
//
// AN EMPTY RESULT IS AN ANSWER. "No payment matches that reference" is true, useful, and not a
// failure, the DIB-443 lesson carried into a new module.
//
// AN UNFILTERED QUERY IS REFUSED. All three facts absent is a report, not a lookup, and it belongs
// on a surface that can page it.

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/likeutil"
)

// ErrPaymentLookupNotConfigured marks a store without this read (a unit-test fake). A wiring state,
// not a failure.
var ErrPaymentLookupNotConfigured = errors.New("payments: payment lookup not configured")

// ErrUnfilteredLookup is the refusal for a query with no facts in it at all.
var ErrUnfilteredLookup = errors.New("payments: a lookup needs a reference, an amount or a customer")

// BranchReach is a caller's branch authority projected onto a read: every branch, or a set of
// granted ones. Built from the guard-resolved caller and passed IN, this module must not import
// identity, and a read must never re-derive an authority the request guard already decided.
//
// Global false with NO BranchIDs means "no branch grants at all" and resolves to NO payments. Not to
// every payment: a caller with no branch access sees no till, which is the exact inversion branch
// scoping exists to prevent.
type BranchReach struct {
	Global    bool
	BranchIDs []string
}

// branchFilter renders the reach for the `($n::uuid[] IS NULL OR branch_id = ANY($n::uuid[]))`
// predicate. nil → SQL NULL → no fence; an empty non-nil array matches nothing.
func (r BranchReach) branchFilter() any {
	if r.Global {
		return nil
	}
	if r.BranchIDs == nil {
		return []string{}
	}
	return r.BranchIDs
}

// LookupQuery is the three-shape filter. At least one of Reference / AmountCents / CustomerRef must
// be set; Limit defaults and is capped.
type LookupQuery struct {
	// Reference matches payments.payment_no (CITEXT) or payments.gateway_ref.
	Reference string
	// AmountCents is an EXACT match in cents. Zero means "no amount filter", which is safe as a
	// sentinel because the payments table CHECKs amount_cents > 0, so no real payment can be $0.00
	// and the sentinel can never collide with a legitimate search.
	AmountCents int64
	// CustomerRef matches the account name or the account number, partially.
	CustomerRef string
	Limit       int
}

func (q LookupQuery) empty() bool {
	return strings.TrimSpace(q.Reference) == "" && q.AmountCents == 0 && strings.TrimSpace(q.CustomerRef) == ""
}

// PaymentMatch is one payment as a staff lookup needs it: the numbers on the document, plus the two
// names that make it recognisable as the right one.
type PaymentMatch struct {
	ID           string    `json:"id"`
	PaymentNo    string    `json:"payment_no"`
	GatewayRef   string    `json:"gateway_ref"`
	CustomerName string    `json:"customer_name"`
	BranchName   string    `json:"branch_name"`
	Method       string    `json:"method"`
	Status       string    `json:"status"`
	AmountCents  int64     `json:"amount_cents"`
	ReceivedAt   time.Time `json:"received_at"`
}

// paymentLookupStore is the optional persistence seam, a separate interface reached by type
// assertion, so paymentStore (implemented by fakes and by the portal composition) is not widened.
type paymentLookupStore interface {
	FindPayments(ctx context.Context, q LookupQuery, reach BranchReach) ([]PaymentMatch, error)
}

// FindPayments answers the lookup within the caller's branch reach, newest first.
//
// An empty slice with a nil error is the "nothing matches" answer. ErrUnfilteredLookup is the
// refusal for a query with no facts in it.
func (s *Service) FindPayments(ctx context.Context, reach BranchReach, q LookupQuery) ([]PaymentMatch, error) {
	if q.empty() {
		return nil, ErrUnfilteredLookup
	}
	ls, ok := s.store.(paymentLookupStore)
	if !ok {
		return nil, ErrPaymentLookupNotConfigured
	}
	if q.Limit <= 0 || q.Limit > 50 {
		q.Limit = 10
	}
	q.Reference = strings.TrimSpace(q.Reference)
	q.CustomerRef = strings.TrimSpace(q.CustomerRef)
	// A caller with no branch grants reaches no till. Short-circuited without a round trip rather
	// than issued as a `= ANY('{}')` that can never match.
	if !reach.Global && len(reach.BranchIDs) == 0 {
		return nil, nil
	}
	return ls.FindPayments(ctx, q, reach)
}

// FindPayments is the pgx read.
//
// ONE STATEMENT WITH THREE OPTIONAL PREDICATES, not three statements unioned. Each filter is written
// as `($n IS NULL OR <match>)` so an absent filter short-circuits and a present one narrows -
// which is what makes "this amount, from this customer" work without a fourth code path, and what
// keeps the three shapes from drifting into three different definitions of what a payment IS.
//
// The reference arm matches payment_no OR gateway_ref because the person holding a reference does
// not know which of the two they are holding. payment_no is CITEXT (case already irrelevant) and
// stays an EXACT comparison on the raw reference; gateway_ref is plain text and is matched
// case-insensitively so a reference copied out of a bank statement in upper case still finds it.
// Both ILIKE arms match the caller's text LITERALLY: the term is escaped with the house escaper
// (platform/likeutil) and each pattern names ESCAPE '\', so a '%' or '_' in a reference or a
// customer name cannot widen the lookup into a wildcard scan. Only the patterns read that way; the
// amount match stays exact, the ordering and the reach are untouched.
func (r *Repository) FindPayments(ctx context.Context, q LookupQuery, reach BranchReach) ([]PaymentMatch, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	var ref, refEsc, cust any
	if q.Reference != "" {
		ref = q.Reference
		// The escaped twin of the reference rides $6: the payment_no comparison above needs the
		// text as it was written, the gateway_ref pattern below needs it as a literal, and one
		// parameter cannot serve both when the reference carries a metacharacter.
		refEsc = likeutil.EscapeLikePattern(q.Reference)
	}
	if q.CustomerRef != "" {
		cust = likeutil.EscapeLikePattern(q.CustomerRef)
	}
	var amount any
	if q.AmountCents != 0 {
		amount = q.AmountCents
	}
	rows, err := r.pool.Query(ctx, `
		SELECT p.id::text, p.payment_no::text, p.gateway_ref,
		       COALESCE(c.name, ''), COALESCE(l.name, ''),
		       p.method, p.status, p.amount_cents, p.received_at
		  FROM payments p
		  LEFT JOIN customers c ON c.id = p.customer_id
		  LEFT JOIN locations l ON l.id = p.branch_id
		 WHERE ($1::text IS NULL
		        OR p.payment_no = $1::citext
		        OR (p.gateway_ref <> '' AND p.gateway_ref ILIKE $6::text ESCAPE '\'))
		   AND ($2::bigint IS NULL OR p.amount_cents = $2::bigint)
		   AND ($3::text IS NULL
		        OR c.name ILIKE '%' || $3::text || '%' ESCAPE '\'
		        OR c.account_no::text ILIKE '%' || $3::text || '%' ESCAPE '\')
		   AND ($4::uuid[] IS NULL OR p.branch_id = ANY($4::uuid[]))
		 ORDER BY p.received_at DESC, p.payment_no DESC
		 LIMIT $5`, ref, amount, cust, reach.branchFilter(), q.Limit, refEsc)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]PaymentMatch, 0, q.Limit)
	for rows.Next() {
		var m PaymentMatch
		if err := rows.Scan(&m.ID, &m.PaymentNo, &m.GatewayRef, &m.CustomerName, &m.BranchName,
			&m.Method, &m.Status, &m.AmountCents, &m.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
