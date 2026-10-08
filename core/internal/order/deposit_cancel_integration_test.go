//go:build integration

// deposit_cancel_integration_test.go, the cancelled special order's supplier
// deposit end to end (row 1193): the manager cancel over the real order
// service, the settlement over the real billing writer, the booking over the
// real gl consumer, both outcomes (an unpaid and a paid deposit), the books
// balanced with 2300 at zero, the AR subledger tied, the override audited,
// and every other money block still holding.
//
//	Run: DATABASE_URL='postgres://.../db?sslmode=disable' go test -tags=integration -count=1 -run DepositCancel ./internal/order/
package order

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/jackc/pgx/v5/pgxpool"
)

// depositCancelWorld wires the whole cancel path the way the composition root
// does: the order service with both cancel guards and both deposit seams, the
// billing writer with the audit recorder, the pos policy default read, and
// the gl consumer registered on a dispatcher over the real outbox.
type depositCancelWorld struct {
	pool  *pgxpool.Pool
	svc   *Service
	h     *Handler
	glSvc *gl.Service
	drain func() int
}

func newDepositCancelWorld(t *testing.T) *depositCancelWorld {
	t.Helper()
	pool := intPool(t)
	billingRepo := billing.NewRepository(pool)
	posRepo := pos.NewRepository(pool)
	auditSvc := audit.NewService(audit.NewRepository(pool), nil)
	svc := NewService(NewRepository(pool)).
		WithAudit(auditSvc).
		WithCancelGuards(billingRepo, posRepo).
		WithDepositCanceller(billing.NewInvoiceWriter(pool).WithAudit(auditSvc)).
		WithDepositCancelDefault(pos.NewService(posRepo))
	glSvc := gl.NewService(gl.NewRepository(pool)).WithInvoiceReader(billingRepo)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	return &depositCancelWorld{
		pool: pool, svc: svc, h: NewHandler(svc), glSvc: glSvc,
		drain: func() int { return dispatcher.DrainOnce(context.Background()) },
	}
}

// seedDepositOrder plants a CONFIRMED order with one live deposit document.
// paid plants the paid shape (PAID, zero balance) the settle path leaves.
func (w *depositCancelWorld) seedDepositOrder(t *testing.T, tag string, paid bool) (cust, ord, dep string) {
	t.Helper()
	ctx := context.Background()
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, payment_terms_days, credit_limit_cents)
		 VALUES ($1, 'Deposit cancel e2e', 'CONTRACTOR', 30, 5000000) RETURNING id::text`,
		fmt.Sprintf("DCE-%s", tag)).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 4000, 4000) RETURNING id::text`,
		fmt.Sprintf("#DCE-%s", tag), cust, trentonBranch).Scan(&ord); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	status, balance := "FINALIZED", 4000
	if paid {
		status, balance = "PAID", 0
	}
	if err := w.pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, source_kind, finalized_at)
		 VALUES ($1, $2, $3, $4, $5, CURRENT_DATE, 4000, 0, 4000, $6, 'SUPPLIER_DEPOSIT', now())
		 RETURNING id::text`,
		fmt.Sprintf("INV-DCE-%s", tag), cust, ord, trentonBranch, status, balance).Scan(&dep); err != nil {
		t.Fatalf("seed deposit document: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM customer_transactions WHERE source_ref_id::text = $1`, dep)
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN
			(SELECT id FROM gl_journals WHERE idempotency_key IN ('deposit.cancelled:'||$1, 'ar.invoice.finalized:'||$1))`, dep)
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key IN ('deposit.cancelled:'||$1, 'ar.invoice.finalized:'||$1)`, dep)
		_, _ = w.pool.Exec(c, `DELETE FROM audit_log WHERE entity_id::text = $1`, dep)
		_, _ = w.pool.Exec(c, `DELETE FROM invoices WHERE id::uuid = $1`, dep)
		_, _ = w.pool.Exec(c, `DELETE FROM orders WHERE id::uuid = $1`, ord)
		_, _ = w.pool.Exec(c, `DELETE FROM customers WHERE id::uuid = $1`, cust)
	})
	return cust, ord, dep
}

