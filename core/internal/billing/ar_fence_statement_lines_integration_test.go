//go:build integration

// ar_fence_statement_lines_integration_test.go, the security review's P2-1: the statement of a shared account carries the reach's lines only.
package billing

import (
	"context"
	"strings"
	"testing"
)

// P2-1: the statement of a shared account lists the reach's lines only, and a stored statement of a run
// made for another reach is not readable.
func TestARFenceReview_StatementLinesFollowTheReach(t *testing.T) {
	w := newARFenceWorld(t)
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, w.dual)
		_, _ = w.pool.Exec(c, `DELETE FROM print_queue WHERE ref_id IN (SELECT id FROM ar_statements WHERE customer_id = $1::uuid)`, w.dual)
		_, _ = w.pool.Exec(c, `DELETE FROM ar_statement_runs WHERE id IN (SELECT run_id FROM ar_statements WHERE customer_id = $1::uuid)`, w.dual)
	})
	before := d510Now.AddDate(0, 0, -20)
	for _, row := range []struct {
		inv    string
		branch string
		cents  int64
	}{{w.dualTrenton, trentonBranch, 10000}, {w.dualKingston, kingstonBranch, 70000}} {
		if _, err := w.pool.Exec(context.Background(),
			`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, occurred_at)
			 VALUES ($1::uuid, 'INVOICE', $2, $2, 'invoice', $3::uuid, $4::uuid, $5)`,
			w.dual, row.cents, row.inv, row.branch, before); err != nil {
			t.Fatalf("seed subledger: %v", err)
		}
	}
	var kingNo string
	if err := w.pool.QueryRow(context.Background(), `SELECT invoice_no::text FROM invoices WHERE id = $1::uuid`, w.dualKingston).Scan(&kingNo); err != nil {
		t.Fatal(err)
	}
	svc := NewStatementService(NewRepository(w.pool))

	_, sts, err := svc.GenerateRun(arTrentonOnly(), RunModeInterim, "", d510Now, "", w.dual)
	if err != nil || len(sts) != 1 {
		t.Fatalf("interim: %d statements, %v", len(sts), err)
	}
	if strings.Contains(sts[0].HTML, kingNo) || strings.Contains(sts[0].HTML, "700.00") {
		t.Fatalf("a Trenton statement must not list the Kingston invoice %s or its amount", kingNo)
	}
	stored, ok, err := svc.LatestStatement(arTrentonOnly(), w.dual)
	if err != nil || !ok || strings.Contains(stored.HTML, kingNo) {
		t.Fatalf("the stored statement of the caller's own run carries the reach only (ok %v, err %v)", ok, err)
	}

	// A global run later: the account's newest statement is the whole account's, which the branch
	// caller must not read; it keeps reading its own run's.
	if _, _, err := svc.GenerateRun(arGlobalPayRec(), RunModeInterim, "", d510Now.AddDate(0, 0, 1), "", w.dual); err != nil {
		t.Fatalf("global interim: %v", err)
	}
	stored, ok, err = svc.LatestStatement(arTrentonOnly(), w.dual)
	if err != nil || !ok || strings.Contains(stored.HTML, kingNo) || stored.ClosingCents != 10000 {
		t.Fatalf("after a global run the branch caller still reads a statement of its own reach only (ok %v closing %d err %v)", ok, stored.ClosingCents, err)
	}
	if g, ok, err := svc.LatestStatement(arGlobalPayRec(), w.dual); err != nil || !ok || g.ClosingCents != 80000 {
		t.Fatalf("a global caller reads the whole account's newest statement (ok %v closing %d err %v)", ok, g.ClosingCents, err)
	}
}
