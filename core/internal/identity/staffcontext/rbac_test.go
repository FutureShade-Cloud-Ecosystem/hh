package staffcontext

import "testing"

const (
	trt = "5d1b3f6e-0000-4000-9000-000000000001" // Trenton
	kgn = "5d1b3f6e-0000-4000-9000-000000000002" // Kingston
	// UUID-shaped fixture ids: DecideApproval now UUID-validates its path id (real ids
	// come from uuid columns), so fixtures must be canonical UUIDs, not "u1"/"ar1".
	tstUser     = "7a1c9f00-0000-4000-9000-0000000000a1" // a staff user id
	tstApproval = "7a1c9f00-0000-4000-9000-0000000000b2" // an approval request id
)

func TestRolesCatalog(t *testing.T) {
	roles := Roles()
	if len(roles) != 13 {
		t.Fatalf("want 13 roles, got %d", len(roles))
	}
	var t1, t2, t3, global int
	for _, r := range roles {
		switch r.Tier {
		case TierLeadership:
			t1++
		case TierManager:
			t2++
		case TierFrontline:
			t3++
		}
		if r.Scope == ScopeGlobal {
			global++
		}
	}
	if t1 != 3 || t2 != 6 || t3 != 4 {
		t.Fatalf("tier split want 3/6/4, got %d/%d/%d", t1, t2, t3)
	}
	if global != 4 { // GM, Financial Controller, HR, Payables/Receivables
		t.Fatalf("want 4 GLOBAL roles, got %d", global)
	}
}

func TestMarginBps(t *testing.T) {
	cases := []struct {
		price, cost, want int64
	}{
		{10000, 8000, 2000}, // 20%
		{10000, 8800, 1200}, // exactly the floor
		{10000, 9400, 600},  // 6%, below floor
		{0, 100, 0},         // guard: zero price
		{10000, 10000, 0},   // zero margin
	}
	for _, c := range cases {
		if got := MarginBps(c.price, c.cost); got != c.want {
			t.Errorf("MarginBps(%d,%d) = %d, want %d", c.price, c.cost, got, c.want)
		}
	}
}

func TestCheckMargin(t *testing.T) {
	if b := CheckMargin(10000, 8000, trt, false); b != nil {
		t.Errorf("20%% margin should pass, got block %v", b)
	}
	if b := CheckMargin(10000, 8800, trt, false); b != nil {
		t.Errorf("exactly 12%% should pass, got block %v", b)
	}
	b := CheckMargin(10000, 9400, trt, false)
	if b == nil || b.Policy != PolicyMargin {
		t.Fatalf("6%% margin should block with MARGIN policy, got %v", b)
	}
	if b := CheckMargin(10000, 9400, trt, true); b != nil {
		t.Errorf("approved override should let a below-margin quote pass, got %v", b)
	}
}

// R1: pins the legacy CheckMargin (4-arg) path unchanged after CheckMarginAt was
// added additively (A-07 / D-A07-4). CheckMargin must still gate at the 1200-bps
// const, must equal CheckMarginAt(...,MarginFloorBps) for the same inputs, and must
// route a below-floor quote to the 0016 queue (Policy == MARGIN_BELOW_MINIMUM).
func TestCheckMargin_LegacyPathUnchanged(t *testing.T) {
	if MarginFloorBps != 1200 {
		t.Fatalf("legacy MarginFloorBps changed to %d; the legacy path must stay at 1200 bps", MarginFloorBps)
	}
	cases := []struct {
		price, cost int64
		override    bool
		wantBlock   bool
	}{
		{10000, 8000, false, false}, // 20%, passes the legacy 12% floor
		{10000, 8800, false, false}, // exactly 12%, passes
		{10000, 9400, false, true},  // 6%, blocks
		{10000, 9400, true, false},  // 6% but approved override, passes
	}
	for _, c := range cases {
		legacy := CheckMargin(c.price, c.cost, trt, c.override)
		at := CheckMarginAt(c.price, c.cost, trt, c.override, MarginFloorBps)
		// Same decision via both entry points.
		if (legacy == nil) != (at == nil) {
			t.Fatalf("CheckMargin and CheckMarginAt(...,MarginFloorBps) disagree for price=%d cost=%d override=%v", c.price, c.cost, c.override)
		}
		if c.wantBlock {
			if legacy == nil {
				t.Fatalf("price=%d cost=%d should block at the legacy floor, but passed", c.price, c.cost)
			}
			if legacy.Policy != PolicyMargin {
				t.Fatalf("legacy block policy = %s, want %s (routes to the 0016 queue)", legacy.Policy, PolicyMargin)
			}
			if legacy.Data["floor_bps"] != "1200" {
				t.Fatalf("legacy floor_bps = %q, want \"1200\"", legacy.Data["floor_bps"])
			}
		} else if legacy != nil {
			t.Fatalf("price=%d cost=%d override=%v should pass the legacy gate, got %v", c.price, c.cost, c.override, legacy)
		}
	}
}

