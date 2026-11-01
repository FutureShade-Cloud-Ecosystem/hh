//go:build integration

package billing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// jaSeedJob plants one open job for the customer and removes it with the test, loudly.
func jaSeedJob(t *testing.T, pool *pgxpool.Pool, customerID, jobNo string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`INSERT INTO customer_jobs (customer_id, branch_id, job_no, name, contact_name, contact_email)
		 VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6) RETURNING id::text`,
		customerID, trentonBranch, jobNo, "Job "+jobNo, "Contact "+jobNo, jobNo+"@example.test").Scan(&id); err != nil {
		t.Fatalf("seed job %s: %v", jobNo, err)
	}
	t.Cleanup(func() {
		// The customer's cleanup (registered earlier, so it runs later) removes the invoices that
		// name the job first only if the job goes after them; cleanups run last in first out, so the
		// job is removed here BEFORE the invoices, which the foreign key would refuse. The invoices
		// are therefore released from the job first.
		c := context.Background()
		if _, err := pool.Exec(c, `UPDATE invoices SET job_id = NULL WHERE job_id = $1::uuid`, id); err != nil {
			t.Errorf("release the job %s from its invoices: %v", jobNo, err)
		}
		if _, err := pool.Exec(c, `DELETE FROM customer_jobs WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("remove the job %s: %v", jobNo, err)
		}
	})
	return id
}

// jaNameInvoice puts an invoice on a job (the column JA-2 added).
func jaNameInvoice(t *testing.T, pool *pgxpool.Pool, invoiceID, jobID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE invoices SET job_id = $2::uuid WHERE id = $1::uuid`, invoiceID, jobID); err != nil {
		t.Fatalf("name the job on the invoice: %v", err)
	}
}

// jaFixture is one customer with two jobs and an account level balance:
//
//	job A   invoice 30000
//	job B   invoice 20000
//	account invoice 10000 (no job, the shape a finance charge has) and an NSF fee 4500
type jaFixture struct {
	customer     string
	jobA, jobB   string
	invA, invB   string
	invAcct      string
	n            string
	totalCents   int64
	accountCents int64
}

func jaSeed(t *testing.T, pool *pgxpool.Pool, tag ...string) jaFixture {
	t.Helper()
	n := ns(t) + strings.Join(tag, "")
	f := jaFixture{n: n}
	f.customer = seedCustomer(t, pool, n+"-JA")
	f.jobA = jaSeedJob(t, pool, f.customer, "A-"+n)
	f.jobB = jaSeedJob(t, pool, f.customer, "B-"+n)
	issue := time.Now().AddDate(0, 0, -45).Format("2006-01-02")
	f.invA = seedInvoice(t, pool, f.customer, n, invSpec{no: "JA-A", status: "FINALIZED", branch: trentonBranch, issue: issue, total: 30000, balance: 30000, finalized: true})
	f.invB = seedInvoice(t, pool, f.customer, n, invSpec{no: "JA-B", status: "FINALIZED", branch: trentonBranch, issue: issue, total: 20000, balance: 20000, finalized: true})
	f.invAcct = seedInvoice(t, pool, f.customer, n, invSpec{no: "JA-X", status: "FINALIZED", branch: trentonBranch, issue: issue, total: 10000, balance: 10000, finalized: true})
	jaNameInvoice(t, pool, f.invA, f.jobA)
	jaNameInvoice(t, pool, f.invB, f.jobB)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO customer_transactions
		   (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
		 VALUES ($1::uuid, 'ADJUSTMENT', 4500, 64500, 'payment_nsf', gen_random_uuid(), $2::uuid, 'NSF fee')`,
		f.customer, trentonBranch); err != nil {
		t.Fatalf("seed the NSF fee: %v", err)
	}
	f.totalCents = 64500
	f.accountCents = 14500
	return f
}

func jaByJob(rows []AgingJobRow, customerID string) (map[string]AgingJobRow, int64) {
	m := map[string]AgingJobRow{}
	var sum int64
	for _, r := range rows {
		if r.CustomerID != customerID {
			continue
		}
		m[r.JobID] = r
		sum += r.TotalCents
	}
	return m, sum
}

// TestIntegration_JA4_AgingByJob_SubtotalsSumToCustomerAndGL pins JA-4 item 1: the by job
// aging has a row per job and one account level row (JobID ""), they sum to the customer's own
// aging total, and the grand total ties to ARBalanceCents (GL 1100) at the same reading.
func TestIntegration_JA4_AgingByJob_SubtotalsSumToCustomerAndGL(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewCreditService(repo)
	ctx := context.Background()
	f := jaSeed(t, pool)
	asOf := time.Now().UTC().Add(time.Hour)

	byJob, err := svc.AgingByJob(ctx, asOf, "", "")
	if err != nil {
		t.Fatalf("AgingByJob: %v", err)
	}
	mine, sum := jaByJob(byJob, f.customer)
	if sum != f.totalCents {
		t.Fatalf("job subtotals sum to %d, want the customer's %d", sum, f.totalCents)
	}
	if got := mine[f.jobA].TotalCents; got != 30000 {
		t.Fatalf("job A = %d, want 30000", got)
	}
	if got := mine[f.jobB].TotalCents; got != 20000 {
		t.Fatalf("job B = %d, want 20000", got)
	}
	acct, ok := mine[""]
	if !ok || acct.TotalCents != f.accountCents || !acct.AccountLevel {
		t.Fatalf("account level row = %+v (found %v), want %d and AccountLevel", acct, ok, f.accountCents)
	}
	if mine[f.jobA].JobNo == "" || mine[f.jobA].AccountLevel {
		t.Fatalf("a job row carries its number and is not account level: %+v", mine[f.jobA])
	}

	// The customer's own aging total is the sum of its job rows.
	plain, err := svc.Aging(ctx, asOf, "")
	if err != nil {
		t.Fatalf("Aging: %v", err)
	}
	var custTotal int64
	for _, r := range plain {
		if r.CustomerID == f.customer {
			custTotal = r.TotalCents
		}
	}
	if custTotal != sum {
		t.Fatalf("customer aging total %d != by job sum %d", custTotal, sum)
	}

	// The grand totals tie to each other and to ARBalanceCents (GL 1100) at the same reading.
	var grandJob, grandPlain int64
	for _, r := range byJob {
		grandJob += r.TotalCents
	}
	for _, r := range plain {
		grandPlain += r.TotalCents
	}
	ar, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("ARBalanceCents: %v", err)
	}
	if grandJob != grandPlain || grandJob != ar {
		t.Fatalf("grand totals: by job %d, plain %d, ARBalanceCents %d must be equal", grandJob, grandPlain, ar)
	}

	// The job filter returns only that job's row.
	only, err := svc.AgingByJob(ctx, asOf, "", f.jobA)
	if err != nil {
		t.Fatalf("AgingByJob filtered: %v", err)
	}
	if len(only) != 1 || only[0].JobID != f.jobA || only[0].TotalCents != 30000 {
		t.Fatalf("filtered by job A = %+v", only)
	}
}
