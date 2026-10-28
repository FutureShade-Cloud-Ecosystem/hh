//go:build integration

package main

// w2_5_seed_reseed_integration_test.go, W2-5 round 2 (FROM-PRINCIPAL 74 item 2):
// the seed opens its stock at the value the books open at, AT INSERT, and never
// deletes a ledger row.
//
// THE DEFECT. stock_moves is an append-only ledger (migration 0037 refuses every
// UPDATE on it). The seed's opening moves are ledger rows, so they are history like
// any other, and a seed that deletes them rewrites the books. Round 1 realigned the
// opening moves' values with one statement at the end of the seed that DELETED every
// 'M7 opening balance' row and re-inserted it at the WAC
// product_branch_costs holds. That statement is a ledger rewrite, and it is not
// idempotent: the seed's own opening INSERTs are unguarded, so a second seed plants
// a second copy of every opening move, and the DELETE statement then swallows both
// copies and hands them back. On a database that already carries the seed, the
// ledger ends up holding every opening move TWICE, under two ids, and the books'
// opening figure and the ledger's opening figure are two different numbers.
//
// THE LEGS, each on its own fresh migrated database:
//
//  1. The seed never deletes a ledger row: the seed file itself carries no DELETE
//     against stock_moves. Round 1's statement is there today, so this leg is red.
//  2. A second seed leaves every ledger row exactly as it is, row for row, every
//     column, the ids included. Today the second seed doubles the opening moves,
//     so this leg is red.
//  3. The opening moves carry the value the books open at: every one of them is its
//     own qty at the WAC the books open at, and the ledger's opening values and the
//     books' opening stock are one number. This leg is green today and must stay
//     green: it is the identity round 1 established, and the fix must not move it.
//
// THE SECOND SEED, AND WHICH SECOND SEED. `core seed` is idempotent by a guard of
// its own: the demo GM login (roles_oneshot.go:242) short-circuits a second
// invocation on a database that already carries the seed, so the command writes
// nothing at all. The re-seed the ruling is about is the one that reaches the seed
// payload: the same embedded file the command applies at roles_oneshot.go:255,
// applied to a database that already carries it. The leg drives both, so neither
// reading of "a second seed" is left untested.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     HH_CORE_TEST_DATABASE_URL='postgres://.../integration_core_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W25 -count=1 -v

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// w25LedgerRow is one stock_moves row, read whole: the id and the row's own text, so
// every column is compared and not a chosen few.
type w25LedgerRow struct {
	id  string
	raw string
}

