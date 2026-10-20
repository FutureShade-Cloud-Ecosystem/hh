package pricing

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// registerFloorRoutes mounts the editors' floor warning (CP0-6): the preview and the
// confirmation. Both sit behind the manager guard, and the contract names the
// pricing_editors list beside the level, as every price write does.
func (h *Handler) registerFloorRoutes(r httpx.Registrar, g *guards.Guards) {
	r.Handle("POST /api/v1/pricing/floor-preview", g.RequireManager(h.staff(h.floorPreview)))
	r.Handle("POST /api/v1/pricing/floor-confirmations", g.RequireManager(h.staff(h.floorConfirmation)))
}

type floorPreviewBody struct {
	Kind          string                 `json:"kind"`
	ID            string                 `json:"id"`
	BranchID      string                 `json:"branch_id"`
	Rule          *rulePayload           `json:"rule"`
	CustomerID    string                 `json:"customer_id"`
	Override      *overridePayload       `json:"override"`
	CategoryID    string                 `json:"category_id"`
	Policy        *categoryPolicyPayload `json:"policy"`
	QuantityBreak *breakPayload          `json:"quantity_break"`
}

type floorPreviewItemJSON struct {
	ProductID    string `json:"product_id"`
	SKU          string `json:"sku"`
	Name         string `json:"name"`
	CategoryID   string `json:"category_id,omitempty"`
	CustomerType string `json:"customer_type,omitempty"`
	BranchID     string `json:"branch_id,omitempty"`
	BranchCode   string `json:"branch_code,omitempty"`
	BranchName   string `json:"branch_name,omitempty"`
	FinalCents   int64  `json:"final_cents"`
	// The margin legs name a cost beside the unit price, so they are pointers: absent for a caller off
	// the cost and margin list, never a zero (cost_gate.go).
	MarginBps          *int64 `json:"margin_bps,omitempty"`
	FloorBps           *int64 `json:"floor_bps,omitempty"`
	PriceUOM           string `json:"price_uom"`
	Source             string `json:"source"`
	PreviousFinalCents int64  `json:"previous_final_cents"`
	PreviousMarginBps  *int64 `json:"previous_margin_bps,omitempty"`
	AlreadyUnderFloor  bool   `json:"already_under_floor"`
}

type floorNoCostBranchJSON struct {
	BranchID   string `json:"branch_id"`
	BranchCode string `json:"branch_code,omitempty"`
	BranchName string `json:"branch_name,omitempty"`
	Products   int    `json:"products"`
}

