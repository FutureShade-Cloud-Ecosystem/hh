package assist

// handler.go, the HTTP surface, and the ONE file in this package that imports identity.
//
// The seam is the same one internal/dashboard uses: the handler asks the existing middleware who is
// calling, converts that into the package's own Caller value, and everything below this file sees
// strings and ints. Two consequences, both intended:
//
//   - nothing under this file can RE-DERIVE authority. There is no identity helper in scope for the
//     registry or the service to reach for, so the caller they act on is the caller the middleware
//     resolved, full stop.
//   - the package is testable without an HTTP request or a token.
//
// ROUTE AUTHORITY, AND WHY THE SPLIT IS WHERE IT IS
//
//	GET  /api/v1/assist/commands     staff , the commands I may run (already filtered by tier)
//	POST /api/v1/assist/command      staff , run one, as me
//	POST /api/v1/assist/chat         staff , ask, as me
//	GET  /api/v1/assist/me           staff , my budget, and my spend IF the toggle allows
//	GET  /api/v1/assist/admin/...    ADMIN , everyone's spend, the limits, the settings
//	PUT/DELETE /api/v1/assist/admin/... ADMIN
//
// My own numbers are staff-gated; everyone else's are admin-gated. The one subtlety is /me, where
// the SPEND half is additionally behind the spend_visible_to_non_admin toggle (DIB-434 house
// pattern), so a deployment that considers per-person usage sensitive can hide it without hiding
// the budget, which a user needs in order to understand a refusal.

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"log/slog"
	"net/http"
	"sort"
	"strings"
)

// maxBodyBytes bounds a chat POST. A question plus six turns of history is a few kilobytes; 64 KiB
// is generous and still refuses a body meant to exhaust memory.
const maxBodyBytes = 64 << 10

// Handler exposes the assistant.
type Handler struct {
	userLink identity.UserIDLinker
	svc      *Service
	log      *slog.Logger
	trail    PaymentTrailReader

	// costRoles and costBranches decide the claim's cost fields (WithCostGate).
	costRoles    CostRoles
	costBranches BranchReader
	poPolicy     PurchaseCostPolicies
	channels     ChannelMembership
}

// PaymentTrailReader says whether a caller holds the payment trail reads (ruling 87). The composition
// root supplies it over the role gate; this package names no role and no level.
type PaymentTrailReader interface {
	HoldsPaymentTrail(ctx context.Context, platformUserID, orgID string) (bool, error)
}

// WithPaymentTrail binds the reader. Unbound, no caller holds the trail: the commands that list
// payments leave them out for everyone.
func (h *Handler) WithPaymentTrail(t PaymentTrailReader) *Handler {
	h.trail = t
	return h
}

// CallerFor is the Caller a request resolves to on the routes that run commands: the guard-resolved staff
// context, the cost and margin fields from the dealer's cost rule, and the payment trail answer. Exported so
// the parity suite builds callers by the same path the routes do.
func (h *Handler) CallerFor(r *http.Request, sc guards.StaffCaller) (Caller, error) {
	return h.callerFor(r, sc)
}

// callerFor is CallerFrom plus the payment trail answer, for the routes that run commands.
func (h *Handler) callerFor(r *http.Request, sc guards.StaffCaller) (Caller, error) {
	c := CallerFrom(r)
	c.SessionKey = sc.SessionID
	h.costFor(r.Context(), &c)
	h.poCostFor(r.Context(), &c)
	h.channelsFor(r.Context(), &c)
	if h.trail == nil {
		return c, nil
	}
	held, err := h.trail.HoldsPaymentTrail(r.Context(), sc.PlatformUserID, sc.OrgID)
	if err != nil {
		return Caller{}, err
	}
	c.PaymentTrail = held
	return c, nil
}

// CostRoles is the dealer's cost and margin role list: the one predicate that decides cost visibility on
// every module's screen (inventory/cost_gate.go, quote, pricing, tasks, dispatch). The composition root wires
// the role configuration behind it.
type CostRoles interface {
	SeesCostAndMargin(role string) (bool, error)
}

// WithCostGate wires the cost rule the signed claim's cost fields follow, and the branch reader used to name
// every branch for a global caller on the list. Without it no caller has cost or margin (fail closed).
func (h *Handler) WithCostGate(roles CostRoles, branches BranchReader) *Handler {
	h.costRoles, h.costBranches = roles, branches
	return h
}

// PurchaseCostPolicies is the per branch purchasing policy the purchasing screens ask for what the dealer pays
// on a purchase order: purchasing.Service.PolicyFor(branch).CostVisibility, which resolves the branch row, else
// the dealer default, and fails closed to MANAGER_UP. The composition root wires the purchasing service behind it.
type PurchaseCostPolicies interface {
	CostVisibility(ctx context.Context, branchID string) string
}

