package billing

// finance_charge_route.go, wave 3 FC-1b: THE CONTROLLER'S DOORS ON THE FINANCE CHARGE RUN.
//
// FC-1 built the run (RunFinanceCharges) and the waiver service with no door in front of either. This
// file is the door: press a house month's run for a branch, read a branch's runs with their charges,
// and set or clear a customer's waiver.
//
// -- THE GATE -----------------------------------------------------------------------
// The route gate is the leadership tier (RequireAdmin), narrowed in the handler to the Controller
// roles, the same gate the AP revaluation's routes carry (HR is leadership and not a Controller). The
// waiver is narrower still: it is the Financial Controller's act alone (FC-1's own rule), so a General
// Manager passes the route gate and is refused by the waiver service in words, 403.
//
// -- EVERY ID IS PARSED --------------------------------------------------------------
// A branch or customer id from the caller is parsed as a uuid and carried in its canonical lower case
// form, so an id typed in upper case is the same branch, the same advisory lock and the same charge key
// as its lower case form (the lesson of the AP revaluation read: a raw string reached the lock and the
// journal key and split both). A value that is not a uuid is a 400 in words.
//
// -- THE REACH ------------------------------------------------------------------------
// A branch outside the caller's own branches is refused (403). The door reads the stamped caller and
// fails closed: an unstamped context is never treated as an internal caller here (the worker does not
// come through this door, it calls the writer's sweep), and a caller with no linked staff record is
// refused, so no charge or waiver is ever filed against nobody.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// financeChargeRoles are the roles that may press or read a finance charge run: the General Manager,
// the Financial Controller and the platform admin (the AP revaluation's own list).
var financeChargeRoles = []string{"GENERAL_MANAGER", "FINANCIAL_CONTROLLER", identity.RoleSuperAdmin}

// FinanceChargeNotStampedWords is what an unstamped or unlinked caller reads.
const FinanceChargeNotStampedWords = "billing: the finance charge run needs a signed in staff member with a linked staff record"

var errFinanceChargeNotStamped = errors.New(FinanceChargeNotStampedWords)

// canonicalUUID parses a caller's id and answers its canonical lower case form, or a 400 in words.
func canonicalUUID(raw, what string) (string, error) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", httpx.BadRequestf("billing: %s %q is not a uuid", what, raw)
	}
	return id.String(), nil
}

// FinanceChargeRunMonth is one house month of one branch's run, with every charge it raised.
type FinanceChargeRunMonth struct {
	BranchID     string          `json:"branch_id"`
	Month        string          `json:"month"`
	MonthEnd     string          `json:"month_end"`
	Charged      int             `json:"charged"`
	ChargedCents int64           `json:"charged_cents"`
	PastDueCents int64           `json:"past_due_cents"`
	Charges      []FinanceCharge `json:"charges"`
}

// FinanceChargeService is the door's body over the writer's run, the waiver service and the
// repository read.
type FinanceChargeService struct {
	w       *InvoiceWriter
	waivers *FinanceChargeWaiverService
	repo    *Repository
}

// NewFinanceChargeService wires the door. The writer carries the audit recorder the press is recorded
// through, the waiver service its own.
func NewFinanceChargeService(w *InvoiceWriter, waivers *FinanceChargeWaiverService, repo *Repository) *FinanceChargeService {
	return &FinanceChargeService{w: w, waivers: waivers, repo: repo}
}

// caller reads the stamped caller and refuses (fail closed) one that is missing or has no staff record.
func (s *FinanceChargeService) caller(ctx context.Context) (identity.StaffContext, error) {
	c, ok := identity.StaffContextFromContext(ctx)
	if !ok || strings.TrimSpace(c.UserID) == "" {
		return identity.StaffContext{}, errFinanceChargeNotStamped
	}
	return c, nil
}

// branch parses the branch id and holds it to the caller's reach.
func (s *FinanceChargeService) branch(c identity.StaffContext, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", httpx.BadRequestf("billing: branch_id is required, a finance charge run is one branch's books")
	}
	branch, err := canonicalUUID(raw, "branch_id")
	if err != nil {
		return "", err
	}
	if !reachOfCaller(c).Allows(branch) {
		return "", ErrBranchForbidden
	}
	return branch, nil
}

