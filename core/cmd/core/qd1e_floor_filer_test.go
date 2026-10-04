package main

// qd1e_floor_filer_test.go is the floor approver ruling's served-surface
// legs (decision log row 743): the branch Sales manager decides the below
// floor requests filed by Inside Sales and Outside Sales in its own branch,
// and nothing else. A request filed by a Branch manager, a Sales manager (a
// peer's included) or the Yard manager is the General Manager's or the
// Financial Controller's, whether manager_own_branch_floor_clearing is on or
// off. One test per surface, one leg per filer role: the decide route, the
// approvals list, the sync pull, the sync door's decide, and the dashboard
// count, every guard real, every request filed through the rail's floor door
// so the filer's role is the one the store reads from the requester's grant.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// qd1ePlantRoleGrant gives one planted linked user a canonical role grant at
// the migration planted branch and removes it when the test ends.
func qd1ePlantRoleGrant(t *testing.T, pool *pgxpool.Pool, sub, role string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id)
		SELECT $1, $2, l.org_id, l.id FROM locations l ORDER BY l.id LIMIT 1
		ON CONFLICT DO NOTHING`, sub, role); err != nil {
		t.Fatalf("plant the %s grant: %v", role, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM staff_role_grant WHERE platform_user_id = $1 AND role_id = $2`, sub, role)
	})
}

// qd1ePlantFiler plants one linked staff user holding exactly one role and
// answers its ERP users.id, the requester stamp the rail's doors take.
func qd1ePlantFiler(t *testing.T, pool *pgxpool.Pool, role string) string {
	t.Helper()
	sub := "fxstaff-qd1e-" + role
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, display_name, platform_user_id, status)
		VALUES ($1, $2, $3, 'ACTIVE')
		ON CONFLICT (platform_user_id) WHERE platform_user_id IS NOT NULL DO UPDATE SET status = 'ACTIVE'
		RETURNING id::text`,
		"fxstaff.qd1e."+role+"@floorfiler.invalid", "QD1E Filer "+role, sub).Scan(&id); err != nil {
		t.Fatalf("plant the %s filer: %v", role, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1::uuid`, id)
	})
	qd1ePlantRoleGrant(t, pool, sub, role)
	return id
}

// qd1eConfigWithClearing is the dealer's decided default with
// manager_own_branch_floor_clearing moved on or off.
func qd1eConfigWithClearing(t *testing.T, on bool) *roleconfig.Config {
	t.Helper()
	raw, err := os.ReadFile("../../internal/roleconfig/default.json")
	if err != nil {
		t.Fatalf("read the default configuration: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("decode the default configuration: %v", err)
	}
	doc["settings"].(map[string]any)[roleconfig.SettingManagerOwnBranchFloorClearing] = on
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("encode the configuration: %v", err)
	}
	cfg, err := roleconfig.Load(out)
	if err != nil {
		t.Fatalf("load the configuration with clearing %v: %v", on, err)
	}
	return cfg
}

// qd1eFilers are the requesters one leg plants: the two salesperson roles
// the Sales manager answers, and the three filers it does not.
type qd1eFilers struct {
	inside, outside, branchMgr, peerSales, yardMgr string
}

func qd1ePlantFilers(t *testing.T, q *qd1bFixture) qd1eFilers {
	t.Helper()
	return qd1eFilers{
		inside:    qd1bUserID(t, q.pool, testkit.SubInsideSales),
		outside:   qd1ePlantFiler(t, q.pool, "outside_sales"),
		branchMgr: qd1bUserID(t, q.pool, xrSubBranchManager),
		peerSales: qd1ePlantFiler(t, q.pool, "sales_manager"),
		yardMgr:   qd1ePlantFiler(t, q.pool, "yard_manager"),
	}
}

