//go:build integration

// Integration tests for the schema invariants added in migration 0021 (M-2),
// run against the live Postgres 16 that `make reset` provisions.
// Run with: make test-integration.
package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func intPoolRaw(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	pool, err := Connect(context.Background(), url)
	if err != nil {
		if set {
			t.Fatalf("integration DB unavailable: %v", err) // CI configured → fail, not skip
		}
		t.Skipf("integration DB unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func uniqueEntryNo() int { return int(time.Now().UnixNano() % 2_000_000_000) }

// A POSTED journal whose debits ≠ credits must be rejected at COMMIT (deferred
// constraint trigger), double-entry is now enforced in the DB (M-2).
func TestIntegration_GLJournal_UnbalancedRejected(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	var acctDebit, acctCredit, periodID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_accounts WHERE code='1000'`).Scan(&acctDebit); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_accounts WHERE code='4000'`).Scan(&acctCredit); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT id::text FROM fiscal_periods WHERE year=2026 AND month=1`).Scan(&periodID); err != nil {
		t.Fatal(err)
	}

	// Unbalanced: one debit line, no credit → must fail at commit.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var jid string
	if err := tx.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, status)
		VALUES (2026, $1, $2, 'MANUAL', 'POSTED') RETURNING id::text`, uniqueEntryNo(), periodID).Scan(&jid); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents)
		VALUES ($1, 1, $2, 10000)`, jid, acctDebit); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE id=$1`, jid)
		t.Fatal("an unbalanced POSTED journal must be rejected at commit")
	}

	// Balanced: debit 100 / credit 100 → commits cleanly.
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, status)
		VALUES (2026, $1, $2, 'MANUAL', 'POSTED') RETURNING id::text`, uniqueEntryNo()+1, periodID).Scan(&jid); err != nil {
		_ = tx2.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx2.Exec(ctx, `
		INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents) VALUES ($1,1,$2,10000)`, jid, acctDebit); err != nil {
		_ = tx2.Rollback(ctx)
		t.Fatal(err)
	}
	if _, err := tx2.Exec(ctx, `
		INSERT INTO gl_journal_lines (journal_id, line_no, account_id, credit_cents) VALUES ($1,2,$2,10000)`, jid, acctCredit); err != nil {
		_ = tx2.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx2.Commit(ctx); err != nil {
		t.Fatalf("a balanced POSTED journal should commit: %v", err)
	}
	_, _ = pool.Exec(ctx, `DELETE FROM gl_journals WHERE id=$1`, jid)
}

// The seeded world must have ZERO drift between the stock ledger and the balance
// projection (M-2 reconciliation view).
func TestIntegration_StockBalanceDrift_IsZero(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)
	// DIB-396: whole-DB read, wait out any in-flight deliberate-drift fixture (dib396_lease_test.go).
	dib396LeaseExclusive(t, pool, dib396StockDriftLease)
	var drifts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM v_stock_balance_drift`).Scan(&drifts); err != nil {
		t.Fatal(err)
	}
	if drifts != 0 {
		t.Fatalf("stock_balances drifted from the ledger in %d rows; projection is inconsistent", drifts)
	}
}

