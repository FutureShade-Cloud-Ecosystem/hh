package pos

// card_refund_attempts_test.go, HH-767 (HH-690b) Leg A: the manager
// resolution of open card refund attempts. A crash between the two
// transactions of the HH-690 flow, or an unknown processor outcome, leaves a
// pos_card_refund_attempts row open (IN_FLIGHT or UNBOOKED) with its amount
// reserved against the sale's card capture ceiling. These tests prove the
// manager surface: the list is branch fenced, the resolve refuses the person
// who started the refund (separation of duties, even when they hold manager
// tier), REFUNDED moves IN_FLIGHT to UNBOOKED with the processor's refund_ref
// so a same-key retry books without a second processor call, NOT_REFUNDED
// closes FAILED and releases the ceiling, and an UNBOOKED row that already
// carries a processor ref cannot be closed not-refunded.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payment"
)

// mgrCtx stamps a manager caller onto a context exactly as the guards do:
// the tier the test chooses, the branch scope it grants, and the actor id
// the separation-of-duties comparison reads.
func mgrCtx(userID string, tier identity.Tier, branches ...string) context.Context {
	grants := make(map[string]bool, len(branches))
	for _, b := range branches {
		grants[b] = true
	}
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: userID, RoleCode: "BRANCH_MANAGER", Tier: tier,
		Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: grants,
	})
}

// plantAttempt installs one row of the fake pos_card_refund_attempts under
// the caller's own facts, with the row id the resolve route names.
func plantAttempt(f *fakeStore, id, refundKey, orderID, branchID, status, refundRef, initiatedBy string, amount int64) {
	f.cardAttempts[refundKey] = fakeCardAttempt{
		id: id,
		fact: cardRefundFact{
			RefundKey: refundKey, OrderID: orderID, BranchID: branchID,
			CustomerID: "cust-abc", GatewayRef: "rp_orig_capture", RefundRef: refundRef,
			AmountCents: amount, InitiatedBy: initiatedBy,
		},
		status: status,
	}
}

func asClientErr(t *testing.T, err error) clientError {
	t.Helper()
	var ce clientError
	if !errors.As(err, &ce) {
		t.Fatalf("want a client error, got %T %v", err, err)
	}
	return ce
}

// The list: a branch-scoped manager sees only the reachable branch's open
// attempts, both open statuses by default, one when filtered, a refusal for
// any other filter, and nothing at all for a frontline caller.
func TestCardRefundAttempts_ListScopesByBranchAndStatus(t *testing.T) {
	h := newHarness()
	plantAttempt(h.store, "11111111-1111-4111-8111-1111111111a1", "refund:k1", "order-1", "branch-trt", "IN_FLIGHT", "", "u-clerk", 4520)
	plantAttempt(h.store, "11111111-1111-4111-8111-1111111111a2", "refund:k2", "order-1", "branch-trt", "UNBOOKED", "rp_kept", "u-clerk", 4520)
	plantAttempt(h.store, "11111111-1111-4111-8111-1111111111a3", "refund:k3", "order-9", "branch-kgx", "IN_FLIGHT", "", "u-clerk", 1000)
	plantAttempt(h.store, "11111111-1111-4111-8111-1111111111a4", "refund:k4", "order-1", "branch-trt", "BOOKED", "rp_b", "u-clerk", 4520)
	plantAttempt(h.store, "11111111-1111-4111-8111-1111111111a5", "refund:k5", "order-1", "branch-trt", "FAILED", "", "u-clerk", 4520)

	ctx := mgrCtx("u-mgr", identity.TierManager, "branch-trt")
	items, err := h.svc.ListOpenCardRefundAttempts(ctx, "")
	if err != nil {
		t.Fatalf("list open attempts: %v", err)
	}
	if len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatalf("the Trenton manager sees only the two open Trenton attempts, got %+v", items)
	}
	for _, it := range items {
		if it.BranchID != "branch-trt" || (it.Status != "IN_FLIGHT" && it.Status != "UNBOOKED") {
			t.Fatalf("an out-of-scope or closed row leaked into the list: %+v", it)
		}
		if it.RefundKey == "" || it.AmountCents != 4520 || it.InitiatedBy != "u-clerk" {
			t.Fatalf("the row carries its ledger facts: %+v", it)
		}
	}

	items, err = h.svc.ListOpenCardRefundAttempts(ctx, "in_flight")
	if err != nil {
		t.Fatalf("list IN_FLIGHT: %v", err)
	}
	if len(items) != 1 || items[0].ID != "11111111-1111-4111-8111-1111111111a1" {
		t.Fatalf("the status filter keeps only IN_FLIGHT, got %+v", items)
	}

	if _, err := h.svc.ListOpenCardRefundAttempts(ctx, "BOOKED"); asClientErr(t, err).status != http.StatusUnprocessableEntity {
		t.Fatalf("any other filter refuses 422, got %v", err)
	}

	// A frontline caller may not list at all.
	front := mgrCtx("u-clerk", identity.TierFrontline, "branch-trt")
	if _, err := h.svc.ListOpenCardRefundAttempts(front, ""); asClientErr(t, err).status != http.StatusForbidden {
		t.Fatalf("a frontline caller is refused 403, got %v", err)
	}
	// A leadership (admin tier) caller may.
	admin := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: identity.StatusActive,
	})
	if _, err := h.svc.ListOpenCardRefundAttempts(admin, ""); err != nil {
		t.Fatalf("an admin tier caller lists across branches: %v", err)
	}
}

