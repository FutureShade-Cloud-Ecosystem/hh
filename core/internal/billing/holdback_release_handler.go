package billing

// holdback_release_handler.go, wave 3 RT-3: the HTTP surface of the holdback release. The due to release
// read is manager gated and branch fenced; the release itself is the Controller's, judged from the caller's
// own role code (never the body) in the service, behind the manager route gate.

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// HoldbackReleaseHandler serves the due to release read and the release.
type HoldbackReleaseHandler struct {
	userLink identity.UserIDLinker
	repo     *Repository
	svc      *HoldbackReleaseService
	log      *slog.Logger
}

// NewHoldbackReleaseHandler wires the handler over the repository (the read) and the service (the act).
func NewHoldbackReleaseHandler(repo *Repository, svc *HoldbackReleaseService, log *slog.Logger) *HoldbackReleaseHandler {
	if log == nil {
		log = slog.Default()
	}
	return &HoldbackReleaseHandler{repo: repo, svc: svc, log: log}
}

// WithUserLinker binds the staff user id linker for request stamping.
func (h *HoldbackReleaseHandler) WithUserLinker(link identity.UserIDLinker) *HoldbackReleaseHandler {
	h.userLink = link
	return h
}

func (h *HoldbackReleaseHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

// RegisterRoutes registers the two routes behind the manager gate.
func (h *HoldbackReleaseHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/ar/holdback/due-to-release", g.RequireManager(h.staff(h.dueToRelease)))
	r.Handle("POST /api/v1/ar/holdback/releases", g.RequireManager(h.staff(h.release)))
	r.Handle("POST /api/v1/ar/holdback/releases/{releaseID}/reversal", g.RequireManager(h.staff(h.reverse)))
}

func (h *HoldbackReleaseHandler) dueToRelease(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	sc, _ := identity.StaffContextFromContext(r.Context())
	rows, err := h.repo.HoldbackDueToRelease(r.Context(), HoldbackDueQuery{
		BranchID: r.URL.Query().Get("branch_id"), Now: time.Now(), CanAccessBranch: sc.CanAccessBranch,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if rows == nil {
		rows = []HoldbackDueRow{}
	}
	type branchTotal struct {
		BranchID string `json:"branch_id"`
		DueCents int64  `json:"due_cents"`
	}
	var totals []branchTotal
	idx := map[string]int{}
	for _, row := range rows {
		i, ok := idx[row.BranchID]
		if !ok {
			i = len(totals)
			idx[row.BranchID] = i
			totals = append(totals, branchTotal{BranchID: row.BranchID})
		}
		if row.Due {
			totals[i].DueCents += row.HoldbackCents
		}
	}
	if totals == nil {
		totals = []branchTotal{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": rows, "branches": totals})
}

func (h *HoldbackReleaseHandler) release(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		CustomerJobID  string `json:"customer_job_id"`
		IdempotencyKey string `json:"idempotency_key"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, _ := identity.StaffContextFromContext(r.Context())
	res, err := h.svc.Release(r.Context(), HoldbackReleaseInput{
		CustomerJobID:   req.CustomerJobID,
		IdempotencyKey:  req.IdempotencyKey,
		ActorID:         sc.UserID,
		OrgID:           caller.OrgID,
		CanAccessBranch: sc.CanAccessBranch,
		IsController:    sc.RoleCode == RoleCodeFinancialController,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if res.Items == nil {
		res.Items = []ReleasedInvoice{}
	}
	status := http.StatusCreated
	if res.Replayed {
		status = http.StatusOK
	}
	httpx.WriteJSON(w, status, res)
}

func (h *HoldbackReleaseHandler) reverse(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		Reason string `json:"reason"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRegisterBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sc, _ := identity.StaffContextFromContext(r.Context())
	res, err := h.svc.Reverse(r.Context(), HoldbackReversalInput{
		ReleaseID: r.PathValue("releaseID"), Reason: req.Reason, ActorID: sc.UserID,
		CanAccessBranch: sc.CanAccessBranch, IsController: sc.RoleCode == RoleCodeFinancialController,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *HoldbackReleaseHandler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var refused HoldbackReleaseRefused
	var bad httpx.BadRequestError
	switch {
	case errors.As(err, &refused):
		if refused.Kind == RefusedNotController {
			httpx.Error(w, http.StatusForbidden, refused.Msg)
			return
		}
		if refused.Kind == RefusedReleaseNotFound {
			httpx.Error(w, http.StatusNotFound, refused.Msg)
			return
		}
		httpx.Error(w, http.StatusConflict, refused.Msg)
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	default:
		h.log.Error("holdback release failed", "method", r.Method, "path", r.URL.Path, "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
