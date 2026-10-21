//go:build integration

package parity

// The receivables row policies tested directly, as the quote and order ones are: the view owner reads the base
// tables under signed claims. Each predicate is pinned on its own, the allocation policy standing alone (row
// security switched off on the two tables it reads through, for one rolled back transaction) so a lost predicate
// cannot hide behind the other table's policy.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

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

// No assist role but the zone reader reads a setting, and the zone reader reads one row of it. The dealer's day
// reaches the aging view through ai.dealer_day(), a definer function owned by the zone reader that returns a date
// and nothing else; the reader may run it, no one else may, and the zone reader holds nothing else.
func TestPolicy_OnlyTheZoneReaderReadsTheBusinessZoneAndTheReaderSeesOnlyTheDealersDay(t *testing.T) {
	env := testsupport.Open(t)
	ctx := testsupport.Ctx(t)
	for _, r := range []string{env.Roles.ViewOwner, env.Roles.Reader, env.Roles.ProReader, env.Roles.Login, env.Roles.ProLogin, env.Roles.ClaimsOwner} {
		var canRead bool
		if err := env.Admin.QueryRow(ctx, `SELECT has_any_column_privilege($1, 'public.system_settings', 'SELECT')`, r).Scan(&canRead); err != nil {
			t.Fatal(err)
		}
		if canRead {
			t.Errorf("role %s can read system_settings", r)
		}
	}
	// The zone reader reads the business zone row and no other, with the row security on.
	if n := countWhere(t, env, `SELECT count(*) FROM system_settings WHERE key::text <> 'business_zone'`); n == 0 {
		t.Fatal("the world holds no other setting to be denied")
	}
	plant := `INSERT INTO system_settings (key, value) VALUES ('business_zone', '"America/Winnipeg"') ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`
	tx, err := env.Admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, plant); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{env.Roles.ZoneReader}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	var all, other int
	if err := tx.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE key::text <> 'business_zone') FROM public.system_settings`).Scan(&all, &other); err != nil {
		t.Fatalf("the zone reader cannot read its one setting: %v", err)
	}
	if all != 1 || other != 0 {
		t.Errorf("the zone reader reads %d settings, %d of them other than the business zone, want exactly the business zone", all, other)
	}
	var day string
	if err := tx.QueryRow(ctx, `SELECT public.settings_business_zone()`).Scan(&day); err != nil || day != "America/Winnipeg" {
		t.Errorf("the zone reader runs the zone rule and gets %q (%v), want America/Winnipeg", day, err)
	}
	_ = tx.Rollback(ctx)

	var readerRuns, publicRuns, definer, stable bool
	var owner string
	if err := env.Admin.QueryRow(ctx, `SELECT has_function_privilege($1, 'ai.dealer_day()', 'EXECUTE'), has_function_privilege('public', 'ai.dealer_day()', 'EXECUTE'),
		p.prosecdef AND p.proconfig IS NOT NULL, p.provolatile = 's', r.rolname
		FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner WHERE p.oid = 'ai.dealer_day()'::regprocedure`, env.Roles.Reader).Scan(&readerRuns, &publicRuns, &definer, &stable, &owner); err != nil {
		t.Fatal(err)
	}
	if !readerRuns || publicRuns || !definer || !stable || owner != env.Roles.ZoneReader {
		t.Errorf("ai.dealer_day: the reader may run it %v (want true), PUBLIC may %v (want false), definer with a fixed search path %v, stable %v, owner %s (want %s)", readerRuns, publicRuns, definer, stable, owner, env.Roles.ZoneReader)
	}
	for _, r := range []string{env.Roles.ViewOwner, env.Roles.ProReader, env.Roles.Login, env.Roles.ProLogin, env.Roles.ClaimsOwner} {
		var may bool
		if err := env.Admin.QueryRow(ctx, `SELECT has_function_privilege($1, 'ai.dealer_day()', 'EXECUTE')`, r).Scan(&may); err != nil {
			t.Fatal(err)
		}
		if may {
			t.Errorf("role %s can run ai.dealer_day", r)
		}
	}
	var member bool
	if err := env.Admin.QueryRow(ctx, `SELECT bool_or(pg_has_role(r.oid, z.oid, 'MEMBER')) FROM pg_roles r, pg_roles z WHERE z.rolname = $1 AND r.rolname = ANY($2)`,
		env.Roles.ZoneReader, []string{env.Roles.ViewOwner, env.Roles.ClaimsOwner, env.Roles.Reader, env.Roles.ProReader, env.Roles.Login, env.Roles.ProLogin}).Scan(&member); err != nil || member {
		t.Errorf("an assist role is a member of the zone reader (%v, %v)", member, err)
	}
}
