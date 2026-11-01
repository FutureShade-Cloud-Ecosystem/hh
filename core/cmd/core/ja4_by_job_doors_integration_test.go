//go:build integration

// ja4_by_job_doors_integration_test.go, JA-4 fix round 1: the statement by job and the aging by job
// agree per job after a sequence driven through the REAL payment doors (the receipt across two jobs,
// the void, the reversal, the re-application), now and at a past date, and both tie to
// ARBalanceCents. The billing package cannot import payments, so the legs live here.
//
//	Run: DATABASE_URL=... go test -tags integration ./cmd/core/ -run JA4Doors -count=1 -p 1
package main

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

type ja4World struct {
	pool                *pgxpool.Pool
	cust                string
	jobA, jobB          string
	invA, invB, invAcct string
	pay                 *payments.Repository
	bill                *billing.Repository
	credit              *billing.CreditService
}

func ja4Exec(t *testing.T, pool *pgxpool.Pool, what, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

// ja4Plant seeds one customer with job A (invoice 30000), job B (invoice 20000) and an account level
// invoice (10000), all issued 45 days ago.
func ja4Plant(t *testing.T) *ja4World {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	w := &ja4World{pool: pool, pay: payments.NewRepository(pool), bill: billing.NewRepository(pool)}
	w.credit = billing.NewCreditService(w.bill)
	ctx := context.Background()
	tag := fmt.Sprintf("JA4D-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', 5000000) RETURNING id::text`, tag, "JA-4 doors "+tag).Scan(&w.cust); err != nil {
		t.Fatalf("seed the customer: %v", err)
	}
	t.Cleanup(func() { w.cleanup(t) })
	for _, j := range []struct {
		dst *string
		no  string
	}{{&w.jobA, "A"}, {&w.jobB, "B"}} {
		if err := pool.QueryRow(ctx,
			`INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, contact_name, contact_email)
			 VALUES ($1::uuid, $2::uuid, $3, $4, 'Contact', 'job@example.test') RETURNING id::text`,
			w.cust, trentonBranchID, j.no+"-"+tag, "Job "+j.no).Scan(j.dst); err != nil {
			t.Fatalf("seed job %s: %v", j.no, err)
		}
	}
	mk := func(dst *string, suffix string, total int64, job *string) {
		var jobArg any
		if job != nil {
			jobArg = *job
		}
		if err := pool.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents,
                      total_cents, balance_cents, finalized_at, doc_type, job_id)
VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', current_date - 45, current_date - 15, $4, 0, $4, $4,
        now() - interval '45 days', 'INVOICE', $5::uuid)
RETURNING id::text`, tag+suffix, w.cust, trentonBranchID, total, jobArg).Scan(dst); err != nil {
			t.Fatalf("seed invoice %s: %v", suffix, err)
		}
	}
	mk(&w.invA, "-A", 30000, &w.jobA)
	mk(&w.invB, "-B", 20000, &w.jobB)
	mk(&w.invAcct, "-X", 10000, nil)
	return w
}

func (w *ja4World) cleanup(t *testing.T) {
	c := context.Background()
	for _, q := range []struct{ table, sql string }{
		{"events_outbox", `DELETE FROM events_outbox WHERE payload->>'payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`},
		{"payment_reapplications", `DELETE FROM payment_reapplications WHERE customer_id = $1::uuid`},
		{"payment_allocations", `DELETE FROM payment_allocations WHERE kind = 'REVERSAL' AND payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
		{"payment_allocations", `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
		{"payments", `UPDATE payments SET reversed_by_payment_id = NULL WHERE customer_id = $1::uuid AND reversed_by_payment_id IS NOT NULL`},
		{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid AND reverses_payment_id IS NOT NULL`},
		{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid`},
		{"customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`},
		{"invoice_lines", `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`},
		{"invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`},
		{"customer_jobs", `DELETE FROM customer_jobs WHERE customer_id = $1::uuid`},
		{"customers", `DELETE FROM customers WHERE id = $1::uuid`},
	} {
		if _, err := w.pool.Exec(c, q.sql, w.cust); err != nil {
			t.Errorf("cleanup %s: %v", q.table, err)
		}
	}
}

// receipt takes a receipt through the real door: 15000 to job A's invoice, 5000 to job B's.
func (w *ja4World) receipt(t *testing.T) payments.Payment {
	t.Helper()
	p, err := w.pay.Pay(context.Background(), w.cust, "CHEQUE", "",
		[]payments.Allocation{{InvoiceID: w.invA, AmountCents: 15000}, {InvoiceID: w.invB, AmountCents: 5000}})
	if err != nil {
		t.Fatalf("the receipt door: %v", err)
	}
	return p
}

// bounce takes a receipt back through the real NSF door (till.Service.MarkNSF), dated at `at`.
func (w *ja4World) bounce(t *testing.T, payID string, at time.Time) {
	t.Helper()
	var userID string
	if err := w.pool.QueryRow(context.Background(), `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("pick a seeded user: %v", err)
	}
	ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
		UserID: userID, Tier: staffcontext.TierManager, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE",
	})
	svc := till.NewService(till.NewRepository(w.pool)).WithClock(func() time.Time { return at })
	if _, err := svc.MarkNSF(ctx, till.NSFInput{PaymentID: payID, Reason: "ja4 bounce leg"}); err != nil {
		t.Fatalf("the NSF door: %v", err)
	}
	// A bounce taken in the past by a test clock has its ledger rows' created_at at the real now (the
	// column default); live, both are the bounce's instant. Bring them together so the as of reads see
	// what a live bounce on that day would have written.
	ja4Exec(t, w.pool, "date the bounce's ledger rows", `UPDATE customer_transactions SET created_at = occurred_at WHERE customer_id = $1::uuid AND created_at > occurred_at`, w.cust)
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM credit_holds WHERE source_payment_id = $1::uuid`,
			`DELETE FROM payment_nsf WHERE payment_id = $1::uuid`,
		} {
			if _, err := w.pool.Exec(context.Background(), q, payID); err != nil {
				t.Errorf("cleanup of the bounce: %v", err)
			}
		}
	})
}

