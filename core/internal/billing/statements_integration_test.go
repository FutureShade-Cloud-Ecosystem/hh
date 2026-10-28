//go:build integration

// statements_integration_test.go, DIB-166.
package billing

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// recordingOutbox is the flag-off notification seam, wired ON so the test can prove the enqueue
// happens inside the statement's transaction.
type recordingOutbox struct{ notices []StatementNotice }

func (o *recordingOutbox) EnqueueStatementTx(_ context.Context, _ pgx.Tx, n StatementNotice) error {
	o.notices = append(o.notices, n)
	return nil
}

func cleanupStatements(t *testing.T, rig *creditRig) {
	t.Helper()
	t.Cleanup(func() {
		c := context.Background()
		_, _ = intPool(t).Exec(c, `DELETE FROM print_queue WHERE ref_id IN
			(SELECT id FROM ar_statements WHERE customer_id = $1::uuid)`, rig.customerID)
		_, _ = intPool(t).Exec(c, `DELETE FROM ar_statement_runs WHERE id IN
			(SELECT run_id FROM ar_statements WHERE customer_id = $1::uuid)`, rig.customerID)
		_, _ = intPool(t).Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, rig.customerID)
	})
}

// TestIntegration_Statements_InterimRunIsAStoredArtefact is the central design claim: what we stored
// is what we said, and it does not change when the underlying data does.
func TestIntegration_Statements_InterimRunIsAStoredArtefact(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 30, 1_000_000_00)
	cleanupStatements(t, rig)

	invID := rig.invoiceDueDaysAgo(t, "ST1", 120000, 95, false)
	// A subledger row so the statement has activity to show.
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id)
		 VALUES ($1::uuid, 'INVOICE', 120000, 120000, 'invoice', $2::uuid, $3::uuid)`,
		rig.customerID, invID, rig.branchID); err != nil {
		t.Fatalf("seed subledger: %v", err)
	}

	out := &recordingOutbox{}
	svc := NewStatementService(rig.repo).WithOutbox(out)
	run, statements, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), "", rig.customerID)
	if err != nil {
		t.Fatalf("interim run: %v", err)
	}
	if run.Mode != RunModeInterim || run.Status != "COMPLETED" {
		t.Fatalf("run = %s/%s, want INTERIM/COMPLETED", run.Mode, run.Status)
	}
	if run.GeneratedCount != 1 || run.QueuedCount != 1 {
		t.Fatalf("run counters = %d generated / %d queued, want 1/1", run.GeneratedCount, run.QueuedCount)
	}
	if len(statements) != 1 {
		t.Fatalf("statements = %d, want 1 (an INTERIM run is one account)", len(statements))
	}

	// The frozen aging. 95 days late → entirely in the 60+ bucket.
	st := statements[0]
	if st.Days90PlusCents != 120000 || st.ClosingCents != 120000 {
		t.Fatalf("statement aging = 60+:%d closing:%d, want 120000/120000", st.Days90PlusCents, st.ClosingCents)
	}
	// The DB's own CHECK asserts the buckets sum to the closing balance, so a bucketing bug is caught
	// at write time rather than by a customer at read time. Insert succeeded → that held.

	// -- THE ARTEFACT DOES NOT MOVE -----------------------------------------------------------
	// Change the world underneath it: pay the invoice off entirely.
	if _, err := pool.Exec(ctx, `UPDATE invoices SET balance_cents = 0, status = 'PAID' WHERE id = $1::uuid`, invID); err != nil {
		t.Fatalf("settle the invoice: %v", err)
	}
	stored, ok, err := svc.LatestStatement(ctx, rig.customerID)
	if err != nil || !ok {
		t.Fatalf("read back the stored statement: ok=%v err=%v", ok, err)
	}
	if stored.ClosingCents != 120000 {
		t.Fatalf("the stored statement now says %d, a statement is a claim we made on a date, and "+
			"re-deriving it later rewrites history", stored.ClosingCents)
	}
	if !strings.Contains(stored.HTML, "$1,200.00") && !strings.Contains(stored.HTML, "$1200.00") {
		// Money() renders without thousands separators; assert the exact rendering it produces.
		if !strings.Contains(stored.HTML, Money(120000)) {
			t.Fatalf("the stored HTML no longer shows the balance it was generated with")
		}
	}

	// -- SELF-CONTAINED HTML ------------------------------------------------------------------
	// No external stylesheet, script or image: this document must render identically out of an
	// archive in five years.
	for _, forbidden := range []string{"<link", "<script", "<img", "url("} {
		if strings.Contains(strings.ToLower(stored.HTML), forbidden) {
			t.Fatalf("the statement HTML contains %q, an external reference is a way for a stored artefact "+
				"to stop looking like what we sent", forbidden)
		}
	}
	if !strings.Contains(stored.HTML, "STATEMENT") {
		t.Fatal("the statement does not identify itself as a statement")
	}

	// -- THE PRINT JOB ------------------------------------------------------------------------
	var docType, target, pstatus string
	if err := pool.QueryRow(ctx,
		`SELECT doc_type, render_target, status FROM print_queue WHERE ref_id = $1::uuid`, st.ID).
		Scan(&docType, &target, &pstatus); err != nil {
		t.Fatalf("read the statement print job: %v", err)
	}
	if docType != "STATEMENT" || target != "BROWSER" || pstatus != "QUEUED" {
		t.Fatalf("print job = %s/%s/%s, want STATEMENT/BROWSER/QUEUED", docType, target, pstatus)
	}

	// -- THE NOTICE WENT TO THE SEAM, CARRYING NO ADDRESS -------------------------------------
	if len(out.notices) != 1 {
		t.Fatalf("outbox received %d notice(s), want 1", len(out.notices))
	}
	if out.notices[0].StatementID != st.ID || out.notices[0].BalanceCents != 120000 {
		t.Fatalf("notice = %+v, want it to name the statement and its balance", out.notices[0])
	}
}

// TestIntegration_Statements_ZeroBalanceIsGeneratedButNotQueued pins the distinction the run counters
// exist to make visible.
func TestIntegration_Statements_ZeroBalanceIsGeneratedButNotQueued(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	rig := newCreditRig(t, 30, 100000)
	cleanupStatements(t, rig)

	// A genuine credit balance, the real-world case (an overpayment, or goods returned after
	// billing) and the one nobody wants auto-emailed.
	//
	// -- W6-C: HOW THIS FIXTURE USED TO BE WRITTEN, AND WHY IT CHANGED --
	// It used to seed ONE invoice and UPDATE its balance_cents to −5000. 0146's ck_invoices_doc_sign
	// now refuses that, and the refusal is correct rather than inconvenient: an INVOICE with a
	// negative balance is a credit the customer holds that no document describes. It cannot be
	// printed, it cannot be matched against their own ledger, and it is indistinguishable from a
	// data-entry error.
	//
	// Checking that no PRODUCTION path could still write one was the load-bearing part of accepting
	// the constraint: pos inserts positive totals; payments/repository.go refuses an allocation
	// greater than `payable` under FOR UPDATE; pos's return path clamps with LEAST(old.balance, …);
	// and the two reversal paths ADD to a balance. Nothing in the tree drives an invoice row below
	// zero, so the constraint binds only fixtures like this one.
	//
	// So the account reaches a credit position the way it actually does: an invoice, and a CREDIT
	// MEMO for more than it. That makes this test strictly better than it was, it now also proves
	// the aging and statement path reads a credit memo as the negative AR item it is.
	inv := rig.invoiceDueDaysAgo(t, "CRED", 50000, 10, false)
	if _, err := pool.Exec(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at,
		                      doc_type, source_kind, reverses_invoice_id, closes_order)
		VALUES ('CM-CRED-'||$1, $2::uuid, $3::uuid, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date - 5, (now() AT TIME ZONE 'America/Toronto')::date - 5,
		        -55000, 0, -55000, -55000, now(), 'CREDIT_MEMO', 'ORDER', $4::uuid, FALSE)`,
		fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000), rig.customerID, rig.branchID, inv); err != nil {
		t.Fatalf("seed credit memo: %v", err)
	}

	out := &recordingOutbox{}
	svc := NewStatementService(rig.repo).WithOutbox(out)
	run, statements, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), "", rig.customerID)
	if err != nil {
		t.Fatalf("interim run: %v", err)
	}
	if run.GeneratedCount != 1 {
		t.Fatalf("generated = %d, want 1, every account is generated so the run is auditable", run.GeneratedCount)
	}
	if run.QueuedCount != 0 {
		t.Fatalf("queued = %d, want 0, a credit-balance statement is a conversation the office should "+
			"choose to have, not one that happens automatically", run.QueuedCount)
	}
	if len(statements) != 1 || statements[0].DeliveryStatus != DeliveryNotQueued {
		t.Fatalf("statement delivery = %v, want NOT_QUEUED", statements)
	}
	if len(out.notices) != 0 {
		t.Fatalf("a credit-balance account was queued for send: %+v", out.notices)
	}
}

