//go:build integration

package billing

// job_exposure_integration_test.go, wave 3 JA-3 item 1: the one reader of exposure.
//
// A customer's exposure sums every job (its running AR balance plus every open order); a job's own
// exposure is its documents only. The legs plant a customer with two jobs, an invoice on each, and
// an open order on one, and read both figures through the reader.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"os"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

type exposureWorld struct {
	cust, other     string
	jobA, jobB      string
	otherJob        string
	orderOnA, order string
}

func plantExposureWorld(t *testing.T, pool *pgxpool.Pool) *exposureWorld {
	t.Helper()
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	w := &exposureWorld{}
	tag := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	mk := func(acct string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
			VALUES ($1, 'Exposure Yard', 'CONTRACTOR', $2::uuid, 30, 1000000) RETURNING id::text`,
			acct+tag, trentonBranch).Scan(&id); err != nil {
			t.Fatalf("plant the customer: %v", err)
		}
		return id
	}
	w.cust, w.other = mk("EXP-"), mk("EXPO-")
	job := func(cust, no string, limit *int64) string {
		j, err := repo.InsertJob(ctx, customer.JobWrite{CustomerID: cust, BranchID: trentonBranch, JobNo: no + tag,
			Fields: customer.JobFields{Name: "Site", CreditLimitCents: limit}})
		if err != nil {
			t.Fatalf("plant the job %s: %v", no, err)
		}
		return j.JobID
	}
	lim := int64(300000)
	w.jobA, w.jobB, w.otherJob = job(w.cust, "EXP-A-", &lim), job(w.cust, "EXP-B-", nil), job(w.other, "EXP-O-", nil)
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range []string{w.cust, w.other} {
			for _, stmt := range []string{
				`DELETE FROM customer_transactions WHERE customer_id = $1`,
				`DELETE FROM invoices WHERE customer_id = $1`,
				`DELETE FROM orders WHERE customer_id = $1`,
				`DELETE FROM customer_jobs WHERE customer_id = $1`,
				`DELETE FROM customers WHERE id = $1`,
			} {
				if _, err := pool.Exec(c, stmt, id); err != nil {
					t.Errorf("clean up %q: %v", stmt, err)
				}
			}
		}
	})
	return w
}

func plantInvoice(t *testing.T, pool *pgxpool.Pool, cust, job, no string, cents int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, job_id)
		VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', current_date, current_date + 30, $4, 0, $4, $4, now(), $5::uuid)`,
		no, cust, trentonBranch, cents, expNullUUID(job)); err != nil {
		t.Fatalf("plant the invoice %s: %v", no, err)
	}
}

func plantLedger(t *testing.T, pool *pgxpool.Pool, cust string, amount, after int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, occurred_at)
		VALUES ($1::uuid, 'INVOICE', $2, $3, 'invoice', gen_random_uuid(), $4::uuid, clock_timestamp())`,
		cust, amount, after, trentonBranch); err != nil {
		t.Fatalf("plant the ledger row: %v", err)
	}
}

func plantOrder(t *testing.T, pool *pgxpool.Pool, cust, job, no, status string, cents int64) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO orders (order_no, customer_id, branch_id, status, total_cents, job_id, org_id)
		VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6::uuid, (SELECT org_id FROM locations WHERE id = $3::uuid))
		RETURNING id::text`, no, cust, trentonBranch, status, cents, expNullUUID(job)).Scan(&id); err != nil {
		t.Fatalf("plant the order %s: %v", no, err)
	}
	return id
}

func expNullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func TestIntegration_Exposure_TheCustomerSumsEveryJobAndTheJobReadsItsOwn(t *testing.T) {
	pool := intPool(t)
	w := plantExposureWorld(t, pool)
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	rd := NewExposureReader(pool, repo)
	tag := fmt.Sprint(time.Now().UnixNano() % 1_000_000_000)

	plantInvoice(t, pool, w.cust, w.jobA, "EXP-IA-"+tag, 100000)
	plantInvoice(t, pool, w.cust, w.jobB, "EXP-IB-"+tag, 70000)
	plantInvoice(t, pool, w.cust, "", "EXP-IN-"+tag, 5000) // the account's own document, no job
	plantLedger(t, pool, w.cust, 175000, 175000)
	w.orderOnA = plantOrder(t, pool, w.cust, w.jobA, "EXP-OA-"+tag, "CONFIRMED", 20000)
	// not open: cancelled, and one that already has an invoice (counted in AR, never twice)
	plantOrder(t, pool, w.cust, w.jobA, "EXP-OC-"+tag, "CANCELLED", 999)
	invoiced := plantOrder(t, pool, w.cust, w.jobB, "EXP-OI-"+tag, "CONFIRMED", 70000)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid WHERE invoice_no = $2`, invoiced, "EXP-IB-"+tag); err != nil {
		t.Fatalf("link the invoice to its order: %v", err)
	}

	got, err := rd.CustomerExposure(ctx, nil, w.cust)
	if err != nil {
		t.Fatalf("customer exposure: %v", err)
	}
	if got != 175000+20000 {
		t.Fatalf("customer exposure = %d, want the ledger 175000 plus the one open order 20000", got)
	}
	aOwn, aExp, aCust, err := rd.JobPosition(ctx, nil, w.cust, w.jobA)
	if err != nil {
		t.Fatalf("job A position: %v", err)
	}
	if aExp != 100000+20000 || aOwn == nil || *aOwn != 300000 || aCust != 1000000 {
		t.Fatalf("job A position = %v %d %d, want exposure 120000, own limit 300000, customer limit 1000000", aOwn, aExp, aCust)
	}
	bOwn, bExp, _, err := rd.JobPosition(ctx, nil, w.cust, w.jobB)
	if err != nil {
		t.Fatalf("job B position: %v", err)
	}
	if bExp != 70000 || bOwn != nil {
		t.Fatalf("job B position = %v %d, want exposure 70000 and no own limit (it reads the customer's)", bOwn, bExp)
	}
	// a job of another customer is not found, never an empty position
	if _, _, _, err := rd.JobPosition(ctx, nil, w.cust, w.otherJob); err == nil {
		t.Fatal("another customer's job read as a position, want not found")
	}
}

// plantQuote plants a quote row for an order's quote_id (a converted quote's order carries it).
func plantQuote(t *testing.T, pool *pgxpool.Pool, cust, no string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO quotes (quote_no, customer_id, branch_id) VALUES ($1, $2::uuid, $3::uuid) RETURNING id::text`,
		no, cust, trentonBranch).Scan(&id); err != nil {
		t.Fatalf("plant the quote %s: %v", no, err)
	}
	t.Cleanup(func() {
		// the order's quote_id is ON DELETE SET NULL, and the world cleanup removes the order
		if _, err := pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("clean up quotes: %v", err)
		}
	})
	return id
}

// JA-3 fix P1: a progress invoice takes its billed part into AR and leaves the order's unbilled
// remainder in the open order term. A 1,000.00 order with one 300.00 progress invoice reads 1,000.00
// in all, never 300.00.
func TestIntegration_Exposure_AProgressInvoiceLeavesTheUnbilledRemainder(t *testing.T) {
	pool := intPool(t)
	w := plantExposureWorld(t, pool)
	ctx := context.Background()
	rd := NewExposureReader(pool, customer.NewRepository(pool))
	tag := fmt.Sprint(time.Now().UnixNano() % 1_000_000_000)

	order := plantOrder(t, pool, w.cust, w.jobB, "EXP-PO-"+tag, "PARTIALLY_FULFILLED", 100000)
	plantInvoice(t, pool, w.cust, w.jobB, "EXP-PI-"+tag, 30000)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid, closes_order = FALSE WHERE invoice_no = $2`, order, "EXP-PI-"+tag); err != nil {
		t.Fatalf("link the progress invoice: %v", err)
	}
	plantLedger(t, pool, w.cust, 30000, 30000)

	got, err := rd.CustomerExposure(ctx, nil, w.cust)
	if err != nil {
		t.Fatalf("customer exposure: %v", err)
	}
	if got != 100000 {
		t.Fatalf("customer exposure = %d, want 100000 (30000 billed in AR plus 70000 unbilled)", got)
	}
	_, jobExp, _, err := rd.JobPosition(ctx, nil, w.cust, w.jobB)
	if err != nil {
		t.Fatalf("job position: %v", err)
	}
	if jobExp != 100000 {
		t.Fatalf("job exposure = %d, want 100000", jobExp)
	}
	// a void invoice does not bill anything, a credit memo is not a bill of the order, and a
	// remainder never goes below zero (an invoice larger than the order)
	plantInvoice(t, pool, w.cust, w.jobB, "EXP-PV-"+tag, 500)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid, status = 'VOID' WHERE invoice_no = $2`, order, "EXP-PV-"+tag); err != nil {
		t.Fatalf("void invoice: %v", err)
	}
	if got, _ := rd.CustomerExposure(ctx, nil, w.cust); got != 100000 {
		t.Fatalf("with a void invoice, customer exposure = %d, want 100000", got)
	}
	// a credit memo naming the order (a negative total) is not a bill of it either
	plantInvoice(t, pool, w.cust, w.jobB, "EXP-PC-"+tag, 500)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid, doc_type = 'CREDIT_MEMO', total_cents = -5000, subtotal_cents = -5000, balance_cents = 0 WHERE invoice_no = $2`, order, "EXP-PC-"+tag); err != nil {
		t.Fatalf("credit memo: %v", err)
	}
	if got, _ := rd.CustomerExposure(ctx, nil, w.cust); got != 100000 {
		t.Fatalf("with a credit memo, customer exposure = %d, want 100000", got)
	}
	over := plantOrder(t, pool, w.cust, w.jobA, "EXP-PX-"+tag, "CONFIRMED", 1000)
	plantInvoice(t, pool, w.cust, w.jobA, "EXP-PXI-"+tag, 4000)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid, closes_order = FALSE WHERE invoice_no = $2`, over, "EXP-PXI-"+tag); err != nil {
		t.Fatalf("link the over invoice: %v", err)
	}
	if got, _ := rd.CustomerExposure(ctx, nil, w.cust); got != 100000 {
		t.Fatalf("with an over billed order, customer exposure = %d, want 100000 (floored at zero)", got)
	}
}

