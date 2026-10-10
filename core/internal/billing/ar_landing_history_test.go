package billing

// ar_landing_history_test.go, the receivables settings change history.
//
// AR shipped with NO audit write on SetPolicy at all, so this file pins a behaviour that did not
// exist rather than guarding one that did. Three of its cases are the traps a straight copy of the
// dispatch recipe would have walked into:
//
//   · THE PRE-IMAGE. Policy() substitutes DefaultARLandingPolicy() whenever the read errors OR
//     AgingEscalateDays == 0, so diffing against it fabricates history rows on the first save of a
//     half-migrated database.
//   · THE SCOPE. The AR landing mounts with NO branch id and keeps branchId = '', so every save this
//     product has ever made targets the dealer row, the one audit writes as SQL NULL and can never
//     read back. Without a sentinel the history is empty BY CONSTRUCTION.
//   · THE EMPTY VALUE. untagged_fallback and ar_tier are TEXT columns defaulted only on the way IN,
//     and audit's record boundary drops empty values, so `before` can be a MISSING KEY, which
//     fmt.Sprint renders as the literal "<nil>".

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// -- fakes ----------------------------------------------------------------------------------------

type arFakeRecorder struct{ rows []audit.SensitiveAccess }

func (r *arFakeRecorder) Record(_ context.Context, a audit.SensitiveAccess) error {
	r.rows = append(r.rows, a)
	return nil
}

// arFakeTrail is the audit READ seam: canned rows per (entity_type, entity_id), oldest-first, which
// is the order *audit.Service.ReopenTransaction returns.
type arFakeTrail struct {
	rows map[string][]audit.Entry
	asks []string
}

func (f *arFakeTrail) ReopenTransaction(_ context.Context, entityType, entityID string) ([]audit.Entry, error) {
	f.asks = append(f.asks, entityType+"/"+entityID)
	return f.rows[entityType+"/"+entityID], nil
}

// arFakeNames is the ActorNames seam. An id it does not hold is absent, which is the real resolver's
// contract for a deleted staff member or a system actor.
type arFakeNames struct {
	names map[string]string
	err   error
}

func (f *arFakeNames) ActorNames(_ context.Context, _ []string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.names, nil
}

// adminCtx is a Tier-1 GLOBAL caller, what PUT /ar/landing/policy already requires.
func adminCtx(userID, role string) context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: userID, RoleCode: role, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal,
	})
}

func arChangeRows(rec *arFakeRecorder) []audit.SensitiveAccess {
	out := []audit.SensitiveAccess{}
	for _, r := range rec.rows {
		if r.Action == ActARPolicyChanged {
			out = append(out, r)
		}
	}
	return out
}

// -- 1 · the write --------------------------------------------------------------------------------

func TestARSetPolicy_RecordsOneRowPerChangedKnob(t *testing.T) {
	store := newFakeARStore()
	rec := &arFakeRecorder{}
	svc := NewARLandingService(store).WithAudit(rec)

	next := store.policy
	next.AgingEscalateDays = 75
	next.Tier = TierPro
	if _, err := svc.SetPolicy(adminCtx("u-fc", "FINANCIAL_CONTROLLER"), next); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	rows := arChangeRows(rec)
	if len(rows) != 2 {
		t.Fatalf("want 2 change rows, got %d: %+v", len(rows), rows)
	}
	got := map[string][2]string{}
	for _, r := range rows {
		if r.EntityType != arPolicyEntity {
			t.Fatalf("entity_type = %q", r.EntityType)
		}
		if r.ActorID != "u-fc" || r.Extra["actor_role"] != "FINANCIAL_CONTROLLER" {
			t.Fatalf("attribution = %q/%v", r.ActorID, r.Extra["actor_role"])
		}
		f, _ := r.Extra["field"].(string)
		b, _ := r.Extra["before"].(string)
		a, _ := r.Extra["after"].(string)
		got[f] = [2]string{b, a}
	}
	if got["aging_escalate_days"] != [2]string{"90", "75"} {
		t.Fatalf("aging_escalate_days row = %v, the PRE-image is missing", got["aging_escalate_days"])
	}
	if got["ar_tier"] != [2]string{TierBase, TierPro} {
		t.Fatalf("ar_tier row = %v", got["ar_tier"])
	}
}

