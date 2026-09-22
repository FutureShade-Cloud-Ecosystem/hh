package lien

import (
	"fmt"
	"net/http"
)

// clientError carries an HTTP status so the handler maps a domain failure to the right code (mirrors the
// internal/till + internal/gl taxonomy). Anything not a clientError is a 500.
type clientError struct {
	status int
	msg    string
}

func (e clientError) Error() string { return e.msg }

func clientErr(status int, format string, a ...any) error {
	return clientError{status: status, msg: fmt.Sprintf(format, a...)}
}

// forbidden (403), an authorization failure (the dual-gate release roles / four-eyes).
func forbidden(format string, a ...any) error {
	return clientError{status: http.StatusForbidden, msg: fmt.Sprintf(format, a...)}
}

// unprocessable (422), a well-formed request whose value violates a rule (PAYABLE release refused, nothing
// to release, missing required field).
func unprocessable(format string, a ...any) error {
	return clientError{status: http.StatusUnprocessableEntity, msg: fmt.Sprintf(format, a...)}
}

var (
	errNoDB         = clientError{status: http.StatusServiceUnavailable, msg: "lien: database not configured"}
	errUnauthorized = clientError{status: http.StatusUnauthorized, msg: "lien: unauthenticated"}
	errJobNotFound  = clientError{status: http.StatusNotFound, msg: "lien: job not found"}
	errDocNotFound  = clientError{status: http.StatusNotFound, msg: "lien: document not found"}
)

// - release-gate roles (the Bill 60 holdback dual gate) -
// The gatekeeper who initiates a holdback release must be the General Manager; the co-signer (four-eyes) must
// be a finance authority, the Financial Controller or central Payables/Receivables. Both are DOCUMENTED
// PLACEHOLDERS pending ratification (M6 has zero client validation; DIB-42 is the checkpoint).
const (
	roleGatekeeper = "GENERAL_MANAGER"
	roleController = "FINANCIAL_CONTROLLER"
	rolePayRec     = "PAYABLES_RECEIVABLES"
)

// holdback directions (stored in lien_jobs.metadata.holdback_direction, 0038 has no direction column, so the
// direction rides the metadata JSONB; recorded as an integrator deviation).
const (
	dirReceivable = "RECEIVABLE" // we hold a customer's retention (the 1150 receivable), releasable
	dirPayable    = "PAYABLE"    // we owe a sub's retention (the 2350 payable), recordable, release refused (422)
)
