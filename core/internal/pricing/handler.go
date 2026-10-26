package pricing

import (
	"context"
	"encoding/json"
	"errors"
	coreidentity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// uuidRe validates a uuid BEFORE it reaches the repo's `::uuid` cast or an `id=$1` lookup, so a
// malformed value returns 400 instead of a Postgres 22P02 → 500 (findings #5 + #9).
var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// requirePathUUID validates a REQUIRED path id (e.g. /rules/{id}); a non-uuid → 400 (finding #9).
func requirePathUUID(w http.ResponseWriter, id, field string) bool {
	if !uuidRe.MatchString(id) {
		httpx.Error(w, http.StatusBadRequest, field+" must be a uuid")
		return false
	}
	return true
}

// optionalBodyUUID validates an OPTIONAL body scope-dimension uuid: empty is allowed (an unset
// dim, or the "clear this dim" signal), but a non-empty non-uuid → 400 (finding #9). This guards
// customer_id/category_id/product_id/branch_id before the repo casts them.
func optionalBodyUUID(w http.ResponseWriter, val, field string) bool {
	if val != "" && !uuidRe.MatchString(val) {
		httpx.Error(w, http.StatusBadRequest, field+" must be a uuid")
		return false
	}
	return true
}

// Handler exposes the pricing module's REST surface: the read-only resolve, the rule
// CRUD, and the A-07 Tier-B admin editor surface, the category margin-policy grid,
// customer overrides, the audit-log viewer and the low-margin batch preview. Reads are
// Sales+ (readGuard / RequireStaff); every WRITE is Manager+ (writeGuard / RequireManager),
// enforced by binding each route to the injected guard (the keystone RBAC point). Deferred:
// the quantity-break editor (gated on D-PA-6 values) and list-price recompute (unbuilt
// costing seam), see RegisterRoutes.
type Handler struct {
	userLink  identity.UserIDLinker
	svc       *Service
	log       *slog.Logger
	costRoles CostRoles
	custReach CustomerReach
}

// CustomerReach is the customer module's own reach rule, declared here by the consumer because pricing
// reads pricing tables only. It answers whether the caller (the stamped staff context on ctx) may see
// the account: false for an account homed to a branch the caller cannot reach and for an unknown one,
// the same answer the customer read gives as not found. The composition root implements it over the
// customer service.
type CustomerReach interface {
	CustomerReachable(ctx context.Context, customerID string) (bool, error)
}

// WithCustomerReach wires the customer reach rule the resolve and low margin doors hold a named
// customer to. CustomerReachWired is the boot check that it was.
func (h *Handler) WithCustomerReach(c CustomerReach) *Handler {
	h.custReach = c
	return h
}

// CustomerReachWired reports whether the customer reach rule is wired.
func (h *Handler) CustomerReachWired() bool { return h.custReach != nil }

// fenceLine holds one priced line to the caller's reach before anything is resolved: a branch the caller
// is not granted is a 403 in words (the till's wording for a branch outside the grants), and a named
// customer the caller cannot reach is a 404, as the account read answers it. A line naming no branch is
// the dealer wide picture and stays answered. It reports whether the line may go on. A handler built
// without the customer rule (the DB free unit tests) skips the customer half; the composition root
// refuses to boot without it.
func (h *Handler) fenceLine(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller, branchID, customerID string) bool {
	if branchID != "" && !caller.CanAccessBranch(branchID) {
		httpx.Error(w, http.StatusForbidden, "that branch is outside your grants")
		return false
	}
	return h.fenceCustomer(w, r, customerID)
}

// fenceCustomer is the one customer answer every pricing door gives (UX-89 round 2): an account the caller
// cannot reach is answered exactly as an id that is no account, 404 "customer not found", by the customer
// module's own rule through the seam. It reports whether the request may go on. A handler built without the
// seam (the DB free unit tests) skips it; the composition root refuses to boot without it.
func (h *Handler) fenceCustomer(w http.ResponseWriter, r *http.Request, customerID string) bool {
	if customerID == "" || h.custReach == nil {
		return true
	}
	ok, err := h.custReach.CustomerReachable(r.Context(), customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return false
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "customer not found")
		return false
	}
	return true
}

// fenceExistingRule and fenceExistingOverride read the row a write names by id and hold it to its account's
// reach before the write; a row that is not there falls through to the write's own not found.
func (h *Handler) fenceExistingRule(w http.ResponseWriter, r *http.Request, id string) bool {
	if h.custReach == nil {
		return true
	}
	rule, err := h.svc.GetRule(r.Context(), id)
	if err != nil {
		return true
	}
	return h.fenceOwnedRow(w, r, rule.CustomerID)
}

func (h *Handler) fenceExistingOverride(w http.ResponseWriter, r *http.Request, id string) bool {
	if h.custReach == nil {
		return true
	}
	o, err := h.svc.store.GetOverride(r.Context(), id)
	if err != nil {
		return true
	}
	return h.fenceOwnedRow(w, r, o.CustomerID)
}

