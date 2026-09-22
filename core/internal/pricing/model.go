// Package pricing is the A-07 rule-based price resolver (W3, gate criterion 2).
// It owns the pricing_* / quantity_break_* / customer_category_overrides tables and
// turns a (customer, product, qty, uom) request into a deterministic line price via
// a precedence waterfall, reusing identity's margin gate (A-20) for the below-floor
// block. Zero-trust: it never SELECTs catalog tables, category + UOM factors come
// through the injected CategoryResolver / UOMConverter (the catalog read seam).
package pricing

import (
	"time"
)

// CalcType is how a rule/override turns the anchor into a price.
const (
	CalcAbsolute     = "ABSOLUTE"       // a set price per price_uom (CUSTOMER_PRODUCT only, discount-%-only rule)
	CalcPctOffList   = "PCT_OFF_LIST"   // discount_bps off the retail anchor (the dominant calc)
	CalcMarkupOnCost = "MARKUP_ON_COST" // builds the retail anchor (cost × 1.85 family), not a per-tier calc
)

// BreakValueType, a quantity break is an ADJUSTMENT on the tier-resolved base, never a competing
// base price. PCT_OFF/ABS_OFF discount the base; SURCHARGE_* uplift it (06-23 partial-skid). The set
// is enforced by validateQuantityBreak (422) AND the 0026 DB CHECK.
const (
	BreakPctOff       = "PCT_OFF"       // discount: percent OFF the resolved base
	BreakAbsOff       = "ABS_OFF"       // discount: flat cents OFF the resolved base
	BreakSurchargePct = "SURCHARGE_PCT" // charge MORE: positive % uplift (partial skid under threshold)
	BreakSurchargeAbs = "SURCHARGE_ABS" // charge MORE: positive flat uplift
)

// breakValueTypes is the allowed adjustment vocabulary (used by validateQuantityBreak + the editor).
var breakValueTypes = map[string]bool{
	BreakPctOff: true, BreakAbsOff: true, BreakSurchargePct: true, BreakSurchargeAbs: true,
}

// Scope values for pricing_rules.
const (
	ScopeCustomerProduct  = "CUSTOMER_PRODUCT"
	ScopeCustomerCategory = "CUSTOMER_CATEGORY"
	ScopeTierCategory     = "TIER_CATEGORY"
	ScopeTierDefault      = "TIER_DEFAULT"
)

// Source is the resolved precedence level, surfaced in the Breakdown.
const (
	SourceCustomerProduct  = "CUSTOMER_PRODUCT_RULE"
	SourceQuantityBreak    = "QUANTITY_BREAK"
	SourceCustomerCategory = "CUSTOMER_CATEGORY_RULE" // a pricing_rules CUSTOMER_CATEGORY row (customer+category)
	SourceCustomerOverride = "CUSTOMER_OVERRIDE"
	SourceTierCategory     = "TIER_CATEGORY"
	SourceTierDefault      = "TIER_DEFAULT"
	SourceList             = "LIST"
)

// BlockMarginBelowMinimum mirrors identity.PolicyMargin, a below-floor block routes
// to the existing A-20 0016 approval queue.
const BlockMarginBelowMinimum = "MARGIN_BELOW_MINIMUM"

// Bypass reasons (W4-A, migration 0114; extended by 0133). The closed set the
// pricing_audit_bypass_reason_known CHECK accepts.
//
// DIB-420 CHANGED WHAT APPROVED_OVERRIDE MEANS, and the change is the whole point. 0114 declared it
// and left it unwritten, with the note "an approved override is not a bypass, it is a pass, the gate
// still runs and identity.CheckMarginAt returns nil for it". That was true of a gate that took the
// CLIENT'S word: the pass was invisible because nobody had decided anything. Now the server decides,
// and a below-floor price that goes out on somebody's authority is exactly the thing a month-end
// reader must be able to count. So the resolver WRITES these two, one per authority source:
//
//	APPROVED_OVERRIDE       an APPROVED margin override on the DIB-360 approvals substrate
//	MANAGER_SCOPE_OVERRIDE  the caller's own manager scope over the branch being priced
//
// Two values rather than one because "how many below-floor prices went out without anybody filing an
// approval?" is the question an auditor actually asks, and a single reason cannot answer it.
const (
	BypassProductPolicy  = "PRODUCT_POLICY_DISABLED"
	BypassCategoryPolicy = "CATEGORY_POLICY_DISABLED"
	BypassApprovedOverr  = "APPROVED_OVERRIDE"
	BypassManagerScope   = "MANAGER_SCOPE_OVERRIDE"
)

