//go:build integration

package billing

// ar_by_job_fix_integration_test.go, JA-4 fix round 1: the by job aging and the by job statement agree
// per job after the REAL credit memo door and the REAL finance charge run, and the by job aging leaves
// out a customer whose account nets to zero, as the plain aging does.

import (
	"context"
	"testing"
	"time"
)

// jaCrossCheck reads the statement by job and the aging by job for one customer at asOf and fails unless
// they agree per job and the aging by job, summed over every customer, ties to ARBalanceCents.
func jaCrossCheck(t *testing.T, repo *Repository, customerID, label string, asOf time.Time) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	lines, err := repo.StatementLinesByJob(ctx, customerID, asOf, AllBranches)
	if err != nil {
		t.Fatalf("%s: StatementLinesByJob: %v", label, err)
	}
	stmt := map[string]int64{}
	for _, ln := range lines {
		stmt[ln.JobID] += ln.ChargeCents - ln.CreditCents
	}
	rows, err := NewCreditService(repo).AgingByJob(ctx, asOf, "", "")
	if err != nil {
		t.Fatalf("%s: AgingByJob: %v", label, err)
	}
	age := map[string]int64{}
	var grand int64
	for _, r := range rows {
		grand += r.TotalCents
		if r.CustomerID == customerID {
			age[r.JobID] += r.TotalCents
		}
	}
	for k := range stmt {
		if stmt[k] != age[k] {
			t.Errorf("%s: job %q statement %d, aging %d (statement %v, aging %v)", label, k, stmt[k], age[k], stmt, age)
		}
	}
	for k := range age {
		if stmt[k] != age[k] {
			t.Errorf("%s: job %q statement %d, aging %d (statement %v, aging %v)", label, k, stmt[k], age[k], stmt, age)
		}
	}
	ar, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("%s: ARBalanceCents: %v", label, err)
	}
	if grand != ar {
		t.Errorf("%s: the by job aging totals %d, ARBalanceCents %d", label, grand, ar)
	}
	return age
}

// TestIntegration_JA4Fix_RealCreditMemoDoorCreditsItsJob: a credit memo issued through the memo door
// against job A's invoice (no job named, so it inherits the invoice's) lands under job A on both reads.
func TestIntegration_JA4Fix_RealCreditMemoDoorCreditsItsJob(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	e := memoJobPlant(t, pool)
	svc, ctx := memoJobSvc(t, pool, e)
	jobB := jaSeedJob(t, pool, e.customer, "B-"+ns(t))
	invA := memoJobInvoice(t, pool, e.customer, e.open)
	memoJobInvoice(t, pool, e.customer, jobB)
	asOf := time.Now().Add(time.Hour)

	before := jaCrossCheck(t, repo, e.customer, "before the memo", asOf)
	if before[e.open] != 100_000 || before[jobB] != 100_000 {
		t.Fatalf("before the memo: A %d, B %d; want 100000 each", before[e.open], before[jobB])
	}
	if _, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: e.customer, BranchID: trentonBranch, InvoiceID: invA, AmountCents: 40_000,
		Reason: "Part came back", IdempotencyKey: "jafix-" + ns(t),
	}, cmTrentonOnlyCaller()); err != nil {
		t.Fatalf("the credit memo door: %v", err)
	}
	after := jaCrossCheck(t, repo, e.customer, "after the memo", asOf)
	if after[e.open] != 60_000 || after[jobB] != 100_000 || after[""] != 0 {
		t.Fatalf("after the memo: A %d, B %d, account %d; want 60000, 100000, 0", after[e.open], after[jobB], after[""])
	}
}

// TestIntegration_JA4Fix_RealFinanceChargeRunStaysAtAccountLevel: the monthly run raises its charge
// through the invoice writer with no job; it reads at account level on both reads and the jobs are
// untouched.
func TestIntegration_JA4Fix_RealFinanceChargeRunStaysAtAccountLevel(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "JAFIX")
	jobA := jaSeedJob(t, r.pool, cust, "A-"+r.tag)
	inv := ""
	r.tradeInvoice(cust, r.branch, fc1Due31, 1_000_000, "JAFIX")
	if err := r.pool.QueryRow(r.ctx, `SELECT id::text FROM invoices WHERE customer_id = $1::uuid`, cust).Scan(&inv); err != nil {
		t.Fatalf("read the invoice: %v", err)
	}
	jaNameInvoice(t, r.pool, inv, jobA)
	repo := NewRepository(r.pool)
	asOf := time.Now().Add(time.Hour)

	if _, err := r.writer(nil).RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month}); err != nil {
		t.Fatalf("the finance charge run: %v", err)
	}
	got := jaCrossCheck(t, repo, cust, "after the run", asOf)
	if got[jobA] != 1_000_000 || got[""] != 15_000 {
		t.Fatalf("after the run: job A %d, account %d; want 1000000 and the 15000 charge", got[jobA], got[""])
	}
}

// TestIntegration_JA4Fix_AgingByJobOmitsACustomerWhoseAccountNetsToZero: job A owes X and the account
// holds a credit of X, so the plain aging has no row for the customer; neither does the by job aging.
func TestIntegration_JA4Fix_AgingByJobOmitsACustomerWhoseAccountNetsToZero(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewCreditService(repo)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n+"-NZ")
	job := jaSeedJob(t, pool, cust, "NZ-"+n)
	inv := seedInvoice(t, pool, cust, n, invSpec{no: "NZ", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -45).Format("2006-01-02"), total: 10000, balance: 10000, finalized: true})
	jaNameInvoice(t, pool, inv, job)
	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'ADJUSTMENT', -10000, 0, 'manual_credit', gen_random_uuid(), $2::uuid, 'Account credit')`,
		cust, trentonBranch); err != nil {
		t.Fatalf("seed the account credit: %v", err)
	}
	asOf := time.Now().Add(time.Hour)

	plain, err := svc.Aging(ctx, asOf, "")
	if err != nil {
		t.Fatalf("Aging: %v", err)
	}
	for _, r := range plain {
		if r.CustomerID == cust {
			t.Fatalf("the plain aging lists a customer whose account nets to zero: %+v", r)
		}
	}
	byJob, err := svc.AgingByJob(ctx, asOf, "", "")
	if err != nil {
		t.Fatalf("AgingByJob: %v", err)
	}
	for _, r := range byJob {
		if r.CustomerID == cust {
			t.Fatalf("the by job aging lists a customer the plain aging leaves out: %+v", r)
		}
	}
	// Asked for that job by name, the row is still there: the filter is a request for the job.
	only, err := svc.AgingByJob(ctx, asOf, "", job)
	if err != nil {
		t.Fatalf("AgingByJob filtered: %v", err)
	}
	if len(only) != 1 || only[0].TotalCents != 10000 {
		t.Fatalf("filtered by the job: %+v", only)
	}
}