// JA-3 fix r2 P3-a: an order whose closing invoice bills less than the order stops counting once
// that closing invoice exists, even while the order has not reached INVOICED.
func TestIntegration_Exposure_AShortClosingInvoiceLeavesNoPhantomRemainder(t *testing.T) {
	pool := intPool(t)
	w := plantExposureWorld(t, pool)
	ctx := context.Background()
	rd := NewExposureReader(pool, customer.NewRepository(pool))
	tag := fmt.Sprint(time.Now().UnixNano() % 1_000_000_000)

	order := plantOrder(t, pool, w.cust, w.jobB, "EXP-CL-"+tag, "CONFIRMED", 10000)
	if got, _ := rd.CustomerExposure(ctx, nil, w.cust); got != 10000 {
		t.Fatalf("an open order alone: customer exposure = %d, want 10000", got)
	}
	plantInvoice(t, pool, w.cust, w.jobB, "EXP-CLI-"+tag, 9800)
	if _, err := pool.Exec(ctx, `UPDATE invoices SET order_id = $1::uuid, closes_order = TRUE WHERE invoice_no = $2`, order, "EXP-CLI-"+tag); err != nil {
		t.Fatalf("link the closing invoice: %v", err)
	}
	plantLedger(t, pool, w.cust, 9800, 9800)
	if got, _ := rd.CustomerExposure(ctx, nil, w.cust); got != 9800 {
		t.Fatalf("with a short closing invoice, customer exposure = %d, want 9800 (AR only, no 200 remainder)", got)
	}
	if _, jobExp, _, _ := rd.JobPosition(ctx, nil, w.cust, w.jobB); jobExp != 9800 {
		t.Fatalf("with a short closing invoice, job exposure = %d, want 9800", jobExp)
	}
}

// JA-3 fix P2: an abandoned reorder or duplicate DRAFT does not count; a converted quote's DRAFT does.
func TestIntegration_Exposure_OnlyAConvertedQuotesDraftCounts(t *testing.T) {
	pool := intPool(t)
	w := plantExposureWorld(t, pool)
	ctx := context.Background()
	rd := NewExposureReader(pool, customer.NewRepository(pool))
	tag := fmt.Sprint(time.Now().UnixNano() % 1_000_000_000)

	plantOrder(t, pool, w.cust, w.jobB, "EXP-DR-"+tag, "DRAFT", 900000)
	got, err := rd.CustomerExposure(ctx, nil, w.cust)
	if err != nil {
		t.Fatalf("customer exposure: %v", err)
	}
	if got != 0 {
		t.Fatalf("an abandoned DRAFT counted: customer exposure = %d, want 0", got)
	}
	if _, jobExp, _, err := rd.JobPosition(ctx, nil, w.cust, w.jobB); err != nil || jobExp != 0 {
		t.Fatalf("an abandoned DRAFT counted on the job: exposure %d err %v, want 0", jobExp, err)
	}
	converted := plantOrder(t, pool, w.cust, w.jobB, "EXP-DQ-"+tag, "DRAFT", 40000)
	q := plantQuote(t, pool, w.cust, "EXP-Q-"+tag)
	if _, err := pool.Exec(ctx, `UPDATE orders SET quote_id = $1::uuid WHERE id = $2::uuid`, q, converted); err != nil {
		t.Fatalf("link the converted quote: %v", err)
	}
	if got, _ := rd.CustomerExposure(ctx, nil, w.cust); got != 40000 {
		t.Fatalf("a converted quote's DRAFT: customer exposure = %d, want 40000", got)
	}
	if _, jobExp, _, _ := rd.JobPosition(ctx, nil, w.cust, w.jobB); jobExp != 40000 {
		t.Fatalf("a converted quote's DRAFT: job exposure = %d, want 40000", jobExp)
	}
}

// JA-3 fix P2: the job limit is read on the caller's open transaction. A pool of ONE connection with
// the transaction open must answer, not wait for a second connection until its deadline.
func TestIntegration_Exposure_TheJobLimitReadJoinsTheCallersTransaction(t *testing.T) {
	pool := intPool(t)
	w := plantExposureWorld(t, pool)
	cfg, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		t.Skipf("no DATABASE_URL to build a one connection pool: %v", err)
	}
	cfg.MaxConns = 1
	one, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("one connection pool: %v", err)
	}
	t.Cleanup(one.Close)
	rd := NewExposureReader(one, customer.NewRepository(one))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := one.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := rd.CustomerExposure(ctx, tx, w.cust); err != nil {
		t.Fatalf("customer exposure on the tx: %v", err)
	}
	own, _, cl, err := rd.JobPosition(ctx, tx, w.cust, w.jobA)
	if err != nil {
		t.Fatalf("job position on the tx waited for a second connection: %v", err)
	}
	if own == nil || *own != 300000 || cl != 1000000 {
		t.Fatalf("job position limits = %v %d, want own 300000 and customer 1000000", own, cl)
	}
}
