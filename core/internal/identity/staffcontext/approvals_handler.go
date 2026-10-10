// approvals_handler.go is the rail's HTTP surface, the staff approvals
// routes the ERP identity handler served (POST/GET /api/v1/staff/approvals
// and the decide route under it). It lands here, in the bridge package,
// with the operations batch (HH-3.2 B5) because that is the batch that
// first wires the rail's entry points to the request path (the A3 review's
// unassigned-surface note): gl filed through it in B4, and dispatch's
// freight gate and messaging's notifier wire in B5, so B5 owns the surface
// and the decide-path re-point that came with it.
//
// THE DECIDE PATH'S AUTHORITY, once and for the record: every route
// registers through the Guards; the guard resolves the caller's authority
// from the Phase B source (staff_role_grant per request, D3); the h.staff
// adapter stamps the bridged StaffContext from that resolution plus the
// ERP users.id the linker supplies (users.platform_user_id, the B4 linkage
// ruling); the service methods take that caller as their authority and
// re-resolve nothing. No code path on this surface reads the identity
// scaffold tables (the A3 deadline: no money-deciding surface may derive
// its caller's authority from the scaffold tables, because scaffold drift
// fails open and a revoked grant could still decide money).
package staffcontext

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// ApprovalsHandler serves the rail's three staff routes over the Guards.
// The linker is required on this surface: requested_by and decided_by are
// ERP users.id values (the rail's four-eyes foreign keys), so an unlinked
// caller fails closed at the create leg's validation rather than filing a
// request attributed to nobody. A nil linker is not separately
// refused at registration; the create leg's fail-closed validation and the
// unlinked-caller 401 legs are the safety, per review.
type ApprovalsHandler struct {
	svc   *Approvals
	link  UserIDLinker
	staff func(next guards.StaffHandler) guards.StaffHandler
}

// NewApprovalsHandler wires the rail's HTTP surface. link may be nil in
// tests that stamp the context themselves.
func NewApprovalsHandler(svc *Approvals, link UserIDLinker) *ApprovalsHandler {
	h := &ApprovalsHandler{svc: svc, link: link}
	h.staff = func(next guards.StaffHandler) guards.StaffHandler {
		return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
			next(w, StampRequest(r, caller, h.link), caller)
		}
	}
	return h
}

// RegisterRoutes registers the rail's staff surface. All three routes are
// RequireStaff exactly as the ERP's authn gate had them: the decide
// authority model (who may approve what, four-eyes) lives in the service,
// resolved from the guard's caller, never from the route.
func (h *ApprovalsHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/staff/approvals", g.RequireStaff(h.staff(h.createApproval)))
	r.Handle("GET /api/v1/staff/approvals", g.RequireStaff(h.staff(h.listApprovals)))
	r.Handle("POST /api/v1/staff/approvals/{id}/decide", g.RequireStaff(h.staff(h.decideApproval)))
}

type createApprovalRequest struct {
	BranchID   string `json:"branch_id"`
	PolicyType string `json:"policy_type"`
	Detail     string `json:"detail"`
	SubjectRef string `json:"subject_ref"` // optional quote/order binding (H-2)
	// NOTE: requested_by is intentionally NOT read from the body. The
	// requester is the authenticated caller, so override provenance
	// cannot be forged (C-1): the stamp the guard chain made is the only
	// source of the requester's id.
}

func (h *ApprovalsHandler) createApproval(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	caller, ok := StaffContextFromContext(r.Context())
	if !ok || caller.UserID == "" {
		// Unreachable through the registered route (the adapter always
		// stamps); fail closed rather than file a request by nobody.
		httpx.Error(w, http.StatusUnauthorized, "no caller")
		return
	}
	var req createApprovalRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out, err := h.svc.CreateApproval(r.Context(), caller.UserID, req.BranchID, PolicyType(req.PolicyType), req.Detail, req.SubjectRef)
	if err != nil {
		writeApprovalErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, out)
}

