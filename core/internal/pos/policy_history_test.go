package pos

// policy_history_test.go, the counter settings change history.
//
// It is built on the shipped service_test.go harness (fakeStore + fakeRecorder), exactly additive:
// no existing fake grows a field and no existing test name moves.
//
// WHAT IS PINNED HERE, AND WHY EACH ONE IS A TRAP RATHER THAN A PREFERENCE:
//
//   · the pre-image is the STORED row, not the DERIVED one, so the first save of an un-curated
//     dealer does not invent a quick_chip_set change nobody made;
//   · a dealer-default save (branch_id "") is filed under a READABLE entity id, because audit writes
//     "" as SQL NULL and no equality predicate ever matches NULL again;
//   · an absent `before` renders as empty and never as the literal "<nil>";
//   · `who` is a PERSON with the role in parentheses, degrading to the role word and never to a uuid.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// -- fakes ----------------------------------------------------------------------------------------

// fakeTrail is the audit READ seam: canned rows per (entity_type, entity_id), oldest-first, which is
// the order *audit.Service.ReopenTransaction returns.
type fakeTrail struct {
	rows map[string][]audit.Entry
	err  error
	asks []string
}

func (f *fakeTrail) ReopenTransaction(_ context.Context, entityType, entityID string) ([]audit.Entry, error) {
	f.asks = append(f.asks, entityType+"/"+entityID)
	if f.err != nil {
		return nil, f.err
	}
	return f.rows[entityType+"/"+entityID], nil
}

// fakeNames is the ActorNames seam. An id it does not hold is simply absent, which is the real
// resolver's contract for a deleted staff member or a system actor.
type fakeNames struct {
	names map[string]string
	err   error
	asked []string
}

func (f *fakeNames) ActorNames(_ context.Context, ids []string) (map[string]string, error) {
	f.asked = append(f.asked, ids...)
	if f.err != nil {
		return nil, f.err
	}
	return f.names, nil
}

// adminHarness is newHarness() with a Tier-1 GLOBAL caller, which is what the policy routes require.
func adminHarness() *harness {
	h := newHarness()
	h.caller = identity.StaffContext{
		UserID: "u-gm", RoleCode: "GENERAL_MANAGER",
		Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	}
	return h
}

// seedPolicy puts a complete, valid stored row under a branch so a save has something to diff from.
func seedPolicy(h *harness, branchID string) PosPolicy {
	p := PosPolicy{
		BranchID: branchID, CODEnabled: true, RestockingFeeBps: 1500,
		LinePresentation: LinePresentationLines, QuickChipsEnabled: true,
		WorkspaceSplitPct: 40, WorkspaceArrangement: "LOOKUP_LEFT",
		LookupPresentation: "CARDS", MarginVisibility: "MANAGER_UP",
		BalanceVisibility: "ALL_STAFF",
	}
	h.store.policies[branchID] = p
	return p
}

func policyRows(rec *fakeRecorder) []audit.SensitiveAccess {
	out := []audit.SensitiveAccess{}
	for _, r := range rec.rows {
		if r.Action == ActPosPolicyChanged {
			out = append(out, r)
		}
	}
	return out
}

// -- 1 · the write --------------------------------------------------------------------------------