// TestIntegration_Statements_SendIsFlagOffByDefault is the shipped-default claim: with no outbox
// wired, a run produces statements and sends nothing, while still recording what WOULD go out.
func TestIntegration_Statements_SendIsFlagOffByDefault(t *testing.T) {
	ctx := context.Background()
	rig := newCreditRig(t, 30, 100000)
	cleanupStatements(t, rig)
	rig.invoiceDueDaysAgo(t, "OFF", 75000, 20, false)

	// No WithOutbox, this is what main.go ships.
	svc := NewStatementService(rig.repo)
	run, statements, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), "", rig.customerID)
	if err != nil {
		t.Fatalf("run with no outbox wired: %v", err)
	}
	if run.GeneratedCount != 1 || run.QueuedCount != 1 {
		t.Fatalf("run counters = %d/%d, want 1 generated / 1 queued", run.GeneratedCount, run.QueuedCount)
	}
	if statements[0].DeliveryStatus != DeliveryQueued {
		t.Fatalf("delivery status = %q, want QUEUED, the set that was MEANT to go out must be findable "+
			"so a later wiring can drain exactly it", statements[0].DeliveryStatus)
	}
	// The statement exists and is complete; nothing was delivered because nothing is wired to deliver.
	if statements[0].ClosingCents != 75000 {
		t.Fatalf("closing balance = %d, want 75000", statements[0].ClosingCents)
	}
}