// Separation of duties: another branch's attempt is a 404 (no existence
// leak), the initiator is refused 403 even holding manager tier, a different
// manager at the sale's branch resolves and the sensitive-access audit row
// names the outcome, the status, the amount and the approver.
func TestCardRefundAttempts_ResolveSeparationOfDuties(t *testing.T) {
	h := newHarness()
	const id = "11111111-1111-4111-8111-1111111111b1"
	plantAttempt(h.store, id, "refund:k1", "order-1", "branch-trt", "IN_FLIGHT", "", "u-clerk", 4520)

	// Another branch's manager: not found, no existence leak.
	if _, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-kgx", identity.TierManager, "branch-kgx"), id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_mgr_1"}); asClientErr(t, err).status != http.StatusNotFound {
		t.Fatalf("a cross-branch resolve is a 404, got %v", err)
	}

	// The initiator, even holding manager tier, is refused.
	if _, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-clerk", identity.TierManager, "branch-trt"), id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_mgr_1"}); asClientErr(t, err).status != http.StatusForbidden {
		t.Fatalf("the initiator is refused 403 even with manager tier, got %v", err)
	}

	// A different manager at the sale's branch resolves; the audit row follows.
	out, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_mgr_1", Note: "processor console shows the reversal"})
	if err != nil {
		t.Fatalf("the different manager resolves: %v", err)
	}
	if out.Status != "UNBOOKED" || out.RefundRef != "rp_mgr_1" || out.ResolvedBy != "u-mgr" || out.ResolutionNote == "" || out.ResolvedAt == nil {
		t.Fatalf("the resolved row carries the resolution facts: %+v", out)
	}
	var row *audit.SensitiveAccess
	for i := range h.rec.rows {
		if h.rec.rows[i].Action == audit.ActCardRefundAttemptResolved {
			row = &h.rec.rows[i]
		}
	}
	if row == nil {
		t.Fatalf("the resolve writes its %s audit row", audit.ActCardRefundAttemptResolved)
	}
	if row.EntityType != "pos.card_refund_attempt" || row.EntityID != id || row.ActorID != "u-mgr" ||
		row.BranchID != "branch-trt" || row.Purpose != "POS_CARD_REFUND_RESOLUTION" {
		t.Fatalf("the audit row names the attempt, the branch and the approver: %+v", row)
	}
	if row.Extra["outcome"] != CardRefundOutcomeRefunded || row.Extra["status"] != "UNBOOKED" ||
		row.Extra["amount_cents"] != int64(4520) || row.Extra["approver_id"] != "u-mgr" || row.Reason == "" {
		t.Fatalf("the audit row carries the outcome, the status, the amount and the approver: %+v", row.Extra)
	}
}

