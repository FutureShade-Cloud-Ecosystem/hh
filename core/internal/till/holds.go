package till

// holds.go, W5 A-05. HasActiveHold answers whether a customer currently has an ACTIVE credit hold (0035
// credit_holds, guarded by ux_credit_holds_one_active). It backs BOTH the AR console and the hold-aware
// credit gate below, which the POS Sale consults through the widened pos.CreditGate seam so an ACCOUNT
// (credit-terms) tender is denied, fail closed, while a hold is active; cash/card are always allowed.

import (
	"context"
	"errors"
	"fmt"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
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
	// The approved override changes no decision but the limit it waives. As on main, it passes a
	// customer with no credit, and it passes whatever the job read says (the job is not read); only a
	// hold above, or a customer exposure read that failed, still denies the account tender.
	//
	// No door passes an approved override today: both the POS ring and the quote conversion pass
	// false, so this branch is unreachable in production. It is built anyway; OverriddenLimit (below)
	// says WHICH limit was waived, the job's own or the customer's, in words and in data, for the
	// door that passes one to record.
	if hasApprovedOverride {
		if g.exposure != nil && termsAreCredit && customerID != "" {
			if _, err := g.exposure.CustomerExposure(ctx, callerTxFrom(ctx), customerID); err != nil {
				return &identity.ActionBlocked{
					Policy:   identity.PolicyCOD,
					BranchID: branchID,
					Detail:   "credit exposure unavailable; account tender denied (cash or card only)",
				}
			}
		}
		return nil
	}
	return g.limitBreak(ctx, customerID, orderTotalCents, openBalanceCents, creditLimitCents, termsAreCredit, branchID)
}

// limitBreak is the limit check with no override: the block the sale would meet, or nil. It reads the
// customer's whole exposure and, when the sale is on a job that carries its own limit, the job's.
func (g *HoldAwareCreditGate) limitBreak(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string) *identity.ActionBlocked {
	if g.exposure == nil || !termsAreCredit || customerID == "" {
		// The fallback path attaches no limit data, as on main.
		return identity.CheckCredit(orderTotalCents, openBalanceCents, creditLimitCents, termsAreCredit, branchID, false)
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
	if creditLimitCents <= 0 {
		// No credit at all: the customer reads COD whatever the job's own limit, so the order of checks
		// can never refuse a smaller sale and pass a larger one.
		return identity.CheckCredit(orderTotalCents, exposure, creditLimitCents, termsAreCredit, branchID, false)
	}
	if jobID := saleJobFrom(ctx); jobID != "" {
		ownLimit, jobExposure, _, err := g.exposure.JobPosition(ctx, tx, customerID, jobID)
		if err != nil {
			if errors.Is(err, customer.ErrNotFound) {
				// The doors' fence refuses a job that is not the customer's earlier; this is only the
				// fallback, and it says so rather than reading as an outage.
				return &identity.ActionBlocked{
					Policy:   identity.PolicyCOD,
					BranchID: branchID,
					Detail:   "this job is not the customer's; account tender denied (cash or card only)",
				}
			}
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

// OverriddenLimit is what an approved override waives: the limit the sale would have broken, said in
// words and in data (Data["limit_scope"] is "job" or "customer", with the figures, and
// Data["overridden"] is "true"). It returns nil when the sale breaks no limit, so nothing was
// waived. It is a note for the audit, never a block: Check still returns nil for an approved override.
// A door that passes an approved override calls it beside Check and records the answer.
func (g *HoldAwareCreditGate) OverriddenLimit(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string) *identity.ActionBlocked {
	blk := g.limitBreak(ctx, customerID, orderTotalCents, openBalanceCents, creditLimitCents, termsAreCredit, branchID)
	if blk == nil || blk.Policy != identity.PolicyCredit {
		return nil
	}
	data := make(map[string]string, len(blk.Data)+1)
	for k, v := range blk.Data {
		data[k] = v
	}
	if data["limit_scope"] == "" {
		// The fallback path carries no limit data; the figure it checked is the customer's.
		data["limit_scope"] = LimitScopeCustomer
		data["credit_limit"] = dollars(creditLimitCents)
		data["open_balance"] = dollars(openBalanceCents)
	}
	data["overridden"] = "true"
	return &identity.ActionBlocked{
		Policy:   identity.PolicyCredit,
		BranchID: branchID,
		Detail:   "an approved override waives this: " + blk.Detail,
		Data:     data,
	}
}

// compile-time guard: the gate must expose exactly the widened credit-gate shape.
var _ interface {
	Check(ctx context.Context, customerID string, orderTotalCents, openBalanceCents, creditLimitCents int64, termsAreCredit bool, branchID string, hasApprovedOverride bool) *identity.ActionBlocked
} = (*HoldAwareCreditGate)(nil)