// The drift view must account for TRANSFER moves on BOTH legs (0022 fix): a transfer
// not reflected in stock_balances must surface drift at the source AND destination.
func TestIntegration_StockBalanceDrift_DetectsTransfer(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)
	// DIB-396: this fixture DELIBERATELY drifts the projection for the length of the test, declare
	// the window so the whole-DB reconciliation gates wait it out instead of reading it as a breach.
	dib396LeaseShared(t, pool, dib396StockDriftLease)

	const trt = "5d1b3f6e-0000-4000-9000-000000000001"
	const kgn = "5d1b3f6e-0000-4000-9000-000000000002"
	var product, uom string
	if err := pool.QueryRow(ctx, `SELECT id::text, base_uom FROM products WHERE sku LIKE 'DBS-%' LIMIT 1`).Scan(&product, &uom); err != nil {
		t.Fatal(err)
	}

	// A TRANSFER move TRT→KGN, WITHOUT updating the balance projection.
	var moveID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO stock_moves (product_id, from_location_id, to_location_id, qty_base, qty_entered, uom_entered, move_type, branch_id, reason)
		VALUES ($1,$2,$3,7,7,$4,'TRANSFER',$2,'drift-test transfer') RETURNING id::text`,
		product, trt, kgn, uom).Scan(&moveID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM stock_moves WHERE id=$1`, moveID) })

	// The view must now report drift for BOTH the source and destination of this product.
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM v_stock_balance_drift WHERE product_id=$1 AND location_id IN ($2,$3)`,
		product, trt, kgn).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("a TRANSFER not in the projection must drift at both legs; got %d drifting locations (the pre-0022 view would miss the source)", n)
	}
}

// -- W5 gate-suite invariants (scaffold-owned; DDL/data assertions only) ---------------------------------

// Every POSTED journal must balance (Σdebit = Σcredit), the double-entry keystone. This asserts it across
// ALL existing POSTED journals as a whole-of-books invariant (complements the 0021 commit-time trigger test).
func TestInvariant_GLJournalsBalance(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)
	var unbalanced int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM (
		  SELECT j.id
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  WHERE j.status = 'POSTED'
		  GROUP BY j.id
		  HAVING SUM(COALESCE(l.debit_cents,0)) <> SUM(COALESCE(l.credit_cents,0))
		) x`).Scan(&unbalanced); err != nil {
		t.Fatal(err)
	}
	if unbalanced != 0 {
		t.Fatalf("%d POSTED journal(s) have Σdebit ≠ Σcredit, double-entry is violated", unbalanced)
	}
}

// No POSTED journal may exist in a CLOSED period with a posted_at AFTER that period's close timestamp, a
// closed period is sealed; nothing may post-date the close into it.
func TestInvariant_NoJournalInClosedPeriodPostdatesClose(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)
	var violations int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM gl_journals j
		JOIN fiscal_periods p ON p.id = j.period_id
		WHERE p.status = 'CLOSED' AND j.status = 'POSTED'
		  AND p.closed_at IS NOT NULL AND j.posted_at > p.closed_at`).Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if violations != 0 {
		t.Fatalf("%d POSTED journal(s) post-date the close of a CLOSED period, a sealed period was written", violations)
	}
}

// Every posting rule must resolve to an ACTIVE gl_accounts row (a boot-time ValidatePostingRules parity),
// AND the 1020 undeposited-funds doctrine (0032) must have landed: payment.received/cash → 1020,
// payment.received/card_clearing → 1010.
func TestInvariant_PostingRulesResolveActiveAccounts(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	var dangling int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM posting_rules pr
		LEFT JOIN gl_accounts a ON a.code = pr.account_code
		WHERE pr.is_active AND (a.id IS NULL OR a.is_active = FALSE)`).Scan(&dangling); err != nil {
		t.Fatal(err)
	}
	if dangling != 0 {
		t.Fatalf("%d active posting rule(s) resolve to a missing or inactive account", dangling)
	}

	// 1020 doctrine: the 0032 UPDATE moved the org-default cash receipt leg 1000 → 1020, and the new
	// card_clearing rule points at 1010.
	assertRuleAccount(t, pool, "payment.received", "cash", "1020")
	assertRuleAccount(t, pool, "payment.received", "card_clearing", "1010")
}

func assertRuleAccount(t *testing.T, pool *pgxpool.Pool, eventType, lineRole, wantCode string) {
	t.Helper()
	var code string
	if err := pool.QueryRow(context.Background(),
		`SELECT account_code::text FROM posting_rules
		 WHERE event_type = $1 AND line_role = $2 AND branch_id IS NULL`, eventType, lineRole).Scan(&code); err != nil {
		t.Fatalf("posting rule %s/%s not found: %v", eventType, lineRole, err)
	}
	if code != wantCode {
		t.Fatalf("posting rule %s/%s = %s, want %s (1020 doctrine)", eventType, lineRole, code, wantCode)
	}
}

