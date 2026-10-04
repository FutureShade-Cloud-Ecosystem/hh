// syncscope_authority.go is the quotes sync door's authority (P2-4, round 3):
// the same ownership ladder the REST edit path runs (ownershipRung, plan
// 3.3), applied at the door's two writes. The store is built by the
// operations wiring, which the role configuration does not pass, so the
// composition root PUBLISHES the configuration it gave the quote service
// (PublishRoleConfig, beside the identity package's PublishFloorApprovers
// pattern) and the door reads it. The held configuration is atomic, and
// nothing published means no level for anyone: the door fails closed, it
// never falls back to a permissive default.
package quote

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// publishedRoles is the role configuration the sync door decides with.
var publishedRoles atomic.Pointer[roleconfig.Config]

// PublishRoleConfig publishes the dealer's role configuration for the
// quotes sync door: the configuration the composition root gave the quote
// service. A nil configuration unpublishes, and an unpublished door refuses
// every write (no level, no edit).
func PublishRoleConfig(c *roleconfig.Config) { publishedRoles.Store(c) }

// authorizeSyncWrite resolves the caller behind the sync filter and runs
// the REST edit path's ladder on one quote: the caller must be an active
// staff user with the quote's branch in reach and a Quote Desk level of
// Work or better, and at Work the quote must be its own. Every failure is
// the operation's rejection, never a skipped check: an unlinked caller, an
// unreadable authority and an unpublished configuration all refuse.
func (s *SyncStore) authorizeSyncWrite(ctx context.Context, f sync.ScopeFilter, branchID, ownerID string) error {
	uid := s.quoteActor(ctx, f)
	if uid == "" {
		return quoteRefused(errors.New("a pushed quote edit needs the caller's linked staff user"))
	}
	caller, err := identity.NewApprovalRepository(s.pool).ResolveStaffInOrg(ctx, uid, f.OrgID)
	if err != nil {
		return quoteRefused(fmt.Errorf("the caller's authority could not be read: %w", err))
	}
	if caller.Status != identity.StatusActive || !caller.CanAccessBranch(branchID) {
		return quoteRefused(ErrForbidden)
	}
	if err := ownershipRung(deskLevelFor(publishedRoles.Load(), caller), caller, ownerID); err != nil {
		return quoteRefused(err)
	}
	return nil
}
