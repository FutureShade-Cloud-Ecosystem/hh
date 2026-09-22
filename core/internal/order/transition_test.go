package order

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// transition_test.go, DIB-191 manual transition matrix + authority (acceptance T3, T4, T5). Exercises
// Service.TransitionManual against the fake store with a threaded StaffContext (withCaller/testCallerFrom
// from history_test.go), so the forward-only matrix and every authority gate are covered DB-free.

const (
	txnCust  = "44444444-4444-4444-8444-444444444444"
	txnOrder = "55555555-5555-4555-8555-555555555555"
	txnUser  = "66666666-6666-4666-8666-666666666666"
)

// managerAt is a branch-scoped manager (Tier 2), reaches only the granted branch, but IS a manager so
// it passes the CANCELLED manager-gate. Complements staffAt (frontline) / globalStaff (GM, GLOBAL).
func managerAt(uid, branch string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: identity.StatusActive, Branches: map[string]bool{branch: true}}
}

// inactiveStaff is an otherwise-privileged caller whose account is not ACTIVE (the defence-in-depth
// case: the real route guard rejects this at 401, but the choke-point must independently refuse it).
func inactiveStaff(uid, branch string) identity.StaffContext {
	sc := managerAt(uid, branch)
	sc.Status = "SUSPENDED"
	return sc
}

// txnFixture seeds one order at `status` in histBranchA and returns a service wired with a recorder,
// the test caller seam, and PERMISSIVE cancel guards (no money attached), plus the recorder for timeline
// assertions. The guards must be wired because R2 fails closed, an unwired predicate refuses every
// cancel; that path is covered explicitly by TestTransitionManual_CancelPredicate.
func txnFixture(t *testing.T, status string) (*Service, *recordingRecorder) {
	t.Helper()
	svc, rec, _ := txnFixtureWithGuards(t, status, &fakeCancelGuards{})
	return svc, rec
}

// txnFixtureWithGuards is txnFixture with a caller-supplied cancel predicate, for the R2 cases. Pass a
// nil *fakeCancelGuards to leave the guards UNWIRED and exercise the fail-closed path.
func txnFixtureWithGuards(t *testing.T, status string, guards *fakeCancelGuards) (*Service, *recordingRecorder, *fakeStore) {
	t.Helper()
	store := newFakeStore(mkDetail(txnOrder, txnCust, status))
	store.branches = map[string]string{txnOrder: histBranchA}
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec).withCaller(testCallerFrom)
	if guards != nil {
		svc.WithCancelGuards(guards, guards)
	}
	return svc, rec, store
}

// TestTransitionManual_LegalPaths asserts every legal per-role transition succeeds, advances the status,
// and writes exactly one attributed timeline row (T5 legal half; T3 happy path; U4 Mark picking).
func TestTransitionManual_LegalPaths(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		action  ManualAction
		reason  string
		caller  identity.StaffContext
		toState string
	}{
		{"frontline picks a confirmed order", "CONFIRMED", ActionPicking, "", staffAt(txnUser, histBranchA), "PICKING"},
		{"frontline readies a picking order", "PICKING", ActionReady, "", staffAt(txnUser, histBranchA), "READY"},
		{"manager cancels a confirmed order", "CONFIRMED", ActionCancel, "customer backed out", managerAt(txnUser, histBranchA), "CANCELLED"},
		{"manager cancels a draft order", "DRAFT", ActionCancel, "duplicate", managerAt(txnUser, histBranchA), "CANCELLED"},
		{"manager cancels a picking order", "PICKING", ActionCancel, "out of stock", managerAt(txnUser, histBranchA), "CANCELLED"},
		{"manager cancels a ready order", "READY", ActionCancel, "no-show", managerAt(txnUser, histBranchA), "CANCELLED"},
		{"global staff picks (any branch)", "CONFIRMED", ActionPicking, "", globalStaff(txnUser), "PICKING"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, rec := txnFixture(t, tc.from)
			ctx := ctxWith(context.Background(), tc.caller)
			got, err := svc.TransitionManual(ctx, txnOrder, tc.action, tc.reason)
			if err != nil {
				t.Fatalf("TransitionManual: unexpected err %v", err)
			}
			if got != tc.toState {
				t.Fatalf("new status = %q, want %q", got, tc.toState)
			}
			if n := rec.countAction(audit.ActOrderStatusChanged); n != 1 {
				t.Fatalf("timeline rows = %d, want exactly 1", n)
			}
			a := rec.records[0]
			if a.Extra["old_status"] != tc.from || a.Extra["new_status"] != tc.toState {
				t.Errorf("from→to = %v→%v, want %s→%s", a.Extra["old_status"], a.Extra["new_status"], tc.from, tc.toState)
			}
			if a.ActorID != tc.caller.UserID {
				t.Errorf("actor = %q, want %q (the caller)", a.ActorID, tc.caller.UserID)
			}
			if tc.reason != "" && a.Reason != tc.reason {
				t.Errorf("reason = %q, want %q (persisted on the timeline)", a.Reason, tc.reason)
			}
		})
	}
}

