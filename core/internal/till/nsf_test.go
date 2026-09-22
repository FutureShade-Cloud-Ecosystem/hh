package till

// nsf_test.go, W5 A-05 unit tests (DB-free fakes). Covers the NSF reversal decisions, the same-tx
// payment.bounced emission, the runtime-config fee, the hold-aware credit gate (ACCOUNT-only deny), the
// Tier-1 hold release + audit, the replay guard, the declined-attempt log, and the till.session.completed
// emission. The full DB-level reversal/reopen + zero-tax-snapshot invariant is asserted in the integration
// suite (integration_test.go). Test IDs map to the A-05 AC matrix.

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// leadershipAt is a Tier-1 (leadership) global caller (the only tier allowed to release a credit hold).
func leadershipAt(uid string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

func seedBouncableCheque(f *fakeStore, branch, customer string, amount int64) string {
	id := f.seedPayment(PaymentRow{Method: MethodCheque, AmountCents: amount, Status: PaymentReceived,
		BranchID: branch, CustomerID: customer, GatewayRef: "chq-0001"})
	f.allocations[id] = []int64{amount} // fully allocated to one invoice
	return id
}

func countTopic(emitted []string, topic string) int {
	n := 0
	for _, t := range emitted {
		if t == topic {
			n++
		}
	}
	return n
}

// AC2 (unit half), MarkNSF flips BOUNCED, reverses the allocation, reopens the invoice, and auto-places a
// credit hold. (The DB-level reopen + zero tax_decision_snapshot writes are asserted in integration.)
func TestNSF_MarkReversesAndHolds(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-ap", trenton))
	pid := seedBouncableCheque(store, trenton, "cust-1", 50000)

	res, err := svc.MarkNSF(ctx, NSFInput{PaymentID: pid, Reason: "cheque returned NSF"})
	if err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	if res.Status != "BOUNCED" {
		t.Fatalf("status = %q, want BOUNCED", res.Status)
	}
	if res.ReversedCents != 50000 || res.ReopenedInvoices != 1 {
		t.Fatalf("reversal = %d/%d, want 50000/1", res.ReversedCents, res.ReopenedInvoices)
	}
	if !res.HoldPlaced {
		t.Fatalf("expected an auto credit hold to be placed")
	}
	held, _ := store.HasActiveHold(ctx, "cust-1")
	if !held {
		t.Fatalf("customer should carry an active hold after the bounce")
	}
	if store.payments[pid].Status != "BOUNCED" {
		t.Fatalf("payment not flipped to BOUNCED")
	}
}

// AC3, payment.bounced is emitted exactly once, in the same tx as the reversal.
func TestNSF_EmitsBouncedOnce(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-ap", trenton))
	pid := seedBouncableCheque(store, trenton, "cust-1", 12000)

	if _, err := svc.MarkNSF(ctx, NSFInput{PaymentID: pid, Reason: "nsf"}); err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	if got := countTopic(store.emitted, "payment.bounced"); got != 1 {
		t.Fatalf("payment.bounced emitted %d times, want 1", got)
	}
}

// AC4, the NSF fee is runtime config: the fee the reversal captures is whatever nsf_policy holds, not a
// hardcoded constant.
func TestNSF_FeeFromRuntimePolicy(t *testing.T) {
	store := newFakeStore()
	store.nsfPolicy = NSFPolicy{FeeCents: 7500, FeeTaxable: true, FeeAccountCode: "4200", AutoHold: true}
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-ap", trenton))
	pid := seedBouncableCheque(store, trenton, "cust-1", 30000)

	res, err := svc.MarkNSF(ctx, NSFInput{PaymentID: pid, Reason: "nsf"})
	if err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	if res.FeeCents != 7500 || !res.FeeTaxable {
		t.Fatalf("result fee = %d taxable=%v, want 7500/true", res.FeeCents, res.FeeTaxable)
	}
	if store.lastNSFIn.FeeCents != 7500 || store.lastNSFIn.FeeAccountCode != "4200" {
		t.Fatalf("store received fee = %d acct=%q, want 7500/4200", store.lastNSFIn.FeeCents, store.lastNSFIn.FeeAccountCode)
	}
}

// AC5 (gate half), the hold-aware credit gate denies an ACCOUNT (credit-terms) tender under an active hold
// but ALWAYS allows cash/card (termsAreCredit=false); with no hold it defers to the identity decision.
func TestNSF_HoldGate_BlocksAccountOnly(t *testing.T) {
	holds := &fakeHolds{active: map[string]bool{"cust-held": true}}
	gate := NewHoldAwareCreditGate(holds)
	ctx := context.Background()

	// ACCOUNT tender (termsAreCredit=true) under a hold → blocked.
	if blk := gate.Check(ctx, "cust-held", 10000, 0, 100000, true, trenton, false); blk == nil {
		t.Fatalf("account tender under a hold must be blocked")
	}
	// Even an approved override cannot buy through a hold.
	if blk := gate.Check(ctx, "cust-held", 10000, 0, 100000, true, trenton, true); blk == nil {
		t.Fatalf("a hold is a hard stop even with an approved override")
	}
	// Cash/card (termsAreCredit=false) is always allowed, hold or not.
	if blk := gate.Check(ctx, "cust-held", 10000, 0, 100000, false, trenton, false); blk != nil {
		t.Fatalf("cash/card must never be blocked by a hold: %v", blk)
	}
	// A clear customer on credit terms within limit → allowed (identity decision).
	if blk := gate.Check(ctx, "cust-clear", 10000, 0, 100000, true, trenton, false); blk != nil {
		t.Fatalf("clear customer within limit must pass: %v", blk)
	}
}

// AC5 (gate fail-closed), if the hold lookup errors on an ACCOUNT tender, the sale is DENIED (fail closed).
func TestNSF_HoldGate_FailsClosed(t *testing.T) {
	holds := &fakeHolds{err: context.DeadlineExceeded}
	gate := NewHoldAwareCreditGate(holds)
	if blk := gate.Check(context.Background(), "cust-1", 10000, 0, 100000, true, trenton, false); blk == nil {
		t.Fatalf("an errored hold lookup must fail closed (deny the account tender)")
	}
	// cash/card still allowed even when the lookup would error (it is not consulted).
	if blk := gate.Check(context.Background(), "cust-1", 10000, 0, 100000, false, trenton, false); blk != nil {
		t.Fatalf("cash/card must not be blocked: %v", blk)
	}
}

// AC5 (release half), releasing a hold is Tier-1 ONLY, requires a reason, and writes an audit row.
func TestNSF_ReleaseHold_Tier1AndAudit(t *testing.T) {
	store := newFakeStore()
	rec := &fakeRecorder{}
	svc := newTestSvc(store, nil).withCaller(testCallerFrom)
	svc.WithAudit(rec)
	// place a hold via an NSF first
	apCtx := ctxWith(context.Background(), staffAt("u-ap", trenton))
	pid := seedBouncableCheque(store, trenton, "cust-1", 20000)
	if _, err := svc.MarkNSF(apCtx, NSFInput{PaymentID: pid, Reason: "nsf"}); err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	holdID := store.activeHold["cust-1"]

	// A manager (Tier-2) may NOT release.
	mgrCtx := ctxWith(context.Background(), managerAt("u-mgr", trenton))
	if err := svc.ReleaseHold(mgrCtx, ReleaseHoldInput{HoldID: holdID, Reason: "ok now"}); err == nil {
		t.Fatalf("a manager must not be able to release a credit hold")
	}
	// Tier-1 with no reason → rejected.
	leadCtx := ctxWith(context.Background(), leadershipAt("u-gm"))
	if err := svc.ReleaseHold(leadCtx, ReleaseHoldInput{HoldID: holdID, Reason: ""}); err == nil {
		t.Fatalf("release without a reason must be rejected")
	}
	// Tier-1 with a reason → released + audited.
	if err := svc.ReleaseHold(leadCtx, ReleaseHoldInput{HoldID: holdID, Reason: "customer paid by wire"}); err != nil {
		t.Fatalf("Tier-1 release: %v", err)
	}
	if store.holds[holdID].Status != HoldReleased {
		t.Fatalf("hold not released")
	}
	if held, _ := store.HasActiveHold(leadCtx, "cust-1"); held {
		t.Fatalf("customer should no longer be on hold")
	}
	if rec.calls != 1 || rec.last.Action != ActCreditHoldRelease || rec.last.Reason != "customer paid by wire" {
		t.Fatalf("expected one audited release with reason, got calls=%d last=%+v", rec.calls, rec.last)
	}
}

// AC6, a second NSF on an already-BOUNCED payment is a 409 (idempotent replay guard).
func TestNSF_ReplayGuard(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-ap", trenton))
	pid := seedBouncableCheque(store, trenton, "cust-1", 15000)

	if _, err := svc.MarkNSF(ctx, NSFInput{PaymentID: pid, Reason: "nsf"}); err != nil {
		t.Fatalf("first MarkNSF: %v", err)
	}
	_, err := svc.MarkNSF(ctx, NSFInput{PaymentID: pid, Reason: "nsf again"})
	wantStatus(t, err, 409)
	// the replay must NOT place a second hold or emit a second event
	if got := countTopic(store.emitted, "payment.bounced"); got != 1 {
		t.Fatalf("payment.bounced emitted %d times after replay, want 1", got)
	}
}

// AC7, the declined-attempt log persists DECLINED/ERROR attempts, validates the result vocabulary, and is
// idempotent on the key.
func TestNSF_PaymentAttempts(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-ap", trenton))

	if err := svc.RecordPaymentAttempt(ctx, PaymentAttempt{IdempotencyKey: "k1", AmountCents: 5000, Method: MethodCard, Result: "DECLINED", Processor: "run_payments"}); err != nil {
		t.Fatalf("record attempt: %v", err)
	}
	// replay on the same key is a no-op
	if err := svc.RecordPaymentAttempt(ctx, PaymentAttempt{IdempotencyKey: "k1", AmountCents: 5000, Method: MethodCard, Result: "DECLINED"}); err != nil {
		t.Fatalf("replay attempt: %v", err)
	}
	if len(store.attempts) != 1 {
		t.Fatalf("attempts = %d, want 1 (idempotent)", len(store.attempts))
	}
	// an invalid result is rejected
	if err := svc.RecordPaymentAttempt(ctx, PaymentAttempt{IdempotencyKey: "k2", AmountCents: 5000, Result: "APPROVED"}); err == nil {
		t.Fatalf("an APPROVED result must be rejected (only DECLINED/ERROR are attempts)")
	}
	// a non-positive amount is rejected
	if err := svc.RecordPaymentAttempt(ctx, PaymentAttempt{IdempotencyKey: "k3", AmountCents: 0, Result: "ERROR"}); err == nil {
		t.Fatalf("a non-positive amount must be rejected")
	}
}

// AC9, closing a till emits till.session.completed (with over/short facts) in the same tx as the close.
func TestNSF_TillCloseEmitsSessionCompleted(t *testing.T) {
	store := newFakeStore()
	svc := newTestSvc(store, nil)
	ctx := ctxWith(context.Background(), staffAt("u-op", trenton))
	// A COUNTED session with no tenders/movements and a zero float → over/short 0 → a staff close is allowed.
	sid := store.seedSession(TillSession{BranchID: trenton, Station: "MAIN", Status: StatusCounted, OpenedBy: "u-op", FloatCents: 0})

	if err := svc.CompleteTill(ctx, CompleteInput{TillSessionID: sid}); err != nil {
		t.Fatalf("CompleteTill: %v", err)
	}
	if got := countTopic(store.emitted, "till.session.completed"); got != 1 {
		t.Fatalf("till.session.completed emitted %d times, want 1", got)
	}
	if store.sessions[sid].Status != StatusClosed {
		t.Fatalf("session not CLOSED")
	}
}

// fakeHolds is a HoldChecker for the credit-gate tests.
type fakeHolds struct {
	active map[string]bool
	err    error
}

func (f *fakeHolds) HasActiveHold(_ context.Context, customerID string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.active[customerID], nil
}
