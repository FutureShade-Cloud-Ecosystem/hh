//go:build integration

package parity

// The receivables row policies tested directly, as the quote and order ones are: the view owner reads the base
// tables under signed claims. Each predicate is pinned on its own, the allocation policy standing alone (row
// security switched off on the two tables it reads through, for one rolled back transaction) so a lost predicate
// cannot hide behind the other table's policy.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func trailClaim(branches []string, global bool) readtx.Claims {
	c := staffClaim(branches, global)
	c.PaymentTrail = true
	return c
}

var noReach = []string{"99999999-9999-9999-9999-999999999999"}

func TestPolicy_InvoiceRowsFollowTheClaimsBranchesAudienceAndTier(t *testing.T) {
	env := testsupport.Open(t)
	w := plantARWorld(t, env)
	const inv = "public.invoices"
	total := countWhere(t, env, `SELECT count(*) FROM invoices`)
	if got := countAsViewOwner(t, env, staffClaim(nil, true), inv); got != total {
		t.Errorf("a global claim reads %d invoices, want %d (positive control)", got, total)
	}
	for _, br := range w.branches() {
		want := countWhere(t, env, `SELECT count(*) FROM invoices WHERE branch_id = $1::uuid`, br)
		if want == 0 || want == total {
			t.Fatalf("the policy world is wrong at %s: %d of %d", br, want, total)
		}
		if got := countAsViewOwner(t, env, staffClaim([]string{br}, false), inv); got != want {
			t.Errorf("branch %s reads %d invoices, want %d", br, got, want)
		}
	}
	for _, c := range []readtx.Claims{staffClaim(nil, false), staffClaim(noReach, false)} {
		if got := countAsViewOwner(t, env, c, inv); got != 0 {
			t.Errorf("a claim with no reach reads %d invoices, want 0", got)
		}
	}
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: w.branches()}
	if got := countAsViewOwner(t, env, pro, inv); got != 0 {
		t.Errorf("a pro audience claim reads %d invoices, want 0", got)
	}
	if got := countAsViewOwnerUnclaimed(t, env, inv); got != 0 {
		t.Errorf("no claim reads %d invoices, want 0", got)
	}
	for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
		c := staffClaim(w.branches(), true)
		c.Tier = tier
		if got := countAsViewOwner(t, env, c, inv); (got > 0) != want {
			t.Errorf("a tier %d claim reads %d invoices, readable want %v", tier, got, want)
		}
	}
}

// A payment is a row of the payment trail routes only (the history by the payment's branch): a caller without the
// trail reads no payment row, in any status, so the presence of a row never tells a status.
func TestPolicy_PaymentRowsAreTheTrailRoutesOnlyByBranchAudienceAndTier(t *testing.T) {
	env := testsupport.Open(t)
	w := plantARWorld(t, env)
	const pay = "public.payments"
	total := countWhere(t, env, `SELECT count(*) FROM payments`)
	if got := countAsViewOwner(t, env, trailClaim(nil, true), pay); got != total {
		t.Errorf("a global claim with the trail reads %d payments, want %d (positive control)", got, total)
	}
	for _, c := range []readtx.Claims{staffClaim(nil, true), staffClaim(w.branches(), false)} {
		if got := countAsViewOwner(t, env, c, pay); got != 0 {
			t.Errorf("a claim without the trail reads %d payments, want 0", got)
		}
	}
	for _, br := range w.branches() {
		all := countWhere(t, env, `SELECT count(*) FROM payments WHERE branch_id = $1::uuid`, br)
		if all == 0 || all == total {
			t.Fatalf("the policy world is wrong at %s: %d of %d payments", br, all, total)
		}
		if got := countAsViewOwner(t, env, trailClaim([]string{br}, false), pay); got != all {
			t.Errorf("branch %s with the trail reads %d payments, want %d", br, got, all)
		}
		if got := countAsViewOwner(t, env, staffClaim([]string{br}, false), pay); got != 0 {
			t.Errorf("branch %s without the trail reads %d payments, want 0", br, got)
		}
	}
	for _, c := range []readtx.Claims{trailClaim(nil, false), trailClaim(noReach, false), staffClaim(noReach, false)} {
		if got := countAsViewOwner(t, env, c, pay); got != 0 {
			t.Errorf("a claim with no reach reads %d payments, want 0", got)
		}
	}
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, PaymentTrail: true, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: w.branches()}
	if got := countAsViewOwner(t, env, pro, pay); got != 0 {
		t.Errorf("a pro audience claim reads %d payments, want 0", got)
	}
	if got := countAsViewOwnerUnclaimed(t, env, pay); got != 0 {
		t.Errorf("no claim reads %d payments, want 0", got)
	}
	for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
		c := trailClaim(w.branches(), true)
		c.Tier = tier
		if got := countAsViewOwner(t, env, c, pay); (got > 0) != want {
			t.Errorf("a tier %d claim reads %d payments, readable want %v", tier, got, want)
		}
	}
}