// bypassReasonForAuthority maps an OverrideAuthority source to its audit reason. An unknown source
// maps to "" and the row is written without a reason rather than dropped, the override still
// happened, and losing the whole row to protect a CHECK would be the worse trade.
func bypassReasonForAuthority(source string) string {
	switch source {
	case AuthorityApproval:
		return BypassApprovedOverr
	case AuthorityManagerScope:
		return BypassManagerScope
	}
	return ""
}

// validBypassReasons mirrors the 0114+0133 CHECK. Same reasoning as validRefTypes: AppendAudit's
// error is discarded at every call site, so a value the CHECK rejects is a SILENTLY LOST audit row.
// The repository drops an unknown reason rather than sending it.
var validBypassReasons = map[string]bool{
	BypassProductPolicy: true, BypassCategoryPolicy: true,
	BypassApprovedOverr: true, BypassManagerScope: true,
}

// Rule is one pricing_rules row (empty strings for unset nullable scope dims).
type Rule struct {
	ID           string     `json:"id"`
	Scope        string     `json:"scope"`
	CustomerID   string     `json:"customer_id,omitempty"`
	CustomerType string     `json:"customer_type,omitempty"`
	CategoryID   string     `json:"category_id,omitempty"`
	ProductID    string     `json:"product_id,omitempty"`
	BranchID     string     `json:"branch_id,omitempty"`
	CalcType     string     `json:"calc_type"`
	PriceCents   *int64     `json:"price_cents,omitempty"`
	DiscountBps  *int64     `json:"discount_bps,omitempty"`
	MarkupBps    *int64     `json:"markup_bps,omitempty"`
	PriceUOM     string     `json:"price_uom"`
	Priority     int64      `json:"priority"`
	ValidFrom    *time.Time `json:"valid_from,omitempty"`
	ValidTo      *time.Time `json:"valid_to,omitempty"`
	DetailText   string     `json:"detail_text"`
	IsActive     bool       `json:"is_active"`
	Version      int64      `json:"version"`
}

// QuantityBreak is a flattened quantity_break_headers × quantity_break_items row used by the
// resolver. It is applied as an ADJUSTMENT on the resolved base (never a competing base price).
type QuantityBreak struct {
	HeaderID       string   `json:"header_id"`
	ItemID         string   `json:"item_id"`
	Name           string   `json:"name"`
	ProductID      string   `json:"product_id,omitempty"`
	CategoryID     string   `json:"category_id,omitempty"`
	ThresholdUOM   string   `json:"threshold_uom"`
	FromQty        float64  `json:"from_qty"`
	ToQty          *float64 `json:"to_qty,omitempty"`
	ValueType      string   `json:"value_type"`
	DiscountBps    *int64   `json:"discount_bps,omitempty"`    // PCT_OFF
	DiscountCents  *int64   `json:"discount_cents,omitempty"`  // ABS_OFF
	SurchargeBps   *int64   `json:"surcharge_bps,omitempty"`   // SURCHARGE_PCT
	SurchargeCents *int64   `json:"surcharge_cents,omitempty"` // SURCHARGE_ABS
	PriceUOM       string   `json:"price_uom"`
}