// The three policy words, as internal/settings spells them (VisibilityManagerUp, VisibilityAllStaff,
// VisibilityHidden). Spelled here because this package imports no other module.
const (
	policyAllStaff = "ALL_STAFF"
	policyHidden   = "HIDDEN"
)

// WithPurchaseCostPolicy wires the policy that decides the claim's purchase cost branches. Without it no caller
// has purchase cost (fail closed).
func (h *Handler) WithPurchaseCostPolicy(p PurchaseCostPolicies) *Handler {
	h.poPolicy = p
	return h
}

// poCostFor mirrors purchasing's costLensFor, branch by branch: ALL_STAFF allows, HIDDEN refuses, and every
// other word (MANAGER_UP, the seeded default, and where every unknown resolves) allows a manager or better,
// which is tier 1 or 2. A caller with no role sees nothing. Branches are the caller's reach: its grants, or
// every branch for a global caller.
func (h *Handler) poCostFor(ctx context.Context, c *Caller) {
	c.POCostBranchIDs = nil
	if h.poPolicy == nil || !c.HasRole() {
		return
	}
	reach := c.BranchIDs
	if c.Global {
		if h.costBranches == nil {
			return
		}
		bs, err := h.costBranches.Branches(ctx, *c)
		if err != nil {
			return
		}
		reach = nil
		for _, b := range bs {
			reach = append(reach, b.BranchID)
		}
	}
	manager := c.Tier == 1 || c.Tier == 2
	for _, id := range reach {
		switch h.poPolicy.CostVisibility(ctx, id) {
		case policyAllStaff:
			c.POCostBranchIDs = append(c.POCostBranchIDs, id)
		case policyHidden:
		default:
			if manager {
				c.POCostBranchIDs = append(c.POCostBranchIDs, id)
			}
		}
	}
	sort.Strings(c.POCostBranchIDs)
}

// costFor resolves the caller's cost fields exactly as inventory's costAllowed does: the canonical role must
// be known and on the list; any other answer, including an error, is no cost.
func (h *Handler) costFor(ctx context.Context, c *Caller) {
	c.CostBranchIDs, c.Margin = nil, false
	if h.costRoles == nil {
		return
	}
	role, ok := identity.CanonicalForERPCode(c.RoleCode)
	if !ok || role == "" {
		return
	}
	allowed, err := h.costRoles.SeesCostAndMargin(role)
	if err != nil || !allowed {
		return
	}
	c.Margin = true
	if !c.Global {
		c.CostBranchIDs = append([]string(nil), c.BranchIDs...)
		return
	}
	if h.costBranches == nil {
		return
	}
	bs, err := h.costBranches.Branches(ctx, *c)
	if err != nil {
		c.Margin = false
		return
	}
	for _, b := range bs {
		c.CostBranchIDs = append(c.CostBranchIDs, b.BranchID)
	}
	sort.Strings(c.CostBranchIDs)
}

func NewHandler(svc *Service, log *slog.Logger) *Handler { return &Handler{svc: svc, log: log} }

// RegisterRoutes wires the assistant behind the injected guards, matching every sibling module.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("GET /api/v1/assist/commands", g.RequireStaff(h.staff(h.commands)))
	r.Handle("POST /api/v1/assist/command", g.RequireStaff(h.staff(h.runCommand)))
	r.Handle("POST /api/v1/assist/chat", g.RequireStaff(h.staff(h.chat)))
	r.Handle("GET /api/v1/assist/me", g.RequireStaff(h.staff(h.me)))

	r.Handle("GET /api/v1/assist/admin/spend", g.RequireAdmin(h.staff(h.adminSpend)))
	r.Handle("GET /api/v1/assist/admin/limits", g.RequireAdmin(h.staff(h.adminLimits)))
	r.Handle("PUT /api/v1/assist/admin/limits/role/{role}", g.RequireAdmin(h.staff(h.putRoleLimit)))
	r.Handle("PUT /api/v1/assist/admin/limits/user/{user}", g.RequireAdmin(h.staff(h.putUserLimit)))
	r.Handle("DELETE /api/v1/assist/admin/limits/user/{user}", g.RequireAdmin(h.staff(h.resetUserLimit)))
	r.Handle("GET /api/v1/assist/admin/settings", g.RequireAdmin(h.staff(h.getSettings)))
	r.Handle("PUT /api/v1/assist/admin/settings", g.RequireAdmin(h.staff(h.putSetting)))
}