// Allocations are rows of the trail routes only: the payment history (by the payment's branch) and the invoice's
// payments (by the invoice's branch). Standing alone, each arm of the reach and each other predicate is pinned.
func TestPolicy_AllocationRowsAreTheTrailRoutesOnlyAndStandAlone(t *testing.T) {
	env := testsupport.Open(t)
	w := plantARWorld(t, env)
	const al = "public.payment_allocations"
	off := []string{`ALTER TABLE public.payments DISABLE ROW LEVEL SECURITY`, `ALTER TABLE public.invoices DISABLE ROW LEVEL SECURITY`}
	total := countWhere(t, env, `SELECT count(*) FROM payment_allocations`)
	if got := countAsViewOwnerWith(t, env, trailClaim(nil, true), al, off...); got != total {
		t.Errorf("standing alone, a global claim with the trail reads %d allocations, want %d (positive control)", got, total)
	}
	if got := countAsViewOwnerWith(t, env, staffClaim(nil, true), al, off...); got != 0 {
		t.Errorf("standing alone, a global claim without the trail reads %d allocations, want 0", got)
	}
	for _, br := range w.branches() {
		want := countWhere(t, env, `SELECT count(*) FROM payment_allocations pa
			WHERE EXISTS (SELECT 1 FROM payments p WHERE p.id = pa.payment_id AND p.branch_id = $1::uuid)
			   OR EXISTS (SELECT 1 FROM invoices i WHERE i.id = pa.invoice_id AND i.branch_id = $1::uuid)`, br)
		viaPayment := countWhere(t, env, `SELECT count(*) FROM payment_allocations pa JOIN payments p ON p.id = pa.payment_id WHERE p.branch_id = $1::uuid`, br)
		viaInvoice := countWhere(t, env, `SELECT count(*) FROM payment_allocations pa JOIN invoices i ON i.id = pa.invoice_id WHERE i.branch_id = $1::uuid`, br)
		// Each arm is needed somewhere: at branch a the payment arm alone is the union's missing part, at branch b
		// the invoice arm is (the cross branch allocation is taken at a and applied to an invoice of b).
		if want == 0 || want == total || (br == w.a && viaInvoice == want) || (br == w.b && viaPayment == want) {
			t.Fatalf("the policy world is wrong at %s: union %d, by payment %d, by invoice %d, total %d", br, want, viaPayment, viaInvoice, total)
		}
		if got := countAsViewOwnerWith(t, env, trailClaim([]string{br}, false), al, off...); got != want {
			t.Errorf("standing alone, branch %s reads %d allocations, want %d", br, got, want)
		}
	}
	for _, c := range []readtx.Claims{trailClaim(nil, false), trailClaim(noReach, false)} {
		if got := countAsViewOwnerWith(t, env, c, al, off...); got != 0 {
			t.Errorf("standing alone, a claim with no reach reads %d allocations, want 0", got)
		}
	}
	pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, PaymentTrail: true, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: w.branches()}
	if got := countAsViewOwnerWith(t, env, pro, al, off...); got != 0 {
		t.Errorf("standing alone, a pro claim reads %d allocations, want 0", got)
	}
	for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
		c := trailClaim(w.branches(), true)
		c.Tier = tier
		if got := countAsViewOwnerWith(t, env, c, al, off...); (got > 0) != want {
			t.Errorf("standing alone, a tier %d claim reads %d allocations, readable want %v", tier, got, want)
		}
	}
	// And through the other tables' own policies (nothing switched off) the trail and the reach still hold.
	if got := countAsViewOwner(t, env, staffClaim(nil, true), al); got != 0 {
		t.Errorf("a global claim without the trail reads %d allocations, want 0", got)
	}
}

// The view owner reads no setting at all. The dealer's day reaches the aging view through the one purpose
// wrapper assist_dealer_day(), which returns a date and nothing else.
func TestPolicy_TheViewOwnerReadsNoSettingAndSeesOnlyTheDealersDay(t *testing.T) {
	env := testsupport.Open(t)
	ctx := testsupport.Ctx(t)
	var canRead bool
	if err := env.Admin.QueryRow(ctx, `SELECT has_any_column_privilege($1, 'public.system_settings', 'SELECT')`, env.Roles.ViewOwner).Scan(&canRead); err != nil {
		t.Fatal(err)
	}
	if canRead {
		t.Error("the view owner can read system_settings")
	}
	var readerRuns, publicRuns, definer bool
	if err := env.Admin.QueryRow(ctx, `SELECT has_function_privilege($1, 'public.assist_dealer_day()', 'EXECUTE'), has_function_privilege('public', 'public.assist_dealer_day()', 'EXECUTE'),
		(SELECT prosecdef AND proconfig IS NOT NULL FROM pg_proc WHERE proname = 'assist_dealer_day')`, env.Roles.Reader).Scan(&readerRuns, &publicRuns, &definer); err != nil {
		t.Fatal(err)
	}
	if !readerRuns || publicRuns || !definer {
		t.Errorf("assist_dealer_day: the reader may run it %v (want true), PUBLIC may %v (want false), definer with a fixed search path %v (want true)", readerRuns, publicRuns, definer)
	}
	for _, r := range []string{env.Roles.ProReader, env.Roles.Login, env.Roles.ProLogin, env.Roles.ClaimsOwner} {
		var may bool
		if err := env.Admin.QueryRow(ctx, `SELECT has_function_privilege($1, 'public.assist_dealer_day()', 'EXECUTE')`, r).Scan(&may); err != nil {
			t.Fatal(err)
		}
		if may {
			t.Errorf("role %s can run assist_dealer_day", r)
		}
	}
}
