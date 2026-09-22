package pos

// dib488_rows_test.go, DIB-488 rev 3 unit tests: the inventory-lookup results presentation
// (lookup_presentation, 0167, CARDS | ROWS).
//
// The ruling under test: the presentation follows the rev-2 workspace persistence model EXACTLY -
// dealer DEFAULT on pos_policy (written through the admin policy PUT, 422-validated in words
// before the CHECK could 500, omitted normalizes to the CARDS column default), per-user override
// riding the SAME whole-or-absent pos_user_workspace row (saved with the split, cleared by the
// reset DELETE). Same harness posture as dib488_ws_test.go: fake store only, no clock, no network.

import (
	"context"
	"errors"
	"testing"
)

// -- the dealer default rides the admin policy write ---------------------------------------------

func TestUpdatePolicy_LookupPresentationRejected_422(t *testing.T) {
	h := newHarness()
	_, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{LookupPresentation: "GRID"})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want a 422 for an unknown lookup_presentation, got %v", err)
	}
	if got := h.store.policies[""].LookupPresentation; got != "" {
		t.Fatalf("a refused policy write reached the store: lookup_presentation=%q", got)
	}
}

func TestUpdatePolicy_LookupPresentationNormalizedAndStored(t *testing.T) {
	// Omitted ("") normalizes to CARDS, the 0167 column-default posture, the line_presentation
	// precedent; an explicit ROWS is stored verbatim.
	h := newHarness()
	p, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{CODEnabled: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if p.LookupPresentation != LookupPresentationCards {
		t.Fatalf("omitted lookup_presentation must normalize to CARDS, got %q", p.LookupPresentation)
	}
	p, err = h.svc.UpdatePolicy(context.Background(), PosPolicy{LookupPresentation: LookupPresentationRows})
	if err != nil {
		t.Fatalf("update(ROWS): %v", err)
	}
	if p.LookupPresentation != LookupPresentationRows {
		t.Fatalf("an authored ROWS default must store, got %q", p.LookupPresentation)
	}
}

// -- resolution: the same whole-or-absent answer the geometry takes ------------------------------

func TestWorkspace_DealerLookupPresentationResolves(t *testing.T) {
	h := newHarness()
	h.store.policies[""] = PosPolicy{WorkspaceSplitPct: 55, WorkspaceArrangement: ArrangementLookupLeft,
		LookupPresentation: LookupPresentationRows}
	got, err := h.svc.Workspace(wsCtx("user-ann"), "")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if got.Source != "dealer" || got.LookupPresentation != LookupPresentationRows {
		t.Fatalf("no user row must resolve the dealer's ROWS default, got %+v", got)
	}
	// A pre-0167 zero value (a fake, a partial read) resolves to CARDS, never an empty answer.
	h.store.policies[""] = PosPolicy{WorkspaceSplitPct: 55, WorkspaceArrangement: ArrangementLookupLeft}
	got, err = h.svc.Workspace(wsCtx("user-ann"), "")
	if err != nil {
		t.Fatalf("workspace(zero): %v", err)
	}
	if got.LookupPresentation != LookupPresentationCards {
		t.Fatalf("a zero-value policy must resolve lookup_presentation to CARDS, got %q", got.LookupPresentation)
	}
}

func TestWorkspace_UserRowLookupPresentationWins(t *testing.T) {
	// Whole-or-absent: the stored row IS the workspace, its presentation included, even when the
	// dealer default says otherwise.
	h := newHarness()
	h.store.policies[""] = PosPolicy{WorkspaceSplitPct: 55, WorkspaceArrangement: ArrangementLookupLeft,
		LookupPresentation: LookupPresentationRows}
	h.store.userWS["user-ann"] = userWorkspaceRow{
		UserID: "user-ann", SplitPct: 40, Arrangement: ArrangementLookupLeft,
		LookupPresentation: LookupPresentationCards,
	}
	got, err := h.svc.Workspace(wsCtx("user-ann"), "")
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	if got.Source != "user" || got.LookupPresentation != LookupPresentationCards {
		t.Fatalf("the user row's presentation must win whole-or-absent, got %+v", got)
	}
}

// -- the save: validated, stored with the geometry, preserved by the replay guard ----------------

func TestSaveWorkspace_LookupPresentationRejected_422(t *testing.T) {
	h := newHarness()
	_, err := h.svc.SaveWorkspace(wsCtx("user-ann"),
		WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 40, LookupPresentation: "TILES"})
	var ce clientError
	if !errors.As(err, &ce) || ce.status != 422 {
		t.Fatalf("want a 422 for an unknown lookup_presentation on a workspace save, got %v", err)
	}
	if len(h.store.userWS) != 0 {
		t.Fatalf("a refused workspace write reached the store: %+v", h.store.userWS)
	}
}

func TestSaveWorkspace_LookupPresentationRidesTheRowAndReplay(t *testing.T) {
	h := newHarness()
	ctx := wsCtx("user-ann")
	first, err := h.svc.SaveWorkspace(ctx,
		WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 40, LookupPresentation: LookupPresentationRows})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if first.LookupPresentation != LookupPresentationRows || h.store.userWS["user-ann"].LookupPresentation != LookupPresentationRows {
		t.Fatalf("ROWS must ride the stored row, got answer=%q stored=%q",
			first.LookupPresentation, h.store.userWS["user-ann"].LookupPresentation)
	}
	// A verbatim replay answers the STORED presentation and writes nothing (the settings idiom).
	again, err := h.svc.SaveWorkspace(ctx,
		WorkspaceSaveInput{IdempotencyKey: "k1", SplitPct: 70, LookupPresentation: LookupPresentationCards})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !again.Replayed || again.LookupPresentation != LookupPresentationRows || h.store.userWSWrites != 1 {
		t.Fatalf("a replayed key must return the stored presentation without writing (got %+v, writes=%d)",
			again, h.store.userWSWrites)
	}
	// An omitted presentation on a save normalizes to CARDS, never an empty stored value.
	next, err := h.svc.SaveWorkspace(ctx, WorkspaceSaveInput{IdempotencyKey: "k2", SplitPct: 70})
	if err != nil {
		t.Fatalf("second save: %v", err)
	}
	if next.LookupPresentation != LookupPresentationCards {
		t.Fatalf("an omitted presentation must normalize to CARDS on save, got %q", next.LookupPresentation)
	}
}
