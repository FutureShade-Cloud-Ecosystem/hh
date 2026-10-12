// staff_roster_handler.go is the staff roster route (identityListStaff),
// the manager's read of who holds a role in the org. Read only: no user is
// created or changed here (D38: FB Console issues users). The identity
// package owns the users table and answers the read; this file shapes it
// into the contract's IdentityStaffMemberPage.
package staffcontext

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// RosterSource is the consumer declared seam over the identity package's
// roster read (identity.StaffRosterReader satisfies it).
type RosterSource interface {
	Roster(ctx context.Context, orgID string) ([]identity.RosterEntry, error)
}

// StaffRosterHandler serves GET /api/v1/staff.
type StaffRosterHandler struct {
	src RosterSource
}

// NewStaffRosterHandler wires the roster route over a source.
func NewStaffRosterHandler(src RosterSource) *StaffRosterHandler {
	return &StaffRosterHandler{src: src}
}

// RegisterRoutes registers the roster read behind the manager tier, as the ERP's RequireManager had it.
func (h *StaffRosterHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/staff", g.RequireManager(h.list))
}

// rosterMember is the contract's IdentityStaffMember. role_code is the ERP code the carried vocabulary
// uses (the canonical id for the two roles with no ERP name), scope is GLOBAL or BRANCH.
type rosterMember struct {
	UserID      string   `json:"user_id"`
	Email       string   `json:"email"`
	DisplayName string   `json:"display_name"`
	RoleCode    string   `json:"role_code"`
	RoleName    string   `json:"role_name"`
	Tier        int      `json:"tier"`
	Scope       string   `json:"scope"`
	BranchIDs   []string `json:"branch_ids"`
}

func (h *StaffRosterHandler) list(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	p, perr := httpx.ParseCursorPage(r)
	if perr != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	entries, err := h.src.Roster(r.Context(), caller.OrgID)
	if err != nil {
		httpx.Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	start := 0
	if p.Cursor != "" {
		tier, name, id, ok := parseRosterCursor(p.Cursor)
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
			return
		}
		// Positional under the list's own order (tier, display name, id), so a member who left the roster
		// since the cursor was minted neither fails the walk nor repeats or skips a row.
		start = sort.Search(len(entries), func(i int) bool { return rosterAfter(entries[i], tier, name, id) })
	}
	page := entries[start:]
	next := ""
	if len(page) > p.Limit {
		page = page[:p.Limit]
		next = rosterCursor(page[len(page)-1])
	}
	items := make([]rosterMember, 0, len(page))
	for _, e := range page {
		code, ok := ERPCodeFor(e.RoleID)
		if !ok {
			code = e.RoleID
		}
		branches := e.BranchIDs
		if branches == nil {
			branches = []string{}
		}
		items = append(items, rosterMember{
			UserID: e.UserID, Email: e.Email, DisplayName: e.DisplayName,
			RoleCode: code, RoleName: e.RoleLabel, Tier: e.Tier,
			Scope: string(ScopeFromCanonical(e.Scope)), BranchIDs: branches,
		})
	}
	httpx.WriteCursorPage(w, items, next, p.Limit)
}

// rosterCursor is "tier|id|display name": the display name last, so a name holding the separator still
// cuts cleanly into three.
func rosterCursor(e identity.RosterEntry) string {
	return strconv.Itoa(e.Tier) + "|" + e.UserID + "|" + e.DisplayName
}

func parseRosterCursor(key string) (tier int, name, id string, ok bool) {
	parts := strings.SplitN(key, "|", 3)
	if len(parts) != 3 || parts[1] == "" {
		return 0, "", "", false
	}
	t, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, "", "", false
	}
	return t, parts[2], parts[1], true
}

// rosterAfter reports whether an entry sorts strictly after the cursor under tier, display name, id.
func rosterAfter(e identity.RosterEntry, tier int, name, id string) bool {
	if e.Tier != tier {
		return e.Tier > tier
	}
	if e.DisplayName != name {
		return e.DisplayName > name
	}
	return e.UserID > id
}
