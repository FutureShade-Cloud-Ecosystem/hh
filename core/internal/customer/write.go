package customer

import (
	"context"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/localization"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// write.go, the customer create/update WRITE path (M5 / A-5). Historically this module was
// read-only (the portal BFF + POS picker only read account facts). M5 needs a server-side
// write path that enforces Canadian postal/province validation on the address, so this adds a
// minimal staff-gated create + address-update surface that runs every billing address through
// internal/localization before persisting. Zero-trust intact: still the sole owner of the
// customers table.

// CreateInput is the staff create-customer payload. billing_address is optional; when
// supplied it is validated + normalized (province → ISO code, postal → "A1A 1A1") before
// insert. account_no + name are required.
//
// PaymentTermsDays is a pointer because absence and zero are different accounts: nil (the
// field left off, how the Counter Till's and Quote Desk's pickers create) means the 30 day
// default, the column's own default and the vendor create's; 0 SENT means Cash On Demand on
// purpose. The two cannot share one int, or a picker's omission reads as COD.
type CreateInput struct {
	AccountNo        string         `json:"account_no"`
	Name             string         `json:"name"`
	CustomerType     string         `json:"customer_type"`
	HomeBranchID     string         `json:"home_branch_id"`
	PaymentTermsDays *int           `json:"payment_terms_days"`
	CreditLimitCents int64          `json:"credit_limit_cents"`
	TaxExempt        bool           `json:"tax_exempt"`
	BillingAddress   map[string]any `json:"billing_address"`
}

// customerWriter is the write persistence the Service needs. Separate from accountStore so
// the read seam stays unchanged; the pgx Repository implements both.
type customerWriter interface {
	InsertCustomer(ctx context.Context, in CreateInput) (Account, error)
	UpdateBillingAddress(ctx context.Context, customerID string, addr map[string]any) (Account, error)
}

// validCustomerType mirrors the customers CHECK set (migration 0006) so a bad type is a 400
// before delegating.
func validCustomerType(t string) bool {
	switch t {
	case "CONTRACTOR", "RETAIL", "BUILDER", "GOVERNMENT", "INTERNAL":
		return true
	default:
		return false
	}
}

// CreateCustomer validates the input (required fields, customer_type, and, the M5 point -
// the billing address in the dealer's country) and inserts the customer. A postal code or
// province of another country, or garbage, is a 400; a valid address is normalized (k8v5p7 → K8V 5P7) on the way
// in, so the stored address is always canonical.
func (s *Service) CreateCustomer(ctx context.Context, in CreateInput) (Account, error) {
	if s.writer == nil {
		return Account{}, httpx.BadRequestf("customer write path not configured")
	}
	if in.AccountNo == "" {
		return Account{}, httpx.BadRequestf("account_no is required")
	}
	if in.Name == "" {
		return Account{}, httpx.BadRequestf("name is required")
	}
	if in.CustomerType == "" {
		in.CustomerType = "CONTRACTOR"
	}
	if !validCustomerType(in.CustomerType) {
		return Account{}, httpx.BadRequestf("invalid customer_type %q", in.CustomerType)
	}
	// defaultTermsDays is the account's terms when the create says nothing: 30, the column's
	// own default and the vendor create's. 0 sent is Cash On Demand and is kept as 0.
	const defaultTermsDays = 30
	if in.PaymentTermsDays == nil {
		terms := defaultTermsDays
		in.PaymentTermsDays = &terms
	} else if *in.PaymentTermsDays < 0 {
		return Account{}, httpx.BadRequestf("payment_terms_days must be >= 0")
	}
	if in.CreditLimitCents < 0 {
		return Account{}, httpx.BadRequestf("credit_limit_cents must be >= 0")
	}
	country, err := dealerCountry(ctx, s.country)
	if err != nil {
		return Account{}, err
	}
	addr, err := localization.NormalizeBillingAddressFor(country, in.BillingAddress)
	if err != nil {
		return Account{}, err // already an httpx.BadRequestError from the validator
	}
	in.BillingAddress = addr
	return s.writer.InsertCustomer(ctx, in)
}

// UpdateBillingAddress validates + normalizes a customer's billing address and persists it.
// Same M5 postal/province enforcement as create. An empty/nil address clears to {} (allowed).
func (s *Service) UpdateBillingAddress(ctx context.Context, customerID string, addr map[string]any) (Account, error) {
	if s.writer == nil {
		return Account{}, httpx.BadRequestf("customer write path not configured")
	}
	if customerID == "" {
		return Account{}, httpx.BadRequestf("customer id is required")
	}
	country, err := dealerCountry(ctx, s.country)
	if err != nil {
		return Account{}, err
	}
	norm, err := localization.NormalizeBillingAddressFor(country, addr)
	if err != nil {
		return Account{}, err
	}
	return s.writer.UpdateBillingAddress(ctx, customerID, norm)
}

// CreditLimitWriter updates a customer's credit limit in cents. Satisfied by *Repository.
type CreditLimitWriter interface {
	UpdateCreditLimit(ctx context.Context, customerID string, newLimitCents int64) (previousCents int64, err error)
}

// UpdateCreditLimit updates a customer's credit_limit_cents and returns the prior limit in cents.
func (s *Service) UpdateCreditLimit(ctx context.Context, customerID string, newLimitCents int64) (int64, error) {
	if customerID == "" {
		return 0, httpx.BadRequestf("customer id is required")
	}
	if newLimitCents < 0 {
		return 0, httpx.BadRequestf("credit limit cannot be negative")
	}
	if cw, ok := s.writer.(CreditLimitWriter); ok && cw != nil {
		return cw.UpdateCreditLimit(ctx, customerID, newLimitCents)
	}
	if cw, ok := s.store.(CreditLimitWriter); ok && cw != nil {
		return cw.UpdateCreditLimit(ctx, customerID, newLimitCents)
	}
	return 0, httpx.BadRequestf("customer credit limit write path not configured")
}
