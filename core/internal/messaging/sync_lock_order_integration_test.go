//go:build integration

// sync_lock_order_integration_test.go: SyncMembership bumps the feed seq
// of every room it seated or pruned, and each bump takes that room's row
// lock. Two syncs that cross (one gains room A and loses room B while
// another gains B and loses A) deadlock if they lock in different orders,
// so the bumps run in ONE order, sorted by room id, across seats and
// prunes alike. The order is proven here by a trigger that logs the room
// of every update in the order the locks are taken, on rooms whose ids
// run against the order the sync's statements would hand them back.

package messaging

import (
	"context"
	"sort"
	"testing"
)

func TestIntegration_SyncMembership_Bumps_Rooms_In_Sorted_Id_Order(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)

	const (
		pruned = "00000000-0000-4000-8000-0000000000f1"
		seatHi = "ffffffff-0000-4000-8000-0000000000f2"
		seatLo = "eeeeeeee-0000-4000-8000-0000000000f3"
	)
	ids := []string{pruned, seatHi, seatLo}

	cleanup := func() {
		_, _ = pool.Exec(ctx, `DROP TRIGGER IF EXISTS r4_lock_log_trg ON msg_channels`)
		_, _ = pool.Exec(ctx, `DROP FUNCTION IF EXISTS r4_lock_log_fn()`)
		_, _ = pool.Exec(ctx, `DROP TABLE IF EXISTS r4_lock_log`)
		_, _ = pool.Exec(ctx, `DELETE FROM msg_channels WHERE id = ANY($1::uuid[])`, ids)
	}
	cleanup()
	t.Cleanup(cleanup)

	mk := func(id, slug string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO msg_channels (id, kind, slug, name, tier, auto_scope, auto_role)
			VALUES ($1, 'TEAM', $2, $3, 'STANDARD', 'GLOBAL', '*')`, id, slug, slug); err != nil {
			t.Fatalf("make %s: %v", slug, err)
		}
	}
	// The pruned room first, so the user is seated in it by a first sync.
	mk(pruned, "r4-lock-pruned")
	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// Two rooms to seat, the higher id inserted first (the scan order the
	// seat statement hands back), and the pruned room no longer qualifies.
	mk(seatHi, "r4-lock-seat-hi")
	mk(seatLo, "r4-lock-seat-lo")
	if _, err := pool.Exec(ctx, `UPDATE msg_channels SET is_archived = TRUE WHERE id = $1`, pruned); err != nil {
		t.Fatalf("archive the pruned room: %v", err)
	}

	for _, stmt := range []string{
		`CREATE TABLE r4_lock_log (n bigserial PRIMARY KEY, id uuid NOT NULL)`,
		`CREATE FUNCTION r4_lock_log_fn() RETURNS trigger LANGUAGE plpgsql AS
		 $$ BEGIN INSERT INTO r4_lock_log (id) VALUES (NEW.id); RETURN NEW; END $$`,
		`CREATE TRIGGER r4_lock_log_trg BEFORE UPDATE ON msg_channels
		 FOR EACH ROW WHEN (NEW.id IN ('` + pruned + `', '` + seatHi + `', '` + seatLo + `'))
		 EXECUTE FUNCTION r4_lock_log_fn()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install the lock order log: %v", err)
		}
	}

	if err := repo.SyncMembership(ctx, uSamRivera); err != nil {
		t.Fatalf("sync: %v", err)
	}

	rows, err := pool.Query(ctx, `SELECT id::text FROM r4_lock_log ORDER BY n`)
	if err != nil {
		t.Fatalf("read the lock order: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, id)
	}
	want := append([]string(nil), ids...)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("each changed room is bumped once: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the bumps must take their row locks in sorted id order across seats and prunes: got %v want %v", got, want)
		}
	}
}