// QuantityBreakHeader is one quantity_break_headers row plus its tiers, the unit the admin editor
// (A-07 Tier-B) creates/edits/retires under an optimistic version. Scope is EXACTLY ONE of
// ProductID / CategoryID (product-specific schedules win over category schedules at resolve time).
type QuantityBreakHeader struct {
	ID           string              `json:"id"`
	Code         string              `json:"code"`
	Name         string              `json:"name"`
	ProductID    string              `json:"product_id,omitempty"`
	CategoryID   string              `json:"category_id,omitempty"`
	ThresholdUOM string              `json:"threshold_uom"`
	IsActive     bool                `json:"is_active"`
	Version      int64               `json:"version"`
	Items        []QuantityBreakItem `json:"items"`
}

// QuantityBreakItem is one tier (bucket) of a header: a [from_qty, to_qty) band in the header's
// threshold_uom and an adjustment of value_type.
type QuantityBreakItem struct {
	ID             string   `json:"id,omitempty"`
	FromQty        float64  `json:"from_qty"`
	ToQty          *float64 `json:"to_qty,omitempty"` // nil = open-ended top tier
	ValueType      string   `json:"value_type"`
	DiscountBps    *int64   `json:"discount_bps,omitempty"`
	DiscountCents  *int64   `json:"discount_cents,omitempty"`
	SurchargeBps   *int64   `json:"surcharge_bps,omitempty"`
	SurchargeCents *int64   `json:"surcharge_cents,omitempty"`
	PriceUOM       string   `json:"price_uom"`
}

// Override is one customer_category_overrides row (ABSOLUTE forbidden by DB CHECK).
type Override struct {
	ID          string `json:"id"`
	CustomerID  string `json:"customer_id"`
	CategoryID  string `json:"category_id"`
	CalcType    string `json:"calc_type"`
	PriceCents  *int64 `json:"price_cents,omitempty"`
	DiscountBps *int64 `json:"discount_bps,omitempty"`
	MarkupBps   *int64 `json:"markup_bps,omitempty"`
	PriceUOM    string `json:"price_uom"`
	Notes       string `json:"notes"`
	Version     int64  `json:"version"`
}

// ListPrice is the retail anchor (pricing_list_prices) for a product.
type ListPrice struct {
	ProductID      string `json:"product_id"`
	ListPriceCents int64  `json:"list_price_cents"`
	PriceUOM       string `json:"price_uom"`
}

// CategoryPolicy is the pricing-owned margin policy for a category.
type CategoryPolicy struct {
	CategoryID          string `json:"category_id"`
	MarginTargetBps     int64  `json:"margin_target_bps"`
	MarginFloorBps      int64  `json:"margin_floor_bps"`
	EnforcementDisabled bool   `json:"margin_enforcement_disabled"`
}

// ProductPolicy is the pricing-owned per-product enforcement-disable flag.
type ProductPolicy struct {
	ProductID           string `json:"product_id"`
	EnforcementDisabled bool   `json:"margin_enforcement_disabled"`
}

// ResolveInput is the resolver request. CostCents is basis-agnostic, the CALLER passes
// the chosen cost basis (the cost-basis dropdown is owned by costing/stock, not A-07),
// expressed per the product's base UOM.
type ResolveInput struct {
	CustomerID   string  `json:"customer_id"`
	CustomerType string  `json:"customer_type"`
	ProductID    string  `json:"product_id"`
	CategoryID   string  `json:"category_id"`
	BranchID     string  `json:"branch_id"`
	QtyEntered   float64 `json:"qty_entered"`
	UOMEntered   string  `json:"uom_entered"`
	CostCents    int64   `json:"cost_cents"`

	// OverrideRequested is DIB-420's renamed `has_approved_override`. THE WIRE NAME IS UNCHANGED so
	// no caller breaks; the GO name changed because the old one asserted a fact the client is not in
	// a position to know. It is a REQUEST for a below-floor price; whether one is honoured is decided
	// server-side by OverrideAuthority against the auth-guard-resolved caller (override_authority.go).
	OverrideRequested bool   `json:"has_approved_override"`
	UserID            string `json:"-"` // for audit attribution; not part of the public body

	// E3 unpark, the DOCUMENT this resolve is for, so a pricing_audit_log row written during the
	// resolve can be clicked through to the quote/order the reader is actually looking at.
	//
	// OPTIONAL, AND OPTIONAL IS THE WHOLE DESIGN. Migration 0098 parked the quote/order drill-through
	// with the reason "pricing.ResolveInput carries customer/product/category/branch and NO document
	// id, so the resolver physically does not know which quote or order it is pricing for". Adding
	// the pair as REQUIRED would have broken every existing caller (POS, procurement, the low-margin
	// preview, the portal reorder) to serve one of them. Left empty, the resolver behaves exactly as
	// it did before and the audit row falls back to the product/category ref.
	//
	// DocRefType must be RefQuote or RefOrder; anything else is DROPPED by normaliseAuditRef rather
	// than written, because the 0114 CHECK would reject the INSERT and every AppendAudit call site
	// discards its error, a rejected INSERT is a silently lost audit row.
	DocRefType string `json:"doc_ref_type,omitempty"`
	DocRefID   string `json:"doc_ref_id,omitempty"`
}