// age moves a payment's rows (and the ledger rows that name it) back by d. Only legal before an undo
// exists (the reversal guard refuses to move an application a reversal names).
func (w *ja4World) age(t *testing.T, payID string, d time.Duration) {
	t.Helper()
	secs := d.Seconds()
	ja4Exec(t, w.pool, "age the allocations", `UPDATE payment_allocations SET created_at = created_at - make_interval(secs => $2) WHERE payment_id = $1::uuid`, payID, secs)
	ja4Exec(t, w.pool, "age the payment", `UPDATE payments SET received_at = received_at - make_interval(secs => $2) WHERE id = $1::uuid`, payID, secs)
	ja4Exec(t, w.pool, "age the ledger", `UPDATE customer_transactions SET occurred_at = occurred_at - make_interval(secs => $2), created_at = created_at - make_interval(secs => $2) WHERE source_ref_id = $1::uuid`, payID, secs)
}

// agree reads the statement by job and the aging by job at asOf and fails unless they agree per job
// and both tie to the plain closing balance. It returns the per job aging figures.
func (w *ja4World) agree(t *testing.T, label string, asOf time.Time) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	lines, err := w.bill.StatementLinesByJob(ctx, w.cust, asOf, billing.AllBranches)
	if err != nil {
		t.Fatalf("%s: StatementLinesByJob: %v", label, err)
	}
	stmt := map[string]int64{}
	var stmtSum int64
	for _, ln := range lines {
		stmt[ln.JobID] += ln.ChargeCents - ln.CreditCents
		stmtSum += ln.ChargeCents - ln.CreditCents
	}
	rows, err := w.credit.AgingByJob(ctx, asOf, "", "")
	if err != nil {
		t.Fatalf("%s: AgingByJob: %v", label, err)
	}
	age := map[string]int64{}
	var ageSum int64
	for _, r := range rows {
		if r.CustomerID == w.cust {
			age[r.JobID] += r.TotalCents
			ageSum += r.TotalCents
		}
	}
	for _, k := range []string{w.jobA, w.jobB, ""} {
		if stmt[k] != age[k] {
			t.Errorf("%s: job %q statement %d, aging %d (statement %v, aging %v)", label, k, stmt[k], age[k], stmt, age)
		}
	}
	plain, err := w.bill.StatementLines(ctx, w.cust, asOf, billing.AllBranches)
	if err != nil {
		t.Fatalf("%s: StatementLines: %v", label, err)
	}
	if closing := plain[len(plain)-1].BalanceRun; closing != stmtSum || closing != ageSum {
		t.Errorf("%s: plain closing %d, by job statement %d, by job aging %d", label, closing, stmtSum, ageSum)
	}
	return age
}