func (h *Handler) floorPreview(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b floorPreviewBody
	if !decode(w, r, &b) {
		return
	}
	if !optionalBodyUUID(w, b.ID, "id") || !optionalBodyUUID(w, b.BranchID, "branch_id") ||
		!optionalBodyUUID(w, b.CustomerID, "customer_id") || !optionalBodyUUID(w, b.CategoryID, "category_id") {
		return
	}
	// A named branch is a branch the caller reaches (UX-88 round 2): a scoped manager naming one it does
	// not hold is refused 403, the shape the yard and branch doors answer. The branch is then held to
	// the caller's org by the candidate seam, so a GLOBAL manager cannot grade against another org's cost.
	if b.BranchID != "" {
		sc, ok := identity.StaffContextFromContext(r.Context())
		if !ok || !sc.CanAccessBranch(b.BranchID) {
			httpx.Error(w, http.StatusForbidden, "pricing: branch out of scope")
			return
		}
	}
	ch := FloorChange{Kind: b.Kind, ID: b.ID, BranchID: b.BranchID, OrgID: caller.OrgID}
	switch b.Kind {
	case "rule":
		if b.Rule != nil {
			if !optionalBodyUUID(w, b.Rule.CustomerID, "rule.customer_id") || !optionalBodyUUID(w, b.Rule.CategoryID, "rule.category_id") ||
				!optionalBodyUUID(w, b.Rule.ProductID, "rule.product_id") || !optionalBodyUUID(w, b.Rule.BranchID, "rule.branch_id") {
				return
			}
			rule := b.Rule.toRule()
			ch.Rule = &rule
		}
	case "override":
		if b.Override != nil {
			if !optionalBodyUUID(w, b.Override.CategoryID, "override.category_id") {
				return
			}
			o := Override{
				CustomerID: b.CustomerID, CategoryID: b.Override.CategoryID, CalcType: b.Override.CalcType,
				PriceCents: b.Override.PriceCents, DiscountBps: b.Override.DiscountBps, MarkupBps: b.Override.MarkupBps,
				PriceUOM: b.Override.PriceUOM, Notes: b.Override.Notes,
			}
			ch.Override = &o
		}
	case "category_policy":
		if b.Policy != nil {
			ch.Policy = &CategoryPolicy{
				CategoryID: b.CategoryID, MarginTargetBps: b.Policy.MarginTargetBps,
				MarginFloorBps: b.Policy.MarginFloorBps, EnforcementDisabled: b.Policy.EnforcementDisabled,
			}
		}
	case "quantity_break":
		if b.QuantityBreak != nil {
			if !optionalBodyUUID(w, b.QuantityBreak.ProductID, "quantity_break.product_id") ||
				!optionalBodyUUID(w, b.QuantityBreak.CategoryID, "quantity_break.category_id") {
				return
			}
			hdr := b.QuantityBreak.toHeader()
			ch.Break = &hdr
		}
	}
	out, err := h.svc.PreviewFloor(r.Context(), ch)
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	figuresOK := h.costAllowed(r)
	items := make([]floorPreviewItemJSON, 0, len(out.Items))
	for _, it := range out.Items {
		item := floorPreviewItemJSON{
			ProductID: it.ProductID, SKU: it.SKU, Name: it.Name, CategoryID: it.CategoryID, CustomerType: it.CustomerType,
			BranchID: it.BranchID, BranchCode: it.BranchCode, BranchName: it.BranchName,
			FinalCents: it.FinalCents, PriceUOM: it.PriceUOM,
			Source: it.Source, PreviousFinalCents: it.PreviousFinalCents,
			AlreadyUnderFloor: it.AlreadyUnderFloor,
		}
		if figuresOK {
			margin, floor, previous := it.MarginBps, it.FloorBps, it.PreviousMarginBps
			item.MarginBps, item.FloorBps, item.PreviousMarginBps = &margin, &floor, &previous
		}
		items = append(items, item)
	}
	noCostBranches := make([]floorNoCostBranchJSON, 0, len(out.NoCostBranches))
	for _, b := range out.NoCostBranches {
		noCostBranches = append(noCostBranches, floorNoCostBranchJSON{
			BranchID: b.BranchID, BranchCode: b.BranchCode, BranchName: b.BranchName, Products: b.Products,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"items": items, "evaluated": out.Evaluated, "not_graded": out.NotGraded, "no_cost": out.NoCost, "truncated": out.Truncated,
		"no_cost_branches": noCostBranches, "not_graded_branches": out.NotGradedBranches,
	})
}

type floorConfirmationBody struct {
	EntityType    string   `json:"entity_type"`
	EntityID      string   `json:"entity_id"`
	ProductIDs    []string `json:"product_ids"`
	RefEntityType string   `json:"ref_entity_type"`
	RefEntityID   string   `json:"ref_entity_id"`
}

func (h *Handler) floorConfirmation(w http.ResponseWriter, r *http.Request, caller guards.StaffCaller) {
	var b floorConfirmationBody
	if !decode(w, r, &b) {
		return
	}
	if !requireBodyUUID(w, b.EntityID, "entity_id") || !optionalBodyUUID(w, b.RefEntityID, "ref_entity_id") {
		return
	}
	for _, id := range b.ProductIDs {
		if !requireBodyUUID(w, id, "product_ids") {
			return
		}
	}
	err := h.svc.RecordFloorConfirmation(r.Context(), FloorConfirmation{
		OrgID: caller.OrgID, EntityType: b.EntityType, EntityID: b.EntityID, ProductIDs: b.ProductIDs,
		RefType: b.RefEntityType, RefID: b.RefEntityID,
	}, staffUserID(r))
	if err != nil {
		h.writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "recorded"})
}

// requireBodyUUID is optionalBodyUUID for a field that must be present.
func requireBodyUUID(w http.ResponseWriter, val, field string) bool {
	if val == "" {
		httpx.Error(w, http.StatusBadRequest, field+" is required")
		return false
	}
	return optionalBodyUUID(w, val, field)
}