// Press runs one branch's finance charges for a house month, as the caller.
func (s *FinanceChargeService) Press(ctx context.Context, in FinanceChargeRequest) (FinanceChargeRunResult, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return FinanceChargeRunResult{}, err
	}
	branch, err := s.branch(c, in.BranchID)
	if err != nil {
		return FinanceChargeRunResult{}, err
	}
	return s.w.RunFinanceCharges(ctx, FinanceChargeRequest{BranchID: branch, Month: in.Month, ActorID: c.UserID})
}

// Runs reads a branch's runs, newest month first, each with its charges.
func (s *FinanceChargeService) Runs(ctx context.Context, rawBranch string) ([]FinanceChargeRunMonth, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return nil, err
	}
	branch, err := s.branch(c, rawBranch)
	if err != nil {
		return nil, err
	}
	return s.repo.FinanceChargeRuns(ctx, branch)
}

// SetWaiver waives a customer's finance charges. isController is the caller's role answer, resolved
// from the staff context by the handler and never out of the body.
func (s *FinanceChargeService) SetWaiver(ctx context.Context, customerID, reason string, isController bool) (FinanceChargeWaiverView, error) {
	return s.waive(ctx, customerID, reason, isController, true)
}

// ClearWaiver releases a waived customer back to the runs.
func (s *FinanceChargeService) ClearWaiver(ctx context.Context, customerID, reason string, isController bool) (FinanceChargeWaiverView, error) {
	return s.waive(ctx, customerID, reason, isController, false)
}

func (s *FinanceChargeService) waive(ctx context.Context, rawCustomer, reason string, isController, set bool) (FinanceChargeWaiverView, error) {
	c, err := s.caller(ctx)
	if err != nil {
		return FinanceChargeWaiverView{}, err
	}
	customerID, err := canonicalUUID(rawCustomer, "customer id")
	if err != nil {
		return FinanceChargeWaiverView{}, err
	}
	in := FinanceChargeWaiverInput{CustomerID: customerID, Reason: reason, ActorID: c.UserID, IsController: isController}
	if set {
		return s.waivers.SetWaiver(ctx, in)
	}
	return s.waivers.ClearWaiver(ctx, in)
}

// FinanceChargeRuns reads a branch's claimed charges with their documents' numbers, newest month first
// and customers in id order, grouped by month.
func (r *Repository) FinanceChargeRuns(ctx context.Context, branchID string) ([]FinanceChargeRunMonth, error) {
	if r.pool == nil {
		return nil, errNoDB
	}
	rows, err := r.pool.Query(ctx, `
		SELECT f.month, f.as_of_date::text, f.customer_id::text, f.past_due_cents, f.charge_cents,
		       COALESCE(f.invoice_id::text, ''), COALESCE(i.invoice_no, '')
		  FROM finance_charge_runs f
		  LEFT JOIN invoices i ON i.id = f.invoice_id
		 WHERE f.branch_id = $1::uuid
		 ORDER BY f.month DESC, f.customer_id`, branchID)
	if err != nil {
		return nil, fmt.Errorf("billing: read the finance charge runs: %w", err)
	}
	defer rows.Close()
	out := []FinanceChargeRunMonth{}
	for rows.Next() {
		var month, asOf string
		var ch FinanceCharge
		if err := rows.Scan(&month, &asOf, &ch.CustomerID, &ch.PastDueCents, &ch.ChargeCents, &ch.InvoiceID, &ch.InvoiceNo); err != nil {
			return nil, fmt.Errorf("billing: scan a finance charge run: %w", err)
		}
		if n := len(out); n == 0 || out[n-1].Month != month {
			out = append(out, FinanceChargeRunMonth{BranchID: branchID, Month: month, MonthEnd: asOf, Charges: []FinanceCharge{}})
		}
		m := &out[len(out)-1]
		m.Charged++
		m.ChargedCents += ch.ChargeCents
		m.PastDueCents += ch.PastDueCents
		m.Charges = append(m.Charges, ch)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("billing: the finance charge runs: %w", err)
	}
	return out, nil
}

// -- the handler ---------------------------------------------------------------------

// FinanceChargeRouteHandler serves the Controller's finance charge doors.
type FinanceChargeRouteHandler struct {
	svc      *FinanceChargeService
	userLink identity.UserIDLinker
}

// NewFinanceChargeRouteHandler wires the handler.
func NewFinanceChargeRouteHandler(svc *FinanceChargeService) *FinanceChargeRouteHandler {
	return &FinanceChargeRouteHandler{svc: svc}
}

