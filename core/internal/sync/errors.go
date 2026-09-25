package sync

import "errors"

// errNilEngine names the missing engine at handler construction, so a
// miswired route surface refuses to exist rather than failing per request.
var errNilEngine = errors.New("sync: engine is required")