func TestARSetPolicy_NoChangeWritesNoRow(t *testing.T) {
	store := newFakeARStore()
	rec := &arFakeRecorder{}
	svc := NewARLandingService(store).WithAudit(rec)
	if _, err := svc.SetPolicy(adminCtx("u-fc", "FINANCIAL_CONTROLLER"), store.policy); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	if rows := arChangeRows(rec); len(rows) != 0 {
		t.Fatalf("a no-op save wrote %d rows: %+v", len(rows), rows)
	}
}

// THE PRE-IMAGE TRAP, stated as a test. The stored row is HALF-MIGRATED, every knob is zero, which
// is exactly the shape that makes Policy() hand back DefaultARLandingPolicy(). Diffing against those
// defaults would report that somebody set the warn edge to 60, the escalate edge to 90, the cadence
// to 7 and six other things they never touched, on a save that moved ONE knob.
func TestARSetPolicy_PreImageIsStoredNotTheSubstitutedDefault(t *testing.T) {
	store := newFakeARStore()
	store.policy = ARLandingPolicy{} // a row that has not been migrated forward
	rec := &arFakeRecorder{}
	svc := NewARLandingService(store).WithAudit(rec)

	// Prove the substitution is live, so this test cannot pass vacuously.
	if got := svc.Policy(context.Background(), ""); got.AgingEscalateDays != 90 {
		t.Fatal("Policy() no longer substitutes the shipped defaults, the trap this test guards cannot fire")
	}

	if _, err := svc.SetPolicy(adminCtx("u-fc", "FINANCIAL_CONTROLLER"), ARLandingPolicy{
		AgingWarnDays: 60, AgingEscalateDays: 90, ReminderCadenceDays: 7, ReminderMaxPerCycle: 3,
		BlackHoleWindowDays: 45, BlackHoleMinSends: 3, ReminderEmailEnabled: true,
		UntaggedFallback: FallbackConfirm, Tier: TierBase,
	}); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	// Against the STORED zero row, every one of those knobs really did move, and every `before` must
	// be the zero that was there, never the default that was never there.
	for _, r := range arChangeRows(rec) {
		f, _ := r.Extra["field"].(string)
		b, _ := r.Extra["before"].(string)
		switch f {
		case "aging_warn_days", "aging_escalate_days", "reminder_cadence_days":
			if b != "0" {
				t.Fatalf("%s recorded before=%q, the diff read through the SUBSTITUTED default", f, b)
			}
		}
	}
}

// THE SCOPE TRAP. The AR landing mounts with no branch id, so this is the everyday path and not an
// edge case: written with "" the row lands as SQL NULL and `entity_id = $2::uuid` never matches it.
func TestARSetPolicy_DealerDefaultIsFiledUnderAReadableScope(t *testing.T) {
	store := newFakeARStore()
	rec := &arFakeRecorder{}
	svc := NewARLandingService(store).WithAudit(rec)

	next := store.policy
	next.BranchID = "" // exactly what the settings panel sends
	next.UntaggedFallback = FallbackRefuse
	if _, err := svc.SetPolicy(adminCtx("u-fc", "FINANCIAL_CONTROLLER"), next); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	rows := arChangeRows(rec)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].EntityID == "" {
		t.Fatal("the dealer-default row was written with an empty entity id, it can never be read back")
	}
	if rows[0].EntityID != arDealerScopeID {
		t.Fatalf("entity_id = %q, want the dealer sentinel", rows[0].EntityID)
	}
}

// A nil recorder records nothing and breaks nothing, the CreditService rule, verbatim.
func TestARSetPolicy_NilRecorderStillSaves(t *testing.T) {
	store := newFakeARStore()
	svc := NewARLandingService(store)
	next := store.policy
	next.AgingEscalateDays = 120
	saved, err := svc.SetPolicy(adminCtx("u-fc", "FINANCIAL_CONTROLLER"), next)
	if err != nil {
		t.Fatalf("an unwired recorder blocked the save: %v", err)
	}
	if saved.AgingEscalateDays != 120 {
		t.Fatalf("the save did not land: %+v", saved)
	}
}

// -- 2 · the read ---------------------------------------------------------------------------------

