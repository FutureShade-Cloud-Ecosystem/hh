//go:build integration

// personaseed_integration_test.go: the persona inbox seeding seam against
// the migrated + seeded database. The demo-tagged populate test proves the
// pipeline end to end (each persona's inbox answers its rooms); this file
// proves the seam's own two halves on a planted persona-shaped user: the
// derived membership in the branch's house channels, and the rep DM channel
// the short pick notice path lands in, visible before any notice fires, and
// stable across a re-run (the guard that keeps a re-seed writing nothing).

package messaging

import (
	"context"
	"testing"
)

// The planted persona: an inside_sales staffer at Trenton (the Sales
// persona's shape), with a fixed id the cleanup names.
const (
	seedPersonaUser  = "e1000000-0000-4000-9000-0000000000e1"
	seedPersonaEmail = "persona.seed@fixture.test"
)

func plantPersonaUser(t *testing.T) {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, status)
		VALUES ($1, $2, 'Fixture Persona Seeder', 'ACTIVE')
		ON CONFLICT (id) DO NOTHING`, seedPersonaUser, seedPersonaEmail); err != nil {
		t.Fatalf("plant persona user: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id)
		SELECT $1, r.id FROM roles r WHERE r.code = 'INSIDE_SALES'
		ON CONFLICT DO NOTHING`, seedPersonaUser); err != nil {
		t.Fatalf("plant persona role: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO user_branch_grants (user_id, branch_id)
		VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, seedPersonaUser, branchTrenton); err != nil {
		t.Fatalf("plant persona grant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM msg_members WHERE user_id = $1`, seedPersonaUser)
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE slug LIKE 'rep-fixture-persona-seeder-' || '%' AND id NOT IN (SELECT channel_id FROM msg_members)`)
		_, _ = pool.Exec(ctx, `DELETE FROM user_branch_grants WHERE user_id = $1`, seedPersonaUser)
		_, _ = pool.Exec(ctx, `DELETE FROM user_roles WHERE user_id = $1`, seedPersonaUser)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, seedPersonaUser)
	})
}

// countRows0 counts a query with no bound parameter (the channel sweep).
func countRows0(t *testing.T, q string) int {
	t.Helper()
	var n int
	if err := intPool(t).QueryRow(context.Background(), q).Scan(&n); err != nil {
		t.Fatalf("count0: %v", err)
	}
	return n
}

// TestIntegration_SeedPersonaInboxes: the seam materializes the persona's
// derived rooms (the branch house channel among them) and ensures the rep DM
// channel; the second pass writes nothing more (the row counts hold).
func TestIntegration_SeedPersonaInboxes(t *testing.T) {
	plantPersonaUser(t)
	pool := intPool(t)
	ctx := context.Background()

	countRows := func(q string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, q, seedPersonaUser).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	// Before: the persona holds no membership and no DM room.
	if n := countRows(`SELECT count(*) FROM msg_members WHERE user_id = $1`); n != 0 {
		t.Fatalf("the planted persona already holds %d memberships", n)
	}

	first, err := SeedPersonaInboxes(ctx, pool)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if first.MembershipsSynced == 0 || first.RepChannelsSeeded == 0 {
		t.Fatalf("the first pass seeded nothing: %+v", first)
	}

	// The inbox answers the branch house channel and the rep DM (the short
	// pick notice path), through the module's own list read.
	repo := NewRepository(pool)
	chans, err := repo.ListChannelsForUser(ctx, seedPersonaUser)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	slugs := map[string]bool{}
	for _, c := range chans {
		if c.Slug != "" {
			slugs[c.Slug] = true
		}
	}
	if !slugs["house-trenton"] {
		t.Errorf("the persona's inbox missed its branch's house channel: %v", slugs)
	}
	repDM := false
	for s := range slugs {
		if len(s) > 4 && s[:4] == "rep-" {
			repDM = true
		}
	}
	if !repDM {
		t.Errorf("the persona's inbox carries no rep DM channel: %v", slugs)
	}

	// The second pass converges: the counts hold, the report answers zero.
	members := countRows(`SELECT count(*) FROM msg_members WHERE user_id = $1`)
	channels := countRows0(t, `SELECT count(*) FROM msg_channels WHERE slug LIKE 'rep-%'`)
	second, err := SeedPersonaInboxes(ctx, pool)
	if err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if second.RepChannelsSeeded != 0 {
		t.Errorf("the reseed ensured %d more rep channels, want none", second.RepChannelsSeeded)
	}
	if got := countRows(`SELECT count(*) FROM msg_members WHERE user_id = $1`); got != members {
		t.Errorf("the reseed changed the membership count: %d then %d", members, got)
	}
	if got := countRows0(t, `SELECT count(*) FROM msg_channels WHERE slug LIKE 'rep-%'`); got != channels {
		t.Errorf("the reseed changed the rep channel count: %d then %d", channels, got)
	}
}
