package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// - caller threading (branch-scope on the staff-gated history read) -
// Mirrors the till history/service tests: a fake StaffContext rides the context and a test
// callerFrom seam (installed via withCaller) reads it, so the branch-scope guard is exercised
// with no identity middleware in the loop.

type ctxKeyOrder struct{}

func ctxWith(ctx context.Context, sc identity.StaffContext) context.Context {
	return context.WithValue(ctx, ctxKeyOrder{}, sc)
}

func testCallerFrom(ctx context.Context) (identity.StaffContext, bool) {
	sc, ok := ctx.Value(ctxKeyOrder{}).(identity.StaffContext)
	return sc, ok
}

// staffAt is a branch-restricted front-line staffer (reaches only the granted branch).
func staffAt(uid, branch string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline,
		Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{branch: true}}
}

// globalStaff is a GLOBAL-scoped caller (reaches any branch), the "authorized regardless of branch" case.
func globalStaff(uid string) identity.StaffContext {
	return identity.StaffContext{UserID: uid, RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE"}
}

const (
	histBranchA = "aaaaaaaa-0000-4000-9000-000000000001" // the branch histOrder lives in
	histBranchB = "bbbbbbbb-0000-4000-9000-000000000002" // a branch the restricted staffer cannot reach
)

// recordingRecorder is an in-memory audit.Recorder + txRecorder capturing every SensitiveAccess the
// choke-point and the edit path emit, so a test can assert the exact action/entity/actor/from/to of a
// row. Implementing RecordTx is what lets order.Service bind it as the TRANSACTIONAL recorder (B2-1),
// so the edit-audit unit tests exercise the same emit path production uses. txErr forces a recorder
// failure (the kill-the-recorder rollback test); the querier is ignored (no real tx in a unit fake).
type recordingRecorder struct {
	records []audit.SensitiveAccess
	err     error // Record (pool path) failure
	txErr   error // RecordTx (transactional path) failure
}

func (r *recordingRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.records = append(r.records, a)
	return r.err
}

func (r *recordingRecorder) RecordTx(_ context.Context, _ audit.Execer, a audit.SensitiveAccess) error {
	if r.txErr != nil {
		return r.txErr
	}
	r.records = append(r.records, a)
	return nil
}

// countAction returns how many recorded rows carry the given action.
func (r *recordingRecorder) countAction(action string) int {
	n := 0
	for _, a := range r.records {
		if a.Action == action {
			n++
		}
	}
	return n
}

// fakeTrail is an in-memory AuditTrailReader: it returns the canned entries for (entityType,entityID),
// mirroring audit.Service.ReopenTransaction (ListByEntity, oldest-first).
type fakeTrail struct {
	byEntity map[string][]audit.Entry
	err      error
}

func (f *fakeTrail) ReopenTransaction(_ context.Context, entityType, entityID string) ([]audit.Entry, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byEntity[entityType+"|"+entityID], nil
}

const (
	histCust  = "11111111-1111-4111-8111-111111111111"
	histOrder = "22222222-2222-4222-8222-222222222222"
	histActor = "33333333-3333-4333-8333-333333333333"
)

// A real status transition emits EXACTLY ONE ORDER_STATUS_CHANGED with the correct from/to/actor,
// through the injected Recorder, the core DIB-159 emit assertion.
func TestSetStatus_EmitsExactlyOneStatusChanged(t *testing.T) {
	store := newFakeStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec)

	if err := svc.SetStatus(context.Background(), histCust, histActor, histOrder, "PICKING", "picker_started"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	if got := rec.countAction(audit.ActOrderStatusChanged); got != 1 {
		t.Fatalf("ORDER_STATUS_CHANGED emitted %d times, want exactly 1", got)
	}
	a := rec.records[0]
	if a.EntityType != "order" || a.EntityID != histOrder {
		t.Errorf("entity = %s/%s, want order/%s", a.EntityType, a.EntityID, histOrder)
	}
	if a.ActorID != histActor {
		t.Errorf("actor = %s, want %s", a.ActorID, histActor)
	}
	if a.Extra["old_status"] != "CONFIRMED" || a.Extra["new_status"] != "PICKING" {
		t.Errorf("from→to = %v→%v, want CONFIRMED→PICKING", a.Extra["old_status"], a.Extra["new_status"])
	}
	if a.Reason != "picker_started" {
		t.Errorf("reason = %q, want picker_started", a.Reason)
	}
	// The store actually performed the transition.
	if store.statusCalls != 1 {
		t.Errorf("store.UpdateStatus called %d times, want 1", store.statusCalls)
	}
}

// A no-op transition (target == current) records NO history row and surfaces ErrNoTransition.
func TestSetStatus_NoOpEmitsNothing(t *testing.T) {
	store := newFakeStore(mkDetail(histOrder, histCust, "FULFILLED"))
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec)

	err := svc.SetStatus(context.Background(), histCust, histActor, histOrder, "FULFILLED", "")
	if !errors.Is(err, ErrNoTransition) {
		t.Fatalf("err = %v, want ErrNoTransition", err)
	}
	if got := rec.countAction(audit.ActOrderStatusChanged); got != 0 {
		t.Fatalf("a no-op transition emitted %d ORDER_STATUS_CHANGED rows, want 0", got)
	}
}