func arTrailRow(id, role, field, before, after, at string) audit.Entry {
	changes := map[string]any{"field": field, "actor_role": role}
	// The record boundary DROPS EMPTY VALUES, so a fake modelling an empty pre-image must OMIT the
	// key, which is exactly the shape that used to render "<nil>".
	if before != "" {
		changes["before"] = before
	}
	if after != "" {
		changes["after"] = after
	}
	return audit.Entry{
		Action: ActARPolicyChanged, EntityType: arPolicyEntity, EntityID: arDealerScopeID,
		ActorID: id, ActorRole: role, Changes: changes, CreatedAt: at,
	}
}

func TestARPolicyHistory_NewestFirstWithNames(t *testing.T) {
	trail := &arFakeTrail{rows: map[string][]audit.Entry{
		arPolicyEntity + "/" + arDealerScopeID: {
			arTrailRow("u-1", "FINANCIAL_CONTROLLER", "aging_escalate_days", "90", "75", "2026-08-01T10:00:00Z"),
			arTrailRow("u-2", "GENERAL_MANAGER", "ar_tier", "BASE", "PRO", "2026-08-02T10:00:00Z"),
		},
	}}
	svc := NewARLandingService(newFakeARStore()).
		WithAuditTrail(trail).
		WithActorNames(&arFakeNames{names: map[string]string{"u-1": "Dana Reid", "u-2": "Alex Morgan"}})

	got, err := svc.PolicyHistory(adminCtx("u-admin", "SUPER_ADMIN"), "", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}
	if got[0].Field != "ar_tier" {
		t.Fatalf("newest row is %q, the trail is not being walked backwards", got[0].Field)
	}
	if got[0].Who != "Alex Morgan (General Manager)" {
		t.Fatalf("who = %q", got[0].Who)
	}
	if got[1].Who != "Dana Reid (Financial Controller)" {
		t.Fatalf("who = %q", got[1].Who)
	}
	if got[1].Before != "90" || got[1].After != "75" {
		t.Fatalf("before/after = %q/%q", got[1].Before, got[1].After)
	}
}

// The read asks for the SAME scope the write used. Without this the two halves agree in code and
// disagree in the database, which is the failure the whole sentinel exists to prevent.
func TestARPolicyHistory_DealerDefaultReadsTheScopeTheWriteUsed(t *testing.T) {
	trail := &arFakeTrail{rows: map[string][]audit.Entry{}}
	svc := NewARLandingService(newFakeARStore()).WithAuditTrail(trail)
	if _, err := svc.PolicyHistory(adminCtx("u-admin", "SUPER_ADMIN"), "", 0); err != nil {
		t.Fatalf("history: %v", err)
	}
	want := arPolicyEntity + "/" + arDealerScopeID
	if len(trail.asks) != 1 || trail.asks[0] != want {
		t.Fatalf("asked %v, want %q", trail.asks, want)
	}
}

// END TO END ON THE ONE PATH THAT MATTERS: a dealer-default save, then a dealer-default read. This
// is the assertion that would have failed against a straight copy of the dispatch recipe.
func TestARPolicyHistory_ADealerDefaultSaveIsReadableAfterwards(t *testing.T) {
	store := newFakeARStore()
	rec := &arFakeRecorder{}
	svc := NewARLandingService(store).WithAudit(rec)

	next := store.policy
	next.AgingEscalateDays = 75
	if _, err := svc.SetPolicy(adminCtx("u-fc", "FINANCIAL_CONTROLLER"), next); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	// Replay what the recorder captured back through the trail seam, keyed the way audit keys it.
	trail := &arFakeTrail{rows: map[string][]audit.Entry{}}
	for _, r := range arChangeRows(rec) {
		changes := map[string]any{}
		for k, v := range r.Extra {
			if s, ok := v.(string); ok && s == "" {
				continue // audit's put() drops empty values, model that faithfully
			}
			changes[k] = v
		}
		key := r.EntityType + "/" + r.EntityID
		trail.rows[key] = append(trail.rows[key], audit.Entry{
			Action: r.Action, EntityType: r.EntityType, EntityID: r.EntityID,
			ActorID: r.ActorID, ActorRole: "FINANCIAL_CONTROLLER",
			Changes: changes, CreatedAt: "2026-08-31T09:00:00Z",
		})
	}
	svc.WithAuditTrail(trail)

	got, err := svc.PolicyHistory(adminCtx("u-admin", "SUPER_ADMIN"), "", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a dealer-default save produced %d readable history rows, want 1, the scopes disagree", len(got))
	}
	if got[0].Field != "aging_escalate_days" || got[0].Before != "90" || got[0].After != "75" {
		t.Fatalf("round-tripped row = %+v", got[0])
	}
}