func (h *ApprovalsHandler) listApprovals(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	caller, ok := StaffContextFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, "no caller")
		return
	}
	list, err := h.svc.ListApprovalsByKind(r.Context(), caller, r.URL.Query().Get("status"), PolicyType(r.URL.Query().Get("kind")))
	if err != nil {
		writeApprovalErr(w, r, err)
		return
	}
	// The contract's IdentityApprovalPage: the list is ordered (newest first, id as the tie break), the
	// cursor is the id of the last row of the page before, and a page of limit rows carries next_cursor
	// while more follow.
	p, perr := httpx.ParseCursorPage(r)
	if perr != nil {
		httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
		return
	}
	start := 0
	if p.Cursor != "" {
		at, id, ok := parseApprovalCursor(p.Cursor)
		if !ok {
			httpx.Error(w, http.StatusBadRequest, "cursor is malformed")
			return
		}
		// Positional: the page starts at the first row strictly after the (requested_at, id) pair under the
		// list's own order, so the row the cursor names may have left the list since (decided, tombstoned)
		// and the walk neither fails nor skips nor repeats a row.
		start = sort.Search(len(list), func(i int) bool { return approvalAfter(list[i], at, id) })
	}
	page := list[start:]
	next := ""
	if len(page) > p.Limit {
		page = page[:p.Limit]
		next = approvalCursor(page[len(page)-1])
	}
	httpx.WriteCursorPage(w, page, next, p.Limit)
}

type decideApprovalRequest struct {
	Approve bool   `json:"approve"`
	Reason  string `json:"reason"`
}

func (h *ApprovalsHandler) decideApproval(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	caller, ok := StaffContextFromContext(r.Context())
	if !ok || caller.UserID == "" {
		// An unlinked caller has no ERP id, so four-eyes and decided_by
		// could not be recorded: refuse rather than decide anonymously.
		httpx.Error(w, http.StatusUnauthorized, "no caller")
		return
	}
	var req decideApprovalRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := h.svc.DecideApproval(r.Context(), r.PathValue("id"), caller, req.Approve, req.Reason); err != nil {
		writeApprovalErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// writeApprovalErr maps the rail's errors to HTTP without leaking
// internals: a typed clientError answers its own status with its safe
// message, the unconfigured-pool state answers 503, everything else is a
// generic 500 (the rail's messages are already caller-safe by
// construction).
func writeApprovalErr(w http.ResponseWriter, r *http.Request, err error) {
	if status, ok := ApprovalErrorStatus(err); ok {
		httpx.Error(w, status, err.Error())
		return
	}
	if errors.Is(err, errNoDB) {
		httpx.Error(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}

// approvalCursor is the keyset position of a row under the list's order (requested_at descending, id
// ascending): its filing time to the microsecond Postgres keeps, and its id.
func approvalCursor(a ApprovalRequest) string {
	return a.RequestedAt.UTC().Format(approvalCursorTime) + "|" + a.ID
}

const approvalCursorTime = "2006-01-02T15:04:05.000000Z07:00"

// parseApprovalCursor reads a cursor key back into its pair; anything else is malformed.
func parseApprovalCursor(key string) (time.Time, string, bool) {
	ts, id, ok := strings.Cut(key, "|")
	if !ok {
		return time.Time{}, "", false
	}
	at, err := time.Parse(approvalCursorTime, ts)
	if err != nil {
		return time.Time{}, "", false
	}
	if _, err := uuid.Parse(id); err != nil {
		return time.Time{}, "", false
	}
	return at, id, true
}

// approvalAfter reports whether a row sorts strictly after the pair under requested_at DESC, id ASC.
func approvalAfter(a ApprovalRequest, at time.Time, id string) bool {
	rt := a.RequestedAt.UTC().Truncate(time.Microsecond)
	if rt.Equal(at) {
		return a.ID > id
	}
	return rt.Before(at)
}