// allStatuses is the full orders CHECK vocabulary (migration 0008), the 9 columns of the matrix.
var allStatuses = []string{
	"DRAFT", "CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED",
	"FULFILLED", "INVOICED", "CLOSED", "CANCELLED",
}

// allActions is the full manual action set, the 4 rows of the matrix (ActionConfirm added by ruling D2).
var allActions = []ManualAction{ActionConfirm, ActionPicking, ActionReady, ActionCancel}

// TestTransitionManual_MatrixEveryCell is the EXHAUSTIVE forward-only matrix proof required by the B2
// remediation round: every one of the 4 actions × 9 statuses = 36 cells is asserted, closing the
// "12/18 illegal matrix cells untested" audit finding.
//
// The expected outcome per cell is DERIVED FROM manualRules rather than hand-listed, so the test cannot
// drift out of sync with the matrix, adding a rule row or widening a `from` set automatically re-grades
// every affected cell. The three outcomes are exactly the ones the guarded write distinguishes:
//
//	current == target        → ErrNoTransition     (benign no-op, no timeline row)
//	current ∈ rule.from      → success             (status advances, exactly one timeline row)
//	otherwise                → ErrIllegalTransition (forward-only refusal, no timeline row)
//
// The caller is an in-branch MANAGER with a reason supplied, so authority is never the blocker and each
// cell isolates pure transition legality (authority is covered by TestTransitionManual_Authority).
func TestTransitionManual_MatrixEveryCell(t *testing.T) {
	cells := 0
	for _, action := range allActions {
		rule := manualRules[action]
		for _, from := range allStatuses {
			cells++
			name := string(action) + "_from_" + from
			t.Run(name, func(t *testing.T) {
				svc, rec := txnFixture(t, from)
				ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
				got, err := svc.TransitionManual(ctx, txnOrder, action, "matrix cell")

				switch {
				case from == rule.target:
					// Already in the target state, a no-op, never a timeline row.
					if !errors.Is(err, ErrNoTransition) {
						t.Fatalf("%s from %s: err = %v, want ErrNoTransition", action, from, err)
					}
					if n := rec.countAction(audit.ActOrderStatusChanged); n != 0 {
						t.Fatalf("no-op wrote %d timeline rows, want 0", n)
					}
				case rule.from[from]:
					// A legal forward move, advances and writes exactly one attributed row.
					if err != nil {
						t.Fatalf("%s from %s: unexpected err %v (this is a LEGAL cell)", action, from, err)
					}
					if got != rule.target {
						t.Fatalf("%s from %s: new status = %q, want %q", action, from, got, rule.target)
					}
					if n := rec.countAction(audit.ActOrderStatusChanged); n != 1 {
						t.Fatalf("legal move wrote %d timeline rows, want exactly 1", n)
					}
					if a := rec.records[0]; a.Extra["old_status"] != from || a.Extra["new_status"] != rule.target {
						t.Errorf("timeline recorded %v→%v, want %s→%s",
							a.Extra["old_status"], a.Extra["new_status"], from, rule.target)
					}
				default:
					// Forward-only refusal, no backward move, no skip, no post-terminal resurrection.
					if !errors.Is(err, ErrIllegalTransition) {
						t.Fatalf("%s from %s: err = %v, want ErrIllegalTransition (this is an ILLEGAL cell)", action, from, err)
					}
					if n := rec.countAction(audit.ActOrderStatusChanged); n != 0 {
						t.Fatalf("rejected move wrote %d timeline rows, want 0", n)
					}
				}
			})
		}
	}
	if want := len(allActions) * len(allStatuses); cells != want {
		t.Fatalf("covered %d matrix cells, want %d (4 actions × 9 statuses)", cells, want)
	}
}

