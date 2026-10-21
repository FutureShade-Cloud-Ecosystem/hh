package billing

// customer_reach.go (UX-89 round 2): the one customer wall the AR doors that take a customer share. The rule is
// the customer module's own (customer/account_lookup.go): an account homed at a branch is held to callers who
// reach that branch, and an account with no home is shared company wide. The seam is WorkspaceReach
// (customer_ar_workspace_access.go), implemented once by CustomerModuleReach over the customer read, so a caller
// with no linked staff row is not reachable. Billing reads no customer table.

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/customerwall"
)

// FenceCustomer holds one named customer to the caller's reach with the one customer answer; it is the shared
// platform wall (customerwall.Fence), kept here under the name the AR doors already call.
func FenceCustomer(w http.ResponseWriter, r *http.Request, reach WorkspaceReach, customerID string) bool {
	if reach == nil { // a typed nil interface would not compare equal inside Fence
		return true
	}
	return customerwall.Fence(w, r, reach, customerID)
}