// The credit_holds partial unique index must permit at most ONE ACTIVE hold per customer, asserted both
// ways: a second ACTIVE row for the same customer is rejected, but a RELEASED row does not block a new ACTIVE.
func TestInvariant_OneActiveCreditHoldPerCustomer(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	var customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	const marker = "invariant-test-one-active-hold"
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM credit_holds WHERE reason = $1`, marker) })
	// Start clean in case a prior aborted run left rows.
	_, _ = pool.Exec(ctx, `DELETE FROM credit_holds WHERE reason = $1`, marker)

	// First ACTIVE hold, permitted.
	var firstID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO credit_holds (customer_id, status, reason) VALUES ($1, 'ACTIVE', $2) RETURNING id::text`,
		customerID, marker).Scan(&firstID); err != nil {
		t.Fatalf("first ACTIVE hold should be permitted: %v", err)
	}

	// Second ACTIVE hold for the SAME customer, must be rejected by ux_credit_holds_one_active.
	if _, err := pool.Exec(ctx,
		`INSERT INTO credit_holds (customer_id, status, reason) VALUES ($1, 'ACTIVE', $2)`,
		customerID, marker); err == nil {
		t.Fatal("a second ACTIVE credit hold for the same customer must be rejected (partial unique index)")
	}

	// Release the first, then a new ACTIVE hold must be permitted (the index only constrains ACTIVE rows).
	if _, err := pool.Exec(ctx, `UPDATE credit_holds SET status = 'RELEASED' WHERE id = $1`, firstID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO credit_holds (customer_id, status, reason) VALUES ($1, 'ACTIVE', $2)`,
		customerID, marker); err != nil {
		t.Fatalf("a new ACTIVE hold after releasing the prior one must be permitted: %v", err)
	}
}

// -- W6 gate-suite invariants (scaffold-owned; DDL/data assertions only) ----------------------------------

// stock_moves is an APPEND-ONLY ledger (0037): a posted move may be inserted (and, for GC, deleted) but never
// mutated in place, an UPDATE must be rejected by the trigger, so a settled cost/qty cannot be silently
// rewritten under a posted COGS journal (corrections are compensating moves).
func TestInvariant_StockMovesAppendOnly(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	var id string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM stock_moves LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE stock_moves SET reason = 'tamper' WHERE id = $1`, id); err == nil {
		t.Fatal("stock_moves must be append-only, an UPDATE must be rejected (0037 trigger)")
	}
}

// The 0037 negative-stock relax: on_hand_base may go negative (sell-before-receipt detection), but
// allocated_base must stay non-negative (the retained invariant).
func TestInvariant_StockBalancesAllocatedNonNegative(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	var pid, lid string
	if err := pool.QueryRow(ctx, `SELECT product_id::text, location_id::text FROM stock_balances LIMIT 1`).Scan(&pid, &lid); err != nil {
		t.Fatal(err)
	}

	// Negative on-hand is now permitted (the CHECK on_hand_base >= allocated_base was dropped). Rolled back.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `UPDATE stock_balances SET on_hand_base = -5 WHERE product_id = $1 AND location_id = $2`, pid, lid)
	_ = tx.Rollback(ctx)
	if err != nil {
		t.Fatalf("negative on_hand_base must be permitted after the 0037 relax (sell-before-receipt): %v", err)
	}

	// Negative allocated is still forbidden (stock_balances_allocated_nonneg). Rolled back.
	tx2, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx2.Exec(ctx, `UPDATE stock_balances SET allocated_base = -1 WHERE product_id = $1 AND location_id = $2`, pid, lid)
	_ = tx2.Rollback(ctx)
	if err == nil {
		t.Fatal("negative allocated_base must be rejected (stock_balances_allocated_nonneg)")
	}
}

// Every holdback_ledger RELEASE row must carry the GL journal it posted (0038 holdback_release_has_journal):
// a retention released off-ledger is a book that lies. Asserted both ways, the whole-table invariant holds,
// and a RELEASE with a NULL journal is structurally rejected.
func TestInvariant_HoldbackReleaseHasJournal(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	var bad int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM holdback_ledger WHERE entry_type = 'RELEASE' AND gl_journal_id IS NULL`).Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 0 {
		t.Fatalf("%d holdback RELEASE row(s) posted no GL journal, retention released off-ledger", bad)
	}

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var jobID string
	if err := tx.QueryRow(ctx,
		`INSERT INTO lien_jobs (job_no, branch_id) VALUES ($1, $2) RETURNING id::text`,
		fmt.Sprintf("HB-TEST-%d", uniqueEntryNo()), branchID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO holdback_ledger (job_id, branch_id, entry_type, amount_cents, gl_journal_id)
		 VALUES ($1, $2, 'RELEASE', 1000, NULL)`, jobID, branchID); err == nil {
		t.Fatal("a RELEASE holdback_ledger row with no GL journal must be rejected (holdback_release_has_journal)")
	}
}

