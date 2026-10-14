// stamp.go is the B4 linkage ruling's half of the bridge: the request-path
// adapter that turns the caller the guards resolved into the carried
// StaffContext the money-spine modules read. The bridge's context key is
// the seam (the A3 review's binding note), so carried handlers and the
// carried services' callerFrom defaults (StaffContextFromContext) keep
// reading exactly the name they read at the pin, and the stamp is MADE
// here from the Phase B caller, never trusted from upstream (D3: the
// request path derives authority from resolution, never from a stamp).
package staffcontext

import (
	"context"
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
)

// UserIDLinker resolves the ERP users.id a platform user id links to
// (users.platform_user_id, migration 0215). It is id translation, never
// authority: tier, scope and role come from the resolved authority alone,
// and ok false (no linked row, or an unreadable one) leaves UserID empty,
// which every id-consuming surface treats fail closed (an unattributable
// write is refused, never attributed to nobody). The concrete reader is
// wiring-owned (a pool read in the wire functions); tests inject a static
// map so the carried caller() checks that require a non-empty UserID
// (order, quote) keep both halves provable.
type UserIDLinker func(ctx context.Context, platformUserID string) (userID string, ok bool)

// SyncStaffResolver resolves the staff context a sync door runs a direct
// door's service body under: the caller's authority in the org (role,
// tier, scope, branches) with the linked ERP users.id on it, so the
// branch fence and the audit actor are the REST door's. The sync core's
// ScopeFilter carries the platform subject and the org and no role, so a
// store that must hold the REST door's fences reads them again through
// this seam. ok false is no active authority in the org. The composition
// root builds it over identity's own resolver and the user linker.
type SyncStaffResolver func(ctx context.Context, platformUserID, orgID string) (StaffContext, bool, error)

// StampRequest stamps the bridged StaffContext onto r and returns the
// request to pass on. sc is the authority half (FromAuthority of the
// caller the guard resolved); link, when non-nil, supplies the linked ERP
// users.id. A nil link is legal and yields the authority-only stamp: every
// predicate answers, UserID reads empty.
func StampRequest(r *http.Request, caller guards.StaffCaller, link UserIDLinker) *http.Request {
	sc := FromAuthority(caller.StaffAuthority)
	if link != nil {
		if id, ok := link(r.Context(), caller.PlatformUserID); ok {
			sc.UserID = id
		}
	}
	return r.WithContext(ContextWithStaff(r.Context(), sc))
}