// CallerFrom converts the guard-resolved staff context into this package's Caller.
//
// Exported so main.go's adapters can build the SAME Caller when they need one, and so there is
// exactly one conversion in the codebase. The branch list comes from the caller's GRANTS and never
// from a query parameter, an assistant that let you ask about another branch by editing a URL
// would be a friendlier version of the thing branch scoping exists to prevent.
func CallerFrom(r *http.Request) Caller {
	sc, _ := identity.StaffContextFromContext(r.Context())
	c := Caller{
		Address:  clientAddress(r),
		UserID:   sc.UserID,
		RoleCode: sc.RoleCode,
		Tier:     int(sc.Tier),
		Global:   sc.Scope == identity.ScopeGlobal,
		IsAdmin:  sc.IsAdmin(),
	}
	if !c.Global {
		for id, ok := range sc.Branches {
			if ok {
				c.BranchIDs = append(c.BranchIDs, id)
			}
		}
		// Deterministic order so a downstream SQL plan and any test assertion are stable.
		sort.Strings(c.BranchIDs)
	}
	return c
}

func (h *Handler) commands(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	// callerFor, not CallerFrom: the catalogue the tool description shows depends on the cost fields.
	c, err := h.callerFor(r, caller)
	if err != nil {
		h.fail(w, err)
		return
	}
	cmds := h.svc.Commands(r.Context(), c)
	cfg := h.svc.Settings(r.Context())
	// The list is ALWAYS a JSON array, never null: `{"commands":null}` and `{"commands":[]}` are
	// different claims to a client that does not write `?? []`, and the difference shows up as a
	// blank panel with no explanation. (The DIB-443 lesson, one layer up.)
	if cmds == nil {
		cmds = []Command{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"commands":     cmds,
		"chat_enabled": cfg.ChatEnabled,
	})
}

type commandBody struct {
	Command string `json:"command"`
	Args    Args   `json:"args"`
}

func (h *Handler) runCommand(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body commandBody
	if !decode(w, r, &body) {
		return
	}
	c, err := h.callerFor(r, caller)
	if err != nil {
		h.fail(w, err)
		return
	}
	res, err := h.svc.RunCommand(r.Context(), c, body.Command, body.Args)
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

type chatBody struct {
	Question string `json:"question"`
	// ConversationID, when present, replaces History: the history is rebuilt on the server from the
	// exchange log and History and ran are ignored.
	ConversationID string    `json:"conversation_id"`
	History        []Message `json:"history"`
}

func (h *Handler) chat(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body chatBody
	if !decode(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Question) == "" {
		httpx.Error(w, http.StatusBadRequest, "Ask me a question and I'll have a look.")
		return
	}
	c, err := h.callerFor(r, caller)
	if err != nil {
		h.fail(w, err)
		return
	}
	ans, err := h.svc.AskConversation(r.Context(), c, body.ConversationID, body.Question, body.History)
	if err != nil {
		h.fail(w, err)
		return
	}
	// Usage is stripped for a caller who may not see spend. The service always computes it, the
	// ledger needs it, and the HANDLER decides who may read it, which keeps the toggle in one
	// place instead of threading a visibility flag through the service.
	if ans.Usage != nil && !h.svc.MySpendVisible(r.Context(), c) {
		ans.Usage = nil
	}
	if ans.Results == nil {
		ans.Results = []Result{}
	}
	httpx.WriteJSON(w, http.StatusOK, ans)
}

// me is the chat surface's header: what I may spend, what I have spent, and whether chat is on.
func (h *Handler) me(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	c := CallerFrom(r)
	ctx := r.Context()
	budget, err := h.svc.Budget(ctx, c)
	if err != nil {
		h.fail(w, err)
		return
	}
	cfg := h.svc.Settings(ctx)
	out := map[string]any{
		"budget":        budget,
		"chat_enabled":  cfg.ChatEnabled,
		"provider":      cfg.Provider,
		"spend_visible": h.svc.MySpendVisible(ctx, c),
		"is_admin":      c.IsAdmin,
	}
	// The BUDGET is always visible even when the spend is not. A person refused for hitting a limit
	// has to be able to see what the limit was; hiding it turns a policy into a mystery.
	if h.svc.MySpendVisible(ctx, c) {
		spend, err := h.svc.MonthSpend(ctx, c.UserID)
		if err != nil {
			h.fail(w, err)
			return
		}
		out["spend"] = spend
		out["over_budget"] = OverBudget(spend, budget)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) adminSpend(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	rows, err := h.svc.SpendReport(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if rows == nil {
		rows = []UserSpend{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"month": h.svc.asOfMonth().Format("2006-01"),
		"users": rows,
	})
}

func (h *Handler) adminLimits(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	v, err := h.svc.Limits(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, v)
}

type budgetBody struct {
	Tokens int `json:"tokens"`
}

func (h *Handler) putRoleLimit(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body budgetBody
	if !decode(w, r, &body) {
		return
	}
	role := strings.TrimSpace(r.PathValue("role"))
	if role == "" {
		httpx.Error(w, http.StatusBadRequest, "Which role?")
		return
	}
	if err := h.svc.SetRoleBudget(r.Context(), role, body.Tokens, CallerFrom(r).UserID); err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"role_code": role, "tokens": body.Tokens})
}

func (h *Handler) putUserLimit(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body budgetBody
	if !decode(w, r, &body) {
		return
	}
	user := strings.TrimSpace(r.PathValue("user"))
	if user == "" {
		httpx.Error(w, http.StatusBadRequest, "Which person?")
		return
	}
	if err := h.svc.SetUserBudget(r.Context(), user, body.Tokens, CallerFrom(r).UserID); err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_id": user, "tokens": body.Tokens, "source": SourceUser})
}

// resetUserLimit is reset-to-role. A DELETE, and it is IDEMPOTENT: resetting a person who has no
// override succeeds. The alternative (404) would make an admin wonder whether the reset worked.
func (h *Handler) resetUserLimit(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	user := strings.TrimSpace(r.PathValue("user"))
	if user == "" {
		httpx.Error(w, http.StatusBadRequest, "Which person?")
		return
	}
	if err := h.svc.ResetUserBudget(r.Context(), user); err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"user_id": user, "source": SourceRole})
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	cfg := h.svc.Settings(r.Context())
	// key_env_var is the NAME of the environment variable, and key_present is a BOOLEAN. Neither
	// this endpoint nor any other in this package can return a character of the credential, there
	// is no field here that could carry one. (SOP-1.)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"settings":    cfg,
		"key_env_var": EnvAPIKey,
		"key_present": h.svc.KeyPresent(),
	})
}

