package customer

// account_read.go, the staff read of ONE customer account by id (GET /api/v1/customers/{id}): the
// account facts the create and address writes already answer, with the credit limit and the CURRENT
// BALANCE the Quote Desk needs before it lets a quote go to a customer on credit.
//
// THE BALANCE IS THE PICKER'S BALANCE. It is the latest customer_transactions.balance_after, the
// same running-balance basis SearchAccounts and the POS credit gate read, never a second
// calculation: a figure the counter reads out to a customer is the last place two screens may
// disagree. An account with no ledger rows owes nothing and answers 0, not an absent field.
//
// THE FENCE IS THE LOOKUP'S (account_lookup.go): an account homed to a branch the caller can
// reach, or homed to none, is visible; anything else is NOT FOUND, never forbidden. A read of one
// account returns it or nothing, and "this exists but is not yours" is the fact the fence was
// built to withhold. A global caller reaches every branch. Unlike the picker, this read does not
// hide the walk-in house account or an inactive one: asked for by id, the honest answer is the
// row, with is_active in it.

import (
	"context"

	"github.com/google/uuid"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// accountReadStore is the optional persistence seam for the by-id read, reached by type assertion
// exactly as the lookup's and the scoped search's are, because accountStore is implemented outside
// this package and must not be widened.
type accountReadStore interface {
	GetAccountScoped(ctx context.Context, customerID string, reach AccountReach) (Account, bool, error)
}

// GetAccount answers one account, balance composed in, within the caller's reach. The caller is the
// guard-stamped staff context, never a request field.
func (s *Service) GetAccount(ctx context.Context, customerID string) (Account, error) {
	caller, ok := identity.StaffContextFromContext(ctx)
	if !ok || caller.UserID == "" {
		return Account{}, ErrUnauthenticated
	}
	if _, err := uuid.Parse(customerID); err != nil {
		return Account{}, httpx.BadRequestf("customer id must be a uuid")
	}
	rs, ok := s.store.(accountReadStore)
	if !ok {
		return Account{}, ErrLookupNotConfigured
	}
	reach := AccountReach{Global: caller.Scope == identity.ScopeGlobal}
	if !reach.Global {
		reach.BranchIDs = make([]string, 0, len(caller.Branches))
		for id, granted := range caller.Branches {
			if granted {
				reach.BranchIDs = append(reach.BranchIDs, id)
			}
		}
	}
	acct, found, err := rs.GetAccountScoped(ctx, customerID, reach)
	if err != nil {
		return Account{}, err
	}
	if !found {
		return Account{}, ErrNotFound
	}
	return acct, nil
}
