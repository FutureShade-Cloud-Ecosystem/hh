//go:build integration

package parity

// The customer reach rules pinned directly. ai.customers carries its rule in the view (a security barrier whose
// WHERE holds audience, tier floor and home branch), because row security on customers would blank the customer
// names the order, quote, invoice, payment and delivery views join; it is read here through the reader with
// crafted claims. customer_contacts has a row policy (audience, tier floor, an invoice of the account in reach),
// read here as the view owner, standing alone with row security off on invoices and then through it.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func TestView_CustomersFollowTheClaimsHomeBranchAudienceAndTier(t *testing.T) {
	env := testsupport.Open(t)
	w := plantCRMWorld(t, env)
	// Read as the view owner, so the audience rule of the view is exercised too (the staff pool would refuse a
	// pro claim before the database saw it).
	q := `ai.customers WHERE account_no LIKE 'S7B-` + w.tag + `%'`
	check := func(label string, c readtx.Claims, want int) {
		t.Helper()
		if got := countAsViewOwner(t, env, c, q); got != want {
			t.Errorf("%s reads %d of the planted customers, want %d", label, got, want)
		}
	}
	// 7 planted: home-a, held, inactive, no-invoice at a; home-b, exempt at b; company homed to none.
	check("a global claim", staffClaim(nil, true), 7)
	check("a claim for branch a", staffClaim([]string{w.a}, false), 5)
	check("a claim for branch b", staffClaim([]string{w.b}, false), 3)
	check("a claim for both", staffClaim(w.branches(), false), 7)
	check("a claim with no grant (the company wide account only)", staffClaim(nil, false), 1)
	check("a claim for another branch (the company wide account only)", staffClaim(noReach, false), 1)
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: w.branches()}
	check("a pro audience claim", pro, 0)
	for tier, want := range map[int]int{0: 0, 1: 7, 2: 7, 3: 0} {
		c := staffClaim(w.branches(), true)
		c.Tier = tier
		check("a tier "+string(rune('0'+tier))+" claim", c, want)
	}
}

func TestPolicy_CustomerContactRowsNeedAnInvoiceInTheClaimsReachAndStandAlone(t *testing.T) {
	env := testsupport.Open(t)
	w := plantCRMWorld(t, env)
	const cc = "public.customer_contacts"
	off := `ALTER TABLE public.invoices DISABLE ROW LEVEL SECURITY`
	total := countWhere(t, env, `SELECT count(*) FROM customer_contacts`)
	withInvoice := func(br string) int {
		return countWhere(t, env, `SELECT count(*) FROM customer_contacts cc WHERE EXISTS (SELECT 1 FROM invoices i WHERE i.customer_id = cc.customer_id AND i.branch_id = $1::uuid)`, br)
	}
	if got := countAsViewOwnerWith(t, env, staffClaim(nil, true), cc, off); got != countWhere(t, env, `SELECT count(*) FROM customer_contacts cc WHERE EXISTS (SELECT 1 FROM invoices i WHERE i.customer_id = cc.customer_id)`) {
		t.Errorf("standing alone, a global claim reads %d contacts, want those of accounts with an invoice (positive control)", got)
	}
	for _, br := range w.branches() {
		want := withInvoice(br)
		if want == 0 || want >= total {
			t.Fatalf("the policy world is wrong at %s: %d of %d", br, want, total)
		}
		if got := countAsViewOwnerWith(t, env, staffClaim([]string{br}, false), cc, off); got != want {
			t.Errorf("standing alone, branch %s reads %d contacts, want %d", br, got, want)
		}
		if got := countAsViewOwner(t, env, staffClaim([]string{br}, false), cc); got != want {
			t.Errorf("through the invoices' own policy, branch %s reads %d contacts, want %d", br, got, want)
		}
	}
	for _, c := range []readtx.Claims{staffClaim(nil, false), staffClaim(noReach, false)} {
		if got := countAsViewOwnerWith(t, env, c, cc, off); got != 0 {
			t.Errorf("standing alone, a claim with no reach reads %d contacts, want 0", got)
		}
	}
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: w.branches()}
	if got := countAsViewOwnerWith(t, env, pro, cc, off); got != 0 {
		t.Errorf("standing alone, a pro claim reads %d contacts, want 0", got)
	}
	if got := countAsViewOwnerUnclaimed(t, env, cc); got != 0 {
		t.Errorf("no claim reads %d contacts, want 0", got)
	}
	for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
		c := staffClaim(w.branches(), true)
		c.Tier = tier
		if got := countAsViewOwnerWith(t, env, c, cc, off); (got > 0) != want {
			t.Errorf("standing alone, a tier %d claim reads %d contacts, readable want %v", tier, got, want)
		}
	}
}