// One knob moved, one row written, and the row carries the PRE-image, not just the new value.
// "margin_visibility is ALL_STAFF" is not a change; it is a reading.
func TestUpdatePolicy_RecordsOneRowPerChangedKnob(t *testing.T) {
	h := adminHarness()
	before := seedPolicy(h, "branch-trt")

	next := before
	next.MarginVisibility = "ALL_STAFF"
	next.WorkspaceSplitPct = 55
	if _, err := h.svc.UpdatePolicy(context.Background(), next); err != nil {
		t.Fatalf("update: %v", err)
	}

	rows := policyRows(h.rec)
	if len(rows) != 2 {
		t.Fatalf("want 2 change rows, got %d: %+v", len(rows), rows)
	}
	got := map[string][2]string{}
	for _, r := range rows {
		if r.EntityType != posPolicyEntity {
			t.Fatalf("entity_type = %q, want %q", r.EntityType, posPolicyEntity)
		}
		if r.EntityID != "branch-trt" {
			t.Fatalf("entity_id = %q, want the branch id", r.EntityID)
		}
		if r.ActorID != "u-gm" {
			t.Fatalf("actor_id = %q, want the caller", r.ActorID)
		}
		if r.Extra["actor_role"] != "GENERAL_MANAGER" {
			t.Fatalf("actor_role = %v, want GENERAL_MANAGER", r.Extra["actor_role"])
		}
		f, _ := r.Extra["field"].(string)
		b, _ := r.Extra["before"].(string)
		a, _ := r.Extra["after"].(string)
		got[f] = [2]string{b, a}
	}
	if got["margin_visibility"] != [2]string{"MANAGER_UP", "ALL_STAFF"} {
		t.Fatalf("margin_visibility row = %v", got["margin_visibility"])
	}
	if got["workspace_split_pct"] != [2]string{"40", "55"} {
		t.Fatalf("workspace_split_pct row = %v", got["workspace_split_pct"])
	}
}

// A save that moved nothing writes nothing. A history that logged every save would be a list of
// times somebody pressed a button.
func TestUpdatePolicy_NoChangeWritesNoRow(t *testing.T) {
	h := adminHarness()
	before := seedPolicy(h, "branch-trt")
	if _, err := h.svc.UpdatePolicy(context.Background(), before); err != nil {
		t.Fatalf("update: %v", err)
	}
	if rows := policyRows(h.rec); len(rows) != 0 {
		t.Fatalf("a no-op save wrote %d history rows: %+v", len(rows), rows)
	}
}

// THE PRE-IMAGE TRAP. Policy() DERIVES an un-curated quick_chip_set from the top-selling categories.
// If the diff read through it, the first save of every dealer who has never curated a chip would
// record a quick_chip_set change they did not make.
func TestUpdatePolicy_PreImageIsStoredNotDerived(t *testing.T) {
	h := adminHarness()
	before := seedPolicy(h, "branch-trt") // QuickChipSet is empty in the column
	h.store.topApps = []string{"aggregate", "mulch"}

	// Prove the derivation is live: the READ path substitutes the top sellers.
	derived, err := h.svc.Policy(context.Background(), "branch-trt")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	if len(derived.QuickChipSet) == 0 {
		t.Fatal("the derivation is not wired in this harness, the trap this test guards cannot fire")
	}

	next := before
	next.CODEnabled = false
	if _, err := h.svc.UpdatePolicy(context.Background(), next); err != nil {
		t.Fatalf("update: %v", err)
	}
	for _, r := range policyRows(h.rec) {
		if r.Extra["field"] == "quick_chip_set" {
			t.Fatalf("a quick_chip_set change was fabricated from the DERIVED read: %+v", r.Extra)
		}
	}
}

// THE ENTITY-ID TRAP. A dealer-default save carries branch_id "", which audit writes as SQL NULL -
// and `entity_id = $2::uuid` never matches NULL. The row must be filed under the sentinel instead.
func TestUpdatePolicy_DealerDefaultIsFiledUnderAReadableScope(t *testing.T) {
	h := adminHarness()
	before := seedPolicy(h, "")

	next := before
	next.BalanceVisibility = "HIDDEN"
	if _, err := h.svc.UpdatePolicy(context.Background(), next); err != nil {
		t.Fatalf("update: %v", err)
	}
	rows := policyRows(h.rec)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].EntityID == "" {
		t.Fatal("the dealer-default row was written with an empty entity id, it can never be read back")
	}
	if rows[0].EntityID != dealerScopeID {
		t.Fatalf("entity_id = %q, want the dealer sentinel %q", rows[0].EntityID, dealerScopeID)
	}
}

