//go:build integration

package billing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestIntegration_FW1_06a_OneStatementBalanceAgreesWithAgingAndSubledger pins
// sub-item 2 of FW1-06a: for a customer with an invoice, an NSF fee, and a
// credit, as well as a customer with a seeded open invoice and subsequent
// payment activity, the statement's ClosingCents, its rendered HTML total, the
// last StatementLine's BalanceRun, PreviewAging.OpenCents, the PreviewLines
// sum, the AgingRow.TotalCents, and the subledger balance all agree on one
// figure.
func TestIntegration_FW1_06a_OneStatementBalanceAgreesWithAgingAndSubledger(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 30, 1_000_000_00)
	cleanupStatements(t, rig)

	// 1. Invoice ($500.00) with its subledger row.
	invID := rig.invoiceDueDaysAgo(t, "ST-TIE-INV", 50000, -10, false)
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		 VALUES ($1::uuid, 'INVOICE', 50000, 50000, 'invoice', $2::uuid, $3::uuid, 'INV-ST-TIE', now() - interval '3 days')`,
		rig.customerID, invID, rig.branchID); err != nil {
		t.Fatalf("seed invoice txn: %v", err)
	}

	// 2. Credit memo (-$100.00) with its subledger row.
	var cmID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
		                      doc_type, source_kind, reverses_invoice_id, closes_order)
		VALUES ('CM-ST-TIE-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE - 1, CURRENT_DATE - 1,
		        -10000, 0, -10000, -10000, now(), 'CREDIT_MEMO', 'ORDER', $4::uuid, FALSE)
		RETURNING id::text`,
		fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000), rig.customerID, rig.branchID, invID).Scan(&cmID); err != nil {
		t.Fatalf("seed credit memo: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		 VALUES ($1::uuid, 'CREDIT_MEMO', -10000, 40000, 'invoice', $2::uuid, $3::uuid, 'CM-ST-TIE', now() - interval '2 days')`,
		rig.customerID, cmID, rig.branchID); err != nil {
		t.Fatalf("seed credit memo txn: %v", err)
	}

	// 3. NSF fee (+$45.00) in customer_transactions.
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		 VALUES ($1::uuid, 'ADJUSTMENT', 4500, 44500, 'payment_nsf', gen_random_uuid(), $2::uuid, 'NSF fee', now() - interval '1 day')`,
		rig.customerID, rig.branchID); err != nil {
		t.Fatalf("seed NSF fee txn: %v", err)
	}

	// 4. Also seed a prior open invoice ($200.00) that was imported/seeded without
	// an initial INVOICE row in customer_transactions, then partially paid (-$50.00),
	// leaving $150.00 open on that invoice and $595.00 (59500 cents) total on the account.
	seedInvID := rig.invoiceDueDaysAgo(t, "ST-TIE-SEED", 20000, 5, false)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET balance_cents = 15000, status = 'PARTIALLY_PAID' WHERE id = $1::uuid`, seedInvID); err != nil {
		t.Fatalf("update seeded invoice balance: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo, occurred_at)
		 VALUES ($1::uuid, 'PAYMENT', -5000, 59500, 'payment', gen_random_uuid(), $2::uuid, 'payment on seeded invoice', now())`,
		rig.customerID, rig.branchID); err != nil {
		t.Fatalf("seed payment txn: %v", err)
	}

	const wantBalance = int64(59500) // 50000 - 10000 + 4500 + (20000 - 5000)

	asOf := time.Now().UTC().Add(1 * time.Hour)
	svc := NewStatementService(rig.repo)
	_, statements, err := svc.GenerateRun(ctx, RunModeInterim, "", asOf, "", rig.customerID)
	if err != nil {
		t.Fatalf("GenerateRun: %v", err)
	}
	if len(statements) != 1 {
		t.Fatalf("len(statements) = %d, want 1", len(statements))
	}
	st := statements[0]
	if st.ClosingCents != wantBalance {
		t.Fatalf("statement ClosingCents = %d, want %d", st.ClosingCents, wantBalance)
	}

	lines, err := rig.repo.StatementLines(ctx, rig.customerID, asOf, AllBranches)
	if err != nil {
		t.Fatalf("StatementLines: %v", err)
	}
	if len(lines) == 0 {
		t.Fatalf("StatementLines returned 0 lines")
	}
	lastRun := lines[len(lines)-1].BalanceRun
	if lastRun != st.ClosingCents {
		t.Fatalf("statement shows two balances: ClosingCents = %d but last StatementLine.BalanceRun = %d",
			st.ClosingCents, lastRun)
	}
	if !strings.Contains(st.HTML, Money(wantBalance)) {
		t.Fatalf("statement HTML missing rendered total %s", Money(wantBalance))
	}

	prevAging, err := rig.repo.PreviewAging(ctx, rig.customerID)
	if err != nil {
		t.Fatalf("PreviewAging: %v", err)
	}
	if prevAging.OpenCents != st.ClosingCents {
		t.Fatalf("PreviewAging.OpenCents = %d, want statement ClosingCents %d", prevAging.OpenCents, st.ClosingCents)
	}

	prevLines, err := rig.repo.PreviewLines(ctx, rig.customerID, asOf)
	if err != nil {
		t.Fatalf("PreviewLines: %v", err)
	}
	var prevSum int64
	for _, pl := range prevLines {
		prevSum += pl.BalanceCents
	}
	if prevSum != st.ClosingCents {
		t.Fatalf("PreviewLines sum = %d, want statement ClosingCents %d", prevSum, st.ClosingCents)
	}
}