func qd1eStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM permission_approval_requests WHERE id = $1`, id).Scan(&s); err != nil {
		t.Fatalf("read the request %s: %v", id, err)
	}
	return s
}

// qd1eClearingLegs runs one leg with the setting on and then off: who
// decides is the same either way.
func qd1eClearingLegs(t *testing.T, leg func(t *testing.T, q *qd1bFixture, tag string)) {
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprintf("manager_own_branch_floor_clearing_%v", on), func(t *testing.T) {
			q := newQD1BFixtureWith(t, qd1eConfigWithClearing(t, on))
			leg(t, q, map[bool]string{true: "e1", false: "e2"}[on])
		})
	}
}

func TestQD1E_DecideRouteFollowsTheFilerRole(t *testing.T) {
	qd1eClearingLegs(t, func(t *testing.T, q *qd1bFixture, tag string) {
		f := qd1ePlantFilers(t, q)
		a := testkit.BranchNorthAID
		decide := func(id, sub string) int {
			code, _ := qd1bCall(t, q, http.MethodPost, qd1bDecidePath(id), sub, map[string]any{"approve": true})
			return code
		}

		// The Sales manager decides what Inside Sales and Outside Sales filed.
		for i, requester := range []string{f.inside, f.outside} {
			id := qd1bPlantFloor(t, q, requester, a, fmt.Sprintf("0000000%s%02x", tag, i))
			if code := decide(id, testkit.SubSalesManager); code != http.StatusOK {
				t.Errorf("the Sales manager decides a salesperson filing %d: %d, want 200", i, code)
			}
		}
		// It is refused every other filer's request, and nothing persists.
		others := map[string]string{"branch manager": f.branchMgr, "peer sales manager": f.peerSales, "yard manager": f.yardMgr}
		var refused []string
		i := 10
		for name, requester := range others {
			id := qd1bPlantFloor(t, q, requester, a, fmt.Sprintf("0000000%s%02x", tag, i))
			i++
			refused = append(refused, id)
			if code := decide(id, testkit.SubSalesManager); code != http.StatusForbidden {
				t.Errorf("the Sales manager decides a %s filing: %d, want 403", name, code)
			}
			if st := qd1eStatus(t, q.pool, id); st != "PENDING" {
				t.Errorf("the refused %s filing is %s, want PENDING", name, st)
			}
		}
		// The General Manager and the Financial Controller decide all of them.
		if code := decide(refused[0], testkit.SubGeneralMgr); code != http.StatusOK {
			t.Errorf("the General Manager decides a leadership-bound filing: %d, want 200", code)
		}
		if code := decide(refused[1], xrSubFinancialController); code != http.StatusOK {
			t.Errorf("the Financial Controller decides a leadership-bound filing: %d, want 200", code)
		}
		if code := decide(refused[2], testkit.SubGeneralMgr); code != http.StatusOK {
			t.Errorf("the General Manager decides a leadership-bound filing: %d, want 200", code)
		}
		// Another branch's salesperson filing is not this Sales manager's.
		far := qd1bPlantFloor(t, q, f.inside, testkit.BranchNorthBID, fmt.Sprintf("0000000%s20", tag))
		if code := decide(far, testkit.SubSalesManager); code != http.StatusForbidden {
			t.Errorf("the Sales manager decides another branch's salesperson filing: %d, want 403", code)
		}
		// Four eyes holds: no role decides its own request.
		own := qd1bPlantFloor(t, q, qd1bUserID(t, q.pool, testkit.SubGeneralMgr), a, fmt.Sprintf("0000000%s21", tag))
		if code := decide(own, testkit.SubGeneralMgr); code != http.StatusForbidden {
			t.Errorf("the General Manager decides its own filing: %d, want 403", code)
		}
	})
}

func TestQD1E_ReadsFollowTheFilerRole(t *testing.T) {
	qd1eClearingLegs(t, func(t *testing.T, q *qd1bFixture, tag string) {
		f := qd1ePlantFilers(t, q)
		a := testkit.BranchNorthAID
		ids := map[string]string{}
		for i, p := range []struct{ name, requester string }{
			{"inside", f.inside}, {"outside", f.outside},
			{"branch manager", f.branchMgr}, {"peer sales manager", f.peerSales}, {"yard manager", f.yardMgr},
		} {
			ids[p.name] = qd1bPlantFloor(t, q, p.requester, a, fmt.Sprintf("0000000%s3%d", tag, i))
		}
		for _, path := range []string{"/api/v1/staff/approvals", "/api/v1/sync/approvals?since=0&limit=500"} {
			for name, id := range ids {
				wantSales := name == "inside" || name == "outside"
				if listed, _ := qd1cSees(t, q, path, testkit.SubSalesManager, id); listed != wantSales {
					t.Errorf("%s: the %s filing is listed to the Sales manager = %v, want %v", path, name, listed, wantSales)
				}
				for _, sub := range []string{testkit.SubGeneralMgr, xrSubFinancialController} {
					if listed, _ := qd1cSees(t, q, path, sub, id); !listed {
						t.Errorf("%s: the %s filing is not listed to %s", path, name, sub)
					}
				}
			}
		}
	})
}

func TestQD1E_SyncDoorDecidesFollowTheFilerRole(t *testing.T) {
	qd1eClearingLegs(t, func(t *testing.T, q *qd1bFixture, tag string) {
		f := qd1ePlantFilers(t, q)
		a := testkit.BranchNorthAID
		n := 0
		push := func(id, sub string) qd1bPushResult {
			n++
			key := fmt.Sprintf("3ef10000-0e1e-4000-8000-00000000%s%02x", tag, n)
			qd1bSweepKey(t, q, key)
			qd1bCleanSync(t, q, id)
			return qd1bPush(t, q, sub, qd1bPushOp{ClientID: key, Op: "update",
				Payload: map[string]any{"id": id, "status": "APPROVED"}})
		}
		for i, requester := range []string{f.inside, f.outside} {
			id := qd1bPlantFloor(t, q, requester, a, fmt.Sprintf("0000000%s4%d", tag, i))
			qd1bApplied(t, "the Sales manager decides a salesperson filing from the device", push(id, testkit.SubSalesManager))
		}
		for i, requester := range []string{f.branchMgr, f.peerSales, f.yardMgr} {
			id := qd1bPlantFloor(t, q, requester, a, fmt.Sprintf("0000000%s5%d", tag, i))
			res := push(id, testkit.SubSalesManager)
			qd1bRejected(t, "the Sales manager decides a leadership-bound filing from the device", res, "not authorized")
			if st := qd1eStatus(t, q.pool, id); st != "PENDING" {
				t.Errorf("the refused device decision left the request %s, want PENDING", st)
			}
			qd1bApplied(t, "the General Manager decides it from the device", push(id, testkit.SubGeneralMgr))
		}
		far := qd1bPlantFloor(t, q, f.inside, testkit.BranchNorthBID, fmt.Sprintf("0000000%s60", tag))
		qd1bRejected(t, "the Sales manager decides another branch's salesperson filing from the device",
			push(far, testkit.SubSalesManager), "")
	})
}

func TestQD1E_CountFollowsTheFilerRole(t *testing.T) {
	pool := wiringPool(t)
	_, branchA := plantDashboardCaller(t, pool)
	rail := staffcontext.NewApprovals(staffcontext.NewApprovalRepository(pool))
	for i, role := range []string{"inside_sales", "outside_sales", "branch_manager", "sales_manager", "yard_manager"} {
		requester := qd1ePlantFiler(t, pool, role)
		qd1bPlantApproval(t, pool, rail, staffcontext.PolicyMargin, requester, branchA, fmt.Sprintf("000000000e7%d", i))
	}
	for _, c := range []struct {
		name string
		auth identity.StaffAuthority
		want int
	}{
		{"the branch Sales manager", identity.StaffAuthority{RoleID: "sales_manager", Tier: identity.TierManager,
			Scope: identity.ScopeBranch, Branches: []string{branchA}}, 2},
		{"the General Manager", identity.StaffAuthority{RoleID: "general_manager", Tier: identity.TierLeadership,
			Scope: identity.ScopeGlobal}, 5},
		{"the Financial Controller", identity.StaffAuthority{RoleID: "financial_controller", Tier: identity.TierLeadership,
			Scope: identity.ScopeGlobal}, 5},
	} {
		sg := testkit.NewStaffGuards(t)
		sg.SetAuthority(c.auth)
		mux := dashFold(t, pool, sg)
		if n := needsApprovalsCount(t, mux, sg); n != c.want {
			t.Errorf("%s counts %d floor requests, want %d", c.name, n, c.want)
		}
	}
}

// A filer whose role cannot be read as one named role fails closed to
// leadership: a user holding two roles at once, and a user whose only
// grant is tombstoned, are not salespeople to the Sales manager.
func TestQD1E_UnreadableFilerRoleFailsClosedToLeadership(t *testing.T) {
	q := newQD1BFixture(t)
	a := testkit.BranchNorthAID
	decide := func(id, sub string) int {
		code, _ := qd1bCall(t, q, http.MethodPost, qd1bDecidePath(id), sub, map[string]any{"approve": true})
		return code
	}

	both := qd1ePlantFiler(t, q.pool, "inside_sales")
	qd1ePlantRoleGrant(t, q.pool, "fxstaff-qd1e-inside_sales", "yard_team")
	twoRoles := qd1bPlantFloor(t, q, both, a, "000000000e91")
	if code := decide(twoRoles, testkit.SubSalesManager); code != http.StatusForbidden {
		t.Errorf("the Sales manager decides a filing by a user holding two roles: %d, want 403", code)
	}
	if code := decide(twoRoles, testkit.SubGeneralMgr); code != http.StatusOK {
		t.Errorf("the General Manager decides it: %d, want 200", code)
	}

	gone := qd1ePlantFiler(t, q.pool, "outside_sales")
	if _, err := q.pool.Exec(context.Background(),
		`UPDATE staff_role_grant SET deleted_at = now() WHERE platform_user_id = 'fxstaff-qd1e-outside_sales'`); err != nil {
		t.Fatalf("tombstone the grant: %v", err)
	}
	revoked := qd1bPlantFloor(t, q, gone, a, "000000000e92")
	if code := decide(revoked, testkit.SubSalesManager); code != http.StatusForbidden {
		t.Errorf("the Sales manager decides a filing by a user with no live role grant: %d, want 403", code)
	}
}
