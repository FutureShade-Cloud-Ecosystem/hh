package pricing

import (
	"net/http"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
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
	ProductID          string `json:"product_id"`
	SKU                string `json:"sku"`
	Name               string `json:"name"`
	CategoryID         string `json:"category_id,omitempty"`
	CustomerType       string `json:"customer_type,omitempty"`
	BranchID           string `json:"branch_id,omitempty"`
	BranchCode         string `json:"branch_code,omitempty"`
	BranchName         string `json:"branch_name,omitempty"`
	FinalCents         int64  `json:"final_cents"`
	MarginBps          int64  `json:"margin_bps"`
	FloorBps           int64  `json:"floor_bps"`
	PriceUOM           string `json:"price_uom"`
	Source             string `json:"source"`
	PreviousFinalCents int64  `json:"previous_final_cents"`
	PreviousMarginBps  int64  `json:"previous_margin_bps"`
	AlreadyUnderFloor  bool   `json:"already_under_floor"`
}

type floorNoCostBranchJSON struct {
	BranchID   string `json:"branch_id"`
	BranchCode string `json:"branch_code,omitempty"`
	BranchName string `json:"branch_name,omitempty"`
	Products   int    `json:"products"`
}

func (h *Handler) floorPreview(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
	var b floorPreviewBody
	if !decode(w, r, &b) {
		return
	}
	if !optionalBodyUUID(w, b.ID, "id") || !optionalBodyUUID(w, b.BranchID, "branch_id") ||
		!optionalBodyUUID(w, b.CustomerID, "customer_id") || !optionalBodyUUID(w, b.CategoryID, "category_id") {
		return
	}
	ch := FloorChange{Kind: b.Kind, ID: b.ID, BranchID: b.BranchID}
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
	items := make([]floorPreviewItemJSON, 0, len(out.Items))
	for _, it := range out.Items {
		items = append(items, floorPreviewItemJSON{
			ProductID: it.ProductID, SKU: it.SKU, Name: it.Name, CategoryID: it.CategoryID, CustomerType: it.CustomerType,
			BranchID: it.BranchID, BranchCode: it.BranchCode, BranchName: it.BranchName,
			FinalCents: it.FinalCents, MarginBps: it.MarginBps, FloorBps: it.FloorBps, PriceUOM: it.PriceUOM,
			Source: it.Source, PreviousFinalCents: it.PreviousFinalCents, PreviousMarginBps: it.PreviousMarginBps,
			AlreadyUnderFloor: it.AlreadyUnderFloor,
		})
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

func (h *Handler) floorConfirmation(w http.ResponseWriter, r *http.Request, _ guards.StaffCaller) {
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
		EntityType: b.EntityType, EntityID: b.EntityID, ProductIDs: b.ProductIDs,
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