// The audit row rides the settle's own transaction (PR #501 round 1 P2-2): a
// recorder whose transactional write fails aborts the settle with an error,
// never a best effort row dropped after the commit.
func TestCardRefundAttempts_ResolveAuditFailureAbortsSettle(t *testing.T) {
	h := newHarness()
	h.svc = h.svc.WithAudit(failingTxAudit{})
	const id = "11111111-1111-4111-8111-1111111111b9"
	plantAttempt(h.store, id, "refund:k9", "order-1", "branch-trt", "IN_FLIGHT", "", "u-clerk", 4520)
	_, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_mgr_9"})
	if err == nil {
		t.Fatal("a failed audit write must abort the settle, got success")
	}
}

// REFUNDED on an IN_FLIGHT attempt: the row moves to UNBOOKED with the
// manager's processor ref stamped, and the same-key retry of the return
// books in tx 2 without a second processor call.
func TestCardRefundAttempts_RefundedMovesInFlightToUnbookedAndRetryBooks(t *testing.T) {
	h := newHarness()
	seedCardReturnable(h)
	// The unknown outcome holds the attempt IN_FLIGHT, attributed to the clerk.
	h.refunder.err = fmt.Errorf("run_payments: refund: %w: status 504", payment.ErrProcessorOutcomeUnknown)
	clerk := identity.ContextWithStaffUserID(context.Background(), "u-clerk")
	if _, err := h.svc.Return(clerk, cardInput()); asClientErr(t, err).status != http.StatusBadGateway {
		t.Fatalf("the unknown outcome answers 502, got %v", err)
	}
	a := h.store.cardAttempts["refund:ret-key-1"]
	if a.status != "IN_FLIGHT" || a.fact.InitiatedBy != "u-clerk" {
		t.Fatalf("the held attempt names who started it: %+v", a)
	}
	if a.id == "" {
		t.Fatal("the staged attempt carries its row id")
	}

	// REFUNDED without a ref (and none on the row) refuses.
	if _, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), a.id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded}); asClientErr(t, err).status != http.StatusUnprocessableEntity {
		t.Fatalf("REFUNDED needs the processor's refund_ref, got %v", err)
	}

	out, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), a.id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_mgr_checked", Note: "confirmed on the processor console"})
	if err != nil {
		t.Fatalf("the manager resolves REFUNDED: %v", err)
	}
	if out.Status != "UNBOOKED" || out.RefundRef != "rp_mgr_checked" {
		t.Fatalf("the row stands UNBOOKED with the manager's ref: %+v", out)
	}

	// The retry under the same key books the return without a second call.
	h.refunder.err = nil
	if _, err := h.svc.Return(clerk, cardInput()); err != nil {
		t.Fatalf("the retry must book the return: %v", err)
	}
	if n := len(h.refunder.keyCalls); n != 1 {
		t.Fatalf("the retry reuses the resolved ref, no second processor call, got %d", n)
	}
	if a := h.store.cardAttempts["refund:ret-key-1"]; a.status != "BOOKED" {
		t.Fatalf("the booked retry closes the attempt, got %+v", a)
	}
	if len(h.store.returnRows) != 1 {
		t.Fatal("the retry booked the return row")
	}
}