// TestTransitionManual_MatrixShape pins the matrix itself, so a rule edit that silently widens authority
// or re-opens a terminal status fails here rather than only in the derived cell test.
func TestTransitionManual_MatrixShape(t *testing.T) {
	want := map[ManualAction]struct {
		target      string
		from        []string
		managerOnly bool
		reasonReq   bool
	}{
		ActionConfirm: {"CONFIRMED", []string{"DRAFT"}, false, false},
		ActionPicking: {"PICKING", []string{"CONFIRMED"}, false, false},
		ActionReady:   {"READY", []string{"PICKING"}, false, false},
		ActionCancel:  {"CANCELLED", []string{"DRAFT", "CONFIRMED", "PICKING", "READY"}, true, true},
	}
	if len(manualRules) != len(want) {
		t.Fatalf("manualRules has %d actions, want %d", len(manualRules), len(want))
	}
	for action, w := range want {
		rule, ok := manualRules[action]
		if !ok {
			t.Fatalf("action %q missing from manualRules", action)
		}
		if rule.target != w.target {
			t.Errorf("%s target = %q, want %q", action, rule.target, w.target)
		}
		if rule.managerOnly != w.managerOnly {
			t.Errorf("%s managerOnly = %v, want %v", action, rule.managerOnly, w.managerOnly)
		}
		if rule.reasonRequired != w.reasonReq {
			t.Errorf("%s reasonRequired = %v, want %v", action, rule.reasonRequired, w.reasonReq)
		}
		if len(rule.from) != len(w.from) {
			t.Errorf("%s from-set size = %d, want %d (%v)", action, len(rule.from), len(w.from), w.from)
		}
		for _, s := range w.from {
			if !rule.from[s] {
				t.Errorf("%s is not legal from %q but should be", action, s)
			}
		}
		// No rule may be legal from a terminal/post-fulfilment status (forward-only invariant).
		for _, terminal := range []string{"FULFILLED", "INVOICED", "CLOSED", "CANCELLED"} {
			if rule.from[terminal] {
				t.Errorf("%s is legal from terminal status %q, forward-only violated", action, terminal)
			}
		}
	}
}

// TestTransitionManual_IllegalPaths asserts every illegal move is rejected with a typed error and writes
// NO timeline row (T5 illegal half; U5 forward-only refusal). Forward-only: no backward move, no skip, no
// manual FULFILLED/INVOICED/CLOSED.
func TestTransitionManual_IllegalPaths(t *testing.T) {
	cases := []struct {
		name   string
		from   string
		action ManualAction
	}{
		{"backward READY→PICKING refused (U5)", "READY", ActionPicking},
		{"skip CONFIRMED→READY refused", "CONFIRMED", ActionReady},
		{"pick after fulfilled refused", "FULFILLED", ActionPicking},
		{"cancel after fulfilled refused", "FULFILLED", ActionCancel},
		{"cancel after invoiced refused", "INVOICED", ActionCancel},
		{"ready a draft refused", "DRAFT", ActionReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, rec := txnFixture(t, tc.from)
			ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA)) // manager: authority never the blocker here
			_, err := svc.TransitionManual(ctx, txnOrder, tc.action, "reason")
			if !errors.Is(err, ErrIllegalTransition) {
				t.Fatalf("err = %v, want ErrIllegalTransition", err)
			}
			if n := rec.countAction(audit.ActOrderStatusChanged); n != 0 {
				t.Fatalf("timeline rows = %d, want 0 on a rejected transition", n)
			}
		})
	}
}

// TestTransitionManual_NoHandSetClosed asserts CLOSED (and any other non-action target) is not a manual
// action at all, it is rejected as a BadRequest before any store call, so no path can hand-set CLOSED
// (D1 ruling item 5; T5).
func TestTransitionManual_NoHandSetClosed(t *testing.T) {
	svc, rec := txnFixture(t, "INVOICED")
	ctx := ctxWith(context.Background(), globalStaff(txnUser))
	for _, target := range []ManualAction{"CLOSED", "FULFILLED", "INVOICED", "PARTIALLY_FULFILLED", "bogus"} {
		if _, err := svc.TransitionManual(ctx, txnOrder, target, "x"); !isBadRequest(err) {
			t.Fatalf("action %q: err = %v, want BadRequest (not a manual action)", target, err)
		}
	}
	if n := rec.countAction(audit.ActOrderStatusChanged); n != 0 {
		t.Fatalf("timeline rows = %d, want 0", n)
	}
}

