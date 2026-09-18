package identity

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// The carried driver session ceiling, ERP shape at the pinned commit
// (identity/auth.go with the dispatch_policy counterpart, core migration
// core/db/migrations/0209_how_long_a_driver_stays_signed_in_is_a_dealers_knob.sql):
// a 168h backstop, per-role selection (the knob is for the driver role
// alone, every other role keeps the standard staff session), and the
// dealer knob with the range the CHECK admits, 1 to 168 hours. The two
// bounds are pinned to each other here as they are pinned in the ERP.
const (
	// StaffSessionTTL is the standard staff session window the guard
	// honours on every non-driver role.
	StaffSessionTTL = 12 * time.Hour
	// MaxDriverSessionTTL is the longest window honoured on a driver-role
	// token: the backstop that bounds the knob from above.
	MaxDriverSessionTTL = 168 * time.Hour
	// MinDriverSessionHours and MaxDriverSessionHours are the knob's
	// contract, the same 1 to 168 the dispatch_policy CHECK admits.
	MinDriverSessionHours = 1
	MaxDriverSessionHours = 168
)

// DriverSessionHours reads the dealer knob (dispatch_policy.
// driver_session_hours, dealer-default row) for one org. The table is
// dispatch-owned: the concrete reader belongs to the Phase C dispatch
// module, not to identity, so B3 ships the seam and the tests inject
// doubles. The org id is carried in the signature even though the frozen
// schema has no org dimension on dispatch_policy (the ERP is single-org),
// so the Phase C reader can scope when HH-5 adds the dimension without a
// guard-layer change.
type DriverSessionHours interface {
	DriverSessionHours(ctx context.Context, orgID string) (int, error)
}

// SessionCeiling returns the longest token window the guard honours for
// the resolved role. The ERP split this in two: its issuer read the knob
// and fell back to the 12h constant on every unreadable arm, and its
// verifier judged only against the 168h backstop. The core mints nothing,
// so the two collapse into one check that reads the knob at enforcement
// time: every arm that cannot compute a policy (an unwired source, an
// unreadable knob, an out-of-contract value) refuses with an error and
// answers the 503 family upstream, never a guess and never a fallback.
// The backstop stays load-bearing as the bound on the knob itself: no
// value above 168 is in contract, so no window above the backstop can
// pass the driver arm (B2 review-notes item 1, ruling B2-07).
func SessionCeiling(ctx context.Context, roleID, orgID string, knob DriverSessionHours) (time.Duration, error) {
	if roleID == "" {
		return 0, errors.New("identity: session ceiling requires a resolved role")
	}
	if roleID != RoleDriver {
		return StaffSessionTTL, nil
	}
	if knob == nil {
		return 0, errors.New("identity: driver session ceiling is not computable: no knob source wired")
	}
	hours, err := knob.DriverSessionHours(ctx, orgID)
	if err != nil {
		return 0, fmt.Errorf("identity: driver session knob is unreadable: %w", err)
	}
	if hours < MinDriverSessionHours || hours > MaxDriverSessionHours {
		return 0, fmt.Errorf("identity: driver session knob is out of contract: %d hours", hours)
	}
	return time.Duration(hours) * time.Hour, nil
}

// SessionWithinCeiling reports whether the token's self-declared window,
// exp minus iat, fits the ceiling. The comparison carries no clock
// tolerance: B2's verifier applies its skew to issued-at and not-before
// only and gives expiry no grace, and the window is judged the same way,
// so the boundary itself is exact in both directions.
func SessionWithinCeiling(issuedAt, expiresAt time.Time, ceiling time.Duration) bool {
	return expiresAt.Sub(issuedAt) <= ceiling
}
