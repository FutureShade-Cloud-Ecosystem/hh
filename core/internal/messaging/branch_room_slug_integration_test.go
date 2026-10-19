//go:build integration

// branch_room_slug_integration_test.go: a branch's room slugs come from the
// branch's own system rooms, not from a code table. A dealer whose branch
// code is not the room's name (code PLM, rooms named after the city) used to
// land every inbound customer message in #house-unassigned and every payment
// in the global #payments, because the slug was derived as yard-<code>.

package messaging

import (
	"context"
	"testing"
)

const branchHarborR5 = "a9850000-0000-4000-8000-0000000000b1"

func TestIntegration_BranchRoomSlugs_FollowTheBranchsOwnRooms(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	if _, err := pool.Exec(ctx, `
		INSERT INTO locations (id, branch_id, loc_type, code, name)
		VALUES ($1, $1, 'BRANCH', 'HBR5', 'Harbor yard round 5')`, branchHarborR5); err != nil {
		t.Fatalf("add the branch: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE branch_id = $1`, branchHarborR5)
		_, _ = pool.Exec(ctx, `DELETE FROM locations WHERE id = $1`, branchHarborR5)
	})
	// The three system rooms a dealer's branch carries, named after the city
	// (the code is HBR5), each with the auto_scope marker; plus a caller
	// founded BRANCH room with a yard shaped slug that must never be picked.
	for _, r := range []struct{ kind, slug, scope string }{
		{"BRANCH", "yard-harbor", "BRANCH"},
		{"TEAM", "house-harbor", "BRANCH"},
		{"TEAM", "payments-harbor", "BRANCH"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO msg_channels (kind, branch_id, slug, name, auto_scope, auto_role, tier)
			VALUES ($1, $2, $3::citext, '#' || $3::text, $4, '*', 'STANDARD')`, r.kind, branchHarborR5, r.slug, r.scope); err != nil {
			t.Fatalf("plant %s: %v", r.slug, err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO msg_channels (kind, branch_id, slug, name, tier)
		VALUES ('BRANCH', $1, 'yard-aaa-squat', '#yard-aaa-squat', 'STANDARD')`, branchHarborR5); err != nil {
		t.Fatalf("plant the squat: %v", err)
	}

	yard, err := repo.BranchYardSlug(ctx, branchHarborR5)
	if err != nil || yard != "yard-harbor" {
		t.Fatalf("yard slug = %q (%v), want yard-harbor", yard, err)
	}
	house, err := repo.BranchHouseSlug(ctx, branchHarborR5)
	if err != nil || house != "house-harbor" {
		t.Fatalf("house slug = %q (%v), want house-harbor", house, err)
	}
	if got := paymentsSlug(yard); got != "payments-harbor" {
		t.Fatalf("payments slug = %q, want payments-harbor", got)
	}
}