// bookTheLiability emits and drains the deposit document's own finalization,
// the journal the cancel's booking clears (DR 1100 / CR 2300, 0373).
func (w *depositCancelWorld) bookTheLiability(t *testing.T, dep string) {
	t.Helper()
	if _, err := w.pool.Exec(context.Background(),
		`INSERT INTO events_outbox (topic, payload) VALUES ('ar.invoice.finalized', $1)`,
		fmt.Sprintf(`{"invoice_id":"%s"}`, dep)); err != nil {
		t.Fatalf("emit the finalization: %v", err)
	}
	if w.drain() == 0 {
		t.Fatal("the finalization did not drain")
	}
}

// journalAccountNet sums one account's legs over ONLY the journals this test
// owns (their idempotency keys). The integration suite shares one database,
// so an account-wide sum would read every other suite's journals posted in
// the same period; the proof is the pair of journals the deposit's own life
// wrote, the finalize and the cancel.
func (w *depositCancelWorld) journalAccountNet(t *testing.T, code, dep string) int64 {
	t.Helper()
	var net int64
	if err := w.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE a.code = $1
		   AND j.idempotency_key IN ('ar.invoice.finalized:'||$2, 'deposit.cancelled:'||$2)`,
		code, dep).Scan(&net); err != nil {
		t.Fatalf("account net %s: %v", code, err)
	}
	return net
}

// THE WHOLE LIFE, UNPAID, CREDITED BACK: the deposit books its liability,
// the manager cancels without the override, the dealer default (credit back)
// clears the open document, and the booking nets 2300 and 1100 to zero for
// it, tied to the subledger.
func TestIntegration_DepositCancel_UnpaidCreditBackEndToEnd(t *testing.T) {
	w := newDepositCancelWorld(t)
	_, ord, dep := w.seedDepositOrder(t, "U", false)
	w.bookTheLiability(t, dep)
	if got := w.journalAccountNet(t, "2300", dep); got != -4000 {
		t.Fatalf("before the cancel 2300 nets %d, want -4000", got)
	}

	rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"customer walked away"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("the cancel of a deposit-only order: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := liveStatus(t, w.pool, ord); got != "CANCELLED" {
		t.Fatalf("status = %q, want CANCELLED", got)
	}
	if w.drain() == 0 {
		t.Fatal("the cancel's event did not drain")
	}

	// The document: cleared and out of the live set.
	var status string
	var balance int64
	if err := w.pool.QueryRow(context.Background(),
		`SELECT status, balance_cents FROM invoices WHERE id::uuid = $1`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "VOID" || balance != 0 {
		t.Fatalf("the credited deposit = %s/%d, want VOID/0", status, balance)
	}

	// The books: 2300 back to zero, 1100 net zero for the pair (the
	// finalize's DR and the credit back's CR), balanced by construction.
	if got := w.journalAccountNet(t, "2300", dep); got != 0 {
		t.Errorf("2300 nets %d, want 0", got)
	}
	if got := w.journalAccountNet(t, "1100", dep); got != 0 {
		t.Errorf("1100 nets %d, want 0 (the credit back cleared the deposit's own AR)", got)
	}

	// The subledger tie at the settlement's grain: the cancel journal's AR
	// leg (a credit of the deposit) is exactly the open balance it cleared.
	var crAR int64
	if err := w.pool.QueryRow(context.Background(), `
		SELECT COALESCE(l.credit_cents,0) FROM gl_journal_lines l
		 JOIN gl_journals j ON j.id = l.journal_id
		 JOIN gl_accounts a ON a.id = l.account_id
		WHERE j.idempotency_key = 'deposit.cancelled:'||$1 AND a.code = '1100'`, dep).Scan(&crAR); err != nil {
		t.Fatalf("the cancel journal's AR leg: %v", err)
	}
	if crAR != 4000 {
		t.Errorf("the cancel journal credited AR %d, want the deposit's 4000", crAR)
	}

	// The audit row: the settlement recorded the disposition and the reason.
	var reason, disposition string
	if err := w.pool.QueryRow(context.Background(), `
		SELECT changes->>'reason', changes->>'disposition' FROM audit_log
		 WHERE action = 'DEPOSIT_CANCELLED' AND entity_id::text = $1`, dep).Scan(&reason, &disposition); err != nil {
		t.Fatalf("the settlement wrote no audited row: %v", err)
	}
	if reason != "customer walked away" || disposition != "CREDIT_BACK" {
		t.Fatalf("the audited row = %q/%q, want the cancel's reason and CREDIT_BACK", reason, disposition)
	}

	// A second cancel writes nothing.
	if rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"again"}`); rr.Code != http.StatusConflict {
		t.Fatalf("the second cancel: code=%d, want 409", rr.Code)
	}
	w.drain()
	var journals int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM gl_journals WHERE idempotency_key = 'deposit.cancelled:'||$1`, dep).Scan(&journals); err != nil {
		t.Fatal(err)
	}
	if journals != 1 {
		t.Fatalf("the cancelled deposit holds %d journals, want the one", journals)
	}
}

// THE WHOLE LIFE, PAID, CREDITED BACK: the paid deposit's credit lands on the
// account (a negative ADJUSTMENT row), the books carry the credit, and the
// subledger ties to the ledger's negative 1100.
func TestIntegration_DepositCancel_PaidCreditBackEndToEnd(t *testing.T) {
	w := newDepositCancelWorld(t)
	cust, ord, dep := w.seedDepositOrder(t, "P", true)
	w.bookTheLiability(t, dep)

	if rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"give it back"}`); rr.Code != http.StatusOK {
		t.Fatalf("the cancel: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if w.drain() == 0 {
		t.Fatal("the cancel's event did not drain")
	}

	// The account carries the credit: one ADJUSTMENT row off the settlement.
	var amt int64
	if err := w.pool.QueryRow(context.Background(), `
		SELECT amount_cents FROM customer_transactions
		 WHERE customer_id::uuid = $1 AND source_type = 'deposit_cancel'`, cust).Scan(&amt); err != nil {
		t.Fatalf("the paid deposit wrote no account credit: %v", err)
	}
	if amt != -4000 {
		t.Fatalf("the account credit = %d, want -4000", amt)
	}
	// The GL: 2300 zero; the cancel journal's AR leg carries the credit the
	// account row names (the fixture plants the paid shape without a payment
	// journal, so the deposit's finalize DR and the cancel CR net 1100 to
	// zero and the credit itself is the pair of the ADJUSTMENT row).
	if got := w.journalAccountNet(t, "2300", dep); got != 0 {
		t.Errorf("2300 nets %d, want 0", got)
	}
	var crAR int64
	if err := w.pool.QueryRow(context.Background(), `
		SELECT COALESCE(l.credit_cents,0) FROM gl_journal_lines l
		 JOIN gl_journals j ON j.id = l.journal_id
		 JOIN gl_accounts a ON a.id = l.account_id
		WHERE j.idempotency_key = 'deposit.cancelled:'||$1 AND a.code = '1100'`, dep).Scan(&crAR); err != nil {
		t.Fatalf("the cancel journal's AR leg: %v", err)
	}
	if crAR != 4000 {
		t.Errorf("the cancel journal credited AR %d, want the deposit's 4000", crAR)
	}
}

