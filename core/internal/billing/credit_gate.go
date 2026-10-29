package billing

// credit_gate.go, DIB-65 (AR credit-hold gates) and DIB-52 (COD dispatch enforcement).
//
// -----------------------------------------------------------------------------------------------
// THE SHAPE OF THIS, BECAUSE IT IS THE PART WORTH UNDERSTANDING
// -----------------------------------------------------------------------------------------------
// There are already TWO places a credit decision is asked for, and neither of them is changed by
// this file:
//
//	POS ring        → pos.CreditGate  → till.HoldAwareCreditGate → reads credit_holds
//	Quote→Order     → the same seam, wired by W1 at convert
//
// So the correct way to make "AR aging places a hold" real is NOT to build a third gate. It is to
// make ROWS APPEAR in the table those two already watch. That is the whole design: billing evaluates
// AR policy and writes credit_holds; the POS and convert start honouring AR holds with zero code
// change on their side, because they were already asking the right question.
//
// DIB-52 adds the one thing that genuinely has no seam yet, DISPATCH. A COD account is not barred
// from ordering and is certainly not barred from paying; it is barred from having goods leave the
// yard until the money is in. Nothing in the fulfilment path was asking anybody about that, so
// ARDispatchGate below is a new, deliberately tiny port. Swarm C consumes it at the union.
//
// -----------------------------------------------------------------------------------------------
// WHY CanDispatch FAILS CLOSED
// -----------------------------------------------------------------------------------------------
// It returns (bool, reason) with no error. An internal failure resolves to FALSE with a reason that
// names the failure. That is a real choice and it deserves defending: a read failure is not a licence
// to release goods on credit we could not verify. The cost of being wrong in the closed direction is
// a truck waiting while somebody looks at a screen; the cost of being wrong in the open direction is
// stock leaving the yard against an account nobody could check. Those are not symmetric, and a
// database that cannot answer this question cannot serve the order screen either, so the "outage
// stops dispatch" objection describes a system that has already stopped.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/jackc/pgx/v5"
)

// -----------------------------------------------------------------------------------------------
// THE SEAM, this signature is what Swarm C wires at the union. Do not change it without saying so.
// -----------------------------------------------------------------------------------------------

// ARDispatchGate answers one question for the fulfilment path: may this order's goods leave?
//
// reason is a HUMAN-READABLE sentence intended for a driver's screen, not an error code. It is
// non-empty exactly when allowed is false.
type ARDispatchGate interface {
	CanDispatch(ctx context.Context, orderID string) (allowed bool, reason string)
}

// AllowAllDispatchGate is the SHIPPED DEFAULT: it permits everything. Wiring the real gate is a
// main.go decision, so a branch that has not adopted AR dispatch enforcement behaves exactly as it
// did before this file existed.
type AllowAllDispatchGate struct{}

// CanDispatch always allows.
func (AllowAllDispatchGate) CanDispatch(context.Context, string) (bool, string) { return true, "" }

// Compile-time proof both implementations satisfy the port.
var (
	_ ARDispatchGate = AllowAllDispatchGate{}
	_ ARDispatchGate = (*CreditService)(nil)
)

// -----------------------------------------------------------------------------------------------
// POLICY + MODEL
// -----------------------------------------------------------------------------------------------

// Hold kinds (0104 CHECK).
const (
	HoldNSF         = "NSF"
	HoldARAging     = "AR_AGING"
	HoldCreditLimit = "CREDIT_LIMIT"
	HoldCOD         = "COD"
	HoldManual      = "MANUAL"
)

// Audit verbs for the credit gates. audit_log has no CHECK on action, so these are free to add; they
// are constants so a typo cannot split one action into two in the audit trail.
const (
	ActCreditHoldAuto     = "CREDIT_HOLD_AUTO"
	ActCreditHoldOverride = "CREDIT_HOLD_OVERRIDE"
	ActCreditHoldRelease  = "CREDIT_HOLD_RELEASE_AR"
)

// CreditPolicy is one ar_credit_policy row (0104).
type CreditPolicy struct {
	BranchID            string `json:"branch_id"`
	AgingBucketDays     int    `json:"aging_bucket_days"`
	AgingThresholdCents int64  `json:"aging_threshold_cents"`
	AgingHoldEnabled    bool   `json:"aging_hold_enabled"`
	LimitHoldEnabled    bool   `json:"limit_hold_enabled"`
	LimitToleranceCents int64  `json:"limit_tolerance_cents"`
	CODEnforced         bool   `json:"cod_enforced"`
	AutoHoldEnabled     bool   `json:"auto_hold_enabled"`
	// WriteOffLimitCents is the branch's small balance write off limit (0457): the largest balance a
	// manager may write off at this branch, the org default (2500, $25.00) when the branch carries
	// no row of its own. Only the controller passes it.
	WriteOffLimitCents int64 `json:"write_off_limit_cents"`
}

