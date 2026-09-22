package pos

// dib488_ws_test.go, DIB-488 rev 2 unit tests: the two-panel POS workspace's persistence.
//
// The ruling under test (workspace.go / 0166): DEALER defaults on pos_policy (split + arrangement,
// admin-gated), PER-USER ergonomics on pos_user_workspace (staff self-service, whole-or-absent,
// reset = DELETE), maximize never persisted (nothing here stores it, which is the point). Same
// harness posture as dib488_test.go: fake store only, no clock, no network.

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// wsCtx stamps a staff caller id exactly as the RequireStaff guard does in production, the
// workspace service keys everything on the AUTHENTICATED caller, never a user id off the wire.
func wsCtx(userID string) context.Context {
	return identity.ContextWithStaffUserID(context.Background(), userID)
}

// -- dealer defaults ride the admin policy write, validated before the CHECK could 500 -----------

func TestUpdatePolicy_WorkspaceSplitOutOfRange_422(t *testing.T) {
	h := newHarness()
	_, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{WorkspaceSplitPct: 95})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want a 422 for split_pct=95 (bounds 20-80), got %v", err)
	}
	if got := h.store.policies[""].WorkspaceSplitPct; got != 0 {
		t.Fatalf("a refused policy write reached the store: split=%d", got)
	}
}

func TestUpdatePolicy_WorkspaceArrangementRejected_422(t *testing.T) {
	h := newHarness()
	_, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{WorkspaceArrangement: "STACKED"})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want a 422 for an unknown workspace arrangement, got %v", err)
	}
}

func TestUpdatePolicy_WorkspaceDefaultsNormalized(t *testing.T) {
	// Omitted workspace fields normalize to the 0166 column defaults (the line_presentation
	// posture): split 0 → 55, arrangement "" → LOOKUP_LEFT.
	h := newHarness()
	p, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{CODEnabled: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if p.WorkspaceSplitPct != WorkspaceSplitDefault || p.WorkspaceArrangement != ArrangementLookupLeft {
		t.Fatalf("omitted workspace defaults must normalize to 55/LOOKUP_LEFT, got %d/%q",
			p.WorkspaceSplitPct, p.WorkspaceArrangement)
	}
}

// -- resolution: the user override is WHOLE OR ABSENT over the dealer defaults -------------------

func TestWorkspace_DealerDefaultsWhenNoUserRow(t *testing.T) {
	h := newHarness()
	h.store.policies[""] = PosPolicy{WorkspaceSplitPct: 64, WorkspaceArrangement: ArrangementSaleLeft}
	got, err := h.svc.Workspace(wsCtx("user-ann"), "")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if got.Source != "dealer" || got.SplitPct != 64 || got.Arrangement != ArrangementSaleLeft {
		t.Fatalf("no user row must resolve to the dealer defaults (source=dealer), got %+v", got)
	}
	if got.LookupCollapsed || got.SaleCollapsed {
		t.Fatalf("the dealer path opens both panels, nobody configures 'everyone starts collapsed': %+v", got)
	}
}

func TestWorkspace_UserRowWinsWholeOrAbsent(t *testing.T) {
	h := newHarness()
	h.store.policies[""] = PosPolicy{WorkspaceSplitPct: 64, WorkspaceArrangement: ArrangementSaleLeft}
	h.store.userWS["user-ann"] = userWorkspaceRow{
		UserID: "user-ann", SplitPct: 30, Arrangement: ArrangementLookupLeft, SaleCollapsed: true,
	}
	got, err := h.svc.Workspace(wsCtx("user-ann"), "")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if got.Source != "user" || got.SplitPct != 30 || got.Arrangement != ArrangementLookupLeft || !got.SaleCollapsed {
		t.Fatalf("a stored user row IS the workspace (whole-or-absent), got %+v", got)
	}
	// Another user with no row still gets the dealer defaults, the override is per person.
	other, err := h.svc.Workspace(wsCtx("user-bob"), "")
	if err != nil {
		t.Fatalf("workspace(other): %v", err)
	}
	if other.Source != "dealer" || other.SplitPct != 64 {
		t.Fatalf("another user's workspace leaked across users: %+v", other)
	}
}

// -- the write: staff self-service, validated, replay-safe on the settings idempotency key -------

func TestSaveWorkspace_SplitRejected_422(t *testing.T) {
	h := newHarness()
	_, err := h.svc.SaveWorkspace(wsCtx("user-ann"), WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 12})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want a 422 for split_pct=12, got %v", err)
	}
	if len(h.store.userWS) != 0 {
		t.Fatalf("a refused workspace write reached the store: %+v", h.store.userWS)
	}
}

func TestSaveWorkspace_MissingIdempotencyKey_422(t *testing.T) {
	h := newHarness()
	_, err := h.svc.SaveWorkspace(wsCtx("user-ann"), WorkspaceSaveInput{SplitPct: 40})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("a workspace write without its idempotency key must be a 422, got %v", err)
	}
}

func TestSaveWorkspace_ReplaySameKey_NoSecondWrite(t *testing.T) {
	h := newHarness()
	ctx := wsCtx("user-ann")
	first, err := h.svc.SaveWorkspace(ctx, WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 35, SaleCollapsed: true})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if first.Replayed || h.store.userWSWrites != 1 {
		t.Fatalf("first save must write once (writes=%d, replayed=%v)", h.store.userWSWrites, first.Replayed)
	}
	// The verbatim retry (network-error posture): answered from the store, no second write.
	again, err := h.svc.SaveWorkspace(ctx, WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 70})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || again.SplitPct != 35 || h.store.userWSWrites != 1 {
		t.Fatalf("a replayed key must return the stored row without writing (got %+v, writes=%d)",
			again, h.store.userWSWrites)
	}
	// A NEW key is a new decision and writes normally.
	next, err := h.svc.SaveWorkspace(ctx, WorkspaceSaveInput{IdempotencyKey: "k2", SplitPct: 70})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if next.Replayed || next.SplitPct != 70 || h.store.userWSWrites != 2 {
		t.Fatalf("a fresh key must write: %+v (writes=%d)", next, h.store.userWSWrites)
	}
}

func TestResetWorkspace_DeleteIsReset(t *testing.T) {
	h := newHarness()
	h.store.policies[""] = PosPolicy{WorkspaceSplitPct: 55, WorkspaceArrangement: ArrangementLookupLeft}
	ctx := wsCtx("user-ann")
	if _, err := h.svc.SaveWorkspace(ctx, WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 25}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := h.svc.ResetWorkspace(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	got, err := h.svc.Workspace(ctx, "")
	if err != nil {
		t.Fatalf("workspace after reset: %v", err)
	}
	if got.Source != "dealer" || got.SplitPct != 55 {
		t.Fatalf("reset must delete the override and fall back to dealer defaults, got %+v", got)
	}
}