// WithUserLinker sets the users.id linker the stamp carries. Without it no caller has a staff record
// and every door refuses, which is the fail closed answer.
func (h *FinanceChargeRouteHandler) WithUserLinker(link identity.UserIDLinker) *FinanceChargeRouteHandler {
	h.userLink = link
	return h
}

// RegisterRoutes self-registers the doors under /api/v1/ar, the receivables namespace. Every route is
// the leadership tier, narrowed to the Controller roles in the handler.
func (h *FinanceChargeRouteHandler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/ar/finance-charges", g.RequireAdmin(h.staff(h.press)))
	r.Handle("GET /api/v1/ar/finance-charges", g.RequireAdmin(h.staff(h.list)))
	r.Handle("PUT /api/v1/ar/customers/{customerID}/finance-charge-waiver", g.RequireAdmin(h.staff(h.waive)))
	r.Handle("POST /api/v1/ar/customers/{customerID}/finance-charge-waiver/clear", g.RequireAdmin(h.staff(h.clear)))
}

func (h *FinanceChargeRouteHandler) staff(next guards.StaffHandler) guards.StaffHandler {
	return func(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
		if !holdsRole(caller, financeChargeRoles) {
			httpx.Error(w, http.StatusForbidden, "billing: finance charges are the Controller's; this role may not press, read or waive them")
			return
		}
		next(w, identity.StampRequest(r, caller, h.userLink), caller)
	}
}

func holdsRole(caller guards.StaffCaller, roles []string) bool {
	for _, role := range roles {
		if strings.EqualFold(caller.RoleID, role) {
			return true
		}
	}
	return false
}

func (h *FinanceChargeRouteHandler) press(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var in FinanceChargeRequest
	if err := decode(r, &in); err != nil {
		h.writeErr(w, err)
		return
	}
	res, err := h.svc.Press(r.Context(), in)
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

func (h *FinanceChargeRouteHandler) list(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	items, err := h.svc.Runs(r.Context(), r.URL.Query().Get("branch_id"))
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": nil})
}

type financeChargeWaiverBody struct {
	Reason string `json:"reason"`
}

func (h *FinanceChargeRouteHandler) waive(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	h.waiver(w, r, caller, true)
}

func (h *FinanceChargeRouteHandler) clear(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	h.waiver(w, r, caller, false)
}

func (h *FinanceChargeRouteHandler) waiver(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller, set bool) {
	var in financeChargeWaiverBody
	if err := decode(r, &in); err != nil {
		h.writeErr(w, err)
		return
	}
	isController := strings.EqualFold(caller.RoleID, RoleCodeFinanceChargeWaiver)
	var view FinanceChargeWaiverView
	var err error
	if set {
		view, err = h.svc.SetWaiver(r.Context(), r.PathValue("customerID"), in.Reason, isController)
	} else {
		view, err = h.svc.ClearWaiver(r.Context(), r.PathValue("customerID"), in.Reason, isController)
	}
	if err != nil {
		h.writeErr(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, view)
}

// writeErr maps a refusal to its status and words.
func (h *FinanceChargeRouteHandler) writeErr(w http.ResponseWriter, err error) {
	var bad httpx.BadRequestError
	var notEnded *FinanceChargeMonthNotEndedError
	var role *FinanceChargeWaiverRoleError
	switch {
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Error())
	case errors.Is(err, errBodyNotJSON):
		httpx.Error(w, http.StatusBadRequest, errBodyNotJSON.Error())
	case errors.Is(err, ErrFinanceChargeWaiverReasonRequired):
		httpx.Error(w, http.StatusBadRequest, err.Error())
	case errors.As(err, &notEnded):
		httpx.Error(w, http.StatusUnprocessableEntity, notEnded.Error())
	case errors.As(err, &role):
		httpx.Error(w, http.StatusForbidden, role.Error())
	case errors.Is(err, ErrBranchForbidden):
		httpx.Error(w, http.StatusForbidden, BranchForbiddenWords)
	case errors.Is(err, errFinanceChargeNotStamped):
		httpx.Error(w, http.StatusForbidden, FinanceChargeNotStampedWords)
	case errors.Is(err, errNoDB):
		httpx.Error(w, http.StatusServiceUnavailable, "the database is not configured")
	default:
		slog.Error("finance charge door", "err", err)
		httpx.Error(w, http.StatusInternalServerError, "internal error")
	}
}