// THE "<nil>" TRAP, on the two AR knobs that can actually carry an empty pre-image.
func TestARPolicyHistory_AbsentBeforeIsEmptyNotTheWordNil(t *testing.T) {
	trail := &arFakeTrail{rows: map[string][]audit.Entry{
		arPolicyEntity + "/" + arDealerScopeID: {
			arTrailRow("u-1", "GENERAL_MANAGER", "untagged_fallback", "", FallbackConfirm, "2026-08-01T10:00:00Z"),
			arTrailRow("u-1", "GENERAL_MANAGER", "ar_tier", "", TierBase, "2026-08-01T10:00:01Z"),
		},
	}}
	svc := NewARLandingService(newFakeARStore()).WithAuditTrail(trail)
	got, err := svc.PolicyHistory(adminCtx("u-admin", "SUPER_ADMIN"), "", 0)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, row := range got {
		if row.Before != "" {
			t.Fatalf("%s before = %q, want the empty string", row.Field, row.Before)
		}
		if strings.Contains(row.Before, "nil") {
			t.Fatalf(`%s rendered the literal "<nil>"`, row.Field)
		}
	}
}

func TestARPolicyHistory_NameFailureDegradesToTheRoleWord(t *testing.T) {
	trail := &arFakeTrail{rows: map[string][]audit.Entry{
		arPolicyEntity + "/" + arDealerScopeID: {
			arTrailRow("u-1", "PAYABLES_RECEIVABLES", "ar_tier", "BASE", "PRO", "2026-08-01T10:00:00Z"),
		},
	}}
	svc := NewARLandingService(newFakeARStore()).
		WithAuditTrail(trail).WithActorNames(&arFakeNames{err: errors.New("directory down")})

	got, err := svc.PolicyHistory(adminCtx("u-admin", "SUPER_ADMIN"), "", 0)
	if err != nil {
		t.Fatalf("a name-resolution failure must not fail the read: %v", err)
	}
	if got[0].Who != "somebody in receivables" {
		t.Fatalf("who = %q", got[0].Who)
	}
	if strings.Contains(got[0].Who, "u-1") {
		t.Fatalf("who = %q, an actor id reached the wire", got[0].Who)
	}
}

func TestARPolicyHistory_UnwiredTrailIsEmptyNotAnError(t *testing.T) {
	svc := NewARLandingService(newFakeARStore())
	got, err := svc.PolicyHistory(adminCtx("u-admin", "SUPER_ADMIN"), "", 0)
	if err != nil {
		t.Fatalf("an unwired trail must not error: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("want an empty slice, got %#v", got)
	}
}

// The read is gated where the WRITE is gated. A manager may read the landing; only an administrator
// may read who moved the escalation edge.
func TestARPolicyHistory_RefusesANonAdmin(t *testing.T) {
	svc := NewARLandingService(newFakeARStore()).WithAuditTrail(&arFakeTrail{rows: map[string][]audit.Entry{}})
	mgrCtx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "u-mgr", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
	})
	if _, err := svc.PolicyHistory(mgrCtx, "", 0); err == nil {
		t.Fatal("a non-administrator read the receivables rule history")
	}
	if _, err := svc.PolicyHistory(context.Background(), "", 0); err == nil {
		t.Fatal("an unauthenticated caller read the receivables rule history")
	}
}

// -- 3 · the words --------------------------------------------------------------------------------

