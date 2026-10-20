//go:build integration

package parity

// The purchase order row policies tested directly, as the quote and order ones are: the view owner reads
// public.purchasing_pos and public.purchasing_po_lines under signed claims. The lines policy is also tested
// standing alone (row security switched off on purchasing_pos for one rolled back transaction), so a policy
// that lost a predicate cannot hide behind the other table's.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func TestPolicy_PurchaseOrderRowsFollowTheClaimsBranches(t *testing.T) {
	env := testsupport.Open(t)
	w := plantPOWorld(t, env)
	total := countWhere(t, env, `SELECT count(*) FROM purchasing_pos`)
	totalLines := countWhere(t, env, `SELECT count(*) FROM purchasing_po_lines`)
	const pos, lines = "public.purchasing_pos", "public.purchasing_po_lines"
	if got := countAsViewOwner(t, env, staffClaim(nil, true), pos); got != total {
		t.Errorf("a global claim reads %d orders, want %d (positive control)", got, total)
	}
	if got := countAsViewOwner(t, env, staffClaim(nil, true), lines); got != totalLines {
		t.Errorf("a global claim reads %d order lines, want %d (positive control)", got, totalLines)
	}
	for _, br := range w.all() {
		po := countWhere(t, env, `SELECT count(*) FROM purchasing_pos WHERE branch_id = $1::uuid`, br)
		pl := countWhere(t, env, `SELECT count(*) FROM purchasing_po_lines l JOIN purchasing_pos p ON p.id = l.po_id WHERE p.branch_id = $1::uuid`, br)
		if po == 0 || pl == 0 || po == total {
			t.Fatalf("the policy world is wrong at %s: %d orders, %d lines of %d", br, po, pl, total)
		}
		if got := countAsViewOwner(t, env, staffClaim([]string{br}, false), pos); got != po {
			t.Errorf("branch %s reads %d orders, want %d", br, got, po)
		}
		if got := countAsViewOwner(t, env, staffClaim([]string{br}, false), lines); got != pl {
			t.Errorf("branch %s reads %d order lines, want %d", br, got, pl)
		}
	}
	other := staffClaim([]string{"99999999-9999-9999-9999-999999999999"}, false)
	for _, c := range []readtx.Claims{staffClaim(nil, false), other} {
		for _, table := range []string{pos, lines} {
			if got := countAsViewOwner(t, env, c, table); got != 0 {
				t.Errorf("a claim with no reach reads %d rows from %s, want 0", got, table)
			}
		}
	}
}

func TestPolicy_PurchaseOrderAudienceAndTierFloor(t *testing.T) {
	env := testsupport.Open(t)
	w := plantPOWorld(t, env)
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: w.all()}
	for _, table := range []string{"public.purchasing_pos", "public.purchasing_po_lines"} {
		if got := countAsViewOwner(t, env, staffClaim(w.all(), true), table); got == 0 {
			t.Fatalf("the staff control claim reads no rows from %s", table)
		}
		if got := countAsViewOwner(t, env, pro, table); got != 0 {
			t.Errorf("a pro audience claim reads %d rows from %s, want 0", got, table)
		}
		if got := countAsViewOwnerUnclaimed(t, env, table); got != 0 {
			t.Errorf("no claim reads %d rows from %s, want 0", got, table)
		}
		for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
			c := staffClaim(w.all(), true)
			c.Tier = tier
			if got := countAsViewOwner(t, env, c, table); (got > 0) != want {
				t.Errorf("a tier %d claim reads %d rows from %s, readable want %v", tier, got, table, want)
			}
		}
	}
}

func TestPolicy_PurchaseOrderLinesStandAloneOnAudienceTierAndBranch(t *testing.T) {
	env := testsupport.Open(t)
	w := plantPOWorld(t, env)
	const lines = "public.purchasing_po_lines"
	off := `ALTER TABLE public.purchasing_pos DISABLE ROW LEVEL SECURITY`
	la := countWhere(t, env, `SELECT count(*) FROM purchasing_po_lines l JOIN purchasing_pos p ON p.id = l.po_id WHERE p.branch_id = $1::uuid`, w.manager)
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: w.all()}
	if got := countAsViewOwnerWith(t, env, staffClaim([]string{w.manager}, false), lines, off); got != la {
		t.Errorf("standing alone, the branch reads %d lines, want %d (positive control)", got, la)
	}
	if got := countAsViewOwnerWith(t, env, pro, lines, off); got != 0 {
		t.Errorf("standing alone, a pro claim reads %d lines, want 0", got)
	}
	for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
		c := staffClaim(w.all(), true)
		c.Tier = tier
		if got := countAsViewOwnerWith(t, env, c, lines, off); (got > 0) != want {
			t.Errorf("standing alone, a tier %d claim reads %d lines, readable want %v", tier, got, want)
		}
	}
	if got := countAsViewOwnerWith(t, env, staffClaim(nil, false), lines, off); got != 0 {
		t.Errorf("standing alone, a claim with no reach reads %d lines, want 0", got)
	}
}
