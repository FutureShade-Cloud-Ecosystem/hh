package pricing

import (
	"context"
	"time"
)

// - A-08 Price Change Log, batch manufacturer price updates -
//
// This file adds the A-08 surface ON TOP of the A-07 resolver without touching it:
// staged manufacturer price batches with a scheduled effective_date and maker-checker
// (separation-of-duties) approval, CSV imports over the V-*-keyed price_import_profiles
// with retail auto-derivation, an append-only who/when/before/after change log, and the
// variance-alert outlier badge stamped at derivation time.
//
// Pricing is UNIFIED cross-yard: the retail anchor (pricing_list_prices) is ONE row per
// product, org-wide, per-location differences are margin-floor config only (pricing_
// category_policy, already A-07). Applying a batch therefore writes a single list price
// per product, never a per-branch retail.
//
// Manufacturer/vendor names surface ONLY here (internal staff screens), never on the
// portal (rule 1). Nothing in this file is reachable from a portal route.

// Batch lifecycle statuses (mirror the 0039 price_batches CHECK).
const (
	BatchDraft     = "DRAFT"
	BatchStaged    = "STAGED"   // lines imported + derived, awaiting approval
	BatchApproved  = "APPROVED" // approved by a DIFFERENT user than the maker
	BatchApplied   = "APPLIED"  // effective, written to the retail anchor + change log
	BatchCancelled = "CANCELLED"
)

// Retail-derivation anchor precedence (plan §A-08). A trade price + estimated FOB freight
// wins; else cost × markup (×1.85 default); else the manufacturer's suggested retail.
const (
	DeriveTradePlusFOB = "TRADE_PLUS_FOB" // GTA trade + FOB $0.40-0.50/sqft (freight ESTIMATED)
	DeriveCostMarkup   = "COST_MARKUP"    // cost × markup_bps (×1.85 default)
	DeriveMfrRetail    = "MFR_RETAIL"     // manufacturer-supplied retail, used as-is
	DeriveNone         = "NONE"           // no usable source column → line cannot be derived
)

// AnchorPolicy is one pricing_anchor_policy row (org default = branch NULL). ALL values are
// placeholders pending Amanda's confirmation (D-021); every number is a DB row, not a const.
type AnchorPolicy struct {
	BranchID         string `json:"branch_id,omitempty"`
	MarkupBps        int64  `json:"markup_bps"`         // ×1.85 = 18500
	FOBLowCents      int64  `json:"fob_low_cents"`      // $0.40/sqft band low
	FOBHighCents     int64  `json:"fob_high_cents"`     // $0.50/sqft band high
	VarianceAlertBps int64  `json:"variance_alert_bps"` // outlier threshold (15% = 1500)
}

// fobMidCents is the estimated FOB freight added per price_uom (SF), the band midpoint.
// FOB is a $/sqft band and pavers price per SF, so it applies directly to a per-SF retail.
func (p AnchorPolicy) fobMidCents() int64 { return (p.FOBLowCents + p.FOBHighCents) / 2 }

// ImportProfile is one price_import_profiles row, the CSV column mapping for a manufacturer
// vendor (seeded for the 7 V-* vendors). mapping keys: sku_col, cost_col, retail_col,
// effective_col, and the OPTIONAL trade_col (GTA trade price → FOB derivation path).
type ImportProfile struct {
	ID         string            `json:"id"`
	VendorID   string            `json:"vendor_id"`
	VendorName string            `json:"vendor_name,omitempty"` // INTERNAL-only (rule 1)
	Name       string            `json:"name"`
	Mapping    map[string]string `json:"mapping"`
	MarkupBps  int64             `json:"markup_bps"`
	IsActive   bool              `json:"is_active"`
}

func (p ImportProfile) col(key, def string) string {
	if v, ok := p.Mapping[key]; ok && v != "" {
		return v
	}
	return def
}

// ProductRef is the SKU→product lookup result from the catalog read seam.
type ProductRef struct {
	ID       string `json:"id"`
	SKU      string `json:"sku"`
	PriceUOM string `json:"price_uom"`
}

