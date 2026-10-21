//go:build integration

package parity

// The delivery row policies tested directly: the view owner reads public.delivery_runs and public.delivery_stops
// under signed claims. Each predicate (audience, tier, branch reach) is pinned on its own table.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/readtx/testsupport"
)

func TestPolicy_DeliveryRowsFollowTheClaimsBranchesAudienceAndTier(t *testing.T) {
	env := testsupport.Open(t)
	w := plantDeliveryWorld(t, env)
	for _, table := range []string{"delivery_runs", "delivery_stops"} {
		fq := "public." + table
		total := countWhere(t, env, `SELECT count(*) FROM `+table)
		if got := countAsViewOwner(t, env, staffClaim(nil, true), fq); got != total {
			t.Errorf("a global claim reads %d rows of %s, want %d (positive control)", got, table, total)
		}
		for _, br := range w.branches() {
			want := countWhere(t, env, `SELECT count(*) FROM `+table+` WHERE branch_id = $1::uuid`, br)
			if want == 0 || want == total {
				t.Fatalf("the policy world is wrong at %s for %s: %d of %d", br, table, want, total)
			}
			if got := countAsViewOwner(t, env, staffClaim([]string{br}, false), fq); got != want {
				t.Errorf("branch %s reads %d rows of %s, want %d", br, got, table, want)
			}
		}
		for _, c := range []readtx.Claims{staffClaim(nil, false), staffClaim(noReach, false)} {
			if got := countAsViewOwner(t, env, c, fq); got != 0 {
				t.Errorf("a claim with no reach reads %d rows of %s, want 0", got, table)
			}
		}
		pro := readtx.Claims{Audience: readtx.AudiencePro, AskerID: "policy-test-pro", Tier: 2, CustomerID: "33333333-3333-3333-3333-333333333333", Global: true, BranchIDs: w.branches()}
		if got := countAsViewOwner(t, env, pro, fq); got != 0 {
			t.Errorf("a pro audience claim reads %d rows of %s, want 0", got, table)
		}
		if got := countAsViewOwnerUnclaimed(t, env, fq); got != 0 {
			t.Errorf("no claim reads %d rows of %s, want 0", got, table)
		}
		for tier, want := range map[int]bool{0: false, 1: true, 2: true, 3: false} {
			c := staffClaim(w.branches(), true)
			c.Tier = tier
			if got := countAsViewOwner(t, env, c, fq); (got > 0) != want {
				t.Errorf("a tier %d claim reads %d rows of %s, readable want %v", tier, got, table, want)
			}
		}
	}
}