func TestCheckCredit(t *testing.T) {
	// within limit
	if b := CheckCredit(50000, 10000, 100000, true, trt, false); b != nil {
		t.Errorf("within limit should pass, got %v", b)
	}
	// over limit
	b := CheckCredit(50000, 60000, 100000, true, trt, false)
	if b == nil || b.Policy != PolicyCredit {
		t.Fatalf("over limit should block with CREDIT policy, got %v", b)
	}
	// no credit + credit terms => COD
	cod := CheckCredit(50000, 0, 0, true, trt, false)
	if cod == nil || cod.Policy != PolicyCOD {
		t.Fatalf("zero credit on credit terms should be COD, got %v", cod)
	}
	// cash terms with zero credit is fine
	if b := CheckCredit(50000, 0, 0, false, trt, false); b != nil {
		t.Errorf("cash terms should pass regardless of credit, got %v", b)
	}
	// approved override bypasses
	if b := CheckCredit(50000, 60000, 100000, true, trt, true); b != nil {
		t.Errorf("approved override should pass, got %v", b)
	}
}

func ctx(role string, tier Tier, scope Scope, branches ...string) StaffContext {
	m := map[string]bool{}
	for _, b := range branches {
		m[b] = true
	}
	return StaffContext{UserID: "u-" + role, RoleCode: role, Tier: tier, Scope: scope, Branches: m}
}

// TestCanApprove_RailAuthority pins the commander-ruled authority set (DIB-216 decision #1): the two
// approvals-rail policies, MARGIN_BELOW_MINIMUM and JE_OVER_THRESHOLD, are decidable ONLY by the three
// Tier-1 GLOBAL rail-approver roles (GM / Financial Controller / platform admin). This deliberately
// NARROWS the prior margin rule (Tier<=Manager): branch/sales managers no longer approve a margin override,
// and HR (a Tier-1 GLOBAL role, but not a ruled approver) is excluded, so the test proves both the
// inclusion of the three and the exclusion of the near-misses.
func TestCanApprove_RailAuthority(t *testing.T) {
	gm := ctx("GENERAL_MANAGER", TierLeadership, ScopeGlobal)
	fc := ctx("FINANCIAL_CONTROLLER", TierLeadership, ScopeGlobal)
	admin := ctx(RoleSuperAdmin, TierLeadership, ScopeGlobal)
	hr := ctx("HR", TierLeadership, ScopeGlobal)                    // Tier-1 GLOBAL but NOT a ruled approver
	salesMgr := ctx("SALES_MANAGER", TierManager, ScopeBranch, trt) // was allowed for margin, no longer
	branchMgr := ctx("BRANCH_MANAGER", TierManager, ScopeBranch, trt)
	clerk := ctx("INSIDE_SALES", TierFrontline, ScopeBranch, trt)

	for _, policy := range []PolicyType{PolicyMargin, PolicyJEThreshold} {
		// The three ruled roles approve, in ANY branch (all GLOBAL scope).
		for _, ok := range []StaffContext{gm, fc, admin} {
			if !CanApprove(ok, policy, trt) || !CanApprove(ok, policy, kgn) {
				t.Errorf("%s should approve %s in any branch", ok.RoleCode, policy)
			}
		}
		// Everyone else, including HR, branch/sales managers, and front-line, is denied.
		for _, no := range []StaffContext{hr, salesMgr, branchMgr, clerk} {
			if CanApprove(no, policy, trt) {
				t.Errorf("%s must NOT approve %s (rail authority = GM/FC/SUPER_ADMIN only)", no.RoleCode, policy)
			}
		}
	}

	// A ruled role that somehow could not reach the branch is still rejected up front (defence in depth):
	// a branch-scoped GM (contrived) without the grant fails CanAccessBranch.
	scopedGM := StaffContext{UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeBranch, Branches: map[string]bool{trt: true}}
	if CanApprove(scopedGM, PolicyMargin, kgn) {
		t.Error("a branch-scoped approver must not reach a branch they lack a grant for")
	}
}