// fenceOwnedRow is fenceCustomer for a row found by its own id (a rule or an override): the row of an
// unreachable account answers what a missing row answers, 404 "not found".
func (h *Handler) fenceOwnedRow(w http.ResponseWriter, r *http.Request, customerID string) bool {
	if customerID == "" || h.custReach == nil {
		return true
	}
	ok, err := h.custReach.CustomerReachable(r.Context(), customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return false
	}
	if !ok {
		httpx.Error(w, http.StatusNotFound, "not found")
		return false
	}
	return true
}

func NewHandler(svc *Service, log *slog.Logger) *Handler {
	if log == nil {
		log = slog.Default()
	}
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes self-registers on the shared mux. readGuard = any ACTIVE staff (Sales+);
// writeGuard = Tier≤Manager (pricing policy is management information). Both are injected
// from cmd/server so a route is auth-gated by construction.
func (h *Handler) RegisterRoutes(r httpx.Registrar, g *guards.Guards) {
	if g == nil {
		panic("pricing.RegisterRoutes: guards are required (routes must be auth-gated)")
	}
	r.Handle("POST /api/v1/pricing/resolve", g.RequireStaff(h.staff(h.resolve)))

	// Rules (US-P04-P08). Reads = Sales+; writes = Manager+.
	r.Handle("GET /api/v1/pricing/rules", g.RequireStaff(h.staff(h.listRules)))
	r.Handle("GET /api/v1/pricing/rules/{id}", g.RequireStaff(h.staff(h.getRule)))
	r.Handle("POST /api/v1/pricing/rules", g.RequireManager(h.staff(h.createRule)))
	r.Handle("PUT /api/v1/pricing/rules/{id}", g.RequireManager(h.staff(h.updateRule)))
	r.Handle("DELETE /api/v1/pricing/rules/{id}", g.RequireManager(h.staff(h.deleteRule)))

	// Category margin-policy grid (US-P01/02/03, the keystone).
	r.Handle("GET /api/v1/pricing/category-policies", g.RequireStaff(h.staff(h.listCategoryPolicies)))
	r.Handle("PUT /api/v1/pricing/category-policies/{categoryId}", g.RequireManager(h.staff(h.upsertCategoryPolicy)))

	// Customer overrides (US-P09).
	r.Handle("GET /api/v1/pricing/customers/{customerId}/overrides", g.RequireStaff(h.staff(h.listOverrides)))
	r.Handle("POST /api/v1/pricing/customers/{customerId}/overrides", g.RequireManager(h.staff(h.createOverride)))
	r.Handle("PUT /api/v1/pricing/overrides/{id}", g.RequireManager(h.staff(h.updateOverride)))
	r.Handle("DELETE /api/v1/pricing/overrides/{id}", g.RequireManager(h.staff(h.deleteOverride)))

	// Audit-log viewer (US-P12) + low-margin batch preview (US-P13). Read-only (Sales+).
	r.Handle("GET /api/v1/pricing/audit", g.RequireStaff(h.staff(h.listAudit)))
	r.Handle("POST /api/v1/pricing/reports/low-margin", g.RequireStaff(h.staff(h.lowMarginReport)))
	// D-PA-3 auto-populated daily low-margin report, the live-sales scan. Read-only (Sales+),
	// exactly like the batch preview above.
	r.Handle("GET /api/v1/pricing/reports/low-margin/scan", g.RequireStaff(h.staff(h.lowMarginScan)))

	// Quantity-break editor (A-07 Tier-B). Under D1 a break prices off the retail anchor and
	// REPLACES the resolved price when strictly cheaper (service.go step 6); the editor lets the
	// dealer principal configure + review the PROVISIONAL skid trigger values. Reads =
	// Sales+; writes = Manager+ (the keystone RBAC point, a break is real-money config).
	r.Handle("GET /api/v1/pricing/quantity-breaks", g.RequireStaff(h.staff(h.listQuantityBreaks)))
	r.Handle("GET /api/v1/pricing/quantity-breaks/{id}", g.RequireStaff(h.staff(h.getQuantityBreak)))
	r.Handle("POST /api/v1/pricing/quantity-breaks", g.RequireManager(h.staff(h.createQuantityBreak)))
	r.Handle("PUT /api/v1/pricing/quantity-breaks/{id}", g.RequireManager(h.staff(h.updateQuantityBreak)))
	r.Handle("DELETE /api/v1/pricing/quantity-breaks/{id}", g.RequireManager(h.staff(h.deleteQuantityBreak)))

	// A-08 Price Change Log, batch price updates, CSV imports, append-only change log.
	h.registerBatchRoutes(r, g)

	// CP0-6: the pricing editors' floor warning (preview, then the confirmation in the log).
	h.registerFloorRoutes(r, g)

	// The price-file import (0198): upload → map → PREVIEW → apply. Its preview is behind the READ
	// guard on purpose and its apply behind the write guard, pricefile_handler.go argues both.
	h.registerPriceFileRoutes(r, g)

	// DEFERRED (out of this build): the list-price GET/PUT + POST /pricing/list-prices/recompute
	// depends on an UNBUILT costing seam (D-PA-3/US-P15), do not promote into core.
}

// resolveRequest mirrors the §4 payload plus the caller-provided cost basis. cost_cents
// is basis-agnostic (the cost-basis dropdown is owned by costing/stock, not A-07) and is
// optional so a read-only inspector can exercise the margin gate without a costing module.
type resolveRequest struct {
	CustomerID   string  `json:"customer_id"`
	CustomerType string  `json:"customer_type"`
	ProductID    string  `json:"product_id"`
	CategoryID   string  `json:"category_id"`
	BranchID     string  `json:"branch_id"`
	QtyEntered   float64 `json:"qty_entered"`
	UOMEntered   string  `json:"uom_entered"`
	CostCents    int64   `json:"cost_cents"`
	// HasApprovedOverride is READ AND DROPPED (the QD-1 review's P2-5,
	// closing D3 on this route): this is the read-only preview route, and
	// through the flag any approved margin card answered "granted" whatever
	// its subject, even used up, planting false BYPASS rows in the pricing
	// log. A below-floor price that may be cleared is cleared by the quote
	// module's own floor decision, which binds the request to the line's
	// own row. The wire name is unchanged so no caller breaks.
	HasApprovedOverride bool `json:"has_approved_override"`
	// E3 unpark: the document being priced, so a BYPASS audit row can be clicked through to it.
	// Optional, every pre-W4 caller omits it and gets the previous behaviour exactly.
	DocRefType string `json:"doc_ref_type"`
	DocRefID   string `json:"doc_ref_id"`
}

func (h *Handler) resolve(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req resolveRequest
	if !decode(w, r, &req) {
		return
	}
	// The doc ref is a uuid heading for a `::uuid` cast on INSERT. Validating it HERE (400) rather
	// than letting normaliseAuditRef drop it silently is the difference between a caller learning
	// their id is malformed and an audit row quietly losing its drill-through.
	if !optionalBodyUUID(w, req.DocRefID, "doc_ref_id") {
		return
	}
	if req.DocRefType != "" && !docRefTypes[req.DocRefType] {
		httpx.Error(w, http.StatusBadRequest, "doc_ref_type must be 'quote' or 'order'")
		return
	}
	if !h.fenceLine(w, r, caller, req.BranchID, req.CustomerID) {
		return
	}
	bd, err := h.svc.ResolvePrice(r.Context(), ResolveInput{
		CustomerID:   req.CustomerID,
		CustomerType: req.CustomerType,
		ProductID:    req.ProductID,
		CategoryID:   req.CategoryID,
		BranchID:     req.BranchID,
		QtyEntered:   req.QtyEntered,
		UOMEntered:   req.UOMEntered,
		CostCents:    req.CostCents,
		// req.HasApprovedOverride is deliberately NOT carried: a preview
		// requests no override, the quote floor path is the only door.
		// Attribute the resolve so an enforcement-disabled BYPASS row records WHO ran it
		// (R-1; service.go BYPASS path), never a silent, unattributed bypass.
		UserID:     staffUserID(r),
		DocRefType: req.DocRefType,
		DocRefID:   req.DocRefID,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, bd)
}

func (h *Handler) listRules(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	categoryID := q.Get("category_id")
	if categoryID != "" && !uuidRe.MatchString(categoryID) {
		httpx.Error(w, http.StatusBadRequest, "category_id must be a uuid")
		return
	}
	rules, err := h.svc.ListRules(r.Context(), RuleFilter{
		CustomerType: q.Get("customer_type"),
		CategoryID:   categoryID,
		Scope:        q.Get("scope"),
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	// A rule scoped to one account is that account's price: the list holds only the rules of accounts the
	// caller reaches, as if an unreachable account had no rules (the reach is asked once per account).
	if h.custReach != nil {
		seen := map[string]bool{}
		kept := rules[:0:0]
		for _, rl := range rules {
			if rl.CustomerID != "" {
				ok, known := seen[rl.CustomerID]
				if !known {
					var rerr error
					if ok, rerr = h.custReach.CustomerReachable(r.Context(), rl.CustomerID); rerr != nil {
						h.writeErr(w, r, rerr)
						return
					}
					seen[rl.CustomerID] = ok
				}
				if !ok {
					continue
				}
			}
			kept = append(kept, rl)
		}
		rules = kept
	}
	if rules == nil {
		rules = []Rule{}
	}
	httpx.WriteJSON(w, http.StatusOK, rules)
}

// rulePayload is the write body for create/update.
type rulePayload struct {
	Scope        string `json:"scope"`
	CustomerID   string `json:"customer_id"`
	CustomerType string `json:"customer_type"`
	CategoryID   string `json:"category_id"`
	ProductID    string `json:"product_id"`
	BranchID     string `json:"branch_id"`
	CalcType     string `json:"calc_type"`
	PriceCents   *int64 `json:"price_cents"`
	DiscountBps  *int64 `json:"discount_bps"`
	MarkupBps    *int64 `json:"markup_bps"`
	PriceUOM     string `json:"price_uom"`
	Priority     int64  `json:"priority"`
	DetailText   string `json:"detail_text"`
	Version      int64  `json:"version"`
}

func (p rulePayload) toRule() Rule {
	return Rule{
		Scope: p.Scope, CustomerID: p.CustomerID, CustomerType: p.CustomerType,
		CategoryID: p.CategoryID, ProductID: p.ProductID, BranchID: p.BranchID,
		CalcType: p.CalcType, PriceCents: p.PriceCents, DiscountBps: p.DiscountBps,
		MarkupBps: p.MarkupBps, PriceUOM: p.PriceUOM, Priority: p.Priority, DetailText: p.DetailText,
	}
}

func (h *Handler) createRule(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var p rulePayload
	if !decode(w, r, &p) {
		return
	}
	// Validate the body scope-dimension uuids before they reach the repo's casts (finding #9).
	if !optionalBodyUUID(w, p.CustomerID, "customer_id") || !optionalBodyUUID(w, p.CategoryID, "category_id") ||
		!optionalBodyUUID(w, p.ProductID, "product_id") || !optionalBodyUUID(w, p.BranchID, "branch_id") {
		return
	}
	if !h.fenceCustomer(w, r, p.CustomerID) {
		return
	}
	out, err := h.svc.CreateRule(r.Context(), p.toRule(), staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": out.ID, "version": out.Version})
}

func (h *Handler) getRule(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	rule, err := h.svc.GetRule(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if !h.fenceOwnedRow(w, r, rule.CustomerID) {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rule)
}

// ruleUpdatePayload is the PATCH body for editing a rule. Pointer fields distinguish
// "absent → leave unchanged" from "present → set" (incl. priority 0, is_active false, a
// validity bound), so the editor can retire a rule and edit priority 0, the gap the old
// non-pointer payload could not express (R-3).
type ruleUpdatePayload struct {
	Scope        *string `json:"scope"`
	CustomerID   *string `json:"customer_id"`
	CustomerType *string `json:"customer_type"`
	CategoryID   *string `json:"category_id"`
	ProductID    *string `json:"product_id"`
	BranchID     *string `json:"branch_id"`
	CalcType     *string `json:"calc_type"`
	PriceCents   *int64  `json:"price_cents"`
	DiscountBps  *int64  `json:"discount_bps"`
	MarkupBps    *int64  `json:"markup_bps"`
	PriceUOM     *string `json:"price_uom"`
	Priority     *int64  `json:"priority"`
	ValidFrom    *string `json:"valid_from"`
	ValidTo      *string `json:"valid_to"`
	DetailText   *string `json:"detail_text"`
	IsActive     *bool   `json:"is_active"`
	Version      int64   `json:"version"`
}

func (h *Handler) updateRule(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	var p ruleUpdatePayload
	if !decode(w, r, &p) {
		return
	}
	// Validate the body scope-dimension uuids (when present); a non-empty non-uuid → 400 (finding #9).
	if !optionalBodyUUIDPtr(w, p.CustomerID, "customer_id") || !optionalBodyUUIDPtr(w, p.CategoryID, "category_id") ||
		!optionalBodyUUIDPtr(w, p.ProductID, "product_id") || !optionalBodyUUIDPtr(w, p.BranchID, "branch_id") {
		return
	}
	// valid_from is start-of-day inclusive; valid_to is END-of-day inclusive so a date-only
	// valid_to covers the whole day (finding #8). Each bound is tri-state: absent → leave, ""
	// → clear, value → set (finding #7).
	vf, clearVF, ok := parseValidityBound(w, p.ValidFrom, "valid_from", false)
	if !ok {
		return
	}
	vt, clearVT, ok := parseValidityBound(w, p.ValidTo, "valid_to", true)
	if !ok {
		return
	}
	patch := RulePatch{
		Scope: p.Scope, CustomerID: p.CustomerID, CustomerType: p.CustomerType,
		CategoryID: p.CategoryID, ProductID: p.ProductID, BranchID: p.BranchID,
		CalcType: p.CalcType, PriceCents: p.PriceCents, DiscountBps: p.DiscountBps,
		MarkupBps: p.MarkupBps, PriceUOM: p.PriceUOM, Priority: p.Priority,
		ValidFrom: vf, ValidTo: vt, ClearValidFrom: clearVF, ClearValidTo: clearVT,
		DetailText: p.DetailText, IsActive: p.IsActive,
	}
	if !h.fenceExistingRule(w, r, id) {
		return
	}
	if p.CustomerID != nil && !h.fenceCustomer(w, r, *p.CustomerID) {
		return
	}
	out, err := h.svc.UpdateRule(r.Context(), id, patch, p.Version, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// deleteRule soft-retires a rule (is_active=false) under optimistic version. The version
// is read from the query (?version=) so a stale retire returns the same friendly 409.
func (h *Handler) deleteRule(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	ver, ok := parseVersion(w, r)
	if !ok {
		return
	}
	if !h.fenceExistingRule(w, r, id) {
		return
	}
	if err := h.svc.DeleteRule(r.Context(), id, ver, staffUserID(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "retired"})
}

// - category margin-policy grid (US-P01/02/03) -

func (h *Handler) listCategoryPolicies(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	pols, err := h.svc.ListCategoryPolicies(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if pols == nil {
		pols = []CategoryPolicy{}
	}
	httpx.WriteJSON(w, http.StatusOK, pols)
}

type categoryPolicyPayload struct {
	MarginTargetBps     int64 `json:"margin_target_bps"`
	MarginFloorBps      int64 `json:"margin_floor_bps"`
	EnforcementDisabled bool  `json:"margin_enforcement_disabled"`
}

func (h *Handler) upsertCategoryPolicy(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	categoryID := r.PathValue("categoryId")
	if !uuidRe.MatchString(categoryID) {
		httpx.Error(w, http.StatusBadRequest, "categoryId must be a uuid")
		return
	}
	var p categoryPolicyPayload
	if !decode(w, r, &p) {
		return
	}
	out, err := h.svc.UpsertCategoryPolicy(r.Context(), CategoryPolicy{
		CategoryID:          categoryID,
		MarginTargetBps:     p.MarginTargetBps,
		MarginFloorBps:      p.MarginFloorBps,
		EnforcementDisabled: p.EnforcementDisabled,
	}, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// - customer overrides (US-P09) -

func (h *Handler) listOverrides(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	customerID := r.PathValue("customerId")
	if !uuidRe.MatchString(customerID) {
		httpx.Error(w, http.StatusBadRequest, "customerId must be a uuid")
		return
	}
	if !h.fenceCustomer(w, r, customerID) {
		return
	}
	list, err := h.svc.ListOverrides(r.Context(), customerID)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if list == nil {
		list = []Override{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

type overridePayload struct {
	CategoryID  string `json:"category_id"`
	CalcType    string `json:"calc_type"`
	PriceCents  *int64 `json:"price_cents"`
	DiscountBps *int64 `json:"discount_bps"`
	MarkupBps   *int64 `json:"markup_bps"`
	PriceUOM    string `json:"price_uom"`
	Notes       string `json:"notes"`
	Version     int64  `json:"version"`
}

func (h *Handler) createOverride(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	customerID := r.PathValue("customerId")
	if !uuidRe.MatchString(customerID) {
		httpx.Error(w, http.StatusBadRequest, "customerId must be a uuid")
		return
	}
	var p overridePayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.CategoryID, "category_id") {
		return
	}
	if !h.fenceCustomer(w, r, customerID) {
		return
	}
	out, err := h.svc.CreateOverride(r.Context(), Override{
		CustomerID: customerID, CategoryID: p.CategoryID, CalcType: p.CalcType,
		PriceCents: p.PriceCents, DiscountBps: p.DiscountBps, MarkupBps: p.MarkupBps,
		PriceUOM: p.PriceUOM, Notes: p.Notes,
	}, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": out.ID, "version": out.Version})
}

func (h *Handler) updateOverride(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	var p overridePayload
	if !decode(w, r, &p) {
		return
	}
	if !h.fenceExistingOverride(w, r, id) {
		return
	}
	out, err := h.svc.UpdateOverride(r.Context(), id, Override{
		CalcType: p.CalcType, PriceCents: p.PriceCents, DiscountBps: p.DiscountBps,
		MarkupBps: p.MarkupBps, PriceUOM: p.PriceUOM, Notes: p.Notes,
	}, p.Version, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// deleteOverride removes an override under an optimistic version guard (finding #4). The version
// is read from ?version= (a DELETE has no body), so a stale delete returns the same friendly 409
// as a stale rule retire.
func (h *Handler) deleteOverride(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	ver, ok := parseVersion(w, r)
	if !ok {
		return
	}
	if !h.fenceExistingOverride(w, r, id) {
		return
	}
	if err := h.svc.DeleteOverride(r.Context(), id, ver, staffUserID(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// - audit viewer (US-P12) + low-margin preview (US-P13) -

func (h *Handler) listAudit(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	q := r.URL.Query()
	entityID := q.Get("entity_id")
	if entityID != "" && !uuidRe.MatchString(entityID) {
		httpx.Error(w, http.StatusBadRequest, "entity_id must be a uuid")
		return
	}
	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			httpx.Error(w, http.StatusBadRequest, "limit must be a non-negative integer")
			return
		}
		limit = n
	}
	rows, err := h.svc.ListAudit(r.Context(), AuditFilter{
		EntityType: q.Get("entity_type"),
		EntityID:   entityID,
		Action:     q.Get("action"),
		Limit:      limit,
	})
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if rows == nil {
		rows = []AuditLogRow{}
	}
	httpx.WriteJSON(w, http.StatusOK, rows)
}

// lowMarginReport runs a caller-supplied batch of line contexts through the resolver and
// returns only the breaching lines (margin < target/floor), worst-first. The batch input
// is a quoting EXPORT that does not exist yet (D-PA-3), this endpoint is the read-only
// classifier preview, not the principal's daily live-sales report.
func (h *Handler) lowMarginReport(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var req struct {
		Lines []resolveRequest `json:"lines"`
	}
	if !decode(w, r, &req) {
		return
	}
	inputs := make([]ResolveInput, 0, len(req.Lines))
	for _, l := range req.Lines {
		if !h.fenceLine(w, r, caller, l.BranchID, l.CustomerID) {
			return
		}
		inputs = append(inputs, ResolveInput{
			CustomerID: l.CustomerID, CustomerType: l.CustomerType, ProductID: l.ProductID,
			CategoryID: l.CategoryID, BranchID: l.BranchID, QtyEntered: l.QtyEntered,
			UOMEntered: l.UOMEntered, CostCents: l.CostCents,
			// l.HasApprovedOverride is deliberately NOT carried: the
			// classifier grades the arithmetic, an override changes nothing.
			UserID: staffUserID(r),
		})
	}
	out, err := h.svc.LowMarginReport(r.Context(), inputs)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// lowMarginScan is the low margin report the managers scan (D-PA-3): it grades the price every
// document line holds (a committed order line, or a quote line, per doc_type: quotes, orders or both,
// the default) against current per-branch WAC cost and answers the breaching (warn/critical) rows,
// cost-missing lines as invalid, as a cursor page sorted by margin (sort: margin_asc, the default, or
// margin_desc). from/to are the document's own dates (YYYY-MM-DD or RFC3339) defaulting to the last
// 30 days so the report is not empty between sales; branch_id, salesperson_id and cleared_only
// (true: only lines cleared below the floor) narrow it. A caller that is not global scope reads only
// the branches it is granted. The route's gate (the managers who clear
// floors and the roles above them, who may see cost) is the route's access annotation, not this handler's. A
// malformed parameter returns 400, never a 500 (sweep #46 bad-input hygiene).
func (h *Handler) lowMarginScan(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	q := r.URL.Query()
	now := time.Now()
	from, ok := parseScanDate(w, q.Get("from"), "from", now.AddDate(0, 0, -30), false)
	if !ok {
		return
	}
	to, ok := parseScanDate(w, q.Get("to"), "to", now, true)
	if !ok {
		return
	}
	branchID := q.Get("branch_id")
	if branchID != "" && !uuidRe.MatchString(branchID) {
		httpx.Error(w, http.StatusBadRequest, "branch_id must be a uuid")
		return
	}
	// The branch wall (AGENTS.md: scoped data is never read without a scope predicate). A global
	// caller reads every branch; any other caller, the zero caller included, reads only the branches
	// it is granted: a branch_id it cannot reach is refused, and with none named the report is
	// narrowed to the reachable set, an empty grant reading nothing.
	var branchIDs []string
	if caller.Scope != coreidentity.ScopeGlobal {
		if branchID != "" && !caller.CanAccessBranch(branchID) {
			httpx.Error(w, http.StatusForbidden, "you can only read the low margin report for the branches you are granted")
			return
		}
		branchIDs = append([]string{}, caller.Branches...)
	}
	docType := q.Get("doc_type")
	switch docType {
	case "", ScanDocsQuotes, ScanDocsOrders, ScanDocsBoth:
	default:
		httpx.Error(w, http.StatusBadRequest, "doc_type must be quotes, orders or both")
		return
	}
	sortKey := q.Get("sort")
	switch sortKey {
	case "", SortMarginAsc, SortMarginDesc:
	default:
		httpx.Error(w, http.StatusBadRequest, "sort must be margin_asc or margin_desc")
		return
	}
	salespersonID := q.Get("salesperson_id")
	if salespersonID != "" && !uuidRe.MatchString(salespersonID) {
		httpx.Error(w, http.StatusBadRequest, "salesperson_id must be a uuid")
		return
	}
	clearedOnly := false
	switch q.Get("cleared_only") {
	case "", "false":
	case "true":
		clearedOnly = true
	default:
		httpx.Error(w, http.StatusBadRequest, "cleared_only must be true or false")
		return
	}
	limit := 0
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > scanPageMax {
			httpx.Error(w, http.StatusBadRequest, "limit must be a whole number from 1 to 200")
			return
		}
		limit = n
	}
	page, err := h.svc.LowMarginScanPage(r.Context(), LowMarginFilter{
		From: from, To: to, BranchID: branchID, BranchIDs: branchIDs, DocType: docType,
		SalespersonID: salespersonID, ClearedOnly: clearedOnly, Sort: sortKey,
	}, q.Get("cursor"), limit)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, page)
}

// parseScanDate parses an OPTIONAL scan-window bound (YYYY-MM-DD or RFC3339), returning fallback when
// empty. A date-only `to` (endOfDay) is advanced to next-day midnight so the query's exclusive upper
// bound covers the whole target day (matching the rule-editor's valid_to off-by-one fix). A malformed
// value → 400 (surfaced to the caller), never a 500 deep in the service.
func parseScanDate(w http.ResponseWriter, raw, field string, fallback time.Time, endOfDay bool) (time.Time, bool) {
	if raw == "" {
		return fallback, true
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t, true
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		if endOfDay {
			t = t.AddDate(0, 0, 1)
		}
		return t, true
	}
	httpx.Error(w, http.StatusBadRequest, field+" must be RFC3339 or YYYY-MM-DD")
	return time.Time{}, false
}

// - quantity-break editor (A-07 Tier-B) -

type breakItemPayload struct {
	FromQty        float64  `json:"from_qty"`
	ToQty          *float64 `json:"to_qty"`
	ValueType      string   `json:"value_type"`
	DiscountBps    *int64   `json:"discount_bps"`
	DiscountCents  *int64   `json:"discount_cents"`
	SurchargeBps   *int64   `json:"surcharge_bps"`
	SurchargeCents *int64   `json:"surcharge_cents"`
	PriceUOM       string   `json:"price_uom"`
}

type breakPayload struct {
	Code         string             `json:"code"`
	Name         string             `json:"name"`
	ProductID    string             `json:"product_id"`
	CategoryID   string             `json:"category_id"`
	ThresholdUOM string             `json:"threshold_uom"`
	Items        []breakItemPayload `json:"items"`
	Version      int64              `json:"version"`
}

func (p breakPayload) toHeader() QuantityBreakHeader {
	items := make([]QuantityBreakItem, 0, len(p.Items))
	for _, it := range p.Items {
		items = append(items, QuantityBreakItem{
			FromQty: it.FromQty, ToQty: it.ToQty, ValueType: it.ValueType,
			DiscountBps: it.DiscountBps, DiscountCents: it.DiscountCents,
			SurchargeBps: it.SurchargeBps, SurchargeCents: it.SurchargeCents, PriceUOM: it.PriceUOM,
		})
	}
	return QuantityBreakHeader{
		Code: p.Code, Name: p.Name, ProductID: p.ProductID, CategoryID: p.CategoryID,
		ThresholdUOM: p.ThresholdUOM, Items: items,
	}
}

func (h *Handler) listQuantityBreaks(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	list, err := h.svc.ListQuantityBreaks(r.Context())
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	if list == nil {
		list = []QuantityBreakHeader{}
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

func (h *Handler) getQuantityBreak(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	out, err := h.svc.GetQuantityBreak(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (h *Handler) createQuantityBreak(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var p breakPayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.ProductID, "product_id") || !optionalBodyUUID(w, p.CategoryID, "category_id") {
		return
	}
	out, err := h.svc.CreateQuantityBreak(r.Context(), p.toHeader(), staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"id": out.ID, "version": out.Version})
}

func (h *Handler) updateQuantityBreak(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	var p breakPayload
	if !decode(w, r, &p) {
		return
	}
	if !optionalBodyUUID(w, p.ProductID, "product_id") || !optionalBodyUUID(w, p.CategoryID, "category_id") {
		return
	}
	out, err := h.svc.UpdateQuantityBreak(r.Context(), id, p.toHeader(), p.Version, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// deleteQuantityBreak soft-retires a header under optimistic version (?version=, no body), matching
// the rule/override retire path.
func (h *Handler) deleteQuantityBreak(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	id := r.PathValue("id")
	if !requirePathUUID(w, id, "id") {
		return
	}
	ver, ok := parseVersion(w, r)
	if !ok {
		return
	}
	if err := h.svc.DeleteQuantityBreak(r.Context(), id, ver, staffUserID(r)); err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "retired"})
}

// - helpers -

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		httpx.Error(w, http.StatusBadRequest, "invalid_json")
		return false
	}
	return true
}

// optionalBodyUUIDPtr validates an OPTIONAL pointer body uuid (a sparse PATCH field): nil →
// leave-unchanged (skip); non-nil → validate the pointed-at value, where "" is the allowed
// "clear this dim" signal and any other non-uuid → 400 (finding #9).
func optionalBodyUUIDPtr(w http.ResponseWriter, val *string, field string) bool {
	if val == nil {
		return true
	}
	return optionalBodyUUID(w, *val, field)
}

// parseValidityBound parses an optional validity bound as a tri-state (findings #7 + #8):
//   - nil pointer  → (nil, false, true): absent → LEAVE UNCHANGED
//   - empty string → (nil, true,  true): explicit CLEAR (set the bound back to NULL)
//   - RFC3339      → (&t, false, true): an exact instant
//   - YYYY-MM-DD   → (&t, false, true): a bare date; when endOfDay it is parsed to the START OF THE
//     NEXT DAY (UTC), which, combined with withinValidity's exclusive upper bound, makes a
//     date-only valid_to INCLUSIVE of the whole target day (the off-by-one fix). valid_from keeps
//     start-of-day (endOfDay=false) so it is inclusive from midnight.
//
// A malformed value returns 400 rather than a 500 deep in the service.
func parseValidityBound(w http.ResponseWriter, raw *string, field string, endOfDay bool) (val *time.Time, clear bool, ok bool) {
	if raw == nil {
		return nil, false, true
	}
	if *raw == "" {
		return nil, true, true
	}
	if t, err := time.Parse(time.RFC3339, *raw); err == nil {
		return &t, false, true
	}
	if t, err := time.Parse("2006-01-02", *raw); err == nil {
		if endOfDay {
			t = t.AddDate(0, 0, 1) // exclusive upper bound at next-day midnight → whole day inclusive
		}
		return &t, false, true
	}
	httpx.Error(w, http.StatusBadRequest, field+" must be RFC3339 or YYYY-MM-DD")
	return nil, false, false
}

// parseVersion reads the optimistic-lock version from ?version= for a DELETE (which has
// no body). A missing/invalid value is 400, a retire must carry the version it saw.
func parseVersion(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("version")
	if raw == "" {
		httpx.Error(w, http.StatusBadRequest, "version query param is required")
		return 0, false
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		httpx.Error(w, http.StatusBadRequest, "version must be an integer")
		return 0, false
	}
	return v, true
}

// staffUserID returns the audit actor: the authenticated staff caller id stamped onto the
// request context by the RequireManager/RequireStaff guard (fix #7). C-1 removed the
// spoofable X-User-Id header; this is the trusted, server-resolved id, so CREATE/UPDATE
// audit rows now record WHO made the change (the trail the dealer principal asked for). Empty only when
// a route is somehow reached unguarded (audit user_id stays nullable).
func staffUserID(r *http.Request) string { return identity.StaffUserIDFromContext(r.Context()) }

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	if status, msg, ok := ClientStatus(err); ok {
		httpx.Error(w, status, msg)
		return
	}
	h.log.Error("pricing: internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	httpx.Error(w, http.StatusInternalServerError, "internal error")
}

// ClientStatus classifies a pricing error into its client-facing HTTP status + envelope
// message. It is the SINGLE source of truth for pricing's error→HTTP mapping: this handler
// uses it, and, critically, a COMPOSING module reuses it so a pricing failure raised deep
// inside a larger request surfaces as the SAME friendly 4xx the /pricing endpoints return,
// instead of leaking out as a generic 500. The POS counter sale (internal/pos) resolves every
// cart line through DeriveLinePrice; before this seam a walk-in CASH ring whose RETAIL line
// had no retail anchor / no uom factor 500'd the whole sale (DIB-197). ok=false means the
// error is not a known pricing client error and the caller should treat it as a 500.
func ClientStatus(err error) (status int, msg string, ok bool) {
	var bre badRequestError
	var upe unprocessableError
	switch {
	case errors.As(err, &bre):
		return http.StatusBadRequest, bre.Error(), true
	case errors.As(err, &upe):
		return http.StatusUnprocessableEntity, upe.Error(), true
	case errors.Is(err, errRuleCollision):
		return http.StatusConflict, "a conflicting active rule already exists", true
	case errors.Is(err, errOverrideTaken):
		return http.StatusConflict, "an override already exists for this customer + category", true
	case errors.Is(err, errBreakCodeTaken):
		return http.StatusConflict, "a quantity break with this code already exists", true
	// W4-A: promotions / rebate programs.
	case errors.Is(err, errPromoCodeTaken):
		return http.StatusConflict, "a promotion with this code already exists", true
	case errors.Is(err, errRebateCodeTaken):
		return http.StatusConflict, "a rebate program with this code already exists", true
	case errors.Is(err, errConflict):
		return http.StatusConflict, "version conflict", true
	case errors.Is(err, errFKViolation):
		return http.StatusUnprocessableEntity, "references a nonexistent customer, product, category or branch", true
	case errors.Is(err, errNotFound):
		return http.StatusNotFound, "not found", true
	case isNoConversion(err):
		// Both the pricing sentinel and the product module's own ErrNoConversion (the real converter's
		// answer): a product with no factor for the unit asked is a request to correct, not a server fault.
		return http.StatusUnprocessableEntity, "no uom conversion factor", true
	case errors.Is(err, errNoListPrice):
		return http.StatusUnprocessableEntity, "no retail anchor for product", true
	// A-08 price-batch errors.
	case errors.Is(err, errSeparation):
		return http.StatusUnprocessableEntity, "separation of duties: the approver must differ from the batch creator", true
	case errors.Is(err, errNotEffective):
		return http.StatusUnprocessableEntity, "batch effective date has not arrived yet", true
	case errors.Is(err, errBadBatchState):
		return http.StatusConflict, "invalid batch state for this action", true
	case errors.Is(err, errNoProfile):
		return http.StatusUnprocessableEntity, "import profile not found or not set on the batch", true
	case errors.Is(err, errNoLines):
		return http.StatusUnprocessableEntity, "the CSV has no importable rows", true
	case errors.Is(err, errBatchStoreUnset):
		return http.StatusServiceUnavailable, "price-batch store unavailable", true
	// Price-file import (0198). Each message names the thing the operator can do next, because every
	// one of these is reached by somebody standing in front of a four-step flow with a file in hand.
	case errors.Is(err, errNoVendor):
		return http.StatusBadRequest, "pick a manufacturer before importing a price file", true
	case errors.Is(err, errStalePreview):
		return http.StatusConflict, "this file or its column mapping changed since the preview, preview it again before applying", true
	case errors.Is(err, errCatalogUnread):
		return http.StatusServiceUnavailable, "the product catalogue could not be read, so no import can be checked against it", true
	case errors.Is(err, errNothingToStage):
		return http.StatusUnprocessableEntity, "this file moves no prices, check the column mapping before applying", true
	case errors.Is(err, errPriceFileStoreUnset):
		return http.StatusServiceUnavailable, "the price-file import is not configured on this server", true
	case errors.Is(err, errNoDB):
		return http.StatusServiceUnavailable, "database unavailable", true
	default:
		return 0, "", false
	}
}
