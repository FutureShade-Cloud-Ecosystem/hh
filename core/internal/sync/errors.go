package sync

import "errors"

// errNilEngine names the missing engine at handler construction, so a
// miswired route surface refuses to exist rather than failing per request.
var errNilEngine = errors.New("sync: engine is required")

// errNilStaffRoles and errNilPortalRoles name a missing role seam at handler
// construction: a push surface without its write fence would admit every
// caller, so the handler refuses to exist.
var (
	errNilStaffRoles  = errors.New("sync: staff roles seam is required")
	errNilPortalRoles = errors.New("sync: portal roles seam is required")
)