// NOT_REFUNDED on an IN_FLIGHT attempt closes it FAILED, releasing the
// capture ceiling so a fresh return can proceed.
func TestCardRefundAttempts_NotRefundedClosesFailedAndReleasesCeiling(t *testing.T) {
	h := newHarness()
	seedCardReturnable(h)
	// The capture holds exactly the rail total, so the held amount is the
	// whole ceiling (the P1-2 shape).
	oc := h.store.retCtx["order-1"]
	oc.CapturedCents = 4520
	h.store.retCtx["order-1"] = oc
	h.refunder.err = fmt.Errorf("run_payments: refund: %w: status 504", payment.ErrProcessorOutcomeUnknown)
	clerk := identity.ContextWithStaffUserID(context.Background(), "u-clerk")
	if _, err := h.svc.Return(clerk, cardInput()); asClientErr(t, err).status != http.StatusBadGateway {
		t.Fatalf("the unknown outcome answers 502, got %v", err)
	}
	a := h.store.cardAttempts["refund:ret-key-1"]
	if a.status != "IN_FLIGHT" {
		t.Fatalf("the unknown outcome holds the attempt IN_FLIGHT: %+v", a)
	}
	// While it is open the ceiling is reserved: a different-key refund refuses.
	in2 := cardInput()
	in2.IdempotencyKey = "ret-key-2"
	if _, err := h.svc.Return(clerk, in2); asClientErr(t, err).status != http.StatusUnprocessableEntity {
		t.Fatalf("the held amount stays reserved against the capture, got %v", err)
	}

	out, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), a.id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeNotRefunded, Note: "the processor never received it"})
	if err != nil {
		t.Fatalf("the manager resolves NOT_REFUNDED: %v", err)
	}
	if out.Status != "FAILED" || out.ResolvedBy != "u-mgr" {
		t.Fatalf("the row closes FAILED with its resolver: %+v", out)
	}

	// The ceiling is released: the fresh-key return now proceeds and books.
	h.refunder.err = nil
	if _, err := h.svc.Return(clerk, in2); err != nil {
		t.Fatalf("the fresh return proceeds once the ceiling is released: %v", err)
	}
	if n := len(h.refunder.keyCalls); n != 2 {
		t.Fatalf("the held key never reached the processor and the fresh key did, got %d calls", n)
	}
}

// The live-request fence (PR #501 round 1 P1-1): an IN_FLIGHT row whose
// unknown outcome is not recorded and whose attempted_at sits inside the
// processor client's own timeout plus the crash margin refuses BOTH
// outcomes in words naming the live request; the row settles once the
// unknown outcome is recorded, and the crash case (the window long past)
// settles without one.
func TestCardRefundAttempts_LiveRequestFenceHoldsTheSettle(t *testing.T) {
	h := newHarness()
	const id = "11111111-1111-4111-8111-1111111111d1"
	now := time.Now().UTC()
	plantAttempt(h.store, id, "refund:kf", "order-1", "branch-trt", "IN_FLIGHT", "", "u-clerk", 4520)
	a := h.store.cardAttempts["refund:kf"]
	a.attemptedAt = now.Format(time.RFC3339)
	h.store.cardAttempts["refund:kf"] = a

	// Inside the live-request window: REFUNDED refuses with the live words.
	err := func() error {
		_, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id,
			ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_fence"})
		return err
	}()
	if ce := asClientErr(t, err); ce.status != http.StatusConflict || !strings.Contains(ce.Error(), "may still be running at the processor") {
		t.Fatalf("REFUNDED inside the live-request window refuses 409 in the live words, got %d %v", ce.status, err)
	}
	// NOT_REFUNDED refuses with the same words.
	_, err = h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeNotRefunded, Note: "checked"})
	if ce := asClientErr(t, err); ce.status != http.StatusConflict || !strings.Contains(ce.Error(), "may still be running at the processor") {
		t.Fatalf("NOT_REFUNDED inside the live-request window refuses 409 in the live words, got %d %v", ce.status, err)
	}

	// The unknown outcome recorded (processor_at stamped): the row settles.
	a = h.store.cardAttempts["refund:kf"]
	stamped := now.Format(time.RFC3339)
	a.processorAt = &stamped
	h.store.cardAttempts["refund:kf"] = a
	out, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_fence", Note: "recorded unknown, then confirmed"})
	if err != nil {
		t.Fatalf("a recorded unknown outcome settles the row: %v", err)
	}
	if out.Status != "UNBOOKED" || out.RefundRef != "rp_fence" {
		t.Fatalf("the settled row carries its facts: %+v", out)
	}

	// The crash case: no recorded outcome, the window long past, settles.
	const id2 = "11111111-1111-4111-8111-1111111111d2"
	plantAttempt(h.store, id2, "refund:kf2", "order-1", "branch-trt", "IN_FLIGHT", "", "u-clerk", 4520)
	a2 := h.store.cardAttempts["refund:kf2"]
	a2.attemptedAt = now.Add(-2 * (payment.ProcessorClientTimeout + cardRefundLiveRequestMargin)).Format(time.RFC3339)
	h.store.cardAttempts["refund:kf2"] = a2
	out, err = h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id2,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeNotRefunded, Note: "the crash left it, the processor shows nothing"})
	if err != nil {
		t.Fatalf("the crash case settles once the live-request window is past: %v", err)
	}
	if out.Status != "FAILED" {
		t.Fatalf("the crash case closes FAILED, got %+v", out)
	}
}

