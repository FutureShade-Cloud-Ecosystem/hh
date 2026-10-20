//go:build integration

package identity

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
)

// ReachOfUser is the payment fence's identity read: platform identity, grant orgs, branch reach. It
// must agree with the guard's own walk (ResolveStaff) and answer the zero value, never an error, for a
// staffer the gate cannot answer for.
func TestReachOfUser(t *testing.T) {
	pool := claimTestPool(t)
	ctx := context.Background()
	r, err := NewStaffAuthorityResolver(pool)
	if err != nil {
		t.Fatal(err)
	}
	var org string
	if err := pool.QueryRow(ctx, `INSERT INTO org (slug, name) VALUES ('reach-'||substr(gen_random_uuid()::text,1,8), 'reach') RETURNING id::text`).Scan(&org); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM org WHERE id = $1::uuid`, org) })
	rows, err := pool.Query(ctx, `SELECT id::text FROM locations ORDER BY id LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	var branches []string
	for rows.Next() {
		var b string
		_ = rows.Scan(&b)
		branches = append(branches, b)
	}
	rows.Close()
	if len(branches) < 2 {
		t.Skip("the seeded database carries fewer than two branches")
	}

	mk := func(status string) (platformID, userID string) {
		platformID = "reach-" + uuid.NewString()[:8]
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (email, display_name, status, platform_user_id) VALUES ($1, 'Fixture', $2, $3) RETURNING id::text`,
			platformID+"@fixture.invalid", status, platformID).Scan(&userID); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM staff_role_grant WHERE platform_user_id = $1`, platformID)
			_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, userID)
		})
		return platformID, userID
	}
	grant := func(platformID, role string, branch *string) {
		if _, err := pool.Exec(ctx,
			`INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id) VALUES ($1, $2, $3::uuid, $4::uuid)`,
			platformID, role, org, branch); err != nil {
			t.Fatal(err)
		}
	}

	// A global role reaches every branch.
	gmPlatform, gmUser := mk("ACTIVE")
	grant(gmPlatform, "general_manager", nil)
	got, err := r.ReachOfUser(ctx, gmUser)
	if err != nil || got.PlatformUserID != gmPlatform || !got.Global || !reflect.DeepEqual(got.OrgIDs, []string{org}) {
		t.Fatalf("general manager: %+v %v", got, err)
	}

	// A branch role reaches the branches it is granted, through the org scoped branch grants.
	smPlatform, smUser := mk("ACTIVE")
	grant(smPlatform, "sales_manager", &branches[0])
	if _, err := pool.Exec(ctx, `INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES ($1::uuid, $2::uuid, $3::uuid)`, smUser, branches[0], org); err != nil {
		t.Fatal(err)
	}
	got, err = r.ReachOfUser(ctx, smUser)
	if err != nil || got.Global {
		t.Fatalf("sales manager: %+v %v", got, err)
	}
	sort.Strings(got.BranchIDs)
	if !reflect.DeepEqual(got.BranchIDs, []string{branches[0]}) {
		t.Fatalf("sales manager reaches only the granted branch: %+v", got)
	}

	// Not ACTIVE, tombstoned, unlinked and unknown are the zero value and no error.
	_, suspended := mk("SUSPENDED")
	var unlinked string
	if err := pool.QueryRow(ctx, `INSERT INTO users (email, display_name, status) VALUES ($1, 'Fixture', 'ACTIVE') RETURNING id::text`, "reach-"+uuid.NewString()[:8]+"@fixture.invalid").Scan(&unlinked); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1::uuid`, unlinked) })
	for name, id := range map[string]string{"suspended": suspended, "unlinked": unlinked, "unknown": uuid.NewString()} {
		got, err := r.ReachOfUser(ctx, id)
		if err != nil || got.PlatformUserID != "" || got.Global || len(got.OrgIDs) != 0 || len(got.BranchIDs) != 0 {
			t.Errorf("%s must be the zero value: %+v %v", name, got, err)
		}
	}
}
