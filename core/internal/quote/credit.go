package quote

// credit.go, DIB-387. The quote→order conversion is an ORDER WRITE: it inserts an unpaid order that
// draws on the customer's account. Until now it was gated on nothing but status ("is this quote
// ACCEPTED?"), so a customer on an ACTIVE credit hold could convert their way past the counter, the
// exact posture the POS Sale refuses (pos/service.go:403 via the pos.CreditGate seam, wired in main.go
// to till.HoldAwareCreditGate).
//
// This file copies that seam verbatim: quote declares its OWN narrow CreditGate interface with the same
// method shape, so *till.HoldAwareCreditGate satisfies it STRUCTURALLY, quote never imports till (and
// till never imports quote), exactly as pos does it. main.go hands the same gate instance to both.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// CreditGate is the credit/hold decision seam consulted before a conversion writes an order. The shape is
// byte-identical to pos.CreditGate so ONE gate implementation (till.HoldAwareCreditGate) serves both write
// paths without either module importing the other.
type CreditGate interface {
	Check(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked
}

// IdentityCreditGate is the default gate: the identity credit decision alone, with no hold awareness. It
// mirrors pos.IdentityCreditGate so an un-wired Repository still enforces the credit LIMIT rather than
// waving every conversion through; main.go replaces it with the hold-aware gate.
type IdentityCreditGate struct{}

// Check delegates to identity.CheckCredit (ctx/customerID are part of the widened seam the hold-aware
// gate reads; this identity-only impl ignores them).
func (IdentityCreditGate) Check(_ context.Context, _ string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked {
	return identity.CheckCredit(orderTotalCents, openBalanceCents, creditLimitCents, termsAreCredit, branchID, hasApprovedOverride)
}

// compile-time guard: the default gate must expose exactly the seam shape (so a signature drift here is a
// build failure, not a silently un-wired gate at runtime).
var _ CreditGate = IdentityCreditGate{}

// ErrCreditBlocked aborts a conversion whose customer fails the credit/hold gate. It carries the
// identity.ActionBlocked verdict (policy + detail) so the refusal reason survives to the caller.
//
// Unwrap returns ErrInvalidTransition on purpose: the portal BFF's error mapping (portal/handler.go:323)
// turns that into a 409, the SAME status pos/handler.go:285 answers for credit_blocked. A blocked
// conversion is therefore a business refusal to the contractor, never a 500.
type ErrCreditBlocked struct{ Blocked *identity.ActionBlocked }

func (e ErrCreditBlocked) Error() string {
	if e.Blocked == nil {
		return "quote: credit blocked"
	}
	return fmt.Sprintf("quote: credit blocked (%s): %s", e.Blocked.Policy, e.Blocked.Detail)
}

// Unwrap maps the block onto the 409 the BFF already understands (see above).
func (e ErrCreditBlocked) Unwrap() error { return ErrInvalidTransition }

// Policy names the blocking policy for a caller that does not want to reach into the verdict.
func (e ErrCreditBlocked) Policy() string {
	if e.Blocked == nil {
		return ""
	}
	return string(e.Blocked.Policy)
}

// creditFacts is the account posture a conversion is judged on, read INSIDE the convert transaction (so
// the facts and the INSERT see one snapshot). OpenBalanceCents is the latest customer_transactions
// balance_after, the same running-balance basis the POS credit gate reads (pos/repository.go:82).
type creditFacts struct {
	CustomerID       string
	BranchID         string
	TotalCents       int64
	CreditLimitCents int64
	OpenBalanceCents int64
}

// convertCreditFacts reads the quote's money + the customer's account posture INSIDE the convert
// transaction (the quote row is already locked FOR UPDATE by the caller). A missing quote is ErrNotFound
// , the same IDOR-safe answer every other tenant-scoped read gives.
func (r *Repository) convertCreditFacts(ctx context.Context, tx pgx.Tx, customerID, quoteID string) (creditFacts, error) {
	var f creditFacts
	err := tx.QueryRow(ctx, `
		SELECT q.customer_id::text, q.branch_id::text, q.total_cents,
		       COALESCE(c.credit_limit_cents, 0),
		       COALESCE((SELECT t.balance_after FROM customer_transactions t
		                  WHERE t.customer_id = c.id ORDER BY t.occurred_at DESC LIMIT 1), 0)
		  FROM quotes q JOIN customers c ON c.id = q.customer_id
		 WHERE q.id = $2 AND q.customer_id = $1`,
		customerID, quoteID).Scan(&f.CustomerID, &f.BranchID, &f.TotalCents, &f.CreditLimitCents, &f.OpenBalanceCents)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return creditFacts{}, ErrNotFound
		}
		return creditFacts{}, fmt.Errorf("convert quote %q: credit facts: %w", quoteID, err)
	}
	return f, nil
}

