package warranty

import (
	"fmt"
	"net/http"
)

// clientError carries an HTTP status so the handler maps a domain failure to the right code (mirrors the
// internal/lien + internal/till taxonomy). Anything not a clientError is a 500.
type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }

func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

// badRequest (400), a malformed request.
func badRequest(format string, a ...any) error {
	return clientError{status: http.StatusBadRequest, msg: fmt.Sprintf(format, a...)}
}

// forbidden (403), an authorization failure.
func forbidden(format string, a ...any) error {
	return clientError{status: http.StatusForbidden, msg: fmt.Sprintf(format, a...)}
}

// notFound (404).
func notFound(format string, a ...any) error {
	return clientError{status: http.StatusNotFound, msg: fmt.Sprintf(format, a...)}
}

// conflict (409), e.g. a duplicate profile for a vendor.
func conflict(format string, a ...any) error {
	return clientError{status: http.StatusConflict, msg: fmt.Sprintf(format, a...)}
}

// unprocessable (422), a well-formed request whose value violates a rule (an illegal phase transition, an
// unmet checklist gate, a missing required field).
func unprocessable(format string, a ...any) error {
	return clientError{status: http.StatusUnprocessableEntity, msg: fmt.Sprintf(format, a...)}
}

var (
	errNoDB            = clientError{status: http.StatusServiceUnavailable, msg: "warranty: database not configured"}
	errUnauthorized    = clientError{status: http.StatusUnauthorized, msg: "warranty: unauthenticated"}
	errClaimNotFound   = clientError{status: http.StatusNotFound, msg: "warranty: claim not found"}
	errProfileNotFound = clientError{status: http.StatusNotFound, msg: "warranty: vendor profile not found"}

	// errAlreadySettledExit (422), the mutual-exclusion guard, re-checked UNDER the row lock inside
	// AdvanceClaimPhaseGuarded: a settled claim (outcome set) cannot be advanced to a terminal-LOST exit
	// (ABANDONED/REJECTED). Mirrors the Go-side pre-check in AdvancePhase; authoritative because it holds the
	// lock (fixes the TOCTOU that could strand a settled recovery as a loss).
	errAlreadySettledExit = clientError{status: http.StatusUnprocessableEntity, msg: "warranty: claim is settled, a settled claim can only be CLOSED, not advanced to a LOST exit"}

	// errAlreadyTerminal (409), re-checked UNDER the row lock: a claim a concurrent advance already drove to a
	// terminal phase cannot be settled (RecordSettlement) or advanced again (AdvanceClaimPhaseGuarded).
	errAlreadyTerminal = clientError{status: http.StatusConflict, msg: "warranty: claim is already closed"}
)
