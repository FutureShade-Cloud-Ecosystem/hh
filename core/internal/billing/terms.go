package billing

// terms.go, the TERMS seam (wave 3, TM-1).
//
// An invoice is dated by the terms its customer is sold on: the net days the document is due in,
// and the early payment discount's window. The terms live in the customer module's master, so
// billing asks for them through this narrow seam and never reads the master's table.
//
// The seam is the one read the later waves use. TM-2 (the early payment discount at cash
// application) reads the same terms off the document the writer raised, and a job's own terms
// (JA-1) join through the same customer module read, so the terms a document is dated by and the
// terms a payment is discounted by are the same terms.

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// TermsReader is the customer module's terms read: the terms one customer is sold on.
//
// Exact signature for the union adapter:
//
//	TermsFor(ctx context.Context, customerID string) (customer.Terms, error)
type TermsReader interface {
	TermsFor(ctx context.Context, customerID string) (customer.Terms, error)
}

// JobTermsReader is the JOB half of the same seam (JA-2): the terms a document on one job is
// dated by, the job's own when it carries them and the customer's when it does not.
//
// It is a separate, OPTIONAL interface on purpose: the seam's stubs (the terms legs' fixed-answer
// doubles) answer the customer's terms only, and a writer whose facts carry no job never asks the
// question. The production adapter (the customer module's Service) implements both, so a bound
// seam always answers for a job.
//
// Exact signature for the union adapter:
//
//	TermsForJob(ctx context.Context, customerID, jobID string) (customer.Terms, error)
type JobTermsReader interface {
	TermsForJob(ctx context.Context, customerID, jobID string) (customer.Terms, error)
}