// An unknown target status is a 400 before any write/emit.
func TestSetStatus_RejectsUnknownStatus(t *testing.T) {
	store := newFakeStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec)

	if err := svc.SetStatus(context.Background(), histCust, histActor, histOrder, "SHIPPED_TO_MOON", ""); !isBadRequest(err) {
		t.Fatalf("err = %v, want 400", err)
	}
	if store.statusCalls != 0 {
		t.Errorf("no write may happen on an invalid status; got %d calls", store.statusCalls)
	}
	if got := rec.countAction(audit.ActOrderStatusChanged); got != 0 {
		t.Errorf("no emit on invalid status; got %d", got)
	}
}

// A cross-tenant / missing id maps to ErrNotFound with no emit.
func TestSetStatus_TenantScopedNotFound(t *testing.T) {
	store := newFakeStore(mkDetail(histOrder, histCust, "CONFIRMED"))
	rec := &recordingRecorder{}
	svc := NewService(store).WithAudit(rec)

	err := svc.SetStatus(context.Background(), "99999999-9999-4999-8999-999999999999", histActor, histOrder, "PICKING", "")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant err = %v, want ErrNotFound", err)
	}
	if got := rec.countAction(audit.ActOrderStatusChanged); got != 0 {
		t.Errorf("no emit on a cross-tenant miss; got %d", got)
	}
}

// OrderHistory reconstructs the ORDERED status timeline (oldest→newest) from the audit trail, keeping
// only ORDER_STATUS_CHANGED rows and shaping from→to/who/when/reason, the read-side assertion.
func TestOrderHistory_OrderedTimeline(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	trail := &fakeTrail{byEntity: map[string][]audit.Entry{
		"order|" + histOrder: {
			// deliberately out of order + a non-status row mixed in, to prove filtering + sorting.
			entry("ORDER_CHANGED", histOrder, histActor, t0.Add(30*time.Second), map[string]any{"field": "delivery_instructions"}),
			entry(audit.ActOrderStatusChanged, histOrder, histActor, t0.Add(2*time.Minute),
				map[string]any{"old_status": "CONFIRMED", "new_status": "FULFILLED", "reason": "pos_fulfilment"}),
			entry(audit.ActOrderStatusChanged, histOrder, "", t0,
				map[string]any{"old_status": "", "new_status": "CONFIRMED", "reason": "pos_ring"}),
		},
	}}
	store := newFakeStore()
	store.branches = map[string]string{histOrder: histBranchA}
	svc := NewService(store).WithAuditTrail(trail).withCaller(testCallerFrom)

	// A GLOBAL-scoped caller reaches any branch, the read-side timeline assertion is unaffected by scope.
	ctx := ctxWith(context.Background(), globalStaff(histActor))
	h, err := svc.OrderHistory(ctx, histOrder)
	if err != nil {
		t.Fatalf("OrderHistory: %v", err)
	}
	if h.OrderID != histOrder {
		t.Errorf("order id = %s, want %s", h.OrderID, histOrder)
	}
	if len(h.Changes) != 2 {
		t.Fatalf("timeline has %d changes, want 2 (the ORDER_CHANGED row must be filtered out)", len(h.Changes))
	}
	// Oldest first: "" → CONFIRMED, then CONFIRMED → FULFILLED.
	if h.Changes[0].FromStatus != "" || h.Changes[0].ToStatus != "CONFIRMED" {
		t.Errorf("first change = %q→%q, want →CONFIRMED", h.Changes[0].FromStatus, h.Changes[0].ToStatus)
	}
	if h.Changes[1].FromStatus != "CONFIRMED" || h.Changes[1].ToStatus != "FULFILLED" {
		t.Errorf("second change = %q→%q, want CONFIRMED→FULFILLED", h.Changes[1].FromStatus, h.Changes[1].ToStatus)
	}
	if !h.Changes[0].OccurredAt.Equal(t0) {
		t.Errorf("first occurred_at = %v, want %v", h.Changes[0].OccurredAt, t0)
	}
	if h.Changes[1].Reason != "pos_fulfilment" {
		t.Errorf("second reason = %q, want pos_fulfilment", h.Changes[1].Reason)
	}
	if h.Changes[1].ActorID != histActor {
		t.Errorf("second actor = %q, want %s", h.Changes[1].ActorID, histActor)
	}
}