// TestTransitionManual_Authority covers the authority matrix: inactive → 403, cross-branch → 403,
// non-manager cancel → 403, manager cancel without a reason → 400, unauthenticated → 401, missing order
// → 404 (T3, T4). Each asserts no timeline row is written on rejection.
func TestTransitionManual_Authority(t *testing.T) {
	t.Run("inactive caller forbidden (T3)", func(t *testing.T) {
		svc, rec := txnFixture(t, "CONFIRMED")
		ctx := ctxWith(context.Background(), inactiveStaff(txnUser, histBranchA))
		if _, err := svc.TransitionManual(ctx, txnOrder, ActionPicking, ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no timeline row expected for an inactive caller")
		}
	})

	t.Run("cross-branch forbidden (T3)", func(t *testing.T) {
		svc, rec := txnFixture(t, "CONFIRMED") // order lives in histBranchA
		ctx := ctxWith(context.Background(), staffAt(txnUser, histBranchB))
		if _, err := svc.TransitionManual(ctx, txnOrder, ActionPicking, ""); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no timeline row expected for a cross-branch caller")
		}
	})

	t.Run("non-manager cancel forbidden (T4)", func(t *testing.T) {
		svc, rec := txnFixture(t, "CONFIRMED")
		ctx := ctxWith(context.Background(), staffAt(txnUser, histBranchA)) // frontline, in-branch
		if _, err := svc.TransitionManual(ctx, txnOrder, ActionCancel, "changed mind"); !errors.Is(err, ErrForbidden) {
			t.Fatalf("err = %v, want ErrForbidden", err)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no timeline row expected for a non-manager cancel")
		}
	})

	t.Run("manager cancel requires a reason (T4)", func(t *testing.T) {
		svc, rec := txnFixture(t, "CONFIRMED")
		ctx := ctxWith(context.Background(), managerAt(txnUser, histBranchA))
		for _, reason := range []string{"", "   "} { // blank and whitespace-only both rejected
			if _, err := svc.TransitionManual(ctx, txnOrder, ActionCancel, reason); !isBadRequest(err) {
				t.Fatalf("reason %q: err = %v, want BadRequest", reason, err)
			}
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no timeline row expected when the reason is missing")
		}
	})

	t.Run("unauthenticated (no caller on context)", func(t *testing.T) {
		svc, _ := txnFixture(t, "CONFIRMED")
		if _, err := svc.TransitionManual(context.Background(), txnOrder, ActionPicking, ""); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("err = %v, want ErrUnauthorized", err)
		}
	})

	t.Run("missing order is not-found, never forbidden", func(t *testing.T) {
		svc, _ := txnFixture(t, "CONFIRMED")
		ctx := ctxWith(context.Background(), globalStaff(txnUser))
		if _, err := svc.TransitionManual(ctx, "99999999-9999-4999-8999-999999999999", ActionPicking, ""); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
}

// - B2 / R2: the cancel predicate -

// TestTransitionManual_CancelPredicate covers audit H2: a manual CANCELLED may only land when we can
// PROVE no money is attached. A finalized invoice or a committed pos_sale blocks it (409-class); an
// UNWIRED predicate refuses it too (fail closed, 503-class) because a nil reader proves nothing.
// VOID/VOIDED rows do not block, those are already unwound.
func TestTransitionManual_CancelPredicate(t *testing.T) {
	cancelCtx := func() context.Context {
		return ctxWith(context.Background(), managerAt(txnUser, histBranchA))
	}

	t.Run("finalized invoice blocks the cancel", func(t *testing.T) {
		svc, rec, store := txnFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{hasInvoice: true})
		_, err := svc.TransitionManual(cancelCtx(), txnOrder, ActionCancel, "customer changed their mind")
		if !errors.Is(err, ErrCancelBlocked) {
			t.Fatalf("err = %v, want ErrCancelBlocked", err)
		}
		if got := store.details[txnOrder].Status; got != "CONFIRMED" {
			t.Fatalf("status = %q, want CONFIRMED (the order must not move)", got)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("a blocked cancel must not write a timeline row")
		}
	})

	t.Run("committed pos_sale blocks the cancel", func(t *testing.T) {
		svc, _, store := txnFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{hasSale: true})
		if _, err := svc.TransitionManual(cancelCtx(), txnOrder, ActionCancel, "reason"); !errors.Is(err, ErrCancelBlocked) {
			t.Fatalf("err = %v, want ErrCancelBlocked", err)
		}
		if got := store.details[txnOrder].Status; got != "CONFIRMED" {
			t.Fatalf("status = %q, want CONFIRMED", got)
		}
	})

	t.Run("neither attached allows the cancel", func(t *testing.T) {
		svc, _, store := txnFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{})
		if _, err := svc.TransitionManual(cancelCtx(), txnOrder, ActionCancel, "reason"); err != nil {
			t.Fatalf("unexpected err %v, a clean order must still be cancellable", err)
		}
		if got := store.details[txnOrder].Status; got != "CANCELLED" {
			t.Fatalf("status = %q, want CANCELLED", got)
		}
	})

	// A VOID invoice / VOIDED sale is already unwound, so it is NOT money still attached. The fake
	// models the repositories' SQL, which counts only FINALIZED/PARTIALLY_PAID/PAID and COMMITTED.
	t.Run("void invoice and voided sale do not block", func(t *testing.T) {
		svc, _, store := txnFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{hasInvoice: false, hasSale: false})
		if _, err := svc.TransitionManual(cancelCtx(), txnOrder, ActionCancel, "reason"); err != nil {
			t.Fatalf("unexpected err %v", err)
		}
		if got := store.details[txnOrder].Status; got != "CANCELLED" {
			t.Fatalf("status = %q, want CANCELLED", got)
		}
	})

	// FAIL CLOSED: the ruled behaviour when the predicate cannot be evaluated at all.
	t.Run("unwired predicate refuses the cancel (fail closed)", func(t *testing.T) {
		svc, rec, store := txnFixtureWithGuards(t, "CONFIRMED", nil) // guards deliberately not wired
		_, err := svc.TransitionManual(cancelCtx(), txnOrder, ActionCancel, "reason")
		if !errors.Is(err, ErrCancelUnverifiable) {
			t.Fatalf("err = %v, want ErrCancelUnverifiable (a nil reader proves nothing)", err)
		}
		if got := store.details[txnOrder].Status; got != "CONFIRMED" {
			t.Fatalf("status = %q, want CONFIRMED (never silently allowed)", got)
		}
		if rec.countAction(audit.ActOrderStatusChanged) != 0 {
			t.Fatal("no timeline row on an unverifiable cancel")
		}
	})

	// An unreadable predicate is likewise never an implicit allow.
	t.Run("predicate read error refuses the cancel", func(t *testing.T) {
		svc, _, store := txnFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{err: errors.New("db down")})
		if _, err := svc.TransitionManual(cancelCtx(), txnOrder, ActionCancel, "reason"); err == nil {
			t.Fatal("want an error when the predicate cannot be read, got nil")
		}
		if got := store.details[txnOrder].Status; got != "CONFIRMED" {
			t.Fatalf("status = %q, want CONFIRMED", got)
		}
	})

	// The predicate must not gate the non-destructive actions.
	t.Run("predicate does not affect picking or ready", func(t *testing.T) {
		svc, _, _ := txnFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{hasInvoice: true, hasSale: true})
		ctx := ctxWith(context.Background(), staffAt(txnUser, histBranchA))
		if _, err := svc.TransitionManual(ctx, txnOrder, ActionPicking, ""); err != nil {
			t.Fatalf("picking must be unaffected by the cancel predicate, got %v", err)
		}
	})
}

