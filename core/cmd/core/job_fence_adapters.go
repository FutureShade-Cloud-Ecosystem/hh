package main

// job_fence_adapters.go, wave 3 JA-2: the job fence adapters.
//
// The job row belongs to the customer module (customer_jobs, migration 0460), and the three
// refusals a new document's job must meet (another customer's job, a CLOSED job, a job outside the
// caller's reach) are decided there. The quote, pos and billing modules each declare the narrow
// seam they need; these adapters are the composition root joining them, the same shape
// customerTypeAdapter and posAccountAdapter carry.

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// customerJobFence adapts the customer module's job read to the error-only fence the money desk's
// credit memo door declares. The service needs its job store bound, the same wire the staff job
// doors take.
type customerJobFence struct{ svc *customer.Service }

func (a customerJobFence) JobForNewDocument(ctx context.Context, customerID, jobID string) error {
	_, err := a.svc.JobForNewDocument(ctx, customerID, jobID)
	return err
}

// quoteJobFence is the same read in the quote module's shape, which carries the caller's
// transaction (QD5): the conversion of the counter's payment door runs inside that door's one
// transaction, and the fence's read rides it through the customer module's own carrier.
type quoteJobFence struct{ svc *customer.Service }

func (a quoteJobFence) JobForNewDocument(ctx context.Context, tx pgx.Tx, customerID, jobID string) error {
	_, err := a.svc.JobForNewDocument(customer.WithCallerTx(ctx, tx), customerID, jobID)
	return err
}

// posJobFence adapts the same read to the counter's shape, which also asks the job's own terms so
// the ring dates its invoice by them (JA-2 item 3).
type posJobFence struct{ svc *customer.Service }

func (a posJobFence) JobForNewDocument(ctx context.Context, customerID, jobID string) (pos.SaleJob, error) {
	job, err := a.svc.JobForNewDocument(ctx, customerID, jobID)
	if err != nil {
		return pos.SaleJob{}, err
	}
	return pos.SaleJob{TermsDays: job.TermsDays}, nil
}

// newCustomerJobFence builds the fence over the pool, with the job store bound.
func newCustomerJobFence(pool *pgxpool.Pool) customerJobFence {
	repo := customer.NewRepository(pool)
	return customerJobFence{svc: customer.NewService(repo).WithJobs(repo)}
}

// newQuoteJobFence is newCustomerJobFence in the quote module's transaction-carrying shape.
func newQuoteJobFence(pool *pgxpool.Pool) quoteJobFence {
	repo := customer.NewRepository(pool)
	return quoteJobFence{svc: customer.NewService(repo).WithJobs(repo)}
}

// newCreditGate is the ONE credit gate every sale door shares (wave 3 JA-3): the hold-aware gate
// reading the customer's whole exposure and a job's own limit from the billing module's single
// exposure reader. The counter, the POS account card and the quote conversion are all built from
// it, so no door decides exposure on its own.
func newCreditGate(pool *pgxpool.Pool, holds till.HoldChecker) *till.HoldAwareCreditGate {
	reader := billing.NewExposureReader(pool, customer.NewRepository(pool))
	return till.NewHoldAwareCreditGate(holds).WithExposure(reader)
}
