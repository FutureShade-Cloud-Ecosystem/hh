//go:build !integration

package warranty

import "errors"

// status extracts the HTTP status from a clientError (used by the unit-test assertions). The integration build
// has its own copy (integration_test.go), this one is constrained to the default (non-integration) build so
// exactly one definition exists per build tag.
func status(err error) int {
	if err == nil {
		return 0
	}
	var ce clientError
	if errors.As(err, &ce) {
		return ce.status
	}
	return 500
}
