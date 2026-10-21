//go:build integration

package portal

// admin_guard_stop_cycle_integration_test.go: the last-admin guard (guardedAdminUpdate) serialises admin
// changes for a tenant on the customer row. A team member's insert (AcceptInvite) draws the team counter
// first (the sync seq trigger, 0422, or the explicit draw) and then, at its foreign key check, takes FOR KEY
// SHARE on the customer row. A guard that held the customer row FOR UPDATE while it waited for the team
// counter (its own role update draws it) would cycle against that insert: FOR UPDATE conflicts with KEY
// SHARE (PR 556 review, round 3 sweep). The guard takes the row FOR NO KEY UPDATE: it still conflicts with
// itself, so admin changes keep serialising, and not with KEY SHARE.
//
// The insert is the parked session: it stands holding the team counter it drew (a trigger parks the
// sessions of a pool whose application name is sf1-park right after they touch that counter, on an
// advisory lock the test holds); the guarded role change starts behind it; the test releases only when the
// role change is seen waiting.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIntegration_AdminGuardBesideATeamInsertOnTheSameCustomer(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)

	var branchID, custID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Skipf("no seeded BRANCH: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ('AG-'||$1, 'Admin Guard '||$1, 'CONTRACTOR', $2::uuid, 1) RETURNING id::text`, suffix, branchID).Scan(&custID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DROP TRIGGER IF EXISTS sf1_park_team ON scope_sequences`)
		_, _ = pool.Exec(c, `DROP FUNCTION IF EXISTS sf1_park_team()`)
		_, _ = pool.Exec(c, `DELETE FROM portal_invites WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM portal_users WHERE customer_id = $1::uuid`, custID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, custID)
	})
	var adminA, adminB string
	for i, dst := range []*string{&adminA, &adminB} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO portal_users (customer_id, email, name, password_hash, role, status)
			 VALUES ($1::uuid, $2, 'Admin', 'x', 'account_admin', 'active') RETURNING id::text`,
			custID, fmt.Sprintf("ag%d-%s@fixture.test", i, suffix)).Scan(dst); err != nil {
			t.Fatalf("seed admin: %v", err)
		}
	}
	repo := NewRepository(pool)
	if _, err := repo.CreateInvite(ctx, custID, "ag-new-"+suffix+"@fixture.test", "buyer", "raw-"+suffix, "hash-"+suffix, adminA, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("seed invite: %v", err)
	}
	// The team counter row must exist, or the parked session's first draw is an INSERT and no UPDATE
	// trigger fires to park it.
	if _, err := pool.Exec(ctx, `INSERT INTO scope_sequences (scope, next_value) VALUES ('team', 0) ON CONFLICT (scope) DO NOTHING`); err != nil {
		t.Fatalf("seed the team counter: %v", err)
	}
	for _, q := range []string{
		`CREATE OR REPLACE FUNCTION sf1_park_team() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN
		   IF NEW.scope = 'team' AND current_setting('application_name') = 'sf1-park' THEN
		     PERFORM pg_advisory_xact_lock(4242003);
		   END IF;
		   RETURN NULL;
		 END $$`,
		`CREATE TRIGGER sf1_park_team AFTER UPDATE ON scope_sequences FOR EACH ROW EXECUTE FUNCTION sf1_park_team()`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatalf("park trigger: %v", err)
		}
	}

	cfg := pool.Config().Copy()
	cfg.ConnConfig.RuntimeParams["application_name"] = "sf1-park"
	cfg.MaxConns = 2
	parkPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("park pool: %v", err)
	}
	defer parkPool.Close()

	hold, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer hold.Release()
	if _, err := hold.Exec(ctx, `SELECT pg_advisory_lock(4242003)`); err != nil {
		t.Fatalf("hold: %v", err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			_, _ = hold.Exec(ctx, `SELECT pg_advisory_unlock(4242003)`)
		}
	}
	defer release()

	waitFor := func(what, q string) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			var n int
			if err := pool.QueryRow(ctx, q).Scan(&n); err != nil {
				t.Fatalf("%s: %v", what, err)
			}
			if n > 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("never saw: %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}

	errInsert := make(chan error, 1)
	go func() {
		_, err := NewRepository(parkPool).AcceptInvite(ctx, "hash-"+suffix, "New Member",
			func() (string, error) { return "x", nil }, time.Now())
		errInsert <- err
	}()
	waitFor("the team insert parked on its team counter touch",
		`SELECT count(*) FROM pg_locks WHERE locktype = 'advisory' AND NOT granted`)

	errGuard := make(chan error, 1)
	go func() { errGuard <- repo.UpdateRole(ctx, adminA, "buyer", true) }()
	waitFor("the guarded role change waiting behind the parked insert",
		`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND wait_event <> 'advisory'`)

	release()
	for who, ch := range map[string]chan error{"team insert": errInsert, "guarded role change": errGuard} {
		select {
		case err := <-ch:
			if err != nil {
				if strings.Contains(err.Error(), "40P01") || strings.Contains(err.Error(), "deadlock") {
					t.Fatalf("%s was killed as a deadlock victim on one customer: %v", who, err)
				}
				t.Fatalf("%s failed: %v", who, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatalf("%s never finished", who)
		}
	}
}
