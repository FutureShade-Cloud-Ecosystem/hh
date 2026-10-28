//go:build integration

// finance_box_state_integration_test.go, the database's state, against a real
// Postgres.
//
// What only a database can prove: the state the entrypoint acts on (empty,
// unseeded, finance and foreign), read from the same seams the boot guard
// reads, so the guard and the entrypoint can never answer "empty"
// differently (items 2 and 3).
package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_FinanceBoxStateAnswersTheFourStates: the state the
// entrypoint acts on. One definition, read from the same seams the guard
// reads, so the two cannot drift.
func TestIntegration_FinanceBoxStateAnswersTheFourStates(t *testing.T) {
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		pool := freshEmptyDB(t, "finboxstateempty")
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateEmpty {
			t.Fatalf("a fresh database must read as %s, got %s", financeBoxStateEmpty, state)
		}
	})

	t.Run("unseeded", func(t *testing.T) {
		pool, _ := freshMigratedBoxDB(t, "finboxstateunseeded")
		writeBootstrapMarkForTest(t, ctx, pool)
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateUnseeded {
			t.Fatalf("a marked migrated database with no seed and no box kind must read as %s, got %s", financeBoxStateUnseeded, state)
		}
	})

	t.Run("finance", func(t *testing.T) {
		pool, _ := freshMigratedBoxDB(t, "finboxstatefinance")
		writeBoxKindForTest(t, ctx, pool, "finance")
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateFinance {
			t.Fatalf("the finance box's own world must read as %s, got %s", financeBoxStateFinance, state)
		}
	})

	t.Run("foreign", func(t *testing.T) {
		pool, _ := freshUnbookkeptDB(t, "finboxstateforeign")
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateForeign {
			t.Fatalf("a foreign schema must read as %s, got %s", financeBoxStateForeign, state)
		}
	})

	t.Run("another box", func(t *testing.T) {
		pool, _ := freshMigratedBoxDB(t, "finboxstateother")
		writeBoxKindForTest(t, ctx, pool, "demo")
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateForeign {
			t.Fatalf("a database marked as another box must read as %s, got %s", financeBoxStateForeign, state)
		}
	})

	t.Run("interrupted first migrate", func(t *testing.T) {
		pool, _ := freshBookkeepingOnlyDB(t, "finboxstatebookkeeping")
		writeBootstrapMarkForTest(t, ctx, pool)
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateUnseeded {
			t.Fatalf("a marked database with the bookkeeping table and nothing else must read as %s, got %s", financeBoxStateUnseeded, state)
		}
	})

	t.Run("tables that live outside public are foreign", func(t *testing.T) {
		// The S3 read's P2 2: a database that holds its tables in a schema
		// of their own, nothing in public and no bookkeeping, is somebody
		// else's world: it is not empty, and the box refuses it instead of
		// claiming and seeding it.
		pool := freshEmptyDB(t, "finboxstateschema")
		if _, err := pool.Exec(ctx, `
			CREATE SCHEMA other;
			CREATE TABLE other.precious (id INT PRIMARY KEY, note TEXT NOT NULL);
			INSERT INTO other.precious VALUES (1, 'another app row')`); err != nil {
			t.Fatalf("planting the foreign schema: %v", err)
		}
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateForeign {
			t.Fatalf("a database holding only other.precious must read as %s, got %s", financeBoxStateForeign, state)
		}
		if err := financeBoxDatabaseGuard(ctx, pool); err == nil {
			t.Fatal("the boot guard must refuse a database whose tables live outside public")
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM other.precious`).Scan(&n); err != nil {
			t.Fatalf("the foreign row: %v", err)
		}
		if n != 1 {
			t.Fatalf("a refused database must be untouched, other.precious holds %d rows", n)
		}
	})
}

// TestIntegration_FinanceBoxUnseededRecoveryNeedsTheBootstrapMark: recovery
// is the bootstrap ownership marker's answer alone. A migrated database with
// no box kind, no seed and no marker was not planted by this box, and it is
// foreign: the entrypoint refuses it instead of healing it (the K3 read's
// finding 1, a foreign migrated unmarked database was seeded). With the
// marker the same shapes are the box's own interrupted boot, and the seed
// heals them; and the finance box's own world never takes the marker's path
// at all, so the live box is untouched by any of this.
func TestIntegration_FinanceBoxUnseededRecoveryNeedsTheBootstrapMark(t *testing.T) {
	ctx := context.Background()

	t.Run("a foreign migrated unmarked database is refused", func(t *testing.T) {
		pool, _ := freshMigratedBoxDB(t, "finboxstatemarkless")
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateForeign {
			t.Fatalf("a migrated unmarked database with no bootstrap mark must read as %s, got %s", financeBoxStateForeign, state)
		}
	})

	t.Run("an interrupted first boot with the mark heals", func(t *testing.T) {
		// The entrypoint marks the database before the first migrate, so a
		// crash between migrate and seed leaves the mark, the migrations,
		// no box kind and no seed: the state the entrypoint heals.
		pool, _ := freshMigratedBoxDB(t, "finboxstateheal")
		writeBootstrapMarkForTest(t, ctx, pool)
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateUnseeded {
			t.Fatalf("a marked interrupted first boot must read as %s so the boot heals, got %s", financeBoxStateUnseeded, state)
		}
	})

	t.Run("an interrupted first migrate with the mark heals", func(t *testing.T) {
		pool, _ := freshBookkeepingOnlyDB(t, "finboxstatehealmig")
		writeBootstrapMarkForTest(t, ctx, pool)
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateUnseeded {
			t.Fatalf("a marked interrupted first migrate must read as %s so the boot heals, got %s", financeBoxStateUnseeded, state)
		}
	})

	t.Run("the finance box's own world never takes the marker path", func(t *testing.T) {
		// The live box's database is marked box_kind=finance at the first
		// seed, so it answers finance whatever the bootstrap mark says: the
		// mark is read only on an unmarked database, and this leg holds no
		// mark at all.
		pool, _ := freshMigratedBoxDB(t, "finboxstatefinnomark")
		writeBoxKindForTest(t, ctx, pool, "finance")
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateFinance {
			t.Fatalf("the finance box's own world must read as %s with no bootstrap mark present, got %s", financeBoxStateFinance, state)
		}
	})
}

// writeBootstrapMarkForTest writes the finance bootstrap ownership mark the
// entrypoint writes into a completely empty database before the first
// migrate: one schema in the catalog, the word the classifier reads, and no
// migration anywhere.
func writeBootstrapMarkForTest(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, "CREATE SCHEMA "+financeBoxBootstrapSchema); err != nil {
		t.Fatalf("writing the bootstrap mark: %v", err)
	}
}
