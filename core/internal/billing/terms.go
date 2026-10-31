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
