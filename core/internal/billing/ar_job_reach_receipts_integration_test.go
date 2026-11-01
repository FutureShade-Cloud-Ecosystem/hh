//go:build integration

// ar_job_reach_receipts_integration_test.go, W3 0.62 fix round 1 (P1-1): a receipt taken at another
// branch is part of the job's account. Its ledger rows sit at the branch the receipt was taken at, and
// the by job read puts the slice that paid the job's invoice on the job, so a caller who cannot reach
// that branch would read a figure cut by it as the job's whole account. The legs drive the real doors
// (the counter receipt, the void and the bounce) and read the job statement through its handler.
package billing

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// receiptReachWorld is one customer with a job homed at Trenton and one 30000 invoice at Trenton on
// it, paid 20000 by a receipt taken at Kingston through the counter receipt door.
type receiptReachWorld struct {
	customer, job, invoice, payment, user string
}

func newReceiptReachWorld(t *testing.T) receiptReachWorld {
	t.Helper()
	pool := intPool(t)
	n := ns(t)
	w := receiptReachWorld{customer: seedCustomer(t, pool, n+"-RR")}
	w.job = jaSeedJob(t, pool, w.customer, "RR-"+n)
	w.invoice = seedInvoice(t, pool, w.customer, n, invSpec{no: "RR-1", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -45).Format("2006-01-02"), total: 30000, balance: 30000, finalized: true})
	jaNameInvoice(t, pool, w.invoice, w.job)
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO users (email, display_name, status) VALUES ($1, 'Receipt Reach Clerk', 'ACTIVE') RETURNING id::text`,
		"rr-"+n+"@test.local").Scan(&w.user); err != nil {
		t.Fatalf("seed the clerk: %v", err)
	}
	// Registered after the customer's and the job's cleanups, so it runs before them: the payment
	// rows and everything that hangs off them go first, each delete failing the test by its table.
	t.Cleanup(func() {
		c := context.Background()
		for _, s := range []struct{ table, q string }{
			{"payments (self references)", `UPDATE payments SET reversed_by_payment_id = NULL, reverses_payment_id = NULL WHERE customer_id = $1::uuid`},
			{"gl_journal_lines", `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source_ref_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid))`},
			{"gl_journals", `DELETE FROM gl_journals WHERE source_ref_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
			{"events_outbox", `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`},
			{"credit_holds", `DELETE FROM credit_holds WHERE customer_id = $1::uuid`},
			{"payment_nsf", `DELETE FROM payment_nsf WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
			{"payment_reapplications", `DELETE FROM payment_reapplications WHERE customer_id = $1::uuid`},
			{"payment_allocations", `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
			{"customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`},
			{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid`},
			{"users", `DELETE FROM users WHERE id = '` + w.user + `'::uuid AND $1::uuid IS NOT NULL`},
		} {
			if _, err := pool.Exec(c, s.q, w.customer); err != nil {
				t.Errorf("receipt reach %s: remove %s: %v", n, s.table, err)
			}
		}
	})
	out, err := payments.NewRepository(pool).RecordReceipt(context.Background(), payments.ReceiptRequest{
		CustomerID: w.customer, Method: "CHEQUE", Reference: "CHQ-RR-" + n, IdempotencyKey: "rr-" + n,
		ReceivedBy: w.user, BranchID: kingstonBranch,
		Allocations: []payments.Allocation{{InvoiceID: w.invoice, AmountCents: 20000}},
	})
	if err != nil {
		t.Fatalf("take the receipt at Kingston through the counter door: %v", err)
	}
	w.payment = out.Payment.ID
	var at string
	if err := pool.QueryRow(context.Background(),
		`SELECT branch_id::text FROM customer_transactions WHERE source_type = 'payment' AND source_ref_id = $1::uuid LIMIT 1`, w.payment).Scan(&at); err != nil || at != kingstonBranch {
		t.Fatalf("the receipt's ledger row must sit at Kingston (the premise of the leg): %q, %v", at, err)
	}
	return w
}

// wantRefusedUnlessWholeReach: a Trenton only manager is refused in words, JSON and HTML, with no
// figure; both branches and global read the true closing figure.
func (w receiptReachWorld) wantRefusedUnlessWholeReach(t *testing.T, step string, closing int64) {
	t.Helper()
	mux := jobReachMux(t)
	base := "/api/v1/ar/customers/" + w.customer + "/jobs/" + w.job + "/statement"
	for _, format := range []string{"", "?format=html"} {
		rec := jobReachGet(mux, reachCtx(trentonBranch), base+format)
		body := rec.Body.String()
		if rec.Code != http.StatusForbidden || !strings.Contains(body, jobReachWords) {
			t.Fatalf("[%s %q] a Trenton only manager: %d %q, want 403 with the words", step, format, rec.Code, body)
		}
	}
	for name, ctx := range map[string]context.Context{"both branches": reachCtx(trentonBranch, kingstonBranch), "global": AllBranchesContext()} {
		rec := jobReachGet(mux, ctx, base)
		var st JobStatement
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &st) != nil || st.ClosingCents != closing {
			t.Fatalf("[%s %s] the true figure: %d %s, want closing %d", step, name, rec.Code, rec.Body.String(), closing)
		}
	}
}

func TestIntegration_W3P062_ReceiptAtAnotherBranchRefusesTheJobStatement(t *testing.T) {
	w := newReceiptReachWorld(t)
	w.wantRefusedUnlessWholeReach(t, "receipt", 10000)
}

func TestIntegration_W3P062_VoidOfAReceiptAtAnotherBranchRefusesTheJobStatement(t *testing.T) {
	w := newReceiptReachWorld(t)
	if _, err := payments.NewVoidService(payments.NewRepository(intPool(t))).Void(context.Background(), payments.VoidInput{
		PaymentID: w.payment, Reason: "wrong job", ActorID: w.user, IsManager: true}); err != nil {
		t.Fatalf("void the receipt through the void door: %v", err)
	}
	w.wantRefusedUnlessWholeReach(t, "void", 30000)
}

func TestIntegration_W3P062_BounceOfAReceiptAtAnotherBranchRefusesTheJobStatement(t *testing.T) {
	w := newReceiptReachWorld(t)
	ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
		UserID: w.user, Tier: staffcontext.TierLeadership, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE"})
	if _, err := till.NewService(till.NewRepository(intPool(t))).MarkNSF(ctx, till.NSFInput{PaymentID: w.payment, Reason: "returned"}); err != nil {
		t.Fatalf("bounce the receipt through the NSF door: %v", err)
	}
	w.wantRefusedUnlessWholeReach(t, "nsf", 30000)
}
