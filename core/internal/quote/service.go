package quote

import (
	"context"
	"errors"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

var (
	// ErrNotFound, quote missing or owned by another account (IDOR-safe 404).
	ErrNotFound = errors.New("quote: not found")
	// ErrInvalidTransition, the quote is not in a state from which this action is
	// legal (e.g. accepting an EXPIRED quote, converting one that is not ACCEPTED).
	// Maps to HTTP 409.
	ErrInvalidTransition = errors.New("quote: invalid state transition")
)

// quoteStore is the persistence the Service needs. Real impl: Repository (pgx); tests
// use a fake. Every operation is tenant-scoped by customerID.
type quoteStore interface {
	ListQuotes(ctx context.Context, customerID string, f Filter) ([]Quote, error)
	GetQuote(ctx context.Context, customerID, quoteID string) (Detail, error)
	Accept(ctx context.Context, customerID, quoteID string) (Quote, error)
	Reject(ctx context.Context, customerID, quoteID string) (Quote, error)
	// Convert is THE conversion (convert.go). It is declared here as part of the portal store so the
	// contractor door type-checks, but the door does not call it directly, runConvert does, for both
	// doors. opt carries the DIB-483 fulfilment choice; its zero value is "unspecified".
	convertStore
}

// Service is the quotes read + state-machine seam the portal BFF composes in-process.
type Service struct {
	store quoteStore
	audit audit.Recorder
	// jobFence is the customer module's job read (JA-2, job_fence.go). The portal door names no
	// job itself; its conversions inherit the quote's own, and the fence holds that job to the
	// open rule at the moment the order is opened. Nil refuses an inherited job in words.
	jobFence JobFence
}

func NewService(store quoteStore) *Service { return &Service{store: store} }

// WithAudit injects the audit Recorder so quote→order conversion can seed the DIB-159
// order-change (status) history with the new order's creation transition. Optional: a nil
// Recorder simply records no history (the conversion itself is unaffected).
func (s *Service) WithAudit(r audit.Recorder) *Service { s.audit = r; return s }

// ListQuotes returns one tenant-scoped page of quotes (cursor envelope; the
// total echo is gone with the offset family, HH-3.2 fold).
func (s *Service) ListQuotes(ctx context.Context, customerID string, f Filter) ([]Quote, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	if f.Status != "" && !validStatus(f.Status) {
		return nil, httpx.BadRequestf("unknown quote status %q", f.Status)
	}
	return s.store.ListQuotes(ctx, customerID, f)
}

// GetQuote returns one quote with its lines, scoped to the tenant.
func (s *Service) GetQuote(ctx context.Context, customerID, quoteID string) (Detail, error) {
	if customerID == "" || quoteID == "" {
		return Detail{}, httpx.BadRequestf("customer id and quote id are required")
	}
	return s.store.GetQuote(ctx, customerID, quoteID)
}

// Accept marks a SENT quote ACCEPTED. The store enforces the legal source state
// atomically and returns ErrInvalidTransition otherwise.
func (s *Service) Accept(ctx context.Context, customerID, quoteID string) (Quote, error) {
	if customerID == "" || quoteID == "" {
		return Quote{}, httpx.BadRequestf("customer id and quote id are required")
	}
	return s.store.Accept(ctx, customerID, quoteID)
}

// Reject marks a SENT/ACCEPTED quote LOST.
func (s *Service) Reject(ctx context.Context, customerID, quoteID string) (Quote, error) {
	if customerID == "" || quoteID == "" {
		return Quote{}, httpx.BadRequestf("customer id and quote id are required")
	}
	return s.store.Reject(ctx, customerID, quoteID)
}

// Convert turns an ACCEPTED quote into an order (handing off to the ERP order write path) and marks
// the quote CONVERTED. DIB-387: the credit/hold gate runs inside the store's convert transaction
// (repository.go), so a customer on an ACTIVE credit hold, or over their credit limit, is refused
// with ErrCreditBlocked and NO order is written. Returns the new order's id/number.
//
// DIB-483: this is the CONTRACTOR door. It establishes whose quote this is (the session IS the
// customer) and then hands off to runConvert (convert.go), the same function the counter door calls.
// There is no conversion logic in this method, deliberately: a credit gate that exists in two places
// is a credit gate that will eventually disagree with itself.
//
// opt carries the optional fulfilment choice (PICKUP / WILL_CALL). Its zero value is UNSPECIFIED, and
// an unspecified conversion writes exactly the row it wrote before DIB-483.
func (s *Service) Convert(ctx context.Context, customerID, quoteID string, opt ConvertOptions) (ConvertResult, error) {
	return runConvert(ctx, s.store, s.audit, s.jobFence, customerID, quoteID, customerID, opt)
}

// validStatus reports whether status is one of the quotes CHECK values (0008).
func validStatus(status string) bool {
	switch status {
	case "DRAFT", "SENT", "ACCEPTED", "CONVERTED", "EXPIRED", "LOST":
		return true
	default:
		return false
	}
}