// creditGate returns the wired gate, defaulting to the identity-only gate. It never returns nil: a nil
// gate would be a silent bypass, which is the bug this file exists to close.
func (r *Repository) creditGate() CreditGate {
	if r.credit != nil {
		return r.credit
	}
	return IdentityCreditGate{}
}

// WithCreditGate injects the credit/hold gate the conversion consults (main.go wires
// till.NewHoldAwareCreditGate(tillRepo), the same instance POS uses).
func (r *Repository) WithCreditGate(g CreditGate) *Repository { r.credit = g; return r }

// checkCredit runs the gate over the facts and returns ErrCreditBlocked when it refuses.
//
// termsAreCredit is TRUE for every conversion except the one QD-5 pays in full in the same action: a converted order is unpaid at creation and therefore draws on
// the account, there is no cash/card tender at conversion to exempt it (the POS ring passes true for the
// same unpaid-at-ring posture, pos/service.go:403).
//
// hasApprovedOverride is FALSE, exactly as the POS ring passes it: this path claims no override, so the
// gate's override branch stays the caller's to exercise, and a hold blocks regardless of one anyway
// (till/holds.go:59).
func (r *Repository) checkCredit(ctx context.Context, f creditFacts, termsAreCredit bool) error {
	blk := r.creditGate().Check(ctx, f.CustomerID, f.TotalCents, f.OpenBalanceCents, f.CreditLimitCents, termsAreCredit, f.BranchID, false)
	if blk == nil {
		return nil
	}
	// DIB-483 / M3-G7B: attach the MONEY the refusal is about, so the surface can say
	// "$112,200.00 against a $50,000.00 limit" instead of quoting the gate's cents-formatted prose at
	// a person. The gate's own Detail is untouched, POS and every other consumer keep the words they
	// have; this is additive context on a map that already exists for exactly this purpose.
	//
	// A copy, not a mutation of the gate's value: the gate is a shared instance and its verdicts must
	// not accumulate this path's annotations.
	annotated := *blk
	data := make(map[string]string, len(blk.Data)+3)
	for k, v := range blk.Data {
		data[k] = v
	}
	data["order_total"] = money(f.TotalCents)
	data["credit_limit"] = money(f.CreditLimitCents)
	data["open_balance"] = money(f.OpenBalanceCents)
	annotated.Data = data
	return ErrCreditBlocked{Blocked: &annotated}
}

// money renders integer cents as a plain dollar amount with thousands separators, "$112,200.00".
// Counter staff read dollars; the gate speaks cents; this is the one place they meet.
func money(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := strconv.FormatInt(cents/100, 10)
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	b.WriteByte('$')
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	fmt.Fprintf(&b, ".%02d", cents%100)
	return b.String()
}

// creditRefusalMessage turns a credit block into the sentence a HUMAN reads. Both doors call it, the
// contractor's BFF (portal/handler.go) and the counter's staff handler (edit_handler.go), so a
// refusal reads the same at both, which is the M3-G7B acceptance.
//
// The shape is: what happened, in plain words, keyed off the POLICY; then the specifics the verdict
// carries. Never a bare status name. The failure this replaces was "invalid state transition", which
// is true, useless, and actively misleading, it points the reader at the quote's state when the
// problem is the customer's account.
func CreditRefusalMessage(e ErrCreditBlocked) string {
	if e.Blocked == nil {
		return "this customer's account will not carry the order, check with accounts receivable"
	}
	b := e.Blocked
	var lead string
	switch b.Policy {
	case identity.PolicyCredit:
		lead = "this order is over the customer's credit limit"
		if total, ok := b.Data["order_total"]; ok {
			if limit, ok2 := b.Data["credit_limit"]; ok2 {
				lead = fmt.Sprintf("this order is %s against a %s credit limit", total, limit)
				if bal, ok3 := b.Data["open_balance"]; ok3 && bal != "$0.00" {
					lead += fmt.Sprintf(" with %s already owing", bal)
				}
			}
		}
	case identity.PolicyCOD:
		lead = "this customer cannot buy on account right now"
	default:
		lead = "this conversion was refused by a credit policy"
	}
	detail := strings.TrimSpace(b.Detail)
	if detail == "" {
		return lead + ", the quote is unchanged and no order was created; accounts receivable can release or override the hold"
	}
	return lead + " (" + detail + "), the quote is unchanged and no order was created; accounts receivable can release or override the hold"
}
