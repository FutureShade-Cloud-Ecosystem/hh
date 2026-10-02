package reporting

import "errors"

// Sentinel errors. Each maps to exactly one HTTP status in handler.go, so a new call path cannot
// accidentally turn a forbidden read into a 500 or a missing row into a 200 with no rows.
var (
	// ErrNotFound, no such template code, run, rule or saved view.
	ErrNotFound = errors.New("reporting: not found")
	// ErrUnauthorized, no server-stamped staff caller on the context. Never returned for a caller who
	// exists but lacks reach; that is ErrForbidden.
	ErrUnauthorized = errors.New("reporting: unauthorized")
	// ErrForbidden, the caller exists but the branch fence refuses the act: running, printing or
	// previewing outside the caller's reach. A refusal on a single-read path answers ErrNotFound
	// instead, so an id does not confirm that a row exists outside the reach.
	ErrForbidden = errors.New("reporting: forbidden")
	// ErrNoDatabase, scaffold/offline mode. Answered as 503, not 500: nothing is broken, the module
	// simply has nothing to read.
	ErrNoDatabase = errors.New("reporting: no database")
	// ErrColumnDrift, the database returned a column set that is not the template's declared allowlist.
	// This is a DEVELOPMENT error surfaced at runtime rather than a caller error, and it fails the run
	// rather than returning the undeclared data. See runner.go.
	ErrColumnDrift = errors.New("reporting: template returned columns outside its allowlist")
)
