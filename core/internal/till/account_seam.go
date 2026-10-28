package till

// account_seam.go, the till's CONSUMER side seam to the chart of accounts (W2-6b fix round 1).
//
// The till owns the list of the codes the drawer names on a paid in or a paid out, and every code points
// at the GL account that movement posts to. The chart of accounts belongs to the GL, so the till does not
// read a GL table: the till declares the seam it consumes, and the composition root wires the GL service
// into it (cmd/core/wire_operations.go).
//
// A code may only point at an account the chart holds and keeps live. A code that names an account nobody
// owns is found out at post time, when the GL refuses the journal with "unknown account code", and the
// drawer's money sits unposted; a code that names an account that has been turned off is refused the
// same way. The manager writes the list before the drawer needs it, so the refusal belongs where the code
// is written.

import (
	"context"
	"net/http"
)

// AccountPostabilityChecker is the till's consumer side seam: the chart's answer about an account code.
//
// The answer carries two facts because the drawer needs both. A code that names no account at all is a
// different mistake from a code that names an account that has been turned off, and the two refusals say
// so in different words.
type AccountPostabilityChecker interface {
	// AccountPostable answers whether the code names an account the chart holds, and whether that
	// account is live.
	//
	// inChart is false when the chart holds no account under that code at all. active is false when the
	// chart holds one that has been turned off. Both false means the code names nowhere.
	AccountPostable(ctx context.Context, code string) (inChart bool, active bool, err error)
}

// checkAccountPostable refuses a reason code whose account the chart cannot take a posting on: an
// account nobody owns, or one that has been turned off. The refusal says which, so the manager can fix
// the code. Nothing is written: the check runs before the row is built.
//
// Unwired, the till refuses. A seam that is not wired must never wave a code through, and a code whose
// account nobody checked is worse than a refusal.
func (s *Service) checkAccountPostable(ctx context.Context, code string) error {
	if s.accounts == nil {
		return clientErr(http.StatusServiceUnavailable, "till: the chart of accounts is not wired, so the account %s was not checked", code)
	}
	inChart, active, err := s.accounts.AccountPostable(ctx, code)
	if err != nil {
		return err
	}
	if !inChart {
		return unprocessable("till: the account %s is not in the chart", code)
	}
	if !active {
		return unprocessable("till: the account %s is inactive", code)
	}
	return nil
}