func TestARWhoWord_DegradationLadder(t *testing.T) {
	cases := []struct{ name, role, want string }{
		{"Dana Reid", "FINANCIAL_CONTROLLER", "Dana Reid (Financial Controller)"},
		{"Dana Reid", "NOT_A_ROLE", "Dana Reid (Not a role)"},
		{"", "SUPER_ADMIN", "an administrator"},
		{"", "SALES_MANAGER", "a sales manager"},
		{"", "", "somebody"},
	}
	for _, c := range cases {
		if got := arWhoWord(c.name, c.role); got != c.want {
			t.Errorf("arWhoWord(%q,%q) = %q, want %q", c.name, c.role, got, c.want)
		}
	}
}

// The two histories must spell a job the SAME way, or one screen calls somebody a Logistics Manager
// and the next calls them something else.
func TestARRoleTitle_CoversEverySeededRole(t *testing.T) {
	seeded := map[string]string{
		"GENERAL_MANAGER": "General Manager", "FINANCIAL_CONTROLLER": "Financial Controller",
		"HR": "HR", "BRANCH_MANAGER": "Branch Manager", "PROCUREMENT_MANAGER": "Procurement Manager",
		"SALES_MANAGER": "Sales Manager", "YARD_MANAGER": "Yard Manager",
		"LOGISTICS_MANAGER": "Logistics Manager", "PAYABLES_RECEIVABLES": "Payables/Receivables",
		"INSIDE_SALES": "Inside Sales", "OUTSIDE_SALES": "Outside Sales", "YARD_TEAM": "Yard Team",
		"DRIVERS": "Drivers", "SUPER_ADMIN": "Platform Administrator",
	}
	for code, want := range seeded {
		if got := arRoleTitle(code); got != want {
			t.Errorf("arRoleTitle(%q) = %q, want %q (migration 0015/0043)", code, got, want)
		}
	}
	if len(arRoleTitles) != len(seeded) {
		t.Fatalf("the title map holds %d roles and the seed holds %d, one has drifted",
			len(arRoleTitles), len(seeded))
	}
	if got := arRoleTitle("AREA_SUPERVISOR"); got != "Area supervisor" {
		t.Fatalf("an unmapped role renders %q", got)
	}
}

func TestARChangeStr_MissingAndRoundTrippedValues(t *testing.T) {
	if got := arChangeStr(nil); got != "" {
		t.Fatalf("a missing key renders %q, want the empty string", got)
	}
	if got := arChangeStr(float64(90)); got != "90" {
		t.Fatalf("a jsonb round-tripped int renders %q, want 90", got)
	}
	if got := arChangeStr(false); got != "false" {
		t.Fatalf("a bool renders %q", got)
	}
}

// TestSetPolicy_RefusesNonAdminWithTheFourRolesNamedExactly pins the billing admin-refusal text.
// The gate caller.IsAdmin() admits exactly the four Tier-1 GLOBAL roles (General Manager, Financial
// Controller, HR and Platform Administrator), the refusal names those four so the reader sees who
// may act instead of "limited to administrators", and the caller's role code never reaches the
// words; the package prefix is "billing:" to match the convention the settings kit carries.
// Red-first witness: this test fails on main with the old "is limited to administrators, ask a
// tech admin" message, passes here.
func TestSetPolicy_RefusesNonAdminWithTheFourRolesNamedExactly(t *testing.T) {
	frontlineCtx := func() context.Context {
		return identity.ContextWithStaff(context.Background(), identity.StaffContext{
			UserID: "u-frontline", RoleCode: "INSIDE_SALES", Tier: identity.TierFrontline, Scope: identity.ScopeBranch,
		})
	}
	// The store is never reached: the gate fires before any store deref, so nil is enough.
	svc := NewRegisterService(nil)
	const want = "billing: only the General Manager, the Financial Controller, HR or the Platform Administrator change the dealer screen settings"
	err := svc.SetPolicy(frontlineCtx(), RegisterPolicy{
		AccountPositionVisibility: VisibilityManagerUp,
		DueSoonDays:               7,
		ReminderCooldownHours:     24,
		WorkspaceSplitPct:         50,
		WorkspaceArrangement:      "PRIMARY_LEFT",
		ReminderTones:             []string{"NICE"},
	})
	if err == nil {
		t.Fatalf("a frontline caller must be refused by SetPolicy; got nil")
	}
	if got := err.Error(); got != want {
		t.Fatalf("the billing admin refusal must name the four roles exactly:\n  want %q\n  got  %q", want, got)
	}
}
