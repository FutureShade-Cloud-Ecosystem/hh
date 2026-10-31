//go:build integration

package pos

// job_sale_integration_test.go, the counter's sale takes a job (wave 3 JA-2).
//
// The till picks a job: the ring's order is opened on it, the ring's invoice carries it, and an
// invoice on a job with its own terms is due by THOSE terms on the house day, the same rule the
// billing writer's documents follow. The fence is the customer module's own read, bound through the
// same seam shape the composition root binds, and the three refusals arrive in words.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/pos -run TestIntegration_JobSale -count=1

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// pgxPoolAlias names the pool the world planner takes.
type pgxPoolAlias = *pgxpool.Pool

// posJobFenceForTest is the customer module's job read in the counter's shape, the composition
// root's adapter written inline for the leg.
type posJobFenceForTest struct{ svc *customer.Service }

func (f posJobFenceForTest) JobForNewDocument(ctx context.Context, customerID, jobID string) (SaleJob, error) {
	job, err := f.svc.JobForNewDocument(ctx, customerID, jobID)
	if err != nil {
		return SaleJob{}, err
	}
	return SaleJob{TermsDays: job.TermsDays}, nil
}

// jobSaleWorld is one credit customer at Trenton with an OPEN job there, a CLOSED job there, an
// OPEN job at Kingston, and a job under another customer.
type jobSaleWorld struct {
	customer   string
	open, far  string
	closed     string
	otherCust  string
	otherJob   string
	productID  string
	branchID   string
	termsJobID string
}

func plantJobSaleWorld(t *testing.T, pool *pgxpool.Pool, callerID string) *jobSaleWorld {
	t.Helper()
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	w := &jobSaleWorld{}
	w.productID, w.branchID = aPaver(t, pool)
	suffix := fmt.Sprintf("js-%d", time.Now().UnixNano()%1_000_000_000)
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Job Sale Yard', 'CONTRACTOR', $2::uuid, 30, 5000000) RETURNING id::text`,
		"JS-"+suffix, w.branchID).Scan(&w.customer); err != nil {
		t.Fatalf("plant the credit customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, payment_terms_days, credit_limit_cents)
		VALUES ($1, 'Job Sale Other Yard', 'CONTRACTOR', $2::uuid, 30, 5000000) RETURNING id::text`,
		"JSO-"+suffix, w.branchID).Scan(&w.otherCust); err != nil {
		t.Fatalf("plant the other customer: %v", err)
	}
	plant := func(customerID, branchID, jobNo string, terms *int) string {
		j, err := repo.InsertJob(ctx, customer.JobWrite{
			CustomerID: customerID, BranchID: branchID, JobNo: jobNo,
			Fields: customer.JobFields{Name: "Driveway", TermsDays: terms},
		})
		if err != nil {
			t.Fatalf("plant the job %s: %v", jobNo, err)
		}
		return j.JobID
	}
	w.open = plant(w.customer, w.branchID, "J-JS-OPEN", nil)
	w.far = plant(w.customer, kingstonBranchID, "J-JS-FAR", nil)
	w.closed = plant(w.customer, w.branchID, "J-JS-CLOSED", nil)
	if _, err := pool.Exec(ctx, `UPDATE customer_jobs SET status = 'CLOSED' WHERE id = $1`, w.closed); err != nil {
		t.Fatalf("close the closed job: %v", err)
	}
	w.otherJob = plant(w.otherCust, w.branchID, "J-JS-OTHER", nil)
	seven := 7
	w.termsJobID = plant(w.customer, w.branchID, "J-JS-TERMS", &seven)
	t.Cleanup(func() {
		c := context.Background()
		for _, id := range []string{w.customer, w.otherCust} {
			_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM pos_sale WHERE customer_id = $1`, id)
			_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, id)
		}
	})
	return w
}

// TestIntegration_JobSale_TheRingTakesAJobAndTheFenceRefusesInWords: the ring's order and its
// invoice carry the job the fence vouched for, and the three refusals are the customer module's
// own words before anything is rung.
func TestIntegration_JobSale_TheRingTakesAJobAndTheFenceRefusesInWords(t *testing.T) {
	pool := intPool(t)
	w := plantJobSaleWorld(t, pool, "")
	ctx := staffCtx(t, pool, "GLOBAL", w.branchID, kingstonBranchID)
	repo := customer.NewRepository(pool)
	svc := realPosSvc(t, pool, stubCharger{}).
		WithJobFence(posJobFenceForTest{svc: customer.NewService(repo).WithJobs(repo)})

	in := SaleInput{
		IdempotencyKey: key(), BranchID: w.branchID, CustomerID: w.customer,
		SaleType: SaleCollectLater, Tender: TenderAccount, JobID: w.open,
		Lines: []LineInput{{ProductID: w.productID, QtyEntered: 2, UOMEntered: "SF"}},
	}
	sale, err := svc.Sale(ctx, in)
	if err != nil {
		t.Fatalf("ring the sale on the job: %v", err)
	}
	q := `SELECT COALESCE(job_id::text, '') FROM %s WHERE id = $1::uuid`
	var orderJob, invoiceJob string
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(q, "orders"), sale.OrderID).Scan(&orderJob); err != nil {
		t.Fatalf("read the ring's order: %v", err)
	}
	if err := pool.QueryRow(context.Background(),
		fmt.Sprintf(q, "invoices"), sale.InvoiceID).Scan(&invoiceJob); err != nil {
		t.Fatalf("read the ring's invoice: %v", err)
	}
	if orderJob != w.open || invoiceJob != w.open {
		t.Fatalf("the order and the invoice carry the job: order %q invoice %q, want %q", orderJob, invoiceJob, w.open)
	}

	for _, tc := range []struct {
		name  string
		jobID string
		words string
	}{
		{"another customer's job", w.otherJob, "another customer's job"},
		{"a closed job", w.closed, "the job is closed"},
		{"a job outside the reach", w.far, "outside your reach"},
	} {
		ctxScoped := staffCtx(t, pool, "BRANCH", w.branchID)
		_, err := svc.Sale(ctxScoped, SaleInput{
			IdempotencyKey: key(), BranchID: w.branchID, CustomerID: w.customer,
			SaleType: SaleCollectLater, Tender: TenderAccount, JobID: tc.jobID,
			Lines: []LineInput{{ProductID: w.productID, QtyEntered: 2, UOMEntered: "SF"}},
		})
		if err == nil || !strings.Contains(err.Error(), tc.words) {
			t.Fatalf("%s refused with %v, want words naming %q", tc.name, err, tc.words)
		}
	}
}
