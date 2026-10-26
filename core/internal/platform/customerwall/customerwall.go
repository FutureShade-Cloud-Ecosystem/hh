// Package customerwall is the one customer wall every staff door that takes a customer shares (UX-89).
//
// The rule is the customer module's own (customer/account_lookup.go): an account homed at a branch is held to
// callers who reach that branch, and an account with no home is shared company wide. The composition root
// implements Reach once, over the customer read (billing.CustomerModuleReach); a module that takes a customer
// declares nothing of its own, holds a Reach, and calls Fence. An account the caller cannot reach answers
// exactly as an id that is no account, 404 "customer not found" (through httpx.Error), and a lookup that fails is a 503,
// never an admission.
package customerwall

import (
	"context"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// Reach says whether the caller on the context may use one customer account. False for an account homed outside
// the caller's branches, for an id that is no account, and for a caller with no linked staff row.
type Reach interface {
	CustomerReachable(ctx context.Context, customerID string) (bool, error)
}

// Fence holds one named customer to the caller's reach and writes the one answer when it fails. It reports
// whether the request may go on. An empty id passes (a walk in, or a filter left unset); a nil Reach lets the
// request through only for the DB free unit tests, and every composition root refuses to boot without one
// (each handler's CustomerReachWired).
func Fence(w http.ResponseWriter, r *http.Request, reach Reach, customerID string) bool {
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