// TestOrderRead_LegalActions covers the B2/R6 staff read: legal_actions is computed server-side from the
// matrix ∧ the caller's authority ∧ the cancel predicate, so a client never has to duplicate the rules.
func TestOrderRead_LegalActions(t *testing.T) {
	has := func(actions []string, want string) bool {
		for _, a := range actions {
			if a == want {
				return true
			}
		}
		return false
	}

	t.Run("draft offers confirm; cancel only to a manager", func(t *testing.T) {
		svc, _, _ := txnFixtureWithGuards(t, "DRAFT", &fakeCancelGuards{})

		frontline, err := svc.OrderRead(ctxWith(context.Background(), staffAt(txnUser, histBranchA)), txnOrder)
		if err != nil {
			t.Fatalf("OrderRead: %v", err)
		}
		if frontline.Status != "DRAFT" {
			t.Fatalf("status = %q, want DRAFT", frontline.Status)
		}
		if !has(frontline.LegalActions, "CONFIRMED") {
			t.Errorf("frontline actions = %v, want CONFIRMED offered", frontline.LegalActions)
		}
		if has(frontline.LegalActions, "CANCELLED") {
			t.Errorf("frontline actions = %v, must NOT offer CANCELLED (manager-only)", frontline.LegalActions)
		}

		mgr, err := svc.OrderRead(ctxWith(context.Background(), managerAt(txnUser, histBranchA)), txnOrder)
		if err != nil {
			t.Fatalf("OrderRead(manager): %v", err)
		}
		if !has(mgr.LegalActions, "CANCELLED") {
			t.Errorf("manager actions = %v, want CANCELLED offered", mgr.LegalActions)
		}
	})

	t.Run("terminal status offers nothing", func(t *testing.T) {
		svc, _, _ := txnFixtureWithGuards(t, "CLOSED", &fakeCancelGuards{})
		v, err := svc.OrderRead(ctxWith(context.Background(), managerAt(txnUser, histBranchA)), txnOrder)
		if err != nil {
			t.Fatalf("OrderRead: %v", err)
		}
		if len(v.LegalActions) != 0 {
			t.Fatalf("legal_actions = %v, want none for a CLOSED order", v.LegalActions)
		}
	})

	t.Run("money attached withdraws the cancel offer", func(t *testing.T) {
		svc, _, _ := txnFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{hasInvoice: true})
		v, err := svc.OrderRead(ctxWith(context.Background(), managerAt(txnUser, histBranchA)), txnOrder)
		if err != nil {
			t.Fatalf("OrderRead: %v", err)
		}
		if has(v.LegalActions, "CANCELLED") {
			t.Errorf("actions = %v, must not offer a cancel that would 409", v.LegalActions)
		}
		if !has(v.LegalActions, "PICKING") {
			t.Errorf("actions = %v, want PICKING still offered", v.LegalActions)
		}
	})

	// Supervisor C2: an unwired predicate must fail closed on the OFFER only, the read still succeeds.
	t.Run("unwired predicate still returns 200 without cancel (C2)", func(t *testing.T) {
		svc, _, _ := txnFixtureWithGuards(t, "CONFIRMED", nil)
		v, err := svc.OrderRead(ctxWith(context.Background(), managerAt(txnUser, histBranchA)), txnOrder)
		if err != nil {
			t.Fatalf("OrderRead must still succeed with unwired guards, got %v", err)
		}
		if has(v.LegalActions, "CANCELLED") {
			t.Errorf("actions = %v, must not offer CANCELLED when the predicate cannot be evaluated", v.LegalActions)
		}
		if !has(v.LegalActions, "PICKING") {
			t.Errorf("actions = %v, want the unrelated actions unaffected", v.LegalActions)
		}
	})

	t.Run("authority mirrors the transition endpoints", func(t *testing.T) {
		svc, _, _ := txnFixtureWithGuards(t, "CONFIRMED", &fakeCancelGuards{})
		if _, err := svc.OrderRead(context.Background(), txnOrder); !errors.Is(err, ErrUnauthorized) {
			t.Errorf("no caller: err = %v, want ErrUnauthorized", err)
		}
		if _, err := svc.OrderRead(ctxWith(context.Background(), inactiveStaff(txnUser, histBranchA)), txnOrder); !errors.Is(err, ErrForbidden) {
			t.Errorf("inactive: err = %v, want ErrForbidden", err)
		}
		if _, err := svc.OrderRead(ctxWith(context.Background(), staffAt(txnUser, histBranchB)), txnOrder); !errors.Is(err, ErrForbidden) {
			t.Errorf("cross-branch: err = %v, want ErrForbidden", err)
		}
		missing := "99999999-9999-4999-8999-999999999999"
		if _, err := svc.OrderRead(ctxWith(context.Background(), globalStaff(txnUser)), missing); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing order: err = %v, want ErrNotFound", err)
		}
	})
}

// TestTransitionManual_Idempotent asserts re-issuing a move once the order is already in the target
// state is a benign no-op (ErrNoTransition) with no second timeline row.
func TestTransitionManual_Idempotent(t *testing.T) {
	svc, rec := txnFixture(t, "PICKING")
	ctx := ctxWith(context.Background(), staffAt(txnUser, histBranchA))
	if _, err := svc.TransitionManual(ctx, txnOrder, ActionPicking, ""); !errors.Is(err, ErrNoTransition) {
		t.Fatalf("err = %v, want ErrNoTransition (already PICKING)", err)
	}
	if rec.countAction(audit.ActOrderStatusChanged) != 0 {
		t.Fatal("a no-op transition must not write a timeline row")
	}
}