// THE MANAGER'S OVERRIDE, KEPT: the deposit stands open and owed, the
// liability books into other income, and the audited row carries the
// override's reason and KEPT.
func TestIntegration_DepositCancel_ManagerKeepsTheDepositEndToEnd(t *testing.T) {
	w := newDepositCancelWorld(t)
	_, ord, dep := w.seedDepositOrder(t, "K", false)
	w.bookTheLiability(t, dep)

	rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"the supplier already ordered it","keep_deposit":true}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("the cancel with the keep override: code=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := liveStatus(t, w.pool, ord); got != "CANCELLED" {
		t.Fatalf("status = %q, want CANCELLED", got)
	}
	if w.drain() == 0 {
		t.Fatal("the cancel's event did not drain")
	}

	// The document stands open: the customer still owes the kept deposit.
	var status string
	var balance int64
	if err := w.pool.QueryRow(context.Background(),
		`SELECT status, balance_cents FROM invoices WHERE id::uuid = $1`, dep).Scan(&status, &balance); err != nil {
		t.Fatal(err)
	}
	if status != "FINALIZED" || balance != 4000 {
		t.Fatalf("the kept deposit = %s/%d, want FINALIZED/4000", status, balance)
	}
	// The books: 2300 zero, the deposit in other income, AR standing.
	if got := w.journalAccountNet(t, "2300", dep); got != 0 {
		t.Errorf("2300 nets %d, want 0", got)
	}
	if got := w.journalAccountNet(t, "4950", dep); got != -4000 {
		t.Errorf("4950 Other Income nets %d, want -4000", got)
	}
	if got := w.journalAccountNet(t, "1100", dep); got != 4000 {
		t.Errorf("1100 nets %d, want 4000 (the kept deposit's standing debt)", got)
	}

	var reason, disposition string
	if err := w.pool.QueryRow(context.Background(), `
		SELECT changes->>'reason', changes->>'disposition' FROM audit_log
		 WHERE action = 'DEPOSIT_CANCELLED' AND entity_id::text = $1`, dep).Scan(&reason, &disposition); err != nil {
		t.Fatalf("the kept settlement wrote no audited row: %v", err)
	}
	if reason != "the supplier already ordered it" || disposition != "KEPT" {
		t.Fatalf("the audited row = %q/%q, want the override's reason and KEPT", reason, disposition)
	}
}