// Breakdown is the resolver result, the pre-tax price trio plus the "why".
// NEVER carries tax_cents (M5 owns tax).
type Breakdown struct {
	OriginalCents  int64   `json:"original_cents"` // the retail anchor in the result price_uom
	FinalCents     int64   `json:"final_cents"`    // resolved unit price per price_uom (pre-tax)
	DiscountBps    int64   `json:"discount_bps"`   // off-anchor bps (negative = surcharge/uplift)
	Source         string  `json:"source"`
	Detail         string  `json:"detail"`
	PriceUOM       string  `json:"price_uom"`
	QtyBase        float64 `json:"qty_base"`         // entered qty converted to the product base UOM
	LineTotalCents int64   `json:"line_total_cents"` // FinalCents × qty-in-price_uom (pre-tax)
	MarginBps      int64   `json:"margin_bps"`
	FloorBps       int64   `json:"floor_bps"`
	Blocked        bool    `json:"blocked"`
	BlockPolicy    string  `json:"block_policy,omitempty"`

	// - DIB-70 promotions / clearance -
	// PromoCode/PromoName are set ONLY when a promotion actually won base selection (Source ==
	// SourcePromotion). A promo that matched and lost the never-worse guard leaves these empty and
	// says so in Detail, the breakdown must not imply a sale price the customer is not getting.
	PromoCode string `json:"promo_code,omitempty"`
	PromoName string `json:"promo_name,omitempty"`
	// Clearance is the DIB-154 BADGE, and it is deliberately independent of whether a clearance
	// PRICE was applied. An item flagged for clearance with no price still shows the badge on POS
	// search and quote lines while pricing through the ordinary waterfall, that is the "flag these,
	// the manager will deal per customer" case the flag exists for.
	Clearance       bool   `json:"clearance"`
	ClearanceReason string `json:"clearance_reason,omitempty"`

	// BypassReason is set when the margin gate did NOT run (enforcement disabled), and, since
	// DIB-420, when it ran, blocked, and a server-side authority cleared the block anyway. It
	// mirrors the value written to pricing_audit_log.bypass_reason, so a caller reading the
	// breakdown sees the same reason a manager reading the audit log will.
	BypassReason string `json:"bypass_reason,omitempty"`

	// - DIB-420 margin-override request/decision -
	// OverrideRequested echoes back that the CALLER ASKED for a below-floor price. It is set
	// whenever the price is below the floor and the request carried the flag, whether or not the
	// request was honoured. A refused request that echoed nothing is indistinguishable from one
	// that was never made, and the difference is the whole of DIB-420.
	OverrideRequested bool `json:"override_requested,omitempty"`
	// OverrideAuthority names the SERVER-SIDE source that honoured the request (AuthorityApproval
	// or AuthorityManagerScope). Empty means no override was honoured, either none was asked for,
	// or the request carried no authority and Blocked is set.
	OverrideAuthority string `json:"override_authority,omitempty"`
}