// With no audit-trail seam wired, the history is a genuinely-empty timeline (not an error/501) -
// but only AFTER the branch-scope guard passes (the order must exist and be reachable).
func TestOrderHistory_NoSeamEmpty(t *testing.T) {
	store := newFakeStore()
	store.branches = map[string]string{histOrder: histBranchA}
	svc := NewService(store).withCaller(testCallerFrom)
	ctx := ctxWith(context.Background(), globalStaff(histActor))
	h, err := svc.OrderHistory(ctx, histOrder)
	if err != nil {
		t.Fatalf("OrderHistory: %v", err)
	}
	if len(h.Changes) != 0 {
		t.Fatalf("want empty timeline, got %d changes", len(h.Changes))
	}
}

// - DIB-159 branch-scope regression (the IDOR/horizontal-access fix) -
// The staff-gated order-history read must not let a branch-restricted staffer read an order in a
// branch they cannot access by iterating ids, mirrors the payment-history branch check
// (till/history.go:211). A GLOBAL-scoped caller (or one granted the order's branch) gets the timeline;
// a scoped caller who lacks the branch gets forbidden (not the data); a missing id gets not-found
// (never a leak of existence via the authz path); and an unauthenticated request is rejected.
func TestOrderHistory_BranchScope(t *testing.T) {
	// One real transition so an authorized read returns a non-empty, correct timeline.
	trail := &fakeTrail{byEntity: map[string][]audit.Entry{
		"order|" + histOrder: {
			entry(audit.ActOrderStatusChanged, histOrder, histActor, time.Unix(0, 0).UTC(),
				map[string]any{"old_status": "", "new_status": "CONFIRMED", "reason": "pos_ring"}),
		},
	}}
	newSvc := func() *Service {
		store := newFakeStore()
		store.branches = map[string]string{histOrder: histBranchA} // the order lives in branch A
		return NewService(store).WithAuditTrail(trail).withCaller(testCallerFrom)
	}

	t.Run("cross-branch staffer is forbidden (not the data)", func(t *testing.T) {
		// A staffer restricted to branch B tries to read an order in branch A.
		ctx := ctxWith(context.Background(), staffAt("staff-b", histBranchB))
		_, err := newSvc().OrderHistory(ctx, histOrder)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("cross-branch read err = %v, want ErrForbidden", err)
		}
	})

	t.Run("staffer granted the order's branch gets the timeline", func(t *testing.T) {
		ctx := ctxWith(context.Background(), staffAt("staff-a", histBranchA))
		h, err := newSvc().OrderHistory(ctx, histOrder)
		if err != nil {
			t.Fatalf("authorized read err = %v, want nil", err)
		}
		if len(h.Changes) != 1 || h.Changes[0].ToStatus != "CONFIRMED" {
			t.Fatalf("authorized read timeline = %+v, want the ->CONFIRMED transition", h.Changes)
		}
	})

	t.Run("GLOBAL-scoped caller reaches any branch", func(t *testing.T) {
		ctx := ctxWith(context.Background(), globalStaff("gm"))
		h, err := newSvc().OrderHistory(ctx, histOrder)
		if err != nil {
			t.Fatalf("global read err = %v, want nil", err)
		}
		if len(h.Changes) != 1 {
			t.Fatalf("global read timeline has %d changes, want 1", len(h.Changes))
		}
	})

	t.Run("missing order is not-found (no existence leak via authz)", func(t *testing.T) {
		// Even a GLOBAL caller gets not-found, never a data/existence leak, for an unknown id.
		ctx := ctxWith(context.Background(), globalStaff("gm"))
		_, err := newSvc().OrderHistory(ctx, "ffffffff-0000-4000-9000-00000000dead")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing-order err = %v, want ErrNotFound", err)
		}
	})

	t.Run("unauthenticated request is rejected", func(t *testing.T) {
		// No caller in context → the guard-stamped caller is absent → unauthorized.
		_, err := newSvc().OrderHistory(context.Background(), histOrder)
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("no-caller err = %v, want ErrUnauthorized", err)
		}
	})
}

