//go:build integration

package main

// fw1_08b_part_b_round2_integration_test.go holds the round 2 fixture helper.
//
// FW1-08b part B round 2 (FROM-PRINCIPAL 309) made the head office set's org half
// UNCONDITIONAL: a context that carries no staff scope is refused in words rather than
// skipping the tenancy wall. A leg that sets the head office therefore carries the org scope a
// request context carries, and the branches it names belong to that org.
//
// The helper plants the org and puts the box's branches into it. It runs on the legs' own
// throwaway databases, so the rows go away with the database.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// fw1HeadOfficeScope plants an org, puts every branch of the box into it, and answers the org id
// a leg stamps its staff scope with.
func fw1HeadOfficeScope(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	orgID := uuid.NewString()
	if _, err := pool.Exec(ctx,
		`INSERT INTO org (id, slug, name) VALUES ($1::uuid, $2, 'FW1 08b head office org')`,
		orgID, "fw108bho-"+orgID[:8]); err != nil {
		t.Fatalf("plant the org: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE locations SET org_id = $1::uuid WHERE loc_type = 'BRANCH' AND deleted_at IS NULL`,
		orgID); err != nil {
		t.Fatalf("put the branches into the org: %v", err)
	}
	return orgID
}
