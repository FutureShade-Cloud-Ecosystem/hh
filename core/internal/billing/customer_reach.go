package billing

// customer_reach.go (UX-89 round 2): the one customer wall the AR doors that take a customer share. The rule is
// the customer module's own (customer/account_lookup.go): an account homed at a branch is held to callers who
// reach that branch, and an account with no home is shared company wide. The composition root implements the
// seam over the customer service (customer.Service.AccountReachable); billing declares only the narrow question
// it asks and reads no customer table.

import (
	"context"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// CustomerReach answers whether the caller (the stamped staff context on ctx) may see the account. False for an
// account homed outside the caller's branches and for an id that is no account, so the two answer alike.
type CustomerReach interface {
	CustomerReachable(ctx context.Context, customerID string) (bool, error)
}

// FenceCustomer holds one named customer to the caller's reach and writes the one answer when it fails: 404
// {"error":"customer not found"}, the same words for an unreachable account and an unknown one. It reports whether
// the request may go on. A nil reach (the DB free unit tests) lets the request through; the composition root
// refuses to boot without one.
func FenceCustomer(w http.ResponseWriter, r *http.Request, reach CustomerReach, customerID string) bool {
	if reach == nil || customerID == "" {
		return true
	}
	ok, err := reach.CustomerReachable(r.Context(), customerID)
	if err != nil {
		httpx.Error(w, http.StatusServiceUnavailable, "the customer lookup failed")
		return false
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "customer not found")
		return false
	}
	return true
}
