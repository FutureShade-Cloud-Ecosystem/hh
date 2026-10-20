//go:build integration

package parity

// Per record and per column parity for the inventory view (plan r3 section 1, slice 6a). For callers built by
// the handler's own path (CallerFor, over the dealer's real role list), and for every seeded stock row:
//   - the (product, branch) row is in ai.stock_balances iff the inventory module's own caller scoped record
//     read (Service.LookupStock over the caller's BranchReach) returns it;
//   - on hand, reserved and available equal what that read carries;
//   - the average cost is present iff that read carries it for the caller: the cost leg of a stock read is held
//     out for a role off the dealer's cost and margin list (inventory/cost_gate.go), exactly the predicate
//     roleconfig's SeesCostAndMargin answers, and a cost of zero on file reads absent for everyone.

import (
	"context"
	"fmt"
	"math"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
)

type allBranches []assist.NamedBranch

func (a allBranches) Branches(context.Context, assist.Caller) ([]assist.NamedBranch, error) {
	return a, nil
}

// stockBranches returns the two BRANCH locations that hold the most stock rows, the fuller first (ties by id).
// It never assumes they are the only branches or that their rows are the whole table: another package may leave
// a stray stock row at a third location in a shared database, and that location can sort anywhere.
func stockBranches(t *testing.T, env *testsupport.Env) []string {
	t.Helper()
	rows, err := env.Admin.Query(testsupport.Ctx(t), `SELECT l.id::text FROM locations l
		JOIN stock_balances sb ON sb.branch_id = l.id
		WHERE l.loc_type = 'BRANCH'
		GROUP BY l.id ORDER BY count(*) DESC, l.id LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

type stockCell struct {
	onHand, reserved, available float64
	cost                        *int64
}

func parseF(t *testing.T, s string) float64 {
	t.Helper()
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatalf("number %q: %v", s, err)
	}
	return f
}

// viewStock reads ai.stock_balances as the staff reader under the caller's claim.
func viewStock(t *testing.T, r *readtx.Reader, c assist.Caller) map[string]stockCell {
	t.Helper()
	claims, err := c.ReadClaims()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]stockCell{}
	err = r.Run(testsupport.Ctx(t), claims, `SELECT product_id::text, branch_id::text, on_hand_base::text, reserved_base::text, available_base::text, avg_cost_cents::text FROM ai.stock_balances`, nil,
		func(rows pgx.Rows) error {
			for rows.Next() {
				var p, b, oh, rs, av string
				var cost *string
				if err := rows.Scan(&p, &b, &oh, &rs, &av, &cost); err != nil {
					return err
				}
				cell := stockCell{onHand: parseF(t, oh), reserved: parseF(t, rs), available: parseF(t, av)}
				if cost != nil {
					v := int64(parseF(t, *cost))
					cell.cost = &v
				}
				out[p+"|"+b] = cell
			}
			return nil
		})
	if err != nil {
		t.Fatalf("read ai.stock_balances: %v", err)
	}
	return out
}

type stockRig struct {
	env      *testsupport.Env
	oracle   *inventory.Service
	rc       *roleconfig.Config
	h        *assist.Handler
	branches []string
	products map[string]bool
}

func newStockRig(t *testing.T) *stockRig {
	t.Helper()
	env := testsupport.Open(t)
	plantStrayStock(t, env)
	ctx := testsupport.Ctx(t)
	r := &stockRig{env: env, branches: stockBranches(t, env), products: map[string]bool{}}
	if len(r.branches) < 2 {
		t.Fatalf("the stock world holds %d branches, want two", len(r.branches))
	}
	pool, err := pgxpool.New(ctx, env.AdminURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	r.oracle = inventory.NewService(inventory.NewRepository(pool))
	if r.rc, err = roleconfig.Default(); err != nil {
		t.Fatal(err)
	}
	var named allBranches
	for _, b := range r.branches {
		named = append(named, assist.NamedBranch{BranchID: b})
	}
	r.h = assist.NewHandler(nil, nil).WithCostGate(r.rc, named)
	prows, err := env.Admin.Query(ctx, `SELECT DISTINCT product_id::text FROM stock_balances`)
	if err != nil {
		t.Fatal(err)
	}
	for prows.Next() {
		var p string
		if err := prows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		r.products[p] = true
	}
	prows.Close()
	return r
}

func (r *stockRig) callers() map[string]identity.StaffContext {
	a, b := r.branches[0], r.branches[1]
	return map[string]identity.StaffContext{
		"general manager, global":             {UserID: "u-gm", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership, Scope: identity.ScopeGlobal, Status: "ACTIVE"},
		"sales manager at a":                  scoped("SALES_MANAGER", a),
		"sales manager at a and b":            scoped("SALES_MANAGER", a, b),
		"hr manager at a (off the cost list)": scoped("HR", a),
		"yard manager at b":                   scoped("YARD_MANAGER", b),
		"sales manager with no grants":        scoped("SALES_MANAGER"),
	}
}

// compare checks one caller's whole view against the inventory module's own read and returns the rows compared.
func (r *stockRig) compare(t *testing.T, name string, sc identity.StaffContext) int {
	t.Helper()
	ctx := testsupport.Ctx(t)
	req := httptest.NewRequest("POST", "/api/v1/assist/chat", nil).WithContext(identity.ContextWithStaff(ctx, sc))
	c, err := r.h.CallerFor(req, guards.StaffCaller{})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := identity.CanonicalForERPCode(sc.RoleCode)
	listed, _ := r.rc.SeesCostAndMargin(canonical)
	view := viewStock(t, r.env.Readers.Staff, c)
	expected := map[string]stockCell{}
	for p := range r.products {
		_, rows, found, err := r.oracle.LookupStock(ctx, inventory.BranchReach{Global: c.Global, BranchIDs: c.BranchIDs}, p)
		if err != nil || !found {
			t.Fatalf("%s: record read of %s: found=%v err=%v", name, p, found, err)
		}
		for _, row := range rows {
			cell := stockCell{onHand: row.OnHandBase, reserved: row.ReservedBase, available: row.AvailableBase}
			if listed && row.AvgCostCents != 0 {
				v := row.AvgCostCents
				cell.cost = &v
			}
			expected[p+"|"+row.BranchID] = cell
		}
	}
	compared := 0
	for key, want := range expected {
		got, ok := view[key]
		if !ok {
			t.Errorf("%s: %s is returned by the inventory record read but is not in the view", name, key)
			continue
		}
		compared++
		if !same(got.onHand, want.onHand) || !same(got.reserved, want.reserved) || !same(got.available, want.available) {
			t.Errorf("%s: %s quantities differ: view %+v, record read %+v", name, key, got, want)
		}
		if (got.cost != nil) != (want.cost != nil) || (got.cost != nil && *got.cost != *want.cost) {
			t.Errorf("%s: %s cost differs: view %s, record read %s", name, key, show(got.cost), show(want.cost))
		}
	}
	for key := range view {
		if _, ok := expected[key]; !ok {
			t.Errorf("%s: %s is in the view but the inventory record read does not return it for this caller", name, key)
		}
	}
	return compared
}

func TestParity_EveryStockRowAndItsCostFollowTheInventoryModulesOwnRead(t *testing.T) {
	r := newStockRig(t)
	compared := 0
	for name, sc := range r.callers() {
		compared += r.compare(t, name, sc)
	}
	if compared < 50 {
		t.Fatalf("only %d stock rows were compared", compared)
	}
}

// The purchasing policy (MANAGER_UP, ALL_STAFF, HIDDEN, per branch) decides who sees what the dealer PAYS on a
// purchase order. The inventory screens do not ask it: they hold the average cost out by the dealer's cost and
// margin role list alone. The view mirrors the screens, so setting the policy at a branch changes nothing here.
// This test pins that, under each policy value, so the day the screens change the test says so; the report asks
// whether the dealer intends the policy to cover stock cost.
func TestParity_StockCostDoesNotMoveWithThePurchasingBranchPolicyBecauseTheScreensIgnoreIt(t *testing.T) {
	r := newStockRig(t)
	ctx := testsupport.Ctx(t)
	a := r.branches[0]
	t.Cleanup(func() {
		_, _ = r.env.Admin.Exec(ctx, `DELETE FROM purchasing_policy WHERE branch_id = $1::uuid`, a)
	})
	for _, policy := range []string{"MANAGER_UP", "ALL_STAFF", "HIDDEN"} {
		if _, err := r.env.Admin.Exec(ctx, `INSERT INTO purchasing_policy (branch_id, cost_visibility) VALUES ($1::uuid, $2)
			ON CONFLICT (branch_id) DO UPDATE SET cost_visibility = EXCLUDED.cost_visibility`, a, policy); err != nil {
			t.Fatalf("set the policy: %v", err)
		}
		for name, sc := range r.callers() {
			r.compare(t, name+" under "+policy, sc)
		}
	}
}

func scoped(role string, branches ...string) identity.StaffContext {
	sc := identity.StaffContext{UserID: "u-" + role, RoleCode: role, Tier: identity.TierManager, Scope: identity.ScopeBranch, Status: "ACTIVE", Branches: map[string]bool{}}
	for _, b := range branches {
		sc.Branches[b] = true
	}
	return sc
}

func same(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func show(c *int64) string {
	if c == nil {
		return "absent"
	}
	return fmt.Sprint(*c)
}