// tax_decision_snapshot is INSERT-ONLY (0042 trigger): a tax decision is immutable audit truth, a
// return/credit or the M5 recompute writes a NEW reversing snapshot, never an UPDATE.
func TestInvariant_TaxSnapshotsInsertOnly(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	var orderID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM orders LIMIT 1`).Scan(&orderID); err != nil {
		t.Skip("no order to bind a snapshot to")
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sid string
	if err := tx.QueryRow(ctx,
		`INSERT INTO tax_decision_snapshot (order_id, tax_reason_code) VALUES ($1, 'STD_ON_HST_13') RETURNING id::text`,
		orderID).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE tax_decision_snapshot SET rate_bps = 500 WHERE id = $1`, sid); err == nil {
		t.Fatal("tax_decision_snapshot must be insert-only, an UPDATE must be rejected (0042 trigger)")
	}
}

// The GL carries no currency; only the scoped DX-19 PO/landed-cost DOCUMENT tables and the card rail's
// configuration row may carry a currency column. The currency-column scan ALLOWLISTS exactly payment_config
// (the dealer's own currency since item 3b slice 2, checked as a three letter code by 0393; it was a
// constant-CAD CHECK in 0027), purchase_orders, and landed_costs, any other currency column, or a currency
// column on the GL journals, is a violation. The dealer's currency itself lives in system_settings, a key
// and not a column, so no table gained a column for it and this list gained no name.
func TestInvariant_GLStaysCADOnUSDPO(t *testing.T) {
	ctx := context.Background()
	pool := intPoolRaw(t)

	// DIB-421 (W5-C). purchasing_pos + purchasing_bills join the exception list. This is a WIDENING
	// OF THE DOCUMENT LIST, NOT A WEAKENING OF THE RULE: the rule is "the GL and payments are CAD",
	// and the exceptions are documents a vendor denominates in their money and we convert at OUR
	// boundary. 0011's purchase_orders (the receiving document) has carried currency + fx_rate since
	// the beginning and inventory has always converted it at receipt; purchasing_pos and
	// purchasing_bills are the same two documents in the module that now owns them, converted by
	// purchasing's toCAD, which is arithmetically identical to inventory's cadUnit, by test.
	// The teeth are below and unchanged: gl_journals/gl_journal_lines still may carry none.
	allow := map[string]bool{"payment_config": true, "purchase_orders": true, "landed_costs": true,
		"purchasing_pos": true, "purchasing_bills": true}
	rows, err := pool.Query(ctx,
		`SELECT table_name FROM information_schema.columns
		 WHERE column_name = 'currency' AND table_schema = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var tn string
		if err := rows.Scan(&tn); err != nil {
			t.Fatal(err)
		}
		if !allow[tn] {
			t.Fatalf("unexpected currency column on %q, the GL and payments carry no currency column; only PO + landed-cost docs and the card rail row may (DX-19)", tn)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// The USD exception must be fenced to exactly the document tables, and each one must actually
	// HAVE the column. A missing column here is migration 0134 not having run, which would make the
	// loop above pass for the wrong reason (nothing unexpected, because nothing at all).
	for _, tbl := range []string{"purchase_orders", "landed_costs", "purchasing_pos", "purchasing_bills"} {
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = 'currency'`, tbl).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s must carry the DX-19 currency column", tbl)
		}
	}

	// The GL journals themselves never carry a currency column (a journal is in the dealer's own money, never a mix).
	var glCur int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.columns
		 WHERE table_name IN ('gl_journals', 'gl_journal_lines') AND column_name = 'currency'`).Scan(&glCur); err != nil {
		t.Fatal(err)
	}
	if glCur != 0 {
		t.Fatal("gl journals must not carry a currency column, the GL is in one currency, the dealer's")
	}
}
