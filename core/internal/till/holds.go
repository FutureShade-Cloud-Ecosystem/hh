package till

// holds.go, W5 A-05. HasActiveHold answers whether a customer currently has an ACTIVE credit hold (0035
// credit_holds, guarded by ux_credit_holds_one_active). It backs BOTH the AR console and the hold-aware
// credit gate below, which the POS Sale consults through the widened pos.CreditGate seam so an ACCOUNT
// (credit-terms) tender is denied, fail closed, while a hold is active; cash/card are always allowed.

import (
	"context"
	"fmt"

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
//
// WAVE 3 JA-3: with an exposure source bound (WithExposure) the gate also reads the customer's WHOLE
// exposure (every job, plus open orders) from that one place instead of trusting the balance its
// caller passed, and a sale on a job (the job rides the context, WithSaleJob) is checked against the
// job's own limit as well. Either breach holds the sale in words that name the limit and the figures.
type HoldAwareCreditGate struct {
	holds    HoldChecker
	exposure ExposureSource
}

// NewHoldAwareCreditGate builds the gate over a hold checker (the till Repository in production).
func NewHoldAwareCreditGate(holds HoldChecker) *HoldAwareCreditGate {
	return &HoldAwareCreditGate{holds: holds}
}

// ExposureSource is the one reader of exposure every credit gate shares (*billing.ExposureReader
// satisfies it; billing cannot be imported here, so the seam is declared with builtin types only).
// tx is the caller's open transaction when one rides the context, nil otherwise, so a gate
// consulted inside a write transaction reads on the caller's connection. JobPosition answers the
// job's OWN limit (nil when it shares the customer's), the job's own exposure and the customer's
// limit.
type ExposureSource interface {
	CustomerExposure(ctx context.Context, tx pgx.Tx, customerID string) (int64, error)
	JobPosition(ctx context.Context, tx pgx.Tx, customerID, jobID string) (ownLimit *int64, exposure, customerLimit int64, err error)
}

// WithExposure binds the exposure source. Without it the gate behaves as it did before JA-3.
func (g *HoldAwareCreditGate) WithExposure(e ExposureSource) *HoldAwareCreditGate {
	g.exposure = e
	return g
}

type saleJobKey struct{}

// WithSaleJob stamps the job a sale or a conversion is raised on into the context, for the gate to
// check the job's own limit. A blank job rides nothing.
func WithSaleJob(ctx context.Context, jobID string) context.Context {
	if jobID == "" {
		return ctx
	}
	return context.WithValue(ctx, saleJobKey{}, jobID)
}

func saleJobFrom(ctx context.Context) string {
	j, _ := ctx.Value(saleJobKey{}).(string)
	return j
}

// Limit scopes a credit refusal names in Data["limit_scope"].
const (
	LimitScopeCustomer = "customer"
	LimitScopeJob      = "job"
)

// dollars renders integer cents as "$1,234.56" for the words of a refusal.
func dollars(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := fmt.Sprintf("%d", cents/100)
	var b []byte
	for i, c := range []byte(whole) {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, c)
	}
	out := fmt.Sprintf("$%s.%02d", b, cents%100)
	if neg {
		return "-" + out
	}
	return out
}

// limitBlock is the refusal that names WHICH limit the sale breaks and the figures. credit_limit and
// open_balance are the keys the quote desk's position withholding already strips, so a caller who may
// not see the account's position never reads the figures.
func limitBlock(scope string, branchID string, exposure, sale, limit int64) *identity.ActionBlocked {
	who := "the customer's credit limit"
	held := "the customer already carries"
	if scope == LimitScopeJob {
		who = "the job's own credit limit"
		held = "the job already carries"
	}
	return &identity.ActionBlocked{
		Policy:   identity.PolicyCredit,
		BranchID: branchID,
		Detail: fmt.Sprintf("this sale breaks %s: %s %s and the sale adds %s, %s against a %s limit",
			who, held, dollars(exposure), dollars(sale), dollars(exposure+sale), dollars(limit)),
		Data: map[string]string{
			"limit_scope":  scope,
			"credit_limit": dollars(limit),
			"open_balance": dollars(exposure),
		},
	}
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
	if g.exposure == nil || !termsAreCredit || customerID == "" {
		return identity.CheckCredit(orderTotalCents, openBalanceCents, creditLimitCents, termsAreCredit, branchID, hasApprovedOverride)
	}
	// The one exposure reader. A read that fails denies the account tender, the hold read's posture.
	unavailable := &identity.ActionBlocked{
		Policy:   identity.PolicyCOD,
		BranchID: branchID,
		Detail:   "credit exposure unavailable; account tender denied (cash or card only)",
	}
	tx := callerTxFrom(ctx)
	exposure, err := g.exposure.CustomerExposure(ctx, tx, customerID)
	if err != nil {
		return unavailable
	}
	// An approved override still works as today: it waives the limits below, not a hold above.
	if hasApprovedOverride {
		return nil
	}
	if jobID := saleJobFrom(ctx); jobID != "" {
		ownLimit, jobExposure, _, err := g.exposure.JobPosition(ctx, tx, customerID, jobID)
		if err != nil {
			return unavailable
		}
		if ownLimit != nil && jobExposure+orderTotalCents > *ownLimit {
			return limitBlock(LimitScopeJob, branchID, jobExposure, orderTotalCents, *ownLimit)
		}
		// A job limit never raises the customer's: the customer's own limit below always applies, over
		// the customer's whole exposure.
	}
	blk := identity.CheckCredit(orderTotalCents, exposure, creditLimitCents, termsAreCredit, branchID, false)
	if blk != nil && blk.Policy == identity.PolicyCredit {
		return limitBlock(LimitScopeCustomer, branchID, exposure, orderTotalCents, creditLimitCents)
	}
	return blk
}

// compile-time guard: the gate must expose exactly the widened credit-gate shape.
var _ interface {
	Check(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked
} = (*HoldAwareCreditGate)(nil)
