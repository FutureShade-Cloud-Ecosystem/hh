package reporting

import (
	"errors"
	"fmt"
)

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
	// ErrDatasetRoleForbidden, the caller reached the dataset door and the role gate on the surface that
	// owns this set's facts refuses the read. Wraps ErrForbidden so service callers check errors.Is(err, ErrForbidden)
	// while handler.go answers with authz.RefuseForbidden.
	ErrDatasetRoleForbidden = fmt.Errorf("%w: dataset owner surface forbidden", ErrForbidden)
	// ErrNoDatabase, scaffold/offline mode. Answered as 503, not 500: nothing is broken, the module
	// simply has nothing to read.
	ErrNoDatabase = errors.New("reporting: no database")
	// ErrDatasetUnwired, a curated data set whose owning module's read is not bound in this
	// deployment. Like ErrNoDatabase it answers 503: nothing is broken, the set has no source here.
	ErrDatasetUnwired = errors.New("reporting: data set source not wired")
	// ErrColumnDrift, the database returned a column set that is not the template's declared allowlist.
	// This is a DEVELOPMENT error surfaced at runtime rather than a caller error, and it fails the run
	// rather than returning the undeclared data. See runner.go.
	ErrColumnDrift = errors.New("reporting: template returned columns outside its allowlist")
)