// TestIntegration_Statements_MassRunCoversEveryOpenAccount is the month-end sweep.
func TestIntegration_Statements_MassRunCoversEveryOpenAccount(t *testing.T) {
	ctx := context.Background()
	a := newCreditRig(t, 30, 100000)
	b := newCreditRig(t, 30, 100000)
	cleanupStatements(t, a)
	cleanupStatements(t, b)
	a.invoiceDueDaysAgo(t, "MA", 30000, 5, false)
	b.invoiceDueDaysAgo(t, "MB", 45000, 70, false)

	svc := NewStatementService(a.repo)
	run, statements, err := svc.GenerateRun(ctx, RunModeMass, "", time.Now(), "")
	if err != nil {
		t.Fatalf("mass run: %v", err)
	}
	if run.Mode != RunModeMass {
		t.Fatalf("run mode = %q, want MASS", run.Mode)
	}
	got := map[string]int64{}
	for _, s := range statements {
		got[s.CustomerID] = s.ClosingCents
	}
	if got[a.customerID] != 30000 || got[b.customerID] != 45000 {
		t.Fatalf("a MASS run must cover every account with an open balance: got %v", got)
	}
	// It covers the whole book, so it necessarily includes seeded demo accounts too, the run's own
	// counter is the assertion that matters, not an exact total this test could own.
	if run.GeneratedCount < 2 {
		t.Fatalf("generated = %d, want at least the two accounts this test owns", run.GeneratedCount)
	}
}

// TestIntegration_Statements_InterimRejectsAmbiguousScope, an INTERIM run is for one account, and
// "interim for everybody" is a mass run somebody mislabelled.
func TestIntegration_Statements_InterimRejectsAmbiguousScope(t *testing.T) {
	ctx := context.Background()
	rig := newCreditRig(t, 30, 100000)
	svc := NewStatementService(rig.repo)
	if _, _, err := svc.GenerateRun(ctx, RunModeInterim, "", time.Now(), ""); err == nil {
		t.Fatal("an INTERIM run with no account was accepted")
	}
	if _, _, err := svc.GenerateRun(ctx, "WEEKLY", "", time.Now(), ""); err == nil {
		t.Fatal("an unknown run mode was accepted")
	}
}