type settingBody struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func (h *Handler) putSetting(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var body settingBody
	if !decode(w, r, &body) {
		return
	}
	if err := h.svc.SetSetting(r.Context(), strings.TrimSpace(body.Key), strings.TrimSpace(body.Value), CallerFrom(r).UserID); err != nil {
		h.fail(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"key": body.Key, "value": body.Value})
}

// -- plumbing -----------------------------------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		httpx.Error(w, http.StatusBadRequest, "That request wasn't readable.")
		return false
	}
	return true
}

// fail maps this package's errors onto statuses.
//
// FOUR DISTINCT STATUSES, because the four situations have four different remedies and DIB-443 is
// the ticket that proves a user reads the difference. Only the last branch is a 500, and only the
// last branch is logged as an error, a typo must never fill an error log.
func (h *Handler) fail(w http.ResponseWriter, err error) {
	var bad httpx.BadRequestError
	var unavailable UnavailableError
	switch {
	case errors.As(err, &bad):
		httpx.Error(w, http.StatusBadRequest, bad.Msg)
	case errors.Is(err, ErrUnknownCommand):
		httpx.Error(w, http.StatusNotFound, "I don't know that command.")
	case errors.Is(err, ErrRefused), errors.Is(err, ErrDisabled):
		// One message for both, deliberately: telling a user that a command exists but is switched
		// off for them is a slightly more informative way of confirming it exists.
		httpx.Error(w, http.StatusForbidden, "That isn't something you can look up here.")
	case errors.As(err, &unavailable):
		httpx.Error(w, http.StatusServiceUnavailable, "That lookup isn't switched on in this system yet.")
	default:
		if h.log != nil {
			h.log.Error("assist: request failed", "err", err)
		}
		httpx.Error(w, http.StatusInternalServerError, "Something went wrong on our side.")
	}
}

// ChannelMembership is the House Messages membership read: the ids of the channels a person is in right now, as
// the messaging module's own gates answer it (materialised membership, archived rooms left out, the money room
// fence applied), read only. The composition root wires the messaging service behind it.
type ChannelMembership interface {
	ChannelIDs(ctx context.Context, userID string) ([]string, error)
}

// WithChannelMembership wires the read that fills the claim's channel ids. Without it no caller has any channel
// (fail closed).
func (h *Handler) WithChannelMembership(m ChannelMembership) *Handler {
	h.channels = m
	return h
}

// channelsFor resolves the caller's channels at request time. Every failure is no channel: an unwired read, a
// caller with no role or no user id, or an error.
func (h *Handler) channelsFor(ctx context.Context, c *Caller) {
	c.ChannelIDs = nil
	if h.channels == nil || !c.HasRole() || c.UserID == "" {
		return
	}
	ids, err := h.channels.ChannelIDs(ctx, c.UserID)
	if err != nil {
		return
	}
	c.ChannelIDs = append([]string(nil), ids...)
	sort.Strings(c.ChannelIDs)
}
