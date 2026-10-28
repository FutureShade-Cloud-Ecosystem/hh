package main

import (
	"context"
	"testing"
)

// TestQD1B_TheGrantTheCountLegsPlantCarriesAnOrg is the fixture's own precondition: the grant a
// QD1 leg plants names a branch and an org, and a branch whose org is NULL cannot carry one.
//
// The plant must name a branch that carries its own org, not whichever location row happens to
// sort first on the gate. The demo branch the migrations plant carries no org at all (only the
// demo seed names one), so a QD1 only run against a fresh gate planted the grant at a row that
// cannot hold it, and the count legs failed on the grant before they counted anything. The kit's
// own branch carries the kit's own org, and that is the branch the QD1 legs file at.
func TestQD1B_TheGrantTheCountLegsPlantCarriesAnOrg(t *testing.T) {
	pool := wiringPool(t)
	const sub = "fxstaff-qd1b-gate-probe"
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`DELETE FROM staff_role_grant WHERE platform_user_id = $1`, sub)
	})
	qd1ePlantRoleGrant(t, pool, sub, "inside_sales")
	var orgID *string
	if err := pool.QueryRow(context.Background(),
		`SELECT org_id::text FROM staff_role_grant WHERE platform_user_id = $1`, sub).Scan(&orgID); err != nil {
		t.Fatalf("read the probe grant: %v", err)
	}
	if orgID == nil {
		t.Fatal("the grant the count leg plants names a branch whose org is NULL")
	}
}
