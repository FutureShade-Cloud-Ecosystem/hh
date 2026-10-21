//go:build integration

package till

import (
	"context"
	"testing"
)

// LodgementBranch (SF-2g round 2) answers the branch of a LODGEMENT movement through its session, and nothing for
// another movement type, an unknown id or text that is no id.
func TestIntegration_LodgementBranch(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var branch, user, session, lodgement, paidIn string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branch); err != nil {
		t.Skipf("no seeded branch: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users WHERE status = 'ACTIVE' LIMIT 1`).Scan(&user); err != nil {
		t.Skipf("no seeded user: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO till_sessions (branch_id, station, opened_by) VALUES ($1::uuid, 'LBTEST', $2::uuid) RETURNING id::text`, branch, user).Scan(&session); err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM till_movements WHERE till_session_id = $1::uuid`, session)
		_, _ = pool.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, session)
	})
	ins := func(kind string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
			VALUES ($1::uuid, $2, 'CASH', 1000, 'test', 'LB-REF', $3::uuid) RETURNING id::text`, session, kind, user).Scan(&id); err != nil {
			t.Fatalf("movement %s: %v", kind, err)
		}
		return id
	}
	lodgement, paidIn = ins("LODGEMENT"), ins("PAID_IN")
	repo := NewRepository(pool)
	got, found, err := repo.LodgementBranch(ctx, lodgement)
	if err != nil || !found || got != branch {
		t.Fatalf("a lodgement answered branch %q found %v err %v, want %q", got, found, err, branch)
	}
	for name, id := range map[string]string{"a paid in movement": paidIn, "an unknown id": "33333333-3333-4333-8333-3333333333ee", "text": "not-an-id"} {
		if _, found, err := repo.LodgementBranch(ctx, id); err != nil || found {
			t.Errorf("%s: found %v err %v, want not found", name, found, err)
		}
	}
}
