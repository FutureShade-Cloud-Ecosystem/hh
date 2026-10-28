//go:build integration

package main

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestIntegration_CreditMemoRound_TheRoundLeavesNoJournalOnTheGate is the gate rule the round's
// legs owe the shared database: every journal the round posts is the round's own row, and the
// round takes it back when it ends.
//
// The round posts a journal per leg: the reversing entry of an approved credit, the voided
// journal of a refused or withdrawn memo, and the ledger's own parked manual journal a decline
// never bridges. A journal left on the shared gate is a line no later package can decide or
// reverse, and a DRAFT line on 1100 is exactly what gl's deactivation leg reads later in the
// suite's go list order.
//
// The round's legs run here as subtests, so the two numbers are the round's own: the gate's
// journals before it and the gate's journals after it. Each leg takes its own rows back, the
// credit memo among them, so the legs that run after this one meet the gate as it was.
func TestIntegration_CreditMemoRound_TheRoundLeavesNoJournalOnTheGate(t *testing.T) {
	pool := cmRoundPool(t)
	before := cmRoundJournalCount(t, pool)
	for _, leg := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"the real rail posts a parked credit", TestIntegration_CreditMemoRound_TheRealRailPostsAParkedCredit},
		{"a refused park voids the memo", TestIntegration_CreditMemoRound_ARefusedParkVoidsTheMemoAndFreesTheRoom},
		{"a decline bridges nothing", TestIntegration_CreditMemoRound_ADeclineBridgesNothingForTheLedgersOwnJournal},
		{"a branch left on another org still parks", TestIntegration_CreditMemoRound_ABranchLeftOnAnotherOrgStillParks},
		{"a withdrawn park voids the memo", TestIntegration_CreditMemoRound_AWithdrawnParkVoidsTheMemoAndFreesTheRoom},
	} {
		t.Run(leg.name, leg.run)
	}
	after := cmRoundJournalCount(t, pool)
	if after != before {
		t.Fatalf("the round left %d journals on the shared gate: %d before, %d after",
			after-before, before, after)
	}
}

// cmRoundJournalCount counts the ledger's journals on the gate.
func cmRoundJournalCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journals`).Scan(&n); err != nil {
		t.Fatalf("count the gate's journals: %v", err)
	}
	return n
}
