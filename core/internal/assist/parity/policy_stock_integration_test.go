//go:build integration

package parity

// Each new policy pinned on its own (PR 555 read P3 1, as slice 5 did for orders): as the view owner under
// signed claims, read public.stock_balances and public.product_branch_costs directly, not through ai.stock_balances.
// A pro claim, a claim with no grant, a claim for a branch that holds nothing and a tier 3 claim read nothing;
// a tier 1 and a tier 2 claim read exactly their branches' rows.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func rowsAt(t *testing.T, env *testsupport.Env, table, branch string) int {
	t.Helper()
	var n int
	if err := env.Admin.QueryRow(testsupport.Ctx(t), `SELECT count(*) FROM `+table+` WHERE branch_id = $1::uuid`, branch).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPolicy_TheInventoryTablesFollowAudienceTierAndBranches(t *testing.T) {
	env := testsupport.Open(t)
	plantStrayStock(t, env)
	branches := stockBranches(t, env)
	a, b := branches[0], branches[1]
	for _, table := range []string{"public.stock_balances", "public.product_branch_costs"} {
		total := 0
		if err := env.Admin.QueryRow(testsupport.Ctx(t), `SELECT count(*) FROM `+table).Scan(&total); err != nil {
			t.Fatal(err)
		}
		atA, atB := rowsAt(t, env, table, a), rowsAt(t, env, table, b)
		// The two branches are the test's own pair; the table may hold more (another package's stray row), so the
		// pair is never asserted to be the whole table, and the global claim is compared with the whole table.
		if atA == 0 || atB == 0 || atA+atB > total {
			t.Fatalf("%s: the policy world is wrong: a=%d b=%d total=%d", table, atA, atB, total)
		}
		staff := func(tier int, global bool, br ...string) readtx.Claims {
			c := staffClaim(br, global)
			c.Tier = tier
			return c
		}
		// Positive controls: each branch's claim reads exactly its rows, a global claim reads all.
		if got := countAsViewOwner(t, env, staff(2, true), table); got != total {
			t.Errorf("%s: a global tier 2 claim reads %d of %d", table, got, total)
		}
		if got := countAsViewOwner(t, env, staff(1, false, a), table); got != atA {
			t.Errorf("%s: branch a's tier 1 claim reads %d, want %d", table, got, atA)
		}
		if got := countAsViewOwner(t, env, staff(2, false, b), table); got != atB {
			t.Errorf("%s: branch b's claim reads %d, want %d", table, got, atB)
		}
		// Refusals.
		pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-pro", CustomerID: "33333333-3333-3333-3333-333333333333", Tier: 2, Global: true, BranchIDs: branches}
		if got := countAsViewOwner(t, env, pro, table); got != 0 {
			t.Errorf("%s: a pro audience claim reads %d rows, want 0", table, got)
		}
		if got := countAsViewOwner(t, env, staff(2, false), table); got != 0 {
			t.Errorf("%s: a staff claim with no grant reads %d rows, want 0", table, got)
		}
		if got := countAsViewOwner(t, env, staff(2, false, "99999999-9999-9999-9999-999999999999"), table); got != 0 {
			t.Errorf("%s: a claim for another branch reads %d rows, want 0", table, got)
		}
		if got := countAsViewOwner(t, env, staff(3, true, branches...), table); got != 0 {
			t.Errorf("%s: a tier 3 claim reads %d rows, want 0", table, got)
		}
		if got := countAsViewOwner(t, env, staff(0, true, branches...), table); got != 0 {
			t.Errorf("%s: a claim with no tier reads %d rows, want 0", table, got)
		}
		if got := countAsViewOwnerUnclaimed(t, env, table); got != 0 {
			t.Errorf("%s: no claim reads %d rows, want 0", table, got)
		}
	}
}
