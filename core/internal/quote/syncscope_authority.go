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
	"strings"
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

// syncCaller resolves the caller behind the sync filter as an active staff
// user of the filter's org. Every failure is an error the callers read as no
// authority: an unlinked caller, an unreadable authority, an inactive user.
func (s *SyncStore) syncCaller(ctx context.Context, f sync.ScopeFilter) (identity.StaffContext, error) {
	uid := s.quoteActor(ctx, f)
	if uid == "" {
		return identity.StaffContext{}, errors.New("a pushed quote operation needs the caller's linked staff user")
	}
	caller, err := identity.NewApprovalRepository(s.pool).ResolveStaffInOrg(ctx, uid, f.OrgID)
	if err != nil {
		return identity.StaffContext{}, fmt.Errorf("the caller's authority could not be read: %w", err)
	}
	if caller.Status != identity.StatusActive {
		return identity.StaffContext{}, ErrForbidden
	}
	return caller, nil
}

// authorizeSyncCreate is the REST create route's rule for a pushed create:
// the caller needs Work on the Quote Desk and the quote's branch in reach
// (CreateQuote's own two checks). Failure is the operation's rejection.
func (s *SyncStore) authorizeSyncCreate(ctx context.Context, f sync.ScopeFilter, branchID string) error {
	caller, err := s.syncCaller(ctx, f)
	if err != nil {
		return quoteRefused(err)
	}
	if !deskLevelFor(publishedRoles.Load(), caller).AtLeast(roleconfig.LevelWork) || !caller.CanAccessBranch(branchID) {
		return quoteRefused(ErrForbidden)
	}
	return nil
}

// syncReadScope is what the caller may read through the door: the REST list
// rule (StaffQuoteList). Nothing without a Quote Desk level of Read or
// better; at Work the caller's own quotes; at Manage or Read every quote of
// the branches the caller reaches (a global caller reaches every branch).
// The quote's figures are server totals, never cost, so no cost list gates
// the projection. ok=false means nothing is readable: the door fails closed
// on an unlinked caller, an unreadable authority, an unpublished
// configuration.
type syncReadScope struct {
	ok       bool
	global   bool
	branches []string
	owner    string // non-empty: only quotes this user owns (the Work rung)
}

func (s *SyncStore) syncReadScope(ctx context.Context, f sync.ScopeFilter) syncReadScope {
	caller, err := s.syncCaller(ctx, f)
	if err != nil {
		return syncReadScope{}
	}
	level := deskLevelFor(publishedRoles.Load(), caller)
	if !level.AtLeast(roleconfig.LevelRead) {
		return syncReadScope{}
	}
	rs := syncReadScope{ok: true}
	if level == roleconfig.LevelWork {
		if caller.UserID == "" {
			return syncReadScope{}
		}
		rs.owner = caller.UserID
	}
	global, ids := callerBranchScope(caller)
	if !global && len(ids) == 0 {
		return syncReadScope{}
	}
	rs.global, rs.branches = global, ids
	return rs
}

// predicate renders the scope as SQL over quotes, with its arguments
// numbered from argN.
func (r syncReadScope) predicate(argN int) (string, []any) {
	var sb strings.Builder
	var args []any
	if !r.global {
		fmt.Fprintf(&sb, " AND branch_id = ANY($%d::uuid[])", argN)
		args = append(args, r.branches)
		argN++
	}
	if r.owner != "" {
		fmt.Fprintf(&sb, " AND COALESCE(salesperson_id, created_by) = $%d::uuid", argN)
		args = append(args, r.owner)
	}
	return sb.String(), args
}

// The read rung's per-row form (reaches) is gone with the mask it served:
// the rung rides the query now, in the pull and in both row reads alike
// (UX-108), so no read answers on a row the rung would hold back.