// A NON MANAGER cannot override, and the other money blocks still hold: a
// committed counter sale refuses the cancel with the returns flow's
// sentence, and so does a finalized invoice that is not a deposit document.
func TestIntegration_DepositCancel_WallsHoldEndToEnd(t *testing.T) {
	w := newDepositCancelWorld(t)

	t.Run("a non manager cannot override", func(t *testing.T) {
		_, ord, dep := w.seedDepositOrder(t, t.Name(), false)
		w.bookTheLiability(t, dep)
		rr := invoke(t, w.h.cancel, staffAt(intActor, trentonBranch), ord, `{"reason":"not mine to keep","keep_deposit":true}`)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("a non manager's override: code=%d, want 403", rr.Code)
		}
		if got := liveStatus(t, w.pool, ord); got != "CONFIRMED" {
			t.Fatalf("status = %q, want CONFIRMED", got)
		}
	})

	t.Run("the override's reason is required", func(t *testing.T) {
		_, ord, _ := w.seedDepositOrder(t, t.Name(), false)
		rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"","keep_deposit":true}`)
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("a blank reason with the override: code=%d, want 400", rr.Code)
		}
	})

	t.Run("a committed sale still blocks", func(t *testing.T) {
		_, ord, _ := w.seedDepositOrder(t, t.Name(), false)
		if _, err := w.pool.Exec(context.Background(),
			`INSERT INTO pos_sale (idempotency_key, order_id, branch_id, tender, status)
			 VALUES (gen_random_uuid(), $1, $2, 'CASH', 'COMMITTED')`, ord, trentonBranch); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = w.pool.Exec(context.Background(), `DELETE FROM pos_sale WHERE order_id::uuid = $1`, ord)
		})
		rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"nope"}`)
		if rr.Code != http.StatusConflict || !strings.Contains(rr.Body.String(), "returns flow") {
			t.Fatalf("a committed sale must keep blocking: code=%d body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("a non deposit finalized invoice still blocks", func(t *testing.T) {
		cust, ord, _ := w.seedDepositOrder(t, t.Name(), false)
		var inv string
		if err := w.pool.QueryRow(context.Background(),
			`INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, status, due_date, total_cents, balance_cents)
			 VALUES ($1, $2, $3, $4, 'FINALIZED', CURRENT_DATE, 1000, 1000) RETURNING id::text`,
			fmt.Sprintf("INV-DCW-%d", time.Now().UnixNano()), cust, ord, trentonBranch).Scan(&inv); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_, _ = w.pool.Exec(context.Background(), `DELETE FROM invoices WHERE id::uuid = $1`, inv)
		})
		rr := invoke(t, w.h.cancel, managerAt(intManager, trentonBranch), ord, `{"reason":"nope"}`)
		if rr.Code != http.StatusConflict {
			t.Fatalf("an ordinary finalized invoice must keep blocking: code=%d body=%s", rr.Code, rr.Body.String())
		}
	})
}