// preImageFailsStore is the shipped fake with its FIRST policy read broken, the quickpicks_test.go
// idiom (embed, override exactly what this case needs, leave every other test byte-identical). Only
// the first read fails because that is the pre-image; the read-back after the write must still work,
// which is the whole point of the assertion below.
type preImageFailsStore struct {
	*fakeStore
	reads int
}

func (p *preImageFailsStore) ReadPosPolicy(ctx context.Context, branchID string) (PosPolicy, error) {
	p.reads++
	if p.reads == 1 {
		return PosPolicy{}, errors.New("pre-image read failed")
	}
	return p.fakeStore.ReadPosPolicy(ctx, branchID)
}

// A pre-read that fails costs the history a row and must not cost the dealer their save. The
// dealer's intent is the important thing; refusing a legitimate settings change because a SECOND
// read failed would cost them the change itself.
func TestUpdatePolicy_PreImageFailureDoesNotBlockTheSave(t *testing.T) {
	h := adminHarness()
	seedPolicy(h, "branch-trt")
	h.svc.store = &preImageFailsStore{fakeStore: h.store}

	saved, err := h.svc.UpdatePolicy(context.Background(), PosPolicy{
		BranchID: "branch-trt", LinePresentation: LinePresentationCards,
		WorkspaceSplitPct: 40, WorkspaceArrangement: "LOOKUP_LEFT",
		LookupPresentation: "CARDS", MarginVisibility: "MANAGER_UP", BalanceVisibility: "ALL_STAFF",
	})
	if err != nil {
		t.Fatalf("an unreadable pre-image blocked the save: %v", err)
	}
	if saved.LinePresentation != LinePresentationCards {
		t.Fatalf("the save did not land: %+v", saved)
	}
	if rows := policyRows(h.rec); len(rows) != 0 {
		t.Fatalf("a history row was written from a pre-image that was never read: %+v", rows)
	}
}

// -- 2 · the read ---------------------------------------------------------------------------------

func trailRow(id, role, field, before, after, at string) audit.Entry {
	changes := map[string]any{"field": field, "actor_role": role}
	// The record boundary DROPS EMPTY VALUES, so a fake that wants to model an empty pre-image must
	// omit the key rather than set it to "", which is exactly the shape that used to render "<nil>".
	if before != "" {
		changes["before"] = before
	}
	if after != "" {
		changes["after"] = after
	}
	return audit.Entry{
		Action: ActPosPolicyChanged, EntityType: posPolicyEntity, EntityID: "branch-trt",
		ActorID: id, ActorRole: role, Changes: changes, CreatedAt: at,
	}
}