// PriceBatch is one price_batches row, a staged manufacturer update.
type PriceBatch struct {
	ID            string     `json:"id"`
	BatchNo       string     `json:"batch_no"`
	VendorID      string     `json:"vendor_id,omitempty"`
	VendorName    string     `json:"vendor_name,omitempty"` // INTERNAL-only (rule 1)
	ProfileID     string     `json:"profile_id,omitempty"`
	Status        string     `json:"status"`
	EffectiveDate *time.Time `json:"effective_date,omitempty"`
	SourceRef     string     `json:"source_ref"`
	LineCount     int        `json:"line_count"`
	OutlierCount  int        `json:"outlier_count"`
	CreatedBy     string     `json:"created_by,omitempty"`
	ApprovedBy    string     `json:"approved_by,omitempty"`
	ApprovedAt    *time.Time `json:"approved_at,omitempty"`
	AppliedAt     *time.Time `json:"applied_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// PriceChangeLine is one price_change_lines row, before/after per product with the outlier
// flag stamped at derivation. Unmatched SKUs keep ProductID empty (surfaced, never applied).
type PriceChangeLine struct {
	ID                string `json:"id,omitempty"`
	BatchID           string `json:"batch_id"`
	ProductID         string `json:"product_id,omitempty"`
	SKU               string `json:"sku"`
	BeforeCostCents   *int64 `json:"before_cost_cents,omitempty"`
	AfterCostCents    *int64 `json:"after_cost_cents,omitempty"`
	BeforeRetailCents *int64 `json:"before_retail_cents,omitempty"`
	AfterRetailCents  *int64 `json:"after_retail_cents,omitempty"`
	VarianceBps       *int64 `json:"variance_bps,omitempty"`
	IsOutlier         bool   `json:"is_outlier"`
	// DerivedFrom / FreightEstimated are presentation metadata (the ESTIMATED-freight badge +
	// the derivation-anchor column). They are NOT persisted columns, recomputable from source.
	DerivedFrom      string `json:"derived_from,omitempty"`
	FreightEstimated bool   `json:"freight_estimated,omitempty"`
	// Unmatched marks a SKU with no catalog product (product_id NULL). It is reported and
	// skipped at apply, never silently dropped.
	Unmatched bool `json:"unmatched,omitempty"`
}

// ChangeLogRow is one price_change_log row (append-only who/when/before/after).
type ChangeLogRow struct {
	ID            string     `json:"id"`
	ProductID     string     `json:"product_id,omitempty"`
	BatchID       string     `json:"batch_id,omitempty"`
	Field         string     `json:"field"` // 'cost_cents' | 'retail_cents'
	BeforeCents   *int64     `json:"before_cents,omitempty"`
	AfterCents    *int64     `json:"after_cents,omitempty"`
	EffectiveDate *time.Time `json:"effective_date,omitempty"`
	ChangedBy     string     `json:"changed_by,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// BatchFilter narrows a batch list query.
type BatchFilter struct {
	Status   string
	VendorID string
	Limit    int
}

// ChangeLogFilter narrows the append-only change-log read (newest first).
type ChangeLogFilter struct {
	ProductID string
	BatchID   string
	Limit     int
}

// ImportResult is what ImportBatch returns to the UI: the full derived line set plus the
// derived presentation aids, a 5-row preview, the unmatched-SKU report, the ESTIMATED-freight
// list, and the outlier count.
type ImportResult struct {
	Batch        PriceBatch        `json:"batch"`
	Preview      []PriceChangeLine `json:"preview"`       // first 5 matched lines
	Unmatched    []PriceChangeLine `json:"unmatched"`     // SKUs with no catalog product
	Estimated    []PriceChangeLine `json:"estimated"`     // lines with an ESTIMATED FOB freight
	Outliers     []PriceChangeLine `json:"outliers"`      // variance beyond the alert threshold
	TotalLines   int               `json:"total_lines"`   // rows parsed (matched + unmatched)
	MatchedLines int               `json:"matched_lines"` // rows with a catalog product
}

// derivationInput is the pure-function derivation request assembled from a CSV row.
type derivationInput struct {
	costCents      *int64
	tradeCents     *int64
	mfrRetailCents *int64
}

// derivationResult is the pure-function derivation output.
type derivationResult struct {
	retailCents      int64
	source           string
	freightCents     int64
	freightEstimated bool
	ok               bool // false when no source column yields a retail (DeriveNone)
}

// batchStore is the A-08 persistence seam, SEPARATE from pricingStore so the existing
// resolver fake (service_test.go) is untouched. The pgx *Repository implements both; unit
// tests inject an in-memory fake (batch_service_test.go).
type batchStore interface {
	AnchorPolicy(ctx context.Context, branchID string) (AnchorPolicy, error)
	ListImportProfiles(ctx context.Context) ([]ImportProfile, error)
	GetImportProfile(ctx context.Context, id string) (ImportProfile, error)
	UpdateImportProfileMapping(ctx context.Context, id string, mapping map[string]string) (ImportProfile, error)
	ProductBySKU(ctx context.Context, sku string) (ProductRef, error) // errNotFound when unmatched
	CurrentListPrice(ctx context.Context, productID string) (*int64, error)

	CreateBatch(ctx context.Context, b PriceBatch) (PriceBatch, error)
	GetBatch(ctx context.Context, id string) (PriceBatch, error)
	ListBatches(ctx context.Context, f BatchFilter) ([]PriceBatch, error)
	UpdateBatch(ctx context.Context, b PriceBatch) (PriceBatch, error)
	ReplaceBatchLines(ctx context.Context, batchID string, lines []PriceChangeLine) error
	ListBatchLines(ctx context.Context, batchID string) ([]PriceChangeLine, error)
	DueBatches(ctx context.Context, asOf time.Time) ([]PriceBatch, error)

	// ApplyBatch atomically upserts the retail anchor for every matched line and appends the
	// append-only change-log rows, then flips the batch to APPLIED. It is IDEMPOTENT: a batch
	// already APPLIED is a no-op returning its recorded applied count.
	ApplyBatch(ctx context.Context, batchID, userID string) (int, error)

	ListChangeLog(ctx context.Context, f ChangeLogFilter) ([]ChangeLogRow, error)
}