func ja4Want(t *testing.T, label string, got map[string]int64, w *ja4World, a, b, acct int64) {
	t.Helper()
	if got[w.jobA] != a || got[w.jobB] != b || got[""] != acct {
		t.Fatalf("%s: A %d, B %d, account %d; want %d, %d, %d", label, got[w.jobA], got[w.jobB], got[""], a, b, acct)
	}
}

// TestIntegration_JA4Doors_ReceiptAcrossTwoJobs: a 20000 receipt, 15000 to job A and 5000 to job B,
// through the real receipt door (one ledger row per allocation).
func TestIntegration_JA4Doors_ReceiptAcrossTwoJobs(t *testing.T) {
	w := ja4Plant(t)
	w.receipt(t)
	got := w.agree(t, "after the receipt", time.Now().Add(time.Hour))
	ja4Want(t, "after the receipt", got, w, 15000, 15000, 10000)
}

// TestIntegration_JA4Doors_Void: the same receipt voided through the void door; the figures are back
// to the invoices and the agreement holds.
func TestIntegration_JA4Doors_Void(t *testing.T) {
	w := ja4Plant(t)
	p := w.receipt(t)
	if _, err := w.pay.VoidPayment(context.Background(), payments.VoidInput{PaymentID: p.ID, Reason: "ja4 void leg", IsManager: true},
		time.Now(), houseday.DefaultZone()); err != nil {
		t.Fatalf("the void door: %v", err)
	}
	got := w.agree(t, "after the void", time.Now().Add(time.Hour))
	ja4Want(t, "after the void", got, w, 30000, 20000, 10000)
}