// CreditHold is the AR view of a credit_holds row.
type CreditHold struct {
	ID             string     `json:"id"`
	CustomerID     string     `json:"customer_id"`
	CustomerName   string     `json:"customer_name,omitempty"`
	Status         string     `json:"status"`
	Kind           string     `json:"hold_kind"`
	AutoPlaced     bool       `json:"auto_placed"`
	Reason         string     `json:"reason"`
	OverrideUntil  *time.Time `json:"override_until,omitempty"`
	OverrideReason string     `json:"override_reason,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	HomeBranchID   string     `json:"-"`
}

// AgingRow is one account's AR aging, the input to the auto-hold decision AND the month-end aging
// view. One query serves both so the screen an operator looks at and the rule the machine applies can
// never disagree, the commonest way a credit policy loses an argument with a customer.
//
// Bucket fields follow the C8 5-bucket bands, INCLUSIVE at the top of each band: a balance exactly
// 30 days past due sits in 1 to 30, never in 31 to 60, a balance exactly 60 days past due sits in
// 31 to 60, never in 61 to 90, and a balance exactly 90 days past due sits in 61 to 90, never in
// 91 and over. Days90PlusCents holds the 91+ band; "anything 60+ past due" is Days60Cents +
// Days60To90Cents + Days90PlusCents, never just Days90PlusCents.
type AgingRow struct {
	CustomerID       string `json:"customer_id"`
	CustomerName     string `json:"customer_name"`
	AccountNo        string `json:"account_no"`
	BranchID         string `json:"branch_id"`
	TermsDays        int    `json:"payment_terms_days"`
	CreditLimitCents int64  `json:"credit_limit_cents"`
	CurrentCents     int64  `json:"current_cents"`       // 0 days past due or not yet due
	Days30Cents      int64  `json:"days_30_cents"`       // 1 to 30 days past due
	Days60Cents      int64  `json:"days_60_cents"`       // 31 to 60 days past due
	Days60To90Cents  int64  `json:"days_60_to_90_cents"` // 61 to 90 days past due (C8 band)
	Days90PlusCents  int64  `json:"days_90_plus_cents"`  // 91 and over days past due (C8 band)
	TotalCents       int64  `json:"total_cents"`
	PastBucketCents  int64  `json:"past_bucket_cents"` // balance older than the policy's aging window
	OnHold           bool   `json:"on_hold"`
	HoldKind         string `json:"hold_kind,omitempty"`
}

// EvaluationResult is what one sweep did, the job's Result detail, and the dry-run report an
// operator reads BEFORE flipping auto_hold_enabled on.
type EvaluationResult struct {
	Evaluated int      `json:"evaluated"`
	Placed    int      `json:"placed"`
	WouldHold int      `json:"would_hold"` // flag-off: the blast radius, without touching anybody
	DryRun    bool     `json:"dry_run"`
	Customers []string `json:"customers,omitempty"`
}

// -----------------------------------------------------------------------------------------------
// SERVICE
// -----------------------------------------------------------------------------------------------

// creditStore is the persistence the CreditService needs.
type creditStore interface {
	CreditPolicy(ctx context.Context, branchID string) (CreditPolicy, error)
	AgingRows(ctx context.Context, asOf time.Time, bucketDays int, branchID string) ([]AgingRow, error)
	AgingRowsIn(ctx context.Context, asOf time.Time, bucketDays int, branchID string, reach BranchReach) ([]AgingRow, error)
	PlaceHold(ctx context.Context, customerID, kind, reason string, placedBy string) (string, bool, error)
	ActiveHold(ctx context.Context, customerID string) (CreditHold, bool, error)
	ListHolds(ctx context.Context, status string) ([]CreditHold, error)
	ListHoldsIn(ctx context.Context, status string, reach BranchReach) ([]CreditHold, error)
	OverrideHold(ctx context.Context, holdID, reason string, until time.Time, actorID string) error
	ReleaseHold(ctx context.Context, holdID, reason, actorID string) (string, error)
	OrderDispatchFacts(ctx context.Context, orderID string) (DispatchFacts, error)
}

// DispatchFacts is everything CanDispatch needs about one order, in a single round trip.
type DispatchFacts struct {
	OrderID           string
	CustomerID        string
	BranchID          string
	TermsDays         int
	UnpaidCents       int64 // open balance on THIS order's invoices
	HoldActive        bool
	HoldKind          string
	OverrideInForce   bool
	CustomerName      string
	InvoicesFinalized bool // false when nothing has been invoiced yet
}

// CreditService evaluates AR credit policy and answers the dispatch gate.
type CreditService struct {
	store creditStore
	audit audit.Recorder
	clock func() time.Time
	// autoHold is the DIB-483 write seam for the master switch (autohold.go). nil = the switch is
	// unwired and the admin surface renders the setting read-only, rather than offering a control that
	// would fail.
	autoHold autoHoldStore
	position AccountPositionReader
}

// NewCreditService builds the service over a store (the billing Repository in production).
//
// DIB-483: when that store ALSO satisfies autoHoldStore, the real *Repository does, it is bound as
// the auto-hold write seam, so the master switch is live wherever this service is constructed without
// a second wiring line at every call site. A store that only reads (a fake, a scaffold) leaves the
// seam nil and the switch renders read-only rather than failing.
//
// This is StaffService.WithAudit's rule, verbatim (quote/staff.go): one constructor argument, an
// optional capability discovered by type assertion, and a degraded-not-broken fallback.
func NewCreditService(store creditStore) *CreditService {
	s := &CreditService{store: store, clock: time.Now}
	if ah, ok := store.(autoHoldStore); ok {
		s.autoHold = ah
	}
	if pr, ok := store.(registerPolicyReader); ok {
		s.position = NewAccountPositionGate(pr)
	}
	return s
}

// WithAccountPosition binds the account position policy reader (UX-97).
func (s *CreditService) WithAccountPosition(p AccountPositionReader) *CreditService {
	s.position = p
	return s
}

func (s *CreditService) positionAllowed(ctx context.Context, homeBranchID string) bool {
	if s == nil || s.position == nil {
		return false
	}
	return s.position.AccountPositionAllowed(ctx, homeBranchID)
}

// WithAudit attaches the audit recorder. Every override and release is recorded through it; a nil
// recorder is tolerated (scaffold/tests) and simply records nothing.
func (s *CreditService) WithAudit(r audit.Recorder) *CreditService { s.audit = r; return s }

// WithClock overrides the clock (tests).
func (s *CreditService) WithClock(now func() time.Time) *CreditService {
	if now != nil {
		s.clock = now
	}
	return s
}

// Aging returns the AR aging worklist as of a date, for the month-end view, inside the caller's
// branch reach (branch_reach.go). A branch named outside the reach is refused.
func (s *CreditService) Aging(ctx context.Context, asOf time.Time, branchID string) ([]AgingRow, error) {
	reach, err := reachFromContext(ctx).narrow(branchID)
	if err != nil {
		return nil, err
	}
	pol, err := s.store.CreditPolicy(ctx, branchID)
	if err != nil {
		return nil, err
	}
	return s.store.AgingRowsIn(ctx, asOf, pol.AgingBucketDays, branchID, reach)
}

// ListHolds returns credit holds filtered by status ("" = all), for the accounts in the caller's
// branch reach. For a caller the account position policy refuses, any hold reason that names an
// amount, a limit or a bucket is replaced with words.
func (s *CreditService) ListHolds(ctx context.Context, status string) ([]CreditHold, error) {
	holds, err := s.store.ListHoldsIn(ctx, status, reachFromContext(ctx))
	if err != nil {
		return nil, err
	}
	for i := range holds {
		if !s.positionAllowed(ctx, holds[i].HomeBranchID) {
			holds[i].Reason = withholdHoldReason(holds[i].Kind, holds[i].Reason, holds[i].AutoPlaced)
		}
	}
	return holds, nil
}

// Evaluate is the nightly sweep (registered as the 'ar.credit_hold_sweep' JobFunc, schedule in 0104).
//
// IDEMPOTENT BY CONSTRUCTION, which the job runner requires: it converges the hold set toward what
// policy says it should be. Running it twice in a row places nothing the second time, because
// PlaceHold is a no-op when an ACTIVE hold already exists (the ux_credit_holds_one_active index makes
// that structural rather than a check we remembered to write).
//
// It never RELEASES. A machine placing a hold is policy; a machine lifting one is a credit decision,
// and credit decisions belong to a person who can be asked why. An account that pays down below the
// threshold appears on the aging screen with a lifted balance and a still-active hold, which is
// exactly the prompt a credit manager should get.
func (s *CreditService) Evaluate(ctx context.Context, branchID string) (EvaluationResult, error) {
	pol, err := s.store.CreditPolicy(ctx, branchID)
	if err != nil {
		return EvaluationResult{}, err
	}
	rows, err := s.store.AgingRows(ctx, s.clock(), pol.AgingBucketDays, branchID)
	if err != nil {
		return EvaluationResult{}, err
	}

	res := EvaluationResult{Evaluated: len(rows), DryRun: !pol.AutoHoldEnabled}
	for _, r := range rows {
		kind, reason := evaluateAccount(r, pol)
		if kind == "" {
			continue
		}
		if r.OnHold {
			continue // already held; nothing to add
		}
		// Flag-off is a DRY RUN, not a no-op. An operator gets to see the blast radius, how many
		// accounts a flip would stop at the counter tomorrow morning, before flipping it. Shipping
		// this switch without that number is how a credit policy goes live and takes the counter down.
		if !pol.AutoHoldEnabled {
			res.WouldHold++
			res.Customers = append(res.Customers, r.CustomerID)
			continue
		}
		id, placed, err := s.store.PlaceHold(ctx, r.CustomerID, kind, reason, "")
		if err != nil {
			return res, fmt.Errorf("billing: place %s hold on %s: %w", kind, r.CustomerID, err)
		}
		if placed {
			res.Placed++
			res.Customers = append(res.Customers, r.CustomerID)
			s.record(ctx, audit.SensitiveAccess{
				Action: ActCreditHoldAuto, EntityType: "credit_hold", EntityID: id,
				BranchID: r.BranchID, Purpose: kind, Reason: reason,
				Extra: map[string]any{"customer_id": r.CustomerID, "total_cents": r.TotalCents},
			})
		}
	}
	return res, nil
}

// evaluateAccount is the whole policy, as a pure function over one account's facts. It is separated
// out precisely so the rule can be tested without a database, a clock or a job runner, the three
// things that usually make a credit rule untestable and therefore unargued-with.
//
// Order matters: an account that breaches BOTH triggers is held as CREDIT_LIMIT, because that is the
// more actionable fact for whoever picks up the phone ("you are over your limit" beats "you have old
// invoices" when both are true).
func evaluateAccount(r AgingRow, pol CreditPolicy) (kind, reason string) {
	// A cash/COD account has no credit to breach, its enforcement is the dispatch gate, not a hold.
	if r.TermsDays <= 0 {
		return "", ""
	}
	if pol.LimitHoldEnabled && r.CreditLimitCents > 0 && r.TotalCents > r.CreditLimitCents+pol.LimitToleranceCents {
		return HoldCreditLimit, "open AR exceeds the credit limit"
	}
	if pol.AgingHoldEnabled && r.PastBucketCents > pol.AgingThresholdCents {
		return HoldARAging, "open AR is past due"
	}
	return "", ""
}

// withholdHoldReason ensures a caller refused by the account position policy never receives a hold
// reason that names an amount, a credit limit or an aging bucket.
func withholdHoldReason(kind, reason string, autoPlaced bool) string {
	if !autoPlaced && !holdReasonCarriesPositionFigures(reason) {
		return reason
	}
	switch kind {
	case HoldCreditLimit:
		return "open AR exceeds the credit limit"
	case HoldARAging:
		return "open AR is past due"
	case HoldNSF:
		return "a payment was returned"
	case HoldCOD:
		return "COD account with an unpaid balance"
	default:
		if holdReasonCarriesPositionFigures(reason) {
			return "account is on credit hold"
		}
		return reason
	}
}

func holdReasonCarriesPositionFigures(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '$' || (s[i] >= '0' && s[i] <= '9') {
			return true
		}
	}
	return false
}

// Override lets an authorized person permit transacting against a hold that STAYS IN PLACE, until an
// explicit expiry, with a mandatory reason.
//
// Override is not release, and conflating them is the mistake this method exists to prevent. Release
// says the account is good again; override says "this hold is correct and I am taking responsibility
// for letting it through until Friday". Collapsing the second into the first destroys the record that
// the account was ever over its limit, and that record is the entire point of the aging report.
func (s *CreditService) Override(ctx context.Context, holdID, reason string, until time.Time, actorID, branchID string) error {
	if holdID == "" {
		return errors.New("billing: a hold id is required")
	}
	if reason == "" {
		// Also enforced by a CHECK in 0104. Both, on purpose: the DB constraint is the thing that
		// cannot be forgotten by a second call site, and this message is the thing a human reads.
		return errors.New("billing: an override reason is required, an override without a reason is an unsigned cheque")
	}
	if until.IsZero() || !until.After(s.clock()) {
		return errors.New("billing: an override needs an expiry in the future, 'temporarily' must not be able to mean 'forever'")
	}
	if err := s.store.OverrideHold(ctx, holdID, reason, until, actorID); err != nil {
		return err
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: ActCreditHoldOverride, EntityType: "credit_hold", EntityID: holdID,
		ActorID: actorID, BranchID: branchID, Purpose: "CREDIT_OVERRIDE", Reason: reason,
		Extra: map[string]any{"override_until": until.UTC().Format(time.RFC3339)},
	})
	return nil
}

// Release ends a hold placed by the AR gates. A mandatory reason, audited.
//
// It deliberately REFUSES to release an NSF hold and points at till's Tier-1 path instead. Two release
// paths onto one table is already one more than ideal; two release paths with DIFFERENT authorization
// rules onto one table is a hole. NSF holds carry a Tier-1 requirement that this module does not
// enforce, so this module does not get to lift them.
func (s *CreditService) Release(ctx context.Context, holdID, reason, actorID, branchID string) error {
	if holdID == "" {
		return errors.New("billing: a hold id is required")
	}
	if reason == "" {
		return errors.New("billing: a release reason is required")
	}
	kind, err := s.store.ReleaseHold(ctx, holdID, reason, actorID)
	if err != nil {
		return err
	}
	s.record(ctx, audit.SensitiveAccess{
		Action: ActCreditHoldRelease, EntityType: "credit_hold", EntityID: holdID,
		ActorID: actorID, BranchID: branchID, Purpose: kind, Reason: reason,
	})
	return nil
}

// CanDispatch is the DIB-52 seam. See the file header for why it fails closed.
//
// Three ways an order is stopped, in the order a person would check them:
//  1. an ACTIVE credit hold with no override in force, the account is stopped, full stop;
//  2. COD (payment_terms_days = 0) with an unpaid balance, the defining rule of a COD account;
//  3. anything we could not determine.
func (s *CreditService) CanDispatch(ctx context.Context, orderID string) (bool, string) {
	if orderID == "" {
		return false, "AR check unavailable: no order id"
	}
	facts, err := s.store.OrderDispatchFacts(ctx, orderID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "AR check unavailable: order not found"
		}
		return false, "AR check unavailable: " + err.Error()
	}

	// An override IN FORCE clears the order, and it clears it against every rule below, not just the
	// hold that carries it.
	//
	// This ordering was wrong on the first attempt and the integration test caught it: overriding a
	// COD hold while the COD balance rule still fired independently made overrides useless for the
	// exact case DIB-52 exists to handle. An override is a named person taking responsibility for
	// letting this account transact until an expiry; if it does not actually let them transact, it is
	// a checkbox, not a decision. The hold stays ACTIVE, the aging report still shows the breach, and
	// the audit trail names who allowed it, those are the things that must survive, and they do.
	//
	// A COD order with NO hold row cannot be overridden, because an override attaches to a hold. The
	// fix for that order is to take the payment, which is the correct fix and the whole point of COD.
	if facts.OverrideInForce {
		return true, ""
	}

	if facts.HoldActive {
		switch facts.HoldKind {
		case HoldCOD:
			return false, "PAYMENT DUE, this COD account is on hold until the balance is paid"
		case HoldNSF:
			return false, "ACCOUNT ON HOLD, a payment was returned; the office must clear this before delivery"
		default:
			return false, "ACCOUNT ON HOLD, the office must clear this account before delivery"
		}
	}

	// COD enforcement is checked even without a hold row: a brand-new COD order has nothing to have
	// placed a hold yet, and it is precisely that order which must not leave unpaid.
	pol, err := s.store.CreditPolicy(ctx, facts.BranchID)
	if err != nil {
		return false, "AR check unavailable: " + err.Error()
	}
	if pol.CODEnforced && facts.TermsDays <= 0 && facts.UnpaidCents > 0 {
		return false, fmt.Sprintf("PAYMENT DUE, COD account, %s outstanding on this order", Money(facts.UnpaidCents))
	}
	return true, ""
}

// record forwards a sensitive-access entry when an audit recorder is wired. Audit failures are
// swallowed deliberately: a credit override must not be blocked by an audit outage, and the recorder
// logs its own failures.
func (s *CreditService) record(ctx context.Context, a audit.SensitiveAccess) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Record(ctx, a)
}

// Money renders cents as a plain dollar string for the human-readable reasons above. It is exported
// because the statement renderer needs exactly the same formatting, and two money formatters in one
// module is how a statement and a hold reason come to disagree about a cent.
func Money(cents int64) string {
	neg := cents < 0
	if neg {
		cents = -cents
	}
	s := fmt.Sprintf("$%d.%02d", cents/100, cents%100)
	if neg {
		return "-" + s
	}
	return s
}
