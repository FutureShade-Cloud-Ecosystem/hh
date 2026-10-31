package lien

// customer_seam.go, the lien module's view of the customer module's job accounts (wave 3 RT-1).
//
// The job row (customer_jobs) belongs to the customer module. Lien declares the narrow facts it needs
// and this adapter answers them from the customer service, so lien never reads that table itself. The
// customer module's refusals are in words as httpx errors; they are turned into lien's own client
// errors here so the lien handler answers them rather than a bare 500.

import (
	"context"
	"errors"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// JobSite is the facts of a customer job the holdback rule needs.
type JobSite struct {
	JobID      string
	CustomerID string
	BranchID   string
	Status     string // OPEN | CLOSED
	Province   string // "" when the job's site names none
}

// CustomerJobs is the consumer-declared seam over the customer module's jobs.
type CustomerJobs interface {
	// JobSite reads a job by id. The read rides a caller's transaction (customer.WithCallerTx).
	JobSite(ctx context.Context, jobID string) (JobSite, error)
	// JobForLink answers whether a job may be linked for a customer: the customer module's own rule
	// for a new document (same customer, open, inside the caller's reach), refused in its words.
	JobForLink(ctx context.Context, customerID, jobID string) (JobSite, error)
}

type customerJobs struct{ svc *customer.Service }

// NewCustomerJobs adapts the customer service to the seam.
func NewCustomerJobs(svc *customer.Service) CustomerJobs { return customerJobs{svc: svc} }

func siteOf(j customer.Job) JobSite {
	return JobSite{JobID: j.JobID, CustomerID: j.CustomerID, BranchID: j.BranchID, Status: j.Status, Province: j.SiteProvince()}
}

func (c customerJobs) JobSite(ctx context.Context, jobID string) (JobSite, error) {
	j, err := c.svc.JobByID(ctx, jobID)
	if err != nil {
		return JobSite{}, translateCustomerErr(err)
	}
	return siteOf(j), nil
}

func (c customerJobs) JobForLink(ctx context.Context, customerID, jobID string) (JobSite, error) {
	j, err := c.svc.JobForNewDocument(ctx, customerID, jobID)
	if err != nil {
		return JobSite{}, translateCustomerErr(err)
	}
	return siteOf(j), nil
}

func translateCustomerErr(err error) error {
	var nf httpx.NotFoundError
	if errors.As(err, &nf) {
		return clientErr(404, "%s", nf.Msg)
	}
	var br httpx.BadRequestError
	if errors.As(err, &br) {
		return unprocessable("%s", br.Msg)
	}
	return err
}