func TestPolicyHistory_NewestFirstWithNames(t *testing.T) {
	h := adminHarness()
	trail := &fakeTrail{rows: map[string][]audit.Entry{
		posPolicyEntity + "/branch-trt": {
			trailRow("u-1", "GENERAL_MANAGER", "margin_visibility", "MANAGER_UP", "ALL_STAFF", "2026-08-01T10:00:00Z"),
			trailRow("u-2", "YARD_MANAGER", "line_presentation", "LINES", "CARDS", "2026-08-02T10:00:00Z"),
		},
	}}
	names := &fakeNames{names: map[string]string{"u-1": "Alex Morgan", "u-2": "Sam Ochoa"}}
	h.svc.WithAuditTrail(trail).WithActorNames(names)

	got, err := h.svc.PolicyHistory(context.Background(), "branch-trt", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got[0].Field != "line_presentation" {
		t.Fatalf("newest row is %q, the trail is not being walked backwards", got[0].Field)
	}
	if got[0].Who != "Sam Ochoa (Yard Manager)" {
		t.Fatalf("who = %q, want the person with the role in parentheses", got[0].Who)
	}
	if got[1].Who != "Alex Morgan (General Manager)" {
		t.Fatalf("who = %q", got[1].Who)
	}
	if got[1].Before != "MANAGER_UP" || got[1].After != "ALL_STAFF" {
		t.Fatalf("before/after = %q/%q", got[1].Before, got[1].After)
	}
}

// NO USER ID EVER REACHES THE WIRE. The whole reason the name is resolved server-side is that the
// original ruling refused to put an identifier on a screen.
func TestPolicyHistory_NeverPutsAnIdOnTheWire(t *testing.T) {
	h := adminHarness()
	h.svc.WithAuditTrail(&fakeTrail{rows: map[string][]audit.Entry{
		posPolicyEntity + "/branch-trt": {
			trailRow("d7000000-0000-4000-9000-000000000010", "SUPER_ADMIN", "cod_enabled", "true", "false", "2026-08-01T10:00:00Z"),
		},
	}}).WithActorNames(&fakeNames{names: map[string]string{}})

	got, err := h.svc.PolicyHistory(context.Background(), "branch-trt", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if strings.Contains(got[0].Who, "d7000000") {
		t.Fatalf("who = %q, an actor id reached the wire", got[0].Who)
	}
	if got[0].Who != "an administrator" {
		t.Fatalf("who = %q, want the role word when no name resolves", got[0].Who)
	}
}

// An unresolvable name degrades to the role word; a resolver that ERRORS degrades the same way and
// never fails the read.
func TestPolicyHistory_NameFailureDegradesToTheRoleWord(t *testing.T) {
	h := adminHarness()
	h.svc.WithAuditTrail(&fakeTrail{rows: map[string][]audit.Entry{
		posPolicyEntity + "/branch-trt": {
			trailRow("u-1", "BRANCH_MANAGER", "cod_enabled", "true", "false", "2026-08-01T10:00:00Z"),
		},
	}}).WithActorNames(&fakeNames{err: errors.New("directory down")})

	got, err := h.svc.PolicyHistory(context.Background(), "branch-trt", 0)
	if err != nil {
		t.Fatalf("a name-resolution failure must not fail the read: %v", err)
	}
	if got[0].Who != "a branch manager" {
		t.Fatalf("who = %q", got[0].Who)
	}
}

// THE "<nil>" TRAP. audit's put() drops empty values, so a row recorded with an empty pre-image
// comes back with NO `before` key. fmt.Sprint of a missing map entry is the literal "<nil>".
func TestPolicyHistory_AbsentBeforeIsEmptyNotTheWordNil(t *testing.T) {
	h := adminHarness()
	h.svc.WithAuditTrail(&fakeTrail{rows: map[string][]audit.Entry{
		posPolicyEntity + "/branch-trt": {
			trailRow("u-1", "GENERAL_MANAGER", "quick_chip_set", "", "aggregate, mulch", "2026-08-01T10:00:00Z"),
		},
	}})
	got, err := h.svc.PolicyHistory(context.Background(), "branch-trt", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if got[0].Before != "" {
		t.Fatalf("before = %q, want the empty string", got[0].Before)
	}
	if strings.Contains(got[0].Before, "nil") {
		t.Fatal(`the history rendered the literal "<nil>" where a value was absent`)
	}
}

// The dealer-default read asks for the SAME scope the dealer-default write used. Without this the
// two halves would agree in code and disagree in the database.
func TestPolicyHistory_DealerDefaultReadsTheSentinelScope(t *testing.T) {
	h := adminHarness()
	trail := &fakeTrail{rows: map[string][]audit.Entry{}}
	h.svc.WithAuditTrail(trail)
	if _, err := h.svc.PolicyHistory(context.Background(), "", 0); err != nil {
		t.Fatalf("history: %v", err)
	}
	want := posPolicyEntity + "/" + dealerScopeID
	if len(trail.asks) != 1 || trail.asks[0] != want {
		t.Fatalf("asked %v, want %q", trail.asks, want)
	}
}

// An unwired trail is an empty history, never an error: a deployment that has not attached the audit
// read still gets a working settings screen.
func TestPolicyHistory_UnwiredTrailIsEmptyNotAnError(t *testing.T) {
	h := adminHarness()
	got, err := h.svc.PolicyHistory(context.Background(), "branch-trt", 0)
	if err != nil {
		t.Fatalf("an unwired trail must not error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want an empty slice, got %#v", got)
	}
}

// The read is gated where the WRITE is gated. A manager who is not an administrator may run the
// counter; they may not read who moved the visibility gates.
func TestPolicyHistory_RefusesANonAdmin(t *testing.T) {
	h := newHarness() // the shipped harness caller is a TierManager, not an admin
	h.svc.WithAuditTrail(&fakeTrail{rows: map[string][]audit.Entry{}})
	if _, err := h.svc.PolicyHistory(context.Background(), "branch-trt", 0); err == nil {
		t.Fatal("a non-administrator read the counter rule history")
	}
}

// -- 3 · the words --------------------------------------------------------------------------------

// The ladder is total: it never produces a blank cell and never produces a uuid.
func TestWhoWord_DegradationLadder(t *testing.T) {
	cases := []struct{ name, role, want string }{
		{"Alex Morgan", "GENERAL_MANAGER", "Alex Morgan (General Manager)"},
		{"Alex Morgan", "NOT_A_ROLE", "Alex Morgan (Not a role)"},
		{"", "LOGISTICS_MANAGER", "a dispatch manager"},
		{"", "PROCUREMENT_MANAGER", "a procurement manager"},
		{"", "", "somebody"},
		{"   ", "", "somebody"},
	}
	for _, c := range cases {
		if got := whoWord(c.name, c.role); got != c.want {
			t.Errorf("whoWord(%q,%q) = %q, want %q", c.name, c.role, got, c.want)
		}
	}
}

// Every role migration 0015 and 0043 seed has a title, spelled the way the roster spells it, so a
// history never shows a job a different name from the one on the staff screen.
func TestRoleTitle_CoversEverySeededRole(t *testing.T) {
	seeded := map[string]string{
		"GENERAL_MANAGER": "General Manager", "FINANCIAL_CONTROLLER": "Financial Controller",
		"HR": "HR", "BRANCH_MANAGER": "Branch Manager", "PROCUREMENT_MANAGER": "Procurement Manager",
		"SALES_MANAGER": "Sales Manager", "YARD_MANAGER": "Yard Manager",
		"LOGISTICS_MANAGER": "Logistics Manager", "PAYABLES_RECEIVABLES": "Payables/Receivables",
		"INSIDE_SALES": "Inside Sales", "OUTSIDE_SALES": "Outside Sales", "YARD_TEAM": "Yard Team",
		"DRIVERS": "Drivers", "SUPER_ADMIN": "Platform Administrator",
	}
	for code, want := range seeded {
		if got := roleTitle(code); got != want {
			t.Errorf("roleTitle(%q) = %q, want %q (migration 0015/0043)", code, got, want)
		}
	}
	if len(roleTitles) != len(seeded) {
		t.Fatalf("the title map holds %d roles and the seed holds %d, one of them has drifted",
			len(roleTitles), len(seeded))
	}
	// The default arm degrades honestly rather than SHOUTING.
	if got := roleTitle("AREA_SUPERVISOR"); got != "Area supervisor" {
		t.Fatalf("an unmapped role renders %q", got)
	}
	if roleTitle("") != "" {
		t.Fatal("an absent role must be distinguishable from a role with no title")
	}
}

// changeStr is what stands between a dropped audit key and the word "<nil>" on a settings screen.
func TestChangeStr_MissingAndRoundTrippedValues(t *testing.T) {
	if got := changeStr(nil); got != "" {
		t.Fatalf("a missing key renders %q, want the empty string", got)
	}
	if got := changeStr(float64(1250)); got != "1250" {
		t.Fatalf("a jsonb round-tripped int renders %q, want 1250", got)
	}
	if got := changeStr(true); got != "true" {
		t.Fatalf("a bool renders %q", got)
	}
}
