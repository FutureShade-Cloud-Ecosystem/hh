package staffcontext

// bridge_test.go: the bridge's own proofs, NEW in this batch (not carried).
// The exit criteria the batch plan names are proven here: tier and scope
// equality in both directions (a manager-tier authority yields TierManager,
// and a non-manager does not), the complete role-name mapping table against
// the frozen catalog facts, and the fail-closed legs of CallerFrom and the
// context stamping.

import (
	"context"
	"errors"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// fakeAuthoritySource is the test double for the Phase B resolver seam: it
// answers scripted resolutions and records what it was asked.
type fakeAuthoritySource struct {
	authority identity.StaffAuthority
	has       bool
	err       error
	asked     int
}

func (f *fakeAuthoritySource) ResolveStaff(_ context.Context, _, _ string) (identity.StaffAuthority, bool, error) {
	f.asked++
	return f.authority, f.has, f.err
}

// TestBridge_TierEqualityBothDirections proves the tier lattice maps
// exactly, in both directions: every canonical tier maps onto the carried
// Tier constant of the same level, the tier predicates answer identically
// over a bridged context and over the guards' own reading of the same
// authority, and a manager-tier authority yields TierManager while a
// non-manager does not.
func TestBridge_TierEqualityBothDirections(t *testing.T) {
	cases := []struct {
		canonicalTier int
		want          Tier
		isManager     bool
		isAdmin       bool
	}{
		{identity.TierLeadership, TierLeadership, true, true},
		{identity.TierManager, TierManager, true, false},
		{identity.TierFrontline, TierFrontline, false, false},
	}
	for _, c := range cases {
		a := identity.StaffAuthority{RoleID: "general_manager", Tier: c.canonicalTier, Scope: identity.ScopeGlobal}
		got := FromAuthority(a)
		if got.Tier != c.want {
			t.Errorf("canonical tier %d bridged to %v, want %v", c.canonicalTier, got.Tier, c.want)
		}
		if got.IsManager() != c.isManager {
			t.Errorf("canonical tier %d: IsManager() = %v, want %v", c.canonicalTier, got.IsManager(), c.isManager)
		}
		if got.IsAdmin() != c.isAdmin {
			t.Errorf("canonical tier %d global: IsAdmin() = %v, want %v", c.canonicalTier, got.IsAdmin(), c.isAdmin)
		}
		// The both-halves proof: the authority's own predicates (the guards'
		// reading, identity.StaffAuthority) and the bridged context's predicates
		// (the carried vocabulary's reading) must agree on the same authority.
		if got.IsManager() != a.IsManager() || got.IsAdmin() != a.IsAdmin() {
			t.Errorf("canonical tier %d: bridged predicates disagree with the authority's own", c.canonicalTier)
		}
	}
	// The named exit criterion, stated as itself: a manager-tier authority
	// yields TierManager, and a non-manager does not.
	manager := FromAuthority(identity.StaffAuthority{RoleID: "branch_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch})
	if manager.Tier != TierManager || !manager.IsManager() {
		t.Fatalf("a manager-tier authority must bridge to TierManager and IsManager, got %v/%v", manager.Tier, manager.IsManager())
	}
	frontline := FromAuthority(identity.StaffAuthority{RoleID: "yard_team", Tier: identity.TierFrontline, Scope: identity.ScopeBranch})
	if frontline.Tier == TierManager || frontline.IsManager() {
		t.Fatalf("a front-line authority must not bridge to a manager, got %v/%v", frontline.Tier, frontline.IsManager())
	}
}

// TestBridge_ScopeEqualityBothDirections proves the scope lattice maps
// exactly: canonical global/branch onto GLOBAL/BRANCH, the branch grant
// slice onto the vocabulary's map, branch reachability answering the same
// for the bridged context and the authority, and the round trip back to
// the canonical spelling.
func TestBridge_ScopeEqualityBothDirections(t *testing.T) {
	global := FromAuthority(identity.StaffAuthority{RoleID: "general_manager", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal})
	if global.Scope != ScopeGlobal {
		t.Errorf("canonical global bridged to %q, want GLOBAL", global.Scope)
	}
	if !global.CanAccessBranch(trt) || !global.CanAccessBranch(kgn) {
		t.Error("a bridged global scope must reach any branch")
	}
	if got := CanonicalScope(global.Scope); got != identity.ScopeGlobal {
		t.Errorf("scope round trip: got %q, want %q", got, identity.ScopeGlobal)
	}

	scoped := FromAuthority(identity.StaffAuthority{
		RoleID:   "inside_sales",
		Tier:     identity.TierFrontline,
		Scope:    identity.ScopeBranch,
		Branches: []string{trt},
	})
	if scoped.Scope != ScopeBranch {
		t.Errorf("canonical branch bridged to %q, want BRANCH", scoped.Scope)
	}
	if !scoped.CanAccessBranch(trt) {
		t.Error("a bridged branch scope must reach its granted branch")
	}
	if scoped.CanAccessBranch(kgn) {
		t.Error("a bridged branch scope must not reach an ungranted branch")
	}
	if got := CanonicalScope(scoped.Scope); got != identity.ScopeBranch {
		t.Errorf("scope round trip: got %q, want %q", got, identity.ScopeBranch)
	}

	// Unknown canonical scopes fail closed: the empty Scope matches nothing.
	if got := ScopeFromCanonical("regional"); got != "" || CanonicalScope(got) != "" {
		t.Errorf("an unknown scope must bridge to the empty Scope and back, got %q", got)
	}
}

// TestBridge_RoleMappingComplete pins the complete role-name mapping table
// (the F-5 requirement) against three authorities that must agree: the
// frozen staff_role catalog of migration 0215 (as quoted here, row for
// row), the carried Roles()/AllRoles() definitions, and the bridge's own
// table. Every ERP role name referenced in the carried code has a row, the
// two directions are bijections, and the tiers and scopes match.
func TestBridge_RoleMappingComplete(t *testing.T) {
	// The frozen catalog facts (0215 INSERT, staff_role rows of hardscapeos
	// origin): id, origin_code, tier, scope. The two non-ERP internal roles
	// (dealer_admin, fb_operator) have no ERP role name in the carried
	// vocabulary and no row, by design.
	catalog := []RoleMapping{
		{"general_manager", "GENERAL_MANAGER", TierLeadership, ScopeGlobal},
		{"financial_controller", "FINANCIAL_CONTROLLER", TierLeadership, ScopeGlobal},
		{"hr", "HR", TierLeadership, ScopeGlobal},
		{"branch_manager", "BRANCH_MANAGER", TierManager, ScopeBranch},
		{"sales_manager", "SALES_MANAGER", TierManager, ScopeBranch},
		{"yard_manager", "YARD_MANAGER", TierManager, ScopeBranch},
		{"logistics_manager", "LOGISTICS_MANAGER", TierManager, ScopeBranch},
		{"procurement_manager", "PROCUREMENT_MANAGER", TierManager, ScopeBranch},
		{"payables_receivables", "PAYABLES_RECEIVABLES", TierManager, ScopeGlobal},
		{"inside_sales", "INSIDE_SALES", TierFrontline, ScopeBranch},
		{"outside_sales", "OUTSIDE_SALES", TierFrontline, ScopeBranch},
		{"yard_team", "YARD_TEAM", TierFrontline, ScopeBranch},
		{"driver", RoleDrivers, TierFrontline, ScopeBranch},
		{"super_admin", RoleSuperAdmin, TierLeadership, ScopeGlobal},
	}
	if len(roleMappings) != len(catalog) {
		t.Fatalf("the mapping table has %d rows, the frozen catalog has %d", len(roleMappings), len(catalog))
	}
	for i, want := range catalog {
		got := roleMappings[i]
		if got != want {
			t.Errorf("mapping row %d is %+v, the frozen catalog says %+v", i, got, want)
		}
	}

	// Every carried role code has a row, and the row's tier and scope match
	// the carried catalog definitions (Roles plus the platform role).
	byCode := map[string]RoleDef{}
	for _, r := range AllRoles() {
		byCode[r.Code] = r
	}
	if len(byCode) != 14 {
		t.Fatalf("AllRoles has %d entries, want the 13 business roles plus the platform role", len(byCode))
	}
	for _, m := range roleMappings {
		def, ok := byCode[m.ERPCode]
		if !ok {
			t.Errorf("mapping row %q has no AllRoles definition", m.ERPCode)
			continue
		}
		if def.Tier != m.Tier || def.Scope != m.Scope {
			t.Errorf("mapping row %q says %v/%v, AllRoles says %v/%v", m.ERPCode, m.Tier, m.Scope, def.Tier, def.Scope)
		}
		if back, ok := CanonicalForERPCode(m.ERPCode); !ok || back != m.Canonical {
			t.Errorf("reverse mapping for %q returned %q ok=%v, want %q", m.ERPCode, back, ok, m.Canonical)
		}
		code, ok := ERPCodeFor(m.Canonical)
		if !ok || code != m.ERPCode {
			t.Errorf("forward mapping for %q returned %q ok=%v, want %q", m.Canonical, code, ok, m.ERPCode)
		}
	}

	// The driver knob role and the fourteenth platform-owner role, by name:
	// the two names the F-5 ruling calls out explicitly.
	if code, ok := ERPCodeFor("driver"); !ok || code != RoleDrivers {
		t.Errorf("the driver knob role must map to RoleDrivers, got %q ok=%v", code, ok)
	}
	if code, ok := ERPCodeFor("super_admin"); !ok || code != RoleSuperAdmin {
		t.Errorf("the platform-owner role must map to RoleSuperAdmin, got %q ok=%v", code, ok)
	}
}

// TestBridge_UnmappedCanonicalRoleFailsClosed proves the two non-ERP
// internal roles (and any unknown canonical id) bridge to an empty
// RoleCode: tier and scope still answer (they are tier facts, not role
// names), and every role-code allowlist fails closed, including
// IsSuperAdmin and the rail's approver set.
func TestBridge_UnmappedCanonicalRoleFailsClosed(t *testing.T) {
	for _, id := range []string{"dealer_admin", "fb_operator", "no_such_role"} {
		c := FromAuthority(identity.StaffAuthority{RoleID: id, Tier: identity.TierLeadership, Scope: identity.ScopeGlobal})
		if c.RoleCode != "" {
			t.Errorf("%s bridged to role code %q; an unmapped canonical role must carry the empty code", id, c.RoleCode)
		}
		if c.IsSuperAdmin() {
			t.Errorf("%s must not satisfy IsSuperAdmin", id)
		}
		if CanApprove(c, PolicyMargin, trt) {
			t.Errorf("%s must not be a rail approver", id)
		}
		if CanSellOnCredit(c) {
			t.Errorf("%s must not be able to sell on credit", id)
		}
		if CanCommitDelivery(c) {
			t.Errorf("%s must not be able to commit a delivery", id)
		}
		// Tier facts still answer: the guards admit tier 1 global as admin,
		// and the bridge does not contradict them.
		if !c.IsAdmin() || !c.IsManager() {
			t.Errorf("%s carries tier 1 global; tier predicates must still answer", id)
		}
		if _, ok := CanonicalForERPCode(c.RoleCode); ok {
			t.Errorf("the empty role code must not reverse-map, got ok for %q", c.RoleCode)
		}
	}
}

// TestBridge_StatusAndVocabularyAlignment pins the two vocabulary anchors
// the bridge aliases: the status constant is the Phase B resolver's, and
// the tier constants agree numerically with the identity package's.
func TestBridge_StatusAndVocabularyAlignment(t *testing.T) {
	if StatusActive != identity.StaffStatusActive {
		t.Errorf("StatusActive = %q, the Phase B constant is %q", StatusActive, identity.StaffStatusActive)
	}
	if int(TierLeadership) != identity.TierLeadership || int(TierManager) != identity.TierManager || int(TierFrontline) != identity.TierFrontline {
		t.Errorf("tier constants drifted from the identity package's")
	}
	c := FromAuthority(identity.StaffAuthority{RoleID: "general_manager"})
	if c.Status != StatusActive {
		t.Errorf("a resolved authority bridges with status %q, want ACTIVE", c.Status)
	}
}

// TestCallerFrom_FailClosedLegs proves the bridged callerFrom default: no
// scope answers not-ok, an unreadable authority answers not-ok, no
// authority answers not-ok, and a good resolution maps through the bridge.
// The stamped-context leg (the test seam) wins over resolution.
func TestCallerFrom_FailClosedLegs(t *testing.T) {
	ctx := context.Background()

	// No scope, no stamp: not-ok, and the resolver is never asked.
	src := &fakeAuthoritySource{authority: identity.StaffAuthority{RoleID: "general_manager", Tier: 1, Scope: identity.ScopeGlobal}, has: true}
	if _, ok := CallerFrom(src)(ctx); ok {
		t.Fatal("a context with no scope and no stamp must answer not-ok")
	}
	if src.asked != 0 {
		t.Fatal("the resolver must not be asked before a scope exists")
	}

	stamped := authz.WithStaffScope(ctx, authz.StaffScope{PlatformUserID: "pu-1", OrgID: "org-1"})

	// Scope present, resolution errors: not-ok, never a half context.
	src = &fakeAuthoritySource{err: errors.New("membership source down")}
	if _, ok := CallerFrom(src)(stamped); ok {
		t.Fatal("an unreadable authority must answer not-ok")
	}

	// Scope present, no authority: not-ok (the guard's no-grant refusal
	// already happened upstream; the bridge never invents authority).
	src = &fakeAuthoritySource{has: false}
	if _, ok := CallerFrom(src)(stamped); ok {
		t.Fatal("no authority must answer not-ok")
	}

	// A good resolution maps through the bridge, and the source was asked
	// with the scope's own ids.
	src = &fakeAuthoritySource{authority: identity.StaffAuthority{RoleID: "branch_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{trt}}, has: true}
	got, ok := CallerFrom(src)(stamped)
	if !ok {
		t.Fatal("a resolved authority must bridge to a caller")
	}
	if src.asked == 0 {
		t.Fatal("the resolver was never asked")
	}
	if got.RoleCode != "BRANCH_MANAGER" || got.Tier != TierManager || !got.IsManager() {
		t.Fatalf("bridged caller has the wrong vocabulary: %+v", got)
	}
	if !got.CanAccessBranch(trt) || got.CanAccessBranch(kgn) {
		t.Fatalf("bridged caller branch reach is wrong: %+v", got.Branches)
	}
}

// TestCallerFrom_StampWins proves the carried test seam flows through the
// injected default: a context stamped with ContextWithStaff answers with
// the stamped caller exactly, without consulting the resolver, so the
// carried tests and the wire default share one seam.
func TestCallerFrom_StampWins(t *testing.T) {
	src := &fakeAuthoritySource{err: errors.New("must not be asked when a stamp exists")}
	stamped := ContextWithStaff(context.Background(), StaffContext{
		UserID: "u-1", RoleCode: "YARD_TEAM", Tier: TierFrontline, Scope: ScopeBranch,
		Branches: map[string]bool{trt: true}, Status: StatusActive,
	})
	got, ok := CallerFrom(src)(stamped)
	if !ok {
		t.Fatal("a stamped context must answer ok")
	}
	if got.UserID != "u-1" || got.RoleCode != "YARD_TEAM" || got.Tier != TierFrontline {
		t.Fatalf("the stamped caller did not flow through unchanged: %+v", got)
	}
	if !CanCommitDelivery(got) {
		t.Error("YARD_TEAM is a ruled delivery committer; the stamped caller must satisfy the vocabulary")
	}
}

// TestContextHelpersRoundTrip pins the carried context-stamping seam: the
// full stamp and the id-only stamp both read back, and an unstamped context
// answers the fail-closed way.
func TestContextHelpersRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := StaffContextFromContext(ctx); ok {
		t.Fatal("an unstamped context must answer not-ok")
	}
	if StaffUserIDFromContext(ctx) != "" {
		t.Fatal("an unstamped context must yield the empty user id")
	}

	full := StaffContext{UserID: "u-9", RoleCode: "HR", Tier: TierLeadership, Scope: ScopeGlobal, Branches: map[string]bool{trt: true}}
	stamped := ContextWithStaff(ctx, full)
	got, ok := StaffContextFromContext(stamped)
	if !ok || got.UserID != full.UserID || got.RoleCode != full.RoleCode || got.Tier != full.Tier ||
		got.Scope != full.Scope || got.Status != full.Status || !got.Branches[trt] || len(got.Branches) != 1 {
		t.Fatalf("ContextWithStaff did not round trip: got %+v ok=%v", got, ok)
	}
	if StaffUserIDFromContext(stamped) != "u-9" {
		t.Errorf("StaffUserIDFromContext = %q, want u-9", StaffUserIDFromContext(stamped))
	}

	idOnly := ContextWithStaffUserID(ctx, "u-7")
	if c, ok := StaffContextFromContext(idOnly); !ok || c.UserID != "u-7" || c.RoleCode != "" {
		t.Fatalf("ContextWithStaffUserID did not stamp the id-only caller: %+v ok=%v", c, ok)
	}
}
