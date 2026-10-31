//go:build integration

package pos

// job_sale_terms_integration_test.go, the job's own terms date the ring's invoice (wave 3 JA-2,
// item 3). The fixture world is job_sale_integration_test.go's.

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// TestIntegration_JobSale_TheJobsOwnTermsDateTheRingsInvoice: an unpaid ACCOUNT ring on a job with
// its own seven days is due seven house days out, and the same ring on a job with no own terms is
// due the customer's thirty.
func TestIntegration_JobSale_TheJobsOwnTermsDateTheRingsInvoice(t *testing.T) {
	pool := intPool(t)
	w := plantJobSaleWorld(t, pool, "")
	ctx := staffCtx(t, pool, "GLOBAL", w.branchID)
	repo := customer.NewRepository(pool)
	svc := realPosSvc(t, pool, stubCharger{}).
		WithJobFence(posJobFenceForTest{svc: customer.NewService(repo).WithJobs(repo)})

	for _, tc := range []struct {
		name   string
		jobID  string
		wantIn int
	}{
		{"the job's own seven days", w.termsJobID, 7},
		{"no own terms, the customer's thirty", w.open, 30},
	} {
		sale, err := svc.Sale(ctx, SaleInput{
			IdempotencyKey: key(), BranchID: w.branchID, CustomerID: w.customer,
			SaleType: SaleCollectLater, Tender: TenderAccount, JobID: tc.jobID,
			Lines: []LineInput{{ProductID: w.productID, QtyEntered: 2, UOMEntered: "SF"}},
		})
		if err != nil {
			t.Fatalf("ring the sale on %s: %v", tc.name, err)
		}
		var days int
		if err := pool.QueryRow(context.Background(),
			`SELECT due_date - issue_date FROM invoices WHERE id = $1::uuid`, sale.InvoiceID).Scan(&days); err != nil {
			t.Fatalf("read the ring's invoice dates: %v", err)
		}
		if days != tc.wantIn {
			t.Fatalf("%s: the invoice is due %d days out, want %d", tc.name, days, tc.wantIn)
		}
	}
}