// w25Ledger reads every stock_moves row, every column, the ids included, in id order.
func w25Ledger(t *testing.T, pool *pgxpool.Pool) []w25LedgerRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT t.id::text, row_to_json(t)::text FROM stock_moves t ORDER BY t.id`)
	if err != nil {
		t.Fatalf("reading the stock ledger: %v", err)
	}
	defer rows.Close()
	out := []w25LedgerRow{}
	for rows.Next() {
		var r w25LedgerRow
		if err := rows.Scan(&r.id, &r.raw); err != nil {
			t.Fatalf("reading a ledger row: %v", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("reading the stock ledger: %v", err)
	}
	return out
}

// w25SameLedger fails unless the two readings of the ledger are the same rows: the
// same ids, and every column of each row as it was.
func w25SameLedger(t *testing.T, what string, before, after []w25LedgerRow) {
	t.Helper()
	beforeByID := map[string]string{}
	for _, r := range before {
		beforeByID[r.id] = r.raw
	}
	afterByID := map[string]string{}
	for _, r := range after {
		afterByID[r.id] = r.raw
	}
	gone, fresh, changed := 0, 0, 0
	first := ""
	for id, raw := range beforeByID {
		now, ok := afterByID[id]
		switch {
		case !ok:
			gone++
			if first == "" {
				first = "row " + id + " is gone"
			}
		case now != raw:
			changed++
			if first == "" {
				first = "row " + id + " changed: " + raw
			}
		}
	}
	for id := range afterByID {
		if _, ok := beforeByID[id]; !ok {
			fresh++
			if first == "" {
				first = "row " + id + " is new"
			}
		}
	}
	if gone == 0 && fresh == 0 && changed == 0 {
		return
	}
	t.Fatalf("%s: the stock ledger is not unchanged: %d rows before, %d rows after, "+
		"%d row(s) gone, %d row(s) new, %d row(s) changed; %s",
		what, len(before), len(after), gone, fresh, changed, first)
}

// TestW25TheSeedNeverDeletesALedgerRow is the first red: the seed file itself must
// carry no DELETE against the append-only ledger.
func TestW25TheSeedNeverDeletesALedgerRow(t *testing.T) {
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		if strings.Contains(trimmed, "DELETE") && strings.Contains(trimmed, "stock_moves") {
			t.Fatalf("the seed deletes a ledger row, and stock_moves is append only: %s", trimmed)
		}
	}
}

// TestW25ASecondSeedLeavesEveryLedgerRowExactlyAsItIs is the second red: a second
// seed must leave the ledger exactly as the first one left it.
func TestW25ASecondSeedLeavesEveryLedgerRowExactlyAsItIs(t *testing.T) {
	pool := freshMigratedDB(t, "w25reseed")
	dsn := swapDatabase(os.Getenv("DATABASE_URL"), "demoseed_it_w25reseed")

	// The first seed, through the command's own role.
	t.Setenv("APP_ENV", "dev")
	seedEnv := func(k string) string {
		switch k {
		case "DATABASE_URL", "CORE_DATABASE_URL":
			return dsn
		case "APP_ENV":
			return "dev"
		}
		return ""
	}
	if err := seedRole(seedEnv); err != nil {
		t.Fatalf("the first seed: %v", err)
	}
	before := w25Ledger(t, pool)
	if len(before) == 0 {
		t.Fatal("the first seed wrote no ledger rows, the leg proves nothing")
	}

	// The second seed, the command again: its own demo GM guard short-circuits it, so
	// the command writes nothing at all.
	if err := seedRole(seedEnv); err != nil {
		t.Fatalf("the second seed: %v", err)
	}
	w25SameLedger(t, "the second `core seed`", before, w25Ledger(t, pool))

	// The second seed, the re-seed path: the seed payload on a database that already
	// carries it. This is the re-seed the ruling is about.
	applyDibbitsSeed(t, pool)
	w25SameLedger(t, "the second seed of a database that already carries the seed",
		before, w25Ledger(t, pool))
}

// TestW25TheOpeningMovesCarryTheValueTheBooksOpenAt keeps the identity round 1
// established: every opening move is its own qty at the WAC the books open at, and the
// ledger's opening values and the books' opening stock are one number.
func TestW25TheOpeningMovesCarryTheValueTheBooksOpenAt(t *testing.T) {
	ctx := context.Background()
	pool := freshMigratedDB(t, "w25openval")
	applyDibbitsSeed(t, pool)

	var offWAC int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM stock_moves sm
		  LEFT JOIN product_branch_costs pbc
		         ON pbc.product_id = sm.product_id AND pbc.branch_id = sm.branch_id
		 WHERE sm.reason IN ('M7 opening balance', 'L8 landed-cost demo opening balance')
		   AND sm.value_cents <> ROUND(sm.qty_base * COALESCE(pbc.avg_unit_cost_cents, 0))::bigint`).
		Scan(&offWAC); err != nil {
		t.Fatalf("reading the opening moves: %v", err)
	}
	if offWAC != 0 {
		t.Fatalf("%d opening move(s) do not carry the value the books open at "+
			"(their own qty at the WAC product_branch_costs holds)", offWAC)
	}

	var opening, books int64
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT COALESCE(SUM(sm.value_cents), 0)
		          FROM stock_moves sm
		         WHERE sm.reason IN ('M7 opening balance', 'L8 landed-cost demo opening balance')),
		       (SELECT COALESCE(SUM(ROUND(sb.on_hand_base * COALESCE(pbc.avg_unit_cost_cents, 0))), 0)::bigint
		          FROM stock_balances sb
		          LEFT JOIN product_branch_costs pbc
		                 ON pbc.product_id = sb.product_id AND pbc.branch_id = sb.branch_id)`).
		Scan(&opening, &books); err != nil {
		t.Fatalf("reading the opening figures: %v", err)
	}
	if opening != books {
		t.Fatalf("the ledger's opening values and the books' opening stock are two "+
			"numbers: the ledger holds %d, the books hold %d", opening, books)
	}
	fmt.Printf("the opening moves carry the value the books open at: the ledger's opening "+
		"values and the books' opening stock are both %d\n", opening)
}