// The duplicate-ref fence (PR #501 round 1 P1-2): one processor ref never
// settles a second attempt and never settles over a payment already booked
// under it; both refuse 409 in words naming the ref.
func TestCardRefundAttempts_DuplicateRefundRefRefuses409(t *testing.T) {
	h := newHarness()
	const id1 = "11111111-1111-4111-8111-1111111111e1"
	const id2 = "11111111-1111-4111-8111-1111111111e2"
	plantAttempt(h.store, id1, "refund:za", "order-1", "branch-trt", "IN_FLIGHT", "", "u-clerk", 4520)
	plantAttempt(h.store, id2, "refund:zb", "order-1", "branch-trt", "IN_FLIGHT", "", "u-clerk", 4520)

	// The first attempt settles REFUNDED with the shared ref.
	if _, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id1,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_zz_same", Note: "first settle"}); err != nil {
		t.Fatalf("the first attempt settles with the ref: %v", err)
	}
	// The second attempt with the SAME ref refuses in words naming it.
	_, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id2,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_zz_same", Note: "second settle"})
	if ce := asClientErr(t, err); ce.status != http.StatusConflict || !strings.Contains(ce.Error(), "rp_zz_same") ||
		!strings.Contains(ce.Error(), "already recorded against another card refund") {
		t.Fatalf("the shared ref refuses the second settle 409 in words naming it, got %d %v", ce.status, err)
	}
	// A ref already booked as a payment's gateway_ref refuses the same way.
	h.store.paymentGatewayRefs["rp_zz_pay"] = true
	_, err = h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id2,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_zz_pay", Note: "over a booked payment"})
	if ce := asClientErr(t, err); ce.status != http.StatusConflict || !strings.Contains(ce.Error(), "rp_zz_pay") {
		t.Fatalf("a ref already booked as a payment refuses the settle 409, got %d %v", ce.status, err)
	}
	// A ref nowhere else still settles.
	if _, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id2,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_zz_own", Note: "its own ref"}); err != nil {
		t.Fatalf("the second attempt settles with its own ref: %v", err)
	}
}

// NOT_REFUNDED on an UNBOOKED attempt that already carries the processor's
// reversal ref refuses: that money moved, it must be booked or settled
// REFUNDED, never closed as not refunded.
func TestCardRefundAttempts_NotRefundedOnUnbookedWithRefRefuses409(t *testing.T) {
	h := newHarness()
	const id = "11111111-1111-4111-8111-1111111111c1"
	plantAttempt(h.store, id, "refund:k1", "order-1", "branch-trt", "UNBOOKED", "rp_already_moved", "u-clerk", 4520)
	_, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id,
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeNotRefunded})
	if asClientErr(t, err).status != http.StatusConflict {
		t.Fatalf("an UNBOOKED row with a processor ref refuses NOT_REFUNDED, got %v", err)
	}
	// A malformed id and a bad outcome refuse 422; a decided row refuses 409.
	if _, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), "not-a-uuid",
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_x"}); asClientErr(t, err).status != http.StatusUnprocessableEntity {
		t.Fatalf("a malformed id refuses 422, got %v", err)
	}
	if _, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), id,
		ResolveCardRefundAttemptInput{Outcome: "MAYBE"}); asClientErr(t, err).status != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown outcome refuses 422, got %v", err)
	}
	plantAttempt(h.store, "11111111-1111-4111-8111-1111111111c2", "refund:k2", "order-1", "branch-trt", "BOOKED", "rp_b", "u-clerk", 4520)
	if _, err := h.svc.ResolveCardRefundAttempt(mgrCtx("u-mgr", identity.TierManager, "branch-trt"), "11111111-1111-4111-8111-1111111111c2",
		ResolveCardRefundAttemptInput{Outcome: CardRefundOutcomeRefunded, RefundRef: "rp_x"}); asClientErr(t, err).status != http.StatusConflict {
		t.Fatalf("a decided row refuses 409, got %v", err)
	}
}