// TestResolveApprovers_FanoutTargets pins the fan-out target resolution (0075), the pure filter the rail
// uses to pick whose per-person channel gets a card, including the zero-eligible-approver edge.
func TestResolveApprovers_FanoutTargets(t *testing.T) {
	candidates := []StaffContext{
		{UserID: "gm", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal},
		{UserID: "fc", RoleCode: "FINANCIAL_CONTROLLER", Tier: TierLeadership, Scope: ScopeGlobal},
		{UserID: "admin", RoleCode: RoleSuperAdmin, Tier: TierLeadership, Scope: ScopeGlobal},
		{UserID: "hr", RoleCode: "HR", Tier: TierLeadership, Scope: ScopeGlobal},                                                // excluded
		{UserID: "mgr", RoleCode: "SALES_MANAGER", Tier: TierManager, Scope: ScopeBranch, Branches: map[string]bool{trt: true}}, // excluded
		{UserID: "", RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal},                                     // empty id skipped
	}
	got := ResolveApprovers(candidates, PolicyJEThreshold, trt)
	if len(got) != 3 {
		t.Fatalf("want 3 eligible approvers (gm/fc/admin), got %v", got)
	}
	set := map[string]bool{}
	for _, u := range got {
		set[u] = true
	}
	for _, want := range []string{"gm", "fc", "admin"} {
		if !set[want] {
			t.Errorf("expected %s in the fan-out set, got %v", want, got)
		}
	}
	// Zero-eligible edge: a candidate pool with no ruled approver returns an empty (nil) set, not a panic.
	none := ResolveApprovers([]StaffContext{
		{UserID: "hr", RoleCode: "HR", Tier: TierLeadership, Scope: ScopeGlobal},
		{UserID: "clerk", RoleCode: "INSIDE_SALES", Tier: TierFrontline, Scope: ScopeBranch, Branches: map[string]bool{trt: true}},
	}, PolicyMargin, trt)
	if len(none) != 0 {
		t.Fatalf("empty-eligible set should be empty, got %v", none)
	}
}

func TestCanApprove_Credit(t *testing.T) {
	fc := ctx("FINANCIAL_CONTROLLER", TierLeadership, ScopeGlobal)
	if !CanApprove(fc, PolicyCredit, trt) {
		t.Error("Financial Controller (Tier-1) should approve a credit override")
	}
	branchMgr := ctx("BRANCH_MANAGER", TierManager, ScopeBranch, trt)
	if CanApprove(branchMgr, PolicyCredit, trt) {
		t.Error("a Tier-2 manager must NOT approve a credit override (finance decision)")
	}
	if CanApprove(branchMgr, PolicyCOD, trt) {
		t.Error("a Tier-2 manager must NOT approve a COD override")
	}
}

func TestApprovalDecide(t *testing.T) {
	req := &ApprovalRequest{ID: "ar1", RequestedBy: "clerk", BranchID: trt, Policy: PolicyMargin, Status: ApprovalPending}
	gm := ctx("GENERAL_MANAGER", TierLeadership, ScopeGlobal)

	// front-line cannot decide
	clerk := ctx("INSIDE_SALES", TierFrontline, ScopeBranch, trt)
	if err := req.Decide(clerk, true); err == nil {
		t.Error("front-line decide should be rejected")
	}
	if req.Status != ApprovalPending {
		t.Error("status must stay PENDING after an unauthorized decide")
	}
	// GM approves
	if err := req.Decide(gm, true); err != nil {
		t.Fatalf("GM approve should succeed: %v", err)
	}
	if req.Status != ApprovalApproved || req.DecidedBy != gm.UserID {
		t.Fatalf("expected APPROVED by GM, got %s / %s", req.Status, req.DecidedBy)
	}
	// cannot decide twice
	if err := req.Decide(gm, false); err == nil {
		t.Error("deciding an already-decided request should error")
	}
}

func TestBranchScoping(t *testing.T) {
	gm := ctx("GENERAL_MANAGER", TierLeadership, ScopeGlobal)
	if !gm.IsAdmin() || !gm.CanAccessBranch(kgn) {
		t.Error("GM should be admin and reach any branch")
	}
	pr := ctx("PAYABLES_RECEIVABLES", TierManager, ScopeGlobal)
	if !pr.CanAccessBranch(trt) || !pr.CanAccessBranch(kgn) {
		t.Error("central Payables/Receivables (GLOBAL) should reach both branches")
	}
	yard := ctx("YARD_TEAM", TierFrontline, ScopeBranch, trt)
	if !yard.CanAccessBranch(trt) || yard.CanAccessBranch(kgn) {
		t.Error("branch-scoped staff should reach only their granted branch")
	}
}

func TestStaffContext_IsSuperAdmin(t *testing.T) {
	super := StaffContext{RoleCode: RoleSuperAdmin, Tier: TierLeadership, Scope: ScopeGlobal}
	if !super.IsSuperAdmin() || !super.IsAdmin() || !super.IsManager() {
		t.Error("SUPER_ADMIN must be a super admin AND satisfy the admin + manager gates")
	}
	gm := StaffContext{RoleCode: "GENERAL_MANAGER", Tier: TierLeadership, Scope: ScopeGlobal}
	if gm.IsSuperAdmin() {
		t.Error("a General Manager is not a platform admin")
	}
}
