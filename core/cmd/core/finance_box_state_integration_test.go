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
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateUnseeded {
			t.Fatalf("a migrated database with no seed and no box kind must read as %s, got %s", financeBoxStateUnseeded, state)
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
		state, err := financeBoxDatabaseState(ctx, pool)
		if err != nil {
			t.Fatalf("reading the state: %v", err)
		}
		if state != financeBoxStateUnseeded {
			t.Fatalf("a database with the bookkeeping table and nothing else must read as %s, got %s", financeBoxStateUnseeded, state)
		}
	})
}

// TestIntegration_FinanceBoxUnseededRecoveryNeedsTheBootstrapMark: recovery
// is the bootstrap ownership marker's answer alone. A migrated database with
// no box kind, no seed and no marker was not planted by this box, and it is
// foreign: the entrypoint refuses it instead of healing it (the K3 read's
// finding 1, a foreign migrated unmarked database was seeded).
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
}