// MarginBlock is what a MarginGate returns when a price is below the effective floor.
type MarginBlock struct {
	Policy    string
	Detail    string
	MarginBps int64
	FloorBps  int64
}

// AuditEntry is one pricing_audit_log row.
type AuditEntry struct {
	EntityType string
	EntityID   string
	Action     string // CREATE | UPDATE | DELETE | BYPASS
	FieldName  string
	OldValue   string
	NewValue   string
	UserID     string
	// DIB-402 drill-through target: the NAVIGABLE record this change lands on, as opposed to
	// EntityType/EntityID, which name the pricing CONFIG ROW that was written. Both empty means
	// "no derivable target", the viewer then renders the row without a link rather than guessing.
	// Valid RefEntityType values are RefProduct / RefCustomer / RefCategory / RefQuote / RefOrder.
	RefEntityType string
	RefEntityID   string
	// BypassReason (W4-A, migration 0114), why the margin gate did not run. Only ever set on an
	// Action=="BYPASS" row; the 0114 CHECK enforces that, so setting it elsewhere loses the row.
	BypassReason string
}

// RuleFilter narrows a rules list query.
type RuleFilter struct {
	CustomerType string
	CategoryID   string
	Scope        string
}

// - admin-editor (A-07 Tier-B) types -

// RulePatch is a sparse edit to a pricing_rules row. A nil pointer means "leave
// unchanged"; a set pointer means "set to this value" (incl. priority 0 / is_active
// false / a validity bound). This pointer shape is what lets the rule editor edit
// priority=0 and retire a rule, the gap the old Rule-valued patch could not express
// (R-3: applyRulePatch ignored priority=0 and could not touch is_active/validity).
type RulePatch struct {
	Scope        *string
	CustomerID   *string
	CustomerType *string
	CategoryID   *string
	ProductID    *string
	BranchID     *string
	CalcType     *string
	PriceCents   *int64
	DiscountBps  *int64
	MarkupBps    *int64
	PriceUOM     *string
	Priority     *int64
	ValidFrom    *time.Time
	ValidTo      *time.Time
	// ClearValidFrom/ClearValidTo express "set this validity bound back to NULL", distinct
	// from a nil ValidFrom/ValidTo pointer, which means "leave unchanged". This is the tri-state
	// (absent / clear / set) the editor needs to broaden a time-boxed rule back to always-valid
	// (finding #7): a bare *time.Time pointer cannot tell "no edit" apart from "clear to null".
	ClearValidFrom bool
	ClearValidTo   bool
	DetailText     *string
	IsActive       *bool
}

// AuditFilter narrows the audit-log read (US-P12). Limit is clamped server-side.
type AuditFilter struct {
	EntityType string
	EntityID   string
	Action     string
	Limit      int
}

