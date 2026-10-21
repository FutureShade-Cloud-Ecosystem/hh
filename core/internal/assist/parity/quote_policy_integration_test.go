//go:build integration

package parity

// The quote row policies tested directly, as the order ones are (policy_integration_test.go): the view owner
// reads public.quotes and public.quote_lines themselves under signed claims, so a policy that lost its own
// predicate cannot hide behind the other table's.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func countWhere(t *testing.T, env *testsupport.Env, q string, args ...any) int {
	t.Helper()
	var n int
	if err := env.Admin.QueryRow(testsupport.Ctx(t), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPolicy_QuoteRowsFollowTheClaimsBranches(t *testing.T) {
	env := testsupport.Open(t)
	w := plantQuoteWorld(t, env)
	total := countWhere(t, env, `SELECT count(*) FROM quotes`)
	totalLines := countWhere(t, env, `SELECT count(*) FROM quote_lines`)
	qa := countWhere(t, env, `SELECT count(*) FROM quotes WHERE branch_id = $1::uuid`, w.a)
	qb := countWhere(t, env, `SELECT count(*) FROM quotes WHERE branch_id = $1::uuid`, w.b)
	la := countWhere(t, env, `SELECT count(*) FROM quote_lines ql JOIN quotes q ON q.id = ql.quote_id WHERE q.branch_id = $1::uuid`, w.a)
	lb := countWhere(t, env, `SELECT count(*) FROM quote_lines ql JOIN quotes q ON q.id = ql.quote_id WHERE q.branch_id = $1::uuid`, w.b)
	if qa == 0 || qb == 0 || la == 0 || lb == 0 || total <= qa+qb || totalLines <= la+lb {
		t.Fatalf("the policy world is wrong: quotes %d/%d of %d, lines %d/%d of %d", qa, qb, total, la, lb, totalLines)
	}
	const quotes, lines = "public.quotes", "public.quote_lines"
	if got := countAsViewOwner(t, env, staffClaim(nil, true), quotes); got != total {
		t.Errorf("a global claim reads %d quotes, want %d (positive control)", got, total)
	}
	if got := countAsViewOwner(t, env, staffClaim(nil, true), lines); got != totalLines {
		t.Errorf("a global claim reads %d quote lines, want %d (positive control)", got, totalLines)
	}
	for _, c := range []struct {
		branch string
		q, l   int
	}{{w.a, qa, la}, {w.b, qb, lb}} {
		if got := countAsViewOwner(t, env, staffClaim([]string{c.branch}, false), quotes); got != c.q {
			t.Errorf("branch %s reads %d quotes, want %d", c.branch, got, c.q)
		}
		if got := countAsViewOwner(t, env, staffClaim([]string{c.branch}, false), lines); got != c.l {
			t.Errorf("branch %s reads %d quote lines, want %d", c.branch, got, c.l)
		}
	}
	other := staffClaim([]string{"99999999-9999-9999-9999-999999999999"}, false)
	for _, c := range []readtx.Claims{staffClaim(nil, false), other} {
		for _, table := range []string{quotes, lines} {
			if got := countAsViewOwner(t, env, c, table); got != 0 {
				t.Errorf("a claim with no reach reads %d rows from %s, want 0", got, table)
			}
		}
	}
}

func TestPolicy_QuoteAudienceAndTierFloor(t *testing.T) {
	env := testsupport.Open(t)
	w := plantQuoteWorld(t, env)
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: []string{w.a, w.b}}
	for _, table := range []string{"public.quotes", "public.quote_lines"} {
		if got := countAsViewOwner(t, env, staffClaim([]string{w.a}, true), table); got == 0 {
			t.Fatalf("the staff control claim reads no rows from %s", table)
		}
		if got := countAsViewOwner(t, env, pro, table); got != 0 {
			t.Errorf("a pro audience claim reads %d rows from %s, want 0", got, table)
		}
		if got := countAsViewOwnerUnclaimed(t, env, table); got != 0 {
			t.Errorf("no claim reads %d rows from %s, want 0", got, table)
		}
		for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
			c := staffClaim([]string{w.a, w.b}, true)
			c.Tier = tier
			if got := countAsViewOwner(t, env, c, table); (got > 0) != want {
				t.Errorf("a tier %d claim reads %d rows from %s, readable want %v", tier, got, table, want)
			}
		}
	}
}

// The quote_lines policy checks audience and tier itself, not only through the quotes policy its branch test
// reads through. With row security switched off on quotes for the length of one rolled back transaction, the
// lines policy stands alone: a pro claim and a tier 3 claim read no lines, a staff claim at tier 1 and 2 reads
// them, and the branch predicate still holds.
func TestPolicy_QuoteLinesStandAloneOnAudienceTierAndBranch(t *testing.T) {
	env := testsupport.Open(t)
	w := plantQuoteWorld(t, env)
	const lines = "public.quote_lines"
	off := `ALTER TABLE public.quotes DISABLE ROW LEVEL SECURITY`
	la := countWhere(t, env, `SELECT count(*) FROM quote_lines ql JOIN quotes q ON q.id = ql.quote_id WHERE q.branch_id = $1::uuid`, w.a)
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: []string{w.a, w.b}}
	if got := countAsViewOwnerWith(t, env, staffClaim([]string{w.a}, false), lines, off); got != la {
		t.Errorf("standing alone, branch a reads %d lines, want %d (positive control)", got, la)
	}
	if got := countAsViewOwnerWith(t, env, pro, lines, off); got != 0 {
		t.Errorf("standing alone, a pro claim reads %d lines, want 0", got)
	}
	for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
		c := staffClaim([]string{w.a, w.b}, true)
		c.Tier = tier
		if got := countAsViewOwnerWith(t, env, c, lines, off); (got > 0) != want {
			t.Errorf("standing alone, a tier %d claim reads %d lines, readable want %v", tier, got, want)
		}
	}
	if got := countAsViewOwnerWith(t, env, staffClaim(nil, false), lines, off); got != 0 {
		t.Errorf("standing alone, a claim with no reach reads %d lines, want 0", got)
	}
}
