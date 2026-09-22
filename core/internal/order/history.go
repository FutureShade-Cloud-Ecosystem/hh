// history.go, DIB-159 order-change (status) history reads. The client requirement: an order
// must show HOW its status moved over time, from→to, WHO changed it, WHEN, and (optionally) WHY.
// The status-transition CHOKE-POINT (Service.SetStatus, service.go) emits one ORDER_STATUS_CHANGED
// audit row per real transition; this file is the READ side that reconstructs the ordered timeline
// from that append-only trail, the SAME "audit-log-IS-the-history-store" pattern the shipped
// payment-history half uses (till/history.go), so DIB-159 needs no new table and no migration.
//
//	GET /api/v1/orders/{id}/history → the ordered status timeline for one order (staff-gated)
//
// Actor display names come from a LEFT JOIN to users on the audit actor_id, a portal
// (customer-originated) transition has no staff row and surfaces with an empty actor name (mirrors
// the payment-history "customer portal" fallback). Everything here is additive and read-only.
package order

import (
	"context"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"net/http"
	"sort"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/audit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// AuditTrailReader is the READ side of the audit seam (satisfied by *audit.Service.ReopenTransaction):
// the chronological trail for one entity. OrderHistory filters it to ORDER_STATUS_CHANGED rows to
// build the status timeline, the audit log IS the order-status history store (no dedicated table).
type AuditTrailReader interface {
	ReopenTransaction(ctx context.Context, entityType, entityID string) ([]audit.Entry, error)
}

// ActorNameResolver maps a set of actor ids to their staff display names (empty for a
// customer/portal actor with no staff row). Satisfied by *Repository (LEFT JOIN users).
type ActorNameResolver interface {
	ActorNames(ctx context.Context, ids []string) (map[string]string, error)
}

// OrderStatusChange is one status transition on an order, with attribution (DIB-159).
type OrderStatusChange struct {
	FromStatus string    `json:"from_status"`          // the status transitioned FROM ("" at first CONFIRMED)
	ToStatus   string    `json:"to_status"`            // the status transitioned TO
	ActorID    string    `json:"actor_id,omitempty"`   // audit actor_id ("" for a system transition)
	ActorName  string    `json:"actor_name,omitempty"` // users.display_name; "" for a portal/customer actor
	Reason     string    `json:"reason,omitempty"`     // optional short reason recorded on the transition
	OccurredAt time.Time `json:"occurred_at"`          // audit_log.created_at (append-only, authoritative)
}

// OrderHistory is one order's ordered status timeline (oldest → newest).
type OrderHistory struct {
	OrderID string              `json:"order_id"`
	Changes []OrderStatusChange `json:"changes"`
}

// - repository read (actor name resolution over users; read-only) -

// ActorNames resolves staff display names for a set of audit actor ids. A customer/portal actor
// (no staff row) is simply absent from the returned map, so the caller renders an empty name.
func (r *Repository) ActorNames(ctx context.Context, ids []string) (map[string]string, error) {
	out := map[string]string{}
	if r.pool == nil || len(ids) == 0 {
		return out, nil
	}
	// Dedup so the ANY($1) array stays tight; skip empties (a system actor).
	seen := map[string]bool{}
	uniq := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return out, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, display_name FROM users WHERE id = ANY($1::uuid[])`, uniq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

// - service (read guard mirrors the order-detail reads: the caller is auth-gated at the route) -

// WithAuditTrail injects the READ side of the audit seam (satisfied by *audit.Service). OrderHistory
// uses it to reconstruct the ordered status timeline from the ORDER_STATUS_CHANGED trail, no table.
func (s *Service) WithAuditTrail(r AuditTrailReader) *Service { s.auditTrail = r; return s }

// OrderHistory returns the ordered status timeline for one order (DIB-159). It reads the audit trail
// for entity "order", keeps only ORDER_STATUS_CHANGED rows, shapes each into an OrderStatusChange, and
// resolves actor display names. The rows come back oldest-first from the audit repo (ListByEntity ORDER
// BY created_at ASC); we re-sort defensively so the timeline is deterministic even if the trail source
// changes.
//
// AUTHZ (the horizontal-access boundary, mirrors the payment-history reads at till/history.go:211/:290):
// staff-gating at the route proves the caller is staff, but NOT that they may reach THIS order. A
// branch-restricted staffer must not read an order in a branch they cannot access by iterating ids
// (an IDOR gap). So before returning the timeline we resolve the trusted caller, load the order's
// owning branch (migration 0008), and gate on caller.CanAccessBranch: a GLOBAL-scoped caller reaches
// any branch; a scoped caller only their grants. A MISSING order returns ErrNotFound (never a leak of
// existence via the authz path); an EXISTING order in a branch the caller cannot reach returns
// ErrForbidden, the same not-found-vs-forbidden split the tenant-scoped write choke-point already makes.
func (s *Service) OrderHistory(ctx context.Context, orderID string) (OrderHistory, error) {
	if orderID == "" {
		return OrderHistory{}, httpx.BadRequestf("order id is required")
	}
	// Resolve the trusted, server-stamped caller and branch-scope BEFORE any data read, so no order
	// timeline crosses a branch boundary. A missing order is reported as not-found (never forbidden),
	// mirroring the till payment-history reads and the tenant-safe SetStatus choke-point.
	caller, err := s.caller(ctx)
	if err != nil {
		return OrderHistory{}, err
	}
	// F3: accept the visible order_no ("#SO-4"/"SO-4") as an alternate key, resolved up front so the
	// branch check AND the audit-trail lookup below both key off the real id. B2-4: branch-scoped to the
	// caller so a cross-branch #SO answers 404 rather than confirming the order via the number space.
	rGlobal, rBranches := callerBranchScope(caller)
	orderID, err = s.store.ResolveOrderID(ctx, orderID, rGlobal, rBranches)
	if err != nil {
		return OrderHistory{}, err
	}
	branchID, err := s.store.OrderBranch(ctx, orderID)
	if err != nil {
		return OrderHistory{}, err // ErrNotFound for a missing order, no existence leak
	}
	if !caller.CanAccessBranch(branchID) {
		return OrderHistory{}, ErrForbidden
	}
	if s.auditTrail == nil {
		// The seam is not wired, the timeline is genuinely empty rather than a 501, so the UI renders
		// "no status changes" instead of an error (the order may simply have never transitioned).
		return OrderHistory{OrderID: orderID, Changes: []OrderStatusChange{}}, nil
	}
	entries, err := s.auditTrail.ReopenTransaction(ctx, "order", orderID)
	if err != nil {
		return OrderHistory{}, err
	}
	out := OrderHistory{OrderID: orderID, Changes: []OrderStatusChange{}}
	var actorIDs []string
	for _, e := range entries {
		if e.Action != audit.ActOrderStatusChanged {
			continue
		}
		c := OrderStatusChange{
			FromStatus: auditStr(e.Changes["old_status"]),
			ToStatus:   auditStr(e.Changes["new_status"]),
			ActorID:    e.ActorID,
			Reason:     auditStr(e.Changes["reason"]),
		}
		if at, perr := time.Parse(time.RFC3339Nano, e.CreatedAt); perr == nil {
			c.OccurredAt = at
		}
		out.Changes = append(out.Changes, c)
		if e.ActorID != "" {
			actorIDs = append(actorIDs, e.ActorID)
		}
	}
	// Oldest → newest by the authoritative audit timestamp (stable on ties by insertion order).
	sort.SliceStable(out.Changes, func(i, j int) bool {
		return out.Changes[i].OccurredAt.Before(out.Changes[j].OccurredAt)
	})
	if s.names != nil && len(actorIDs) > 0 {
		if m, nerr := s.names.ActorNames(ctx, actorIDs); nerr == nil {
			for i := range out.Changes {
				if n := m[out.Changes[i].ActorID]; n != "" {
					out.Changes[i].ActorName = n
				}
			}
		}
		// A name-resolution failure degrades to actor ids only, the timeline still answers
		// from→to/when, so it never fails the whole read.
	}
	return out, nil
}

// auditStr reads a string out of an audit changes blob, tolerating a nil / non-string value (jsonb
// round-trips through map[string]any).
func auditStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// - HTTP (staff-gated; the order-change timeline is a counter-level read, like payment-history) -

// Handler exposes the DIB-159 order-history read. It is the order module's only HTTP surface (the
// order write/read seams are otherwise composed in-process by the portal BFF); it is registered
// staff-gated from main.go, next to the payment-history reads.
type Handler struct {
	svc      *Service
	userLink identity.UserIDLinker
}

// NewHandler wires the order service for the staff-gated history route.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes wires the order-change history read. requireStaff gates it: "how did this order's
// status move, by whom, when" is a counter-level question (same posture as the payment-history reads).
func (h *Handler) RegisterRoutes(mux *http.ServeMux, g *guards.Guards) {
	mux.Handle("GET /api/v1/orders/{id}/history", g.RequireStaff(h.staff(h.orderHistory)))
	// B2/R6: the staff order read, current status + this caller's legal_actions (handler.go).
	mux.Handle("GET /api/v1/orders/{id}", g.RequireStaff(h.staff(h.orderRead)))
	// DIB-489/490: the staff duplicate trio (duplicate.go) rides this registration so the whole
	// order surface keeps wiring through the calls the composition root already makes, same
	// requireStaff gate, no main.go change.
	h.registerDuplicateRoutes(mux, g)
	// DIB-492/493 (activity.go): the merged activity stream + the human-notes writes. Counter-level
	// surfaces open to any active in-branch staff, exactly like the history read above, the route
	// guard is the capability wrapper (protected by construction) and the service re-checks the
	// caller's branch on the resolved order.
	mux.Handle("GET /api/v1/orders/{id}/activity", g.RequireStaff(h.staff(h.orderActivity)))
	mux.Handle("POST /api/v1/orders/{id}/notes", g.RequireStaff(h.staff(h.addOrderNote)))
	mux.Handle("PATCH /api/v1/orders/{id}/notes/{noteId}", g.RequireStaff(h.staff(h.editOrderNote)))
}

// orderHistory answers: how did this order's status change over time, from→to, who, when, why.
// Error mapping mirrors the not-found-vs-forbidden split the service makes: a missing order is 404
// (no existence leak), an order in a branch the caller cannot reach is 403 (the horizontal-access
// boundary), an unauthenticated request is 401, a bad id is 400, anything else is 500.
func (h *Handler) orderHistory(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	out, err := h.svc.OrderHistory(r.Context(), r.PathValue("id"))
	if err != nil {
		var br httpx.BadRequestError
		switch {
		case errors.As(err, &br):
			httpx.Error(w, http.StatusBadRequest, br.Msg)
		case errors.Is(err, ErrNotFound):
			httpx.Error(w, http.StatusNotFound, "order not found")
		case errors.Is(err, ErrForbidden):
			httpx.Error(w, http.StatusForbidden, "forbidden")
		case errors.Is(err, ErrUnauthorized):
			httpx.Error(w, http.StatusUnauthorized, "unauthenticated")
		default:
			httpx.Error(w, http.StatusInternalServerError, "internal error")
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
