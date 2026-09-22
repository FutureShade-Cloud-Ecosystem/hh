package inventory

// util.go, tiny package-local helpers (nullable text binding + payload marshalling) shared by the repository
// and service. Mirrors the nullStr helper the sibling modules use so an empty string binds as SQL NULL.

import (
	"encoding/json"
	"errors"
)

// nullStr binds "" as SQL NULL (so an optional FK / actor column stores NULL, not an empty string that fails a
// UUID cast).
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// marshalJSON marshals an outbox payload to bytes.
func marshalJSON(v any) ([]byte, error) { return json.Marshal(v) }

// round rounds a float to the nearest integer cent (half away from zero), matching SQL ROUND().
func round(f float64) int64 {
	if f < 0 {
		return int64(f - 0.5)
	}
	return int64(f + 0.5)
}

// asClientError is errors.As for the package's clientError, given a name so the handler layer reads
// as intent ("is this a typed client error?") rather than as a generic reflection call.
func asClientError(err error, target *clientError) bool { return errors.As(err, target) }