// entry builds an audit.Entry the way the repo scan would (CreatedAt as an RFC3339Nano string).
func entry(action, entityID, actor string, at time.Time, changes map[string]any) audit.Entry {
	return audit.Entry{
		Action: action, EntityType: "order", EntityID: entityID, ActorID: actor,
		Changes: changes, CreatedAt: at.Format(time.RFC3339Nano),
	}
}

// TestLooksLikeUUID guards the repository.go OrderBranch pre-query validator: only a canonical
// 36-char 8-4-4-4-12 hex UUID may reach the orders.id uuid column. A malformed id is rejected
// (→ ErrNotFound) rather than raising Postgres SQLSTATE 22P02 → HTTP 500. Fails without the guard.
func TestLooksLikeUUID(t *testing.T) {
	valid := []string{
		"22222222-2222-4222-8222-222222222222",
		"00000000-0000-0000-0000-000000000000",
		"FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF", // upper-case hex is still valid
	}
	invalid := []string{
		"", "not-a-uuid", "1234", "'; DROP TABLE orders;--",
		"22222222-2222-4222-8222-22222222222",   // 35 chars, one short
		"22222222-2222-4222-8222-2222222222222", // 37 chars, one long
		"gggggggg-gggg-gggg-gggg-gggggggggggg",  // right shape, non-hex
		"22222222x2222-4222-8222-222222222222",  // misplaced separator
	}
	for _, s := range valid {
		if !looksLikeUUID(s) {
			t.Errorf("looksLikeUUID(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if looksLikeUUID(s) {
			t.Errorf("looksLikeUUID(%q) = true, want false", s)
		}
	}
}

// TestOrderHistory_ReasonSourceIsTopLevelReason locks the reason_code dead-code removal: the
// transition reason is carried ONLY by the top-level SensitiveAccess.Reason field (persisted as
// changes["reason"]). No emit site ever writes a "reason_code" Extra key, so the read must map the
// "reason" key alone. This test, with a "reason_code" key present but NO "reason", asserts the
// change surfaces an EMPTY reason (the removed fallback would have picked up reason_code instead).
func TestOrderHistory_ReasonSourceIsTopLevelReason(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	trail := &fakeTrail{byEntity: map[string][]audit.Entry{
		"order|" + histOrder: {
			// A canonical row: the reason rides "reason" → it must surface.
			entry(audit.ActOrderStatusChanged, histOrder, histActor, t0,
				map[string]any{"old_status": "", "new_status": "CONFIRMED", "reason": "pos_ring"}),
			// A row carrying a stray "reason_code" but NO "reason": the removed fallback would have
			// surfaced "STALE_CODE"; after the fix the reason is empty.
			entry(audit.ActOrderStatusChanged, histOrder, histActor, t0.Add(time.Minute),
				map[string]any{"old_status": "CONFIRMED", "new_status": "PICKING", "reason_code": "STALE_CODE"}),
		},
	}}
	store := newFakeStore()
	store.branches = map[string]string{histOrder: histBranchA}
	svc := NewService(store).WithAuditTrail(trail).withCaller(testCallerFrom)
	ctx := ctxWith(context.Background(), globalStaff(histActor))

	h, err := svc.OrderHistory(ctx, histOrder)
	if err != nil {
		t.Fatalf("OrderHistory: %v", err)
	}
	if len(h.Changes) != 2 {
		t.Fatalf("timeline has %d changes, want 2", len(h.Changes))
	}
	if h.Changes[0].Reason != "pos_ring" {
		t.Errorf("change[0] reason = %q, want pos_ring (from the top-level reason key)", h.Changes[0].Reason)
	}
	if h.Changes[1].Reason != "" {
		t.Errorf("change[1] reason = %q, want empty, reason_code is dead and must NOT be read", h.Changes[1].Reason)
	}
}