// TestIntegration_JA4Doors_TheWholeSequence: a split receipt taken ten days ago, its reversal two
// days ago, a re-application of the held money to job A's invoice, then a second receipt voided, then a third receipt bounced: the
// statement by job and the aging by job agree per job now and five days ago (receipt taken, not yet
// reversed), and the grand total ties to ARBalanceCents.
func TestIntegration_JA4Doors_TheWholeSequence(t *testing.T) {
	w := ja4Plant(t)
	ctx := context.Background()
	p := w.receipt(t)
	w.age(t, p.ID, 10*24*time.Hour)
	if _, err := w.pay.ReversePayment(ctx, payments.ReverseInput{PaymentID: p.ID, Reason: "ja4 reversal leg", IsManager: true},
		time.Now().Add(-2*24*time.Hour)); err != nil {
		t.Fatalf("the reversal door: %v", err)
	}
	past := w.agree(t, "five days ago", time.Now().Add(-5*24*time.Hour))
	ja4Want(t, "five days ago", past, w, 15000, 15000, 10000)
	afterRev := w.agree(t, "after the reversal", time.Now().Add(time.Hour))
	ja4Want(t, "after the reversal", afterRev, w, 30000, 20000, 10000)

	if _, err := w.pay.ReapplyHeld(ctx, payments.ReapplyInput{
		PaymentID: p.ID, Reason: "ja4 reapply leg", IdempotencyKey: "ja4-" + w.cust, IsManager: true,
		Allocations: []payments.Allocation{{InvoiceID: w.invA, AmountCents: 10000}},
	}, time.Now()); err != nil {
		t.Fatalf("the reapply door: %v", err)
	}
	reapplied := w.agree(t, "after the reapply", time.Now().Add(time.Hour))
	ja4Want(t, "after the reapply", reapplied, w, 20000, 20000, 10000)
	again := w.agree(t, "five days ago, after the reapply", time.Now().Add(-5*24*time.Hour))
	ja4Want(t, "five days ago, after the reapply", again, w, 15000, 15000, 10000)

	p2 := w.receipt(t)
	if _, err := w.pay.VoidPayment(ctx, payments.VoidInput{PaymentID: p2.ID, Reason: "ja4 void leg", IsManager: true},
		time.Now(), houseday.DefaultZone()); err != nil {
		t.Fatalf("the void door: %v", err)
	}
	asOf := time.Now().Add(time.Hour)
	final := w.agree(t, "after the void", asOf)
	ja4Want(t, "after the void", final, w, 20000, 20000, 10000)

	// A third receipt taken six days ago and bounced yesterday through the NSF door: before the
	// bounce (three days ago) the receipt credits its jobs, after it (now) the jobs return to what they
	// owed, and the statement equals the aging per job at each read.
	p3 := w.receipt(t)
	w.age(t, p3.ID, 6*24*time.Hour)
	w.agree(t, "three days ago, receipt taken, bounce not yet", time.Now().Add(-3*24*time.Hour))
	w.bounce(t, p3.ID, time.Now().Add(-24*time.Hour))
	w.agree(t, "two days ago, before the bounce", time.Now().Add(-2*24*time.Hour))
	bounced := w.agree(t, "after the bounce", asOf)
	ja4Want(t, "after the bounce", bounced, w, 20000, 20000, 10000+nsfFeeCents(t, w))

	all, err := w.credit.AgingByJob(ctx, asOf, "", "")
	if err != nil {
		t.Fatalf("AgingByJob: %v", err)
	}
	var grand int64
	for _, r := range all {
		grand += r.TotalCents
	}
	ar, err := w.bill.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("ARBalanceCents: %v", err)
	}
	if grand != ar {
		t.Fatalf("the by job aging totals %d, ARBalanceCents %d", grand, ar)
	}
}

// TestIntegration_JA4Doors_Bounce: the 15000 and 5000 receipt bounced through the NSF door. The jobs
// return to owing what they did before the receipt (the bounce's NSF row is charged back to the jobs
// by the payment's reversal rows), now; and a receipt aged ten days, bounced two days ago, agrees per
// job five days ago (before the bounce), the day before yesterday's bounce was taken, and now.
func TestIntegration_JA4Doors_Bounce(t *testing.T) {
	w := ja4Plant(t)
	p := w.receipt(t)
	w.bounce(t, p.ID, time.Now())
	got := w.agree(t, "after the bounce", time.Now().Add(time.Hour))
	ja4Want(t, "after the bounce", got, w, 30000, 20000, 10000+nsfFeeCents(t, w))
}

func TestIntegration_JA4Doors_BouncePast(t *testing.T) {
	w := ja4Plant(t)
	p := w.receipt(t)
	w.age(t, p.ID, 10*24*time.Hour)
	w.bounce(t, p.ID, time.Now().Add(-2*24*time.Hour))
	before := w.agree(t, "five days ago, before the bounce", time.Now().Add(-5*24*time.Hour))
	ja4Want(t, "five days ago, before the bounce", before, w, 15000, 15000, 10000)
	after := w.agree(t, "one day ago, after the bounce", time.Now().Add(-24*time.Hour))
	ja4Want(t, "one day ago, after the bounce", after, w, 30000, 20000, 10000+nsfFeeCents(t, w))
	now := w.agree(t, "now, after the bounce", time.Now().Add(time.Hour))
	ja4Want(t, "now, after the bounce", now, w, 30000, 20000, 10000+nsfFeeCents(t, w))
}

// nsfFeeCents is the fee the bounce charged the customer (account level in the aging).
func nsfFeeCents(t *testing.T, w *ja4World) int64 {
	t.Helper()
	var fee int64
	if err := w.pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(fee_cents), 0) FROM payment_nsf WHERE customer_id = $1::uuid`, w.cust).Scan(&fee); err != nil {
		t.Fatalf("read the NSF fee: %v", err)
	}
	return fee
}
