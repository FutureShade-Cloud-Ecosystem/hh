package billing

import (
	"context"
	"errors"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ErrNotFound is returned when an invoice does not exist for the tenant. A cross-tenant
// id is reported as not-found too, never as forbidden, so the BFF cannot leak the
// existence of another account's invoice (IDOR-safe). Exported for errors.Is mapping.
var ErrNotFound = errors.New("billing: not found")

// invoiceStore is the persistence the Service needs. Real impl: Repository (pgx); tests
// use a fake. Every read is tenant-scoped by customerID.
type invoiceStore interface {
	ListInvoices(ctx context.Context, customerID string, f Filter) ([]Invoice, error)
	GetInvoice(ctx context.Context, customerID, invoiceID string) (Invoice, error)
	// OpenBalanceCents sums balance_cents across the tenant's FINALIZED/PARTIALLY_PAID
	// invoices, the account's outstanding AR.
	OpenBalanceCents(ctx context.Context, customerID string) (int64, error)
}

// Service is the invoice/AR read seam the portal BFF composes in-process.
type Service struct {
	store invoiceStore
}

func NewService(store invoiceStore) *Service { return &Service{store: store} }

// ListInvoices returns one tenant-scoped page of invoices (cursor envelope;
// the total echo is gone with the offset family, HH-3.2 fold). Status, when
// set, must be a valid invoice status.
func (s *Service) ListInvoices(ctx context.Context, customerID string, f Filter) ([]Invoice, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	if f.Status != "" && !validStatus(f.Status) {
		return nil, httpx.BadRequestf("unknown invoice status %q", f.Status)
	}
	return s.store.ListInvoices(ctx, customerID, f)
}

// GetInvoice returns one invoice (with lines), scoped to the tenant. ErrNotFound when
// the invoice is missing OR belongs to another account.
func (s *Service) GetInvoice(ctx context.Context, customerID, invoiceID string) (Invoice, error) {
	if customerID == "" || invoiceID == "" {
		return Invoice{}, httpx.BadRequestf("customer id and invoice id are required")
	}
	return s.store.GetInvoice(ctx, customerID, invoiceID)
}

// OpenBalanceCents is the tenant's outstanding AR (the dashboard's balance figure).
func (s *Service) OpenBalanceCents(ctx context.Context, customerID string) (int64, error) {
	if customerID == "" {
		return 0, httpx.BadRequestf("customer id is required")
	}
	return s.store.OpenBalanceCents(ctx, customerID)
}

// AvailableCreditCents is creditLimit − open AR, floored at 0 (an over-limit account
// has zero headroom, never a negative one). The caller passes the limit resolved from
// the customer module, keeping the credit fact's ownership with that module.
func (s *Service) AvailableCreditCents(ctx context.Context, customerID string, creditLimitCents int64) (int64, error) {
	if customerID == "" {
		return 0, httpx.BadRequestf("customer id is required")
	}
	bal, err := s.store.OpenBalanceCents(ctx, customerID)
	if err != nil {
		return 0, err
	}
	avail := creditLimitCents - bal
	if avail < 0 {
		avail = 0
	}
	return avail, nil
}

// validStatus reports whether status is one of the invoices CHECK values (0009).
func validStatus(status string) bool {
	switch status {
	case "DRAFT", "FINALIZED", "PARTIALLY_PAID", "PAID", "VOID":
		return true
	default:
		return false
	}
}