// AuditLogRow is one pricing_audit_log row for the read-only viewer (newest first).
// Unlike AuditEntry (the write shape) it carries the row id + created_at + actor.
type AuditLogRow struct {
	ID         string    `json:"id"`
	EntityType string    `json:"entity_type"`
	EntityID   string    `json:"entity_id"`
	Action     string    `json:"action"`
	FieldName  string    `json:"field_name,omitempty"`
	OldValue   string    `json:"old_value,omitempty"`
	NewValue   string    `json:"new_value,omitempty"`
	UserID     string    `json:"user_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	// DIB-402: the drill-through target, omitted from the JSON entirely when absent so the
	// viewer's "is there a link?" test is the presence of the field, not a sentinel string.
	RefEntityType string `json:"ref_entity_type,omitempty"`
	RefEntityID   string `json:"ref_entity_id,omitempty"`
	// W4-A (0114): why the margin gate did not run. Only ever present on a BYPASS row.
	BypassReason string `json:"bypass_reason,omitempty"`
}

// LowMarginEntry is one line of the batch low-margin preview (US-P13). A line is either a
// breaching result (Level "warn", margin < category target, ≥ floor, or "critical", margin
// < floor) OR an INVALID line (Invalid=true + Reason) that could not be evaluated. Invalid
// lines are SURFACED, never silently dropped (finding #6): a dropped line must be visible and
// never indistinguishable from a clean one. Clean (non-breaching, valid) lines are omitted.
type LowMarginEntry struct {
	ProductID  string `json:"product_id"`
	CustomerID string `json:"customer_id,omitempty"`
	CategoryID string `json:"category_id,omitempty"`
	FinalCents int64  `json:"final_cents"`
	MarginBps  int64  `json:"margin_bps"`
	TargetBps  int64  `json:"target_bps"`
	FloorBps   int64  `json:"floor_bps"`
	Level      string `json:"level"`
	Source     string `json:"source"`
	PriceUOM   string `json:"price_uom"`
	// Invalid marks a line that failed validation (qty ≤ 0, bad cost) or failed to resolve.
	// Reason is the human-readable cause. Both are zero/empty for a normal breaching result.
	Invalid bool   `json:"invalid,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// - D-PA-3 daily low-margin scan (the auto-populated live-sales report) -

// LowMarginSaleLine is ONE raw committed sold line the D-PA-3 scan reads from the live-sales feed
// (order_lines JOIN orders JOIN customers JOIN products LEFT JOIN product_branch_costs). It is the
// PRE-classification row: the service converts the per-BASE-uom WAC into the line's price_uom,
// computes the margin against the ACTUAL charged price, resolves the category target/floor and
// grades it. AvgUnitCostCents is nil when the product has NO product_branch_costs row at the sale's
// branch (cost unknown), surfaced as an invalid "no cost on file" row, never omitted (design #5).
type LowMarginSaleLine struct {
	OrderLineID      string
	OrderID          string
	OrderNo          string
	SoldAt           time.Time
	BranchID         string
	CustomerID       string
	CustomerType     string
	ProductID        string
	CategoryID       string
	SKU              string
	ProductName      string
	QtyEntered       float64
	UOMEntered       string
	UnitPriceCents   int64 // the ACTUAL charged price per price_uom (order_lines.unit_price_cents)
	PriceUOM         string
	AvgUnitCostCents *int64 // per product BASE uom; nil = no product_branch_costs row (cost missing)
}

// LowMarginScanRow is one GRADED row of the principal's daily low-margin report (D-PA-3). A row is either a
// breaching result (Level "warn", margin < category target, ≥ floor, or "critical", margin <
// floor) OR an INVALID row (Level "invalid" + Reason) whose cost is unknown or whose charged price
// is non-positive. Invalid rows are SURFACED, never dropped (mirrors the classifier's invalid
// discipline). Clean (at/above target) lines are omitted. Money is pre-tax cents (M5 owns tax).
type LowMarginScanRow struct {
	OrderLineID         string    `json:"order_line_id"`
	OrderID             string    `json:"order_id"`
	OrderNo             string    `json:"order_no"`
	SoldAt              time.Time `json:"sold_at"`
	BranchID            string    `json:"branch_id"`
	CustomerID          string    `json:"customer_id,omitempty"`
	CustomerType        string    `json:"customer_type"`
	ProductID           string    `json:"product_id"`
	SKU                 string    `json:"sku"`
	ProductName         string    `json:"product_name"`
	QtyEntered          float64   `json:"qty_entered"`
	UOMEntered          string    `json:"uom_entered"`
	UnitPriceCents      int64     `json:"unit_price_cents"` // the ACTUAL sold price per price_uom
	PriceUOM            string    `json:"price_uom"`
	CostInPriceUOMCents int64     `json:"cost_in_price_uom_cents"` // WAC converted BASE→price_uom
	MarginBps           int64     `json:"margin_bps"`
	TargetBps           int64     `json:"target_bps"`
	FloorBps            int64     `json:"floor_bps"`
	Level               string    `json:"level"`            // warn | critical | invalid
	Reason              string    `json:"reason,omitempty"` // set only when level == "invalid"
}
