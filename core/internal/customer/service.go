package customer

import (
	"context"
	"errors"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ErrNotFound is returned when a customer (or contact) does not exist. Exported so the
// portal BFF can map it to a 404 via errors.Is rather than letting it surface as a 500.
var ErrNotFound = errors.New("customer: not found")

// ErrDuplicateAccountNo is returned when a create hits the customers.account_no UNIQUE
// constraint (CITEXT, migration 0006). Exported so the handler maps it to a 409 with a
// clear "account number already in use" message instead of a leaked 500.
var ErrDuplicateAccountNo = errors.New("customer: account number already in use")

// accountStore is the persistence the Service needs. Real impl: Repository (pgx); tests
// use a fake.
type accountStore interface {
	ResolveAccount(ctx context.Context, customerID string) (Account, error)
	ListContacts(ctx context.Context, customerID string) ([]Contact, error)
	GetPrimaryContact(ctx context.Context, customerID string) (Contact, error)
	SearchAccounts(ctx context.Context, q string, limit int) ([]SearchResult, error)
}

// scopedAccountSearcher is the U-park E4 branch-fenced search seam.
//
// It is a SEPARATE, optional interface rather than a widened accountStore on purpose: accountStore is
// implemented outside this package (internal/portal composes the Service with its own fake store),
// and widening it would be a breaking change to a module this swarm does not own. *Repository
// implements both, so the real deployment always takes the fenced path.
//
// The scope is computed in the service from the guard-stamped caller, never taken from the request,
// so passing a branch id cannot itself grant access to that branch.
type scopedAccountSearcher interface {
	SearchAccountsScoped(ctx context.Context, q string, limit int, scope SearchScope) ([]SearchResult, error)
}

// Search limits: the picker requests a small page; the endpoint defaults and caps it so
// a caller can never pull the whole customer table in one request.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

// Service is the customers/contacts read seam the portal BFF composes in-process. It
// validates input and delegates persistence; it holds no cross-module state. The optional
// writer seam (M5 / A-5) adds the staff create/update-address write path with Canadian
// postal/province validation; a nil writer keeps the module read-only (unchanged behavior).
type Service struct {
	store    accountStore
	writer   customerWriter
	country  CountrySource
	position PositionGate
}

// CountrySource answers the dealer's country, the ISO 3166-1 alpha-2 code the address rules follow
// ("CA" or "US"). The composition root hands in an adapter over the settings module's dealer
// configuration; this module reads no settings table itself. A nil source is the Canadian dealer.
type CountrySource interface {
	DealerCountry(ctx context.Context) (string, error)
}

// dealerCountry reads the source; nil answers Canada, a failed read is an error, never a guess.
func dealerCountry(ctx context.Context, src CountrySource) (string, error) {
	if src == nil {
		return "CA", nil
	}
	return src.DealerCountry(ctx)
}

// WithCountry injects the dealer's country source so address validation follows it. Returns the
// Service for chaining.
func (s *Service) WithCountry(c CountrySource) *Service { s.country = c; return s }

func NewService(store accountStore) *Service { return &Service{store: store} }

// WithWriter injects the customer write seam (M5 create/update-address path). Returns the
// Service for chaining. When the real Repository is passed it implements both read + write.
func (s *Service) WithWriter(w customerWriter) *Service { s.writer = w; return s }

// ResolveAccount returns the account facts for one tenant (the customer_id carried on
// the portal JWT). The caller is responsible for passing only the authenticated
// tenant's id, this module does not know about portal sessions.
func (s *Service) ResolveAccount(ctx context.Context, customerID string) (Account, error) {
	if customerID == "" {
		return Account{}, httpx.BadRequestf("customer id is required")
	}
	return s.store.ResolveAccount(ctx, customerID)
}

// ListContacts returns the account's contacts (for the admin-sourcing seed UI).
func (s *Service) ListContacts(ctx context.Context, customerID string) ([]Contact, error) {
	if customerID == "" {
		return nil, httpx.BadRequestf("customer id is required")
	}
	return s.store.ListContacts(ctx, customerID)
}

// SearchAccounts backs the POS customer picker: it finds chargeable accounts by name or
// account_no (the walk-in house account is excluded in the store). An empty query is a
// 400, the picker debounces and only calls with a non-blank term. limit defaults to 20
// and is capped at 50 so no caller can page the whole table.
//
// U-park E4: the search is now BRANCH-FENCED. branchID is the branch the caller is acting for (the
// POS picker has always sent it); the fence itself is derived from the guard-stamped caller in
// resolveSearchScope, so passing a branch id does not grant access to it. The fence is resolved
// BEFORE the empty-term 400, because "you may not search this branch" is true regardless of what was
// typed and answering 400 first would leak that the branch is otherwise searchable.
func (s *Service) SearchAccounts(ctx context.Context, q string, limit int, branchID string) ([]SearchResult, error) {
	scope, err := resolveSearchScope(ctx, branchID)
	if err != nil {
		return nil, err
	}
	if q == "" {
		return nil, httpx.BadRequestf("search term is required")
	}
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	if scoped, ok := s.store.(scopedAccountSearcher); ok {
		return scoped.SearchAccountsScoped(ctx, q, limit, scope)
	}
	// A store that cannot fence in SQL must not be asked to serve a fenced search: SearchResult
	// carries no home branch, so the service has nothing to filter on and "best effort" here means
	// leaking the other branch's accounts. An UNFENCED search (a GLOBAL caller naming no branch) is
	// the one case such a store can answer honestly.
	if scope.Global && scope.BranchID == "" {
		return s.store.SearchAccounts(ctx, q, limit)
	}
	return nil, ErrForbidden
}

// GetPrimaryContact returns the account's is_primary contact, the bootstrap source for
// the first Owner invite. ErrNotFound when the account has no primary contact.
func (s *Service) GetPrimaryContact(ctx context.Context, customerID string) (Contact, error) {
	if customerID == "" {
		return Contact{}, httpx.BadRequestf("customer id is required")
	}
	return s.store.GetPrimaryContact(ctx, customerID)
}
