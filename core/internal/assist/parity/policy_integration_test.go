//go:build integration

package parity

// The two row policies tested directly (FROM-PRINCIPAL item 31), not through the views: as the view
// owner role, under a signed claim, read public.orders and public.order_lines themselves. The views join
// orders to order_lines, so a policy that lost its own predicate could hide behind the other table's; here
// each policy stands alone.
//
// The view owner holds no EXECUTE on ai.claims() (the catalogue test forbids it: only the readers do), so
// each test grants it for the length of one transaction on the admin connection and rolls the grant back.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
)

// countAsViewOwner counts the rows of table the view owner reads under the claim.
func countAsViewOwner(t *testing.T, env *testsupport.Env, c readtx.Claims, table string) int {
	t.Helper()
	return countAsViewOwnerWith(t, env, c, table)
}

// countAsViewOwnerWith is countAsViewOwner after running pre in the same transaction (rolled back with it),
// so a test can switch row security off on a table the policy under test reads through, and make that policy
// stand on its own.
func countAsViewOwnerWith(t *testing.T, env *testsupport.Env, c readtx.Claims, table string, pre ...string) int {
	t.Helper()
	ctx := testsupport.Ctx(t)
	signer, err := readtx.NewSigner(redact.NewSecret(env.Key))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := env.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	vo := pgx.Identifier{env.Roles.ViewOwner}.Sanitize()
	if _, err := tx.Exec(ctx, `GRANT EXECUTE ON FUNCTION ai.claims() TO `+vo); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := tx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	tok, err := signer.Sign(c, pid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT set_config('ai.claims', $1, true)`, tok.Reveal()); err != nil {
		t.Fatal(err)
	}
	for _, q := range pre {
		if _, err := tx.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+vo); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("view owner reads %s: %v", table, err)
	}
	return n
}

func linesAt(t *testing.T, env *testsupport.Env, branch string) int {
	t.Helper()
	var n int
	if err := env.Admin.QueryRow(testsupport.Ctx(t), `SELECT count(*) FROM order_lines ol JOIN orders o ON o.id = ol.order_id WHERE o.branch_id = $1::uuid`, branch).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func staffClaim(branches []string, global bool) readtx.Claims {
	return readtx.Claims{Audience: readtx.AudienceStaff, AskerID: "policy-test", Tier: 2, BranchIDs: branches, Global: global}
}

// The order_lines policy carries its own branch predicate: a staff claim with no grant, a claim for a
// branch that holds nothing, and a claim for the other branch each read exactly their branch's lines.
func TestPolicy_OrderLinesRowsFollowTheClaimsBranches(t *testing.T) {
	env := testsupport.Open(t)
	plantSecondBranch(t, env)
	branches := branchesWithOrders(t, env)
	a, b := branches[0], branches[1]
	var total int
	if err := env.Admin.QueryRow(testsupport.Ctx(t), `SELECT count(*) FROM order_lines`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	linesA, linesB := linesAt(t, env, a), linesAt(t, env, b)
	if linesA == 0 || linesB == 0 || total != linesA+linesB {
		t.Fatalf("the policy world is wrong: lines at a=%d b=%d total=%d", linesA, linesB, total)
	}
	const lines = "public.order_lines"
	if got := countAsViewOwner(t, env, staffClaim(nil, true), lines); got != total {
		t.Errorf("a global staff claim reads %d lines, want all %d (positive control)", got, total)
	}
	if got := countAsViewOwner(t, env, staffClaim([]string{a}, false), lines); got != linesA {
		t.Errorf("branch a's claim reads %d lines, want %d", got, linesA)
	}
	if got := countAsViewOwner(t, env, staffClaim([]string{b}, false), lines); got != linesB {
		t.Errorf("branch b's claim reads %d lines, want %d", got, linesB)
	}
	if got := countAsViewOwner(t, env, staffClaim(nil, false), lines); got != 0 {
		t.Errorf("a staff claim with no grant reads %d lines from public.order_lines, want 0", got)
	}
	if got := countAsViewOwner(t, env, staffClaim([]string{"99999999-9999-9999-9999-999999999999"}, false), lines); got != 0 {
		t.Errorf("a claim for another branch reads %d lines from public.order_lines, want 0", got)
	}
}

// The orders policy checks the claim's audience: a contractor (pro) claim reads nothing from orders or
// order_lines, even one that says global and names branches; the staff claim beside it still reads.
func TestPolicy_AProAudienceClaimReadsNothing(t *testing.T) {
	env := testsupport.Open(t)
	plantSecondBranch(t, env)
	branches := branchesWithOrders(t, env)
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: branches}
	if got := countAsViewOwner(t, env, staffClaim(branches, true), "public.orders"); got == 0 {
		t.Fatal("the staff control claim reads no orders")
	}
	for _, table := range []string{"public.orders", "public.order_lines"} {
		if got := countAsViewOwner(t, env, pro, table); got != 0 {
			t.Errorf("a pro audience claim reads %d rows from %s, want 0", got, table)
		}
	}
	// And no claim at all (an unset or forged one verifies to NULL) reads nothing.
	for _, table := range []string{"public.orders", "public.order_lines"} {
		if got := countAsViewOwnerUnclaimed(t, env, table); got != 0 {
			t.Errorf("no claim reads %d rows from %s, want 0", got, table)
		}
	}
}

func countAsViewOwnerUnclaimed(t *testing.T, env *testsupport.Env, table string) int {
	t.Helper()
	ctx := testsupport.Ctx(t)
	tx, err := env.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	vo := pgx.Identifier{env.Roles.ViewOwner}.Sanitize()
	for _, s := range []string{`GRANT EXECUTE ON FUNCTION ai.claims() TO ` + vo, `SELECT set_config('ai.claims', 'v1.1.forged.forged', true)`, `SET LOCAL ROLE ` + vo} {
		if _, err := tx.Exec(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Slice 6a (PR 555 read P3 2): the tier floor is in the claim and in the policies. A tier 3 claim, even one
// that is global and names every branch, reads nothing from public.orders or public.order_lines, and neither
// does a claim signed with no tier at all (an old payload); a tier 1 and a tier 2 claim read.
func TestPolicy_ATierBelowTheFloorReadsNothing(t *testing.T) {
	env := testsupport.Open(t)
	plantSecondBranch(t, env)
	branches := branchesWithOrders(t, env)
	at := func(tier int) readtx.Claims {
		c := staffClaim(branches, true)
		c.Tier = tier
		return c
	}
	for _, table := range []string{"public.orders", "public.order_lines"} {
		for _, tier := range []int{1, 2} {
			if got := countAsViewOwner(t, env, at(tier), table); got == 0 {
				t.Errorf("a tier %d claim reads no rows from %s (positive control)", tier, table)
			}
		}
		if got := countAsViewOwner(t, env, at(3), table); got != 0 {
			t.Errorf("a tier 3 signed claim reads %d rows from %s, want 0", got, table)
		}
		if got := countAsViewOwner(t, env, at(0), table); got != 0 {
			t.Errorf("a claim with no tier reads %d rows from %s, want 0", got, table)
		}
	}
}
