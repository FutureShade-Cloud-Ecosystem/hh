package till

// holds.go, W5 A-05. HasActiveHold answers whether a customer currently has an ACTIVE credit hold (0035
// credit_holds, guarded by ux_credit_holds_one_active). It backs BOTH the AR console and the hold-aware
// credit gate below, which the POS Sale consults through the widened pos.CreditGate seam so an ACCOUNT
// (credit-terms) tender is denied, fail closed, while a hold is active; cash/card are always allowed.

import (
	"context"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5"
)

// callerTxKey carries the CALLER's transaction through the context. AGENTS.md: transactions span
// services through a context-carried transaction, never through shared repositories. This one has to:
// the hold-aware gate below is consulted INSIDE other modules' write transactions (the quote
// conversion's tx between the quote row lock and the order INSERT; the POS ring's sale tx) through the
// widened pos.CreditGate / quote.CreditGate seams, whose Check signature carries the context only, so
// the caller's transaction rides the context or it does not ride at all.
type callerTxKey struct{}

// WithCallerTx stamps an in-flight transaction into the context, for a caller whose write transaction
// is open when it consults the credit gate: the gate's hold read then runs on the caller's own
// connection instead of asking the pool for a SECOND one. That second ask is the pool cycle Postgres
// cannot see (the caller waits for a free connection, every other connection waits on the caller's row
// locks, at the deploy default pool of 4 the process stalls): the shape the inventory lane closed for
// the reservation policy read and this file's pool-cycle regression leg pins. A caller outside any
// transaction passes its context through unchanged, and a nil tx rides nothing.
func WithCallerTx(ctx context.Context, tx pgx.Tx) context.Context {
	if tx == nil {
		return ctx
	}
	return context.WithValue(ctx, callerTxKey{}, tx)
}

// callerTxFrom returns the transaction in flight on this context, nil when none is.
func callerTxFrom(ctx context.Context) pgx.Tx {
	tx, _ := ctx.Value(callerTxKey{}).(pgx.Tx)
	return tx
}

// HasActiveHold reports whether the customer has an ACTIVE credit hold. Empty customer id → false (a
// cash walk-in with no account cannot carry a hold). The read joins the CALLER's transaction when one
// is in flight on the context (WithCallerTx, above) and keeps the pool when none is, the inventory
// store's nil-means-pool convention.
func (r *Repository) HasActiveHold(ctx context.Context, customerID string) (bool, error) {
	if customerID == "" {
		return false, nil
	}
	if tx := callerTxFrom(ctx); tx != nil {
		var exists bool
		err := tx.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM credit_holds WHERE customer_id = $1 AND status = 'ACTIVE')`,
			customerID,
		).Scan(&exists)
		if err != nil {
			return false, err
		}
		return exists, nil
	}
	if r.pool == nil {
		return false, errNoDB
	}
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM credit_holds WHERE customer_id = $1 AND status = 'ACTIVE')`,
		customerID,
	).Scan(&exists)
	if err != nil {
		return false, err
	}
	return exists, nil
}

// HoldChecker is the narrow seam the hold-aware credit gate depends on (*till.Repository satisfies it).
type HoldChecker interface {
	HasActiveHold(ctx context.Context, customerID string) (bool, error)
}

// HoldAwareCreditGate is the W5-A05 credit gate: it wraps the identity credit decision (unchanged) and
// additionally DENIES an ACCOUNT (credit-terms) tender while the customer carries an ACTIVE credit hold.
// It structurally satisfies pos.CreditGate (same Check signature) so main.go wires it via posSvc.
// WithCreditGate WITHOUT any edit to pos/service.go, and without importing pos here (no import cycle:
// pos already imports till). Cash/card sales (termsAreCredit=false) are never blocked by a hold.
//
// FAIL CLOSED: if the hold lookup errors on an ACCOUNT tender, the sale is denied rather than let through.
type HoldAwareCreditGate struct {
	holds HoldChecker
}

// NewHoldAwareCreditGate builds the gate over a hold checker (the till Repository in production).
func NewHoldAwareCreditGate(holds HoldChecker) *HoldAwareCreditGate {
	return &HoldAwareCreditGate{holds: holds}
}

// Check denies an ACCOUNT tender under an active hold, else defers to the identity credit decision (the
// exact W4 behaviour). ctx + customerID are the widened-seam inputs the identity-only gate ignores.
func (g *HoldAwareCreditGate) Check(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked {
	// Only a credit-terms (ACCOUNT) sale draws on the account, so only it can be blocked by a hold. An
	// approved override still cannot buy through an active hold, a hold is a hard stop until released.
	if termsAreCredit && customerID != "" {
		held, err := g.holds.HasActiveHold(ctx, customerID)
		if err != nil {
			// Fail closed: cannot confirm the account is clear → deny the ACCOUNT tender.
			return &identity.ActionBlocked{
				Policy:   identity.PolicyCOD,
				BranchID: branchID,
				Detail:   "credit hold status unavailable; account tender denied (cash or card only)",
			}
		}
		if held {
			return &identity.ActionBlocked{
				Policy:   identity.PolicyCOD,
				BranchID: branchID,
				Detail:   "customer is on a credit hold; account tender denied (cash or card only)",
			}
		}
	}
	return identity.CheckCredit(orderTotalCents, openBalanceCents, creditLimitCents, termsAreCredit, branchID, hasApprovedOverride)
}

// compile-time guard: the gate must expose exactly the widened credit-gate shape.
var _ interface {
	Check(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked
} = (*HoldAwareCreditGate)(nil)
