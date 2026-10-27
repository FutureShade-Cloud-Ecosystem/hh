package testkit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PlantOrgBranch plants one org and one BRANCH location stamped with it, on a database that may carry
// no org at all (the CI integration world has none, and the seeded branches carry a NULL org). UX-95:
// the writers of orders and tasks take the org from the branch, so a leg that proves the stamp needs a
// branch that has one. Both rows are removed at test end; register this BEFORE planting rows that
// reference the branch, because cleanups run last in, first out.
//
// The cleanup also removes what the branch accrues the moment a writer mints against it: the mint's
// counter row (order_no_sequences), and the till sessions and GL journals written at the branch. Those
// rows pin the location through their own foreign keys, and a branch left behind hijacks every
// unordered `FROM locations` pick in the suite. The rows the test itself made (its orders, tasks,
// quotes) stay the caller's to remove; a delete that still fails is a leak, and it fails the test.
func PlantOrgBranch(t *testing.T, pool *pgxpool.Pool) (orgID, branchID string) {
	t.Helper()
	ctx := context.Background()
	tag := fmt.Sprintf("ux95-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO org (slug, name) VALUES ($1::citext, $1::text) RETURNING id::text`, tag).Scan(&orgID); err != nil {
		t.Fatalf("plant org: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		WITH ins AS (SELECT gen_random_uuid() AS id)
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, org_id)
		SELECT id, id, NULL, 'BRANCH', $1::citext, $1::text, $2::uuid FROM ins
		RETURNING id::text`, tag, orgID).Scan(&branchID); err != nil {
		t.Fatalf("plant branch: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		del := func(what, sql string, arg any) {
			if _, err := pool.Exec(c, sql, arg); err != nil {
				t.Errorf("PlantOrgBranch cleanup: deleting the branch's %s: %v", what, err)
			}
		}
		del("mint counter", `DELETE FROM order_no_sequences WHERE branch_id = $1::uuid`, branchID)
		del("till sessions", `DELETE FROM till_sessions WHERE branch_id = $1::uuid`, branchID)
		del("gl journals", `DELETE FROM gl_journals WHERE branch_id = $1::uuid`, branchID)
		del("location", `DELETE FROM locations WHERE id = $1::uuid`, branchID)
		del("org", `DELETE FROM org WHERE id = $1::uuid`, orgID)
	})
	return orgID, branchID
}
