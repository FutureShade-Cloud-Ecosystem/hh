package bistrack

import "time"

// shapes.go, the SOURCE (BisTrack recorded-schema) and TARGET (native) row shapes the mapping layer
// moves between. The BT* fields are drawn from the A-18 recorded schema (docs/legacy/bistrack_map.json:
// subsystems + domain_mappings), the columns the map calls out as load-bearing for each entity. They
// are intentionally a SUBSET: phase 0 freezes the mapping seam and the rules that bite, not every one of
// BisTrack's 189-column tables. When the real export lands, fields are ADDED here; the mapping signatures
// do not change.
//
// Every BT* row carries BisTrackID (its natural key, the crosswalk + cursor key) and UpdatedAt (the
// cursor's timestamp half). Stringly-typed ids because BisTrack keys are ints today but the wire format
// is not frozen (freeze the interface, not the wire, supervisor C1).

// BTCustomer is a dbo.Customer row (+ its source table, so the excluded Customer_BAK_241031 backup is
// visible rather than silently unioned in). Credit-hold columns carry 1:1 per the map.
type BTCustomer struct {
	BisTrackID  string    // dbo.Customer.CustomerID
	SourceTable string    // "Customer" (mapped) | "Customer_BAK_241031" (EXCLUDE)
	AccountNo   string    // account number / code
	Name        string    // customer name
	OnHold      bool      // dbo.Customer.OnHold (credit hold, carries 1:1)
	UpdatedAt   time.Time // change watermark (cursor)
}

// BTProduct is a dbo.Product row flattened to the fields the greenfield catalog + promoted UDFs need
// (migration 0018/0051 promote-list: is_featured, is_special_order, …).
type BTProduct struct {
	BisTrackID     string
	SKU            string // product code
	Name           string
	ProductGroup   string // dbo.ProductGroup hierarchy → product_categories
	IsFeatured     bool   // udfDibbitsFavourite → is_featured
	IsSpecialOrder bool   // udfSpecialOrder → is_special_order
	UpdatedAt      time.Time
}

// BTInventory is a per-product-per-branch stock position (StockValuationData / StockTransaction). The map
// flags negative on-hand + stale allocations to resolve BEFORE snapshot, the mapping surfaces those.
type BTInventory struct {
	BisTrackID        string // the stock row key
	ProductBisTrackID string // FK to the BisTrack product
	BranchCode        string
	OnHandBase        float64 // on-hand in base units
	ValuationMethod   string  // FIFO | WEIGHTED_AVG | STANDARD (confirm-first per the map)
	UpdatedAt         time.Time
}

// BTOrder is a dbo.OrderHeader row. BisTrack stores quotes AND orders in one table differentiated by
// OrderType; the mapping filters on OrderType (never the empty Quote tables).
type BTOrder struct {
	BisTrackID         string
	OrderType          string // "ORDER" (mapped) | "QUOTE"/other (skipped)
	CustomerBisTrackID string
	Status             string // BisTrack status → native lifecycle
	UpdatedAt          time.Time
}

// -- native (target) shapes ------------------------------------------------------------------------
//
// Thin projections of the native domain a mapping produces. Phase 0 does not WRITE these into the
// customers/products/inventory/orders tables (no live sync); it proves the transformation. NativeID is
// the crosswalk result, empty when the BisTrack row has no native counterpart yet (a pull would create
// it; reconciliation records that as MISSING_IN_NATIVE).

type MappedCustomer struct {
	NativeID  string `json:"native_id,omitempty"`
	AccountNo string `json:"account_no"`
	Name      string `json:"name"`
	OnHold    bool   `json:"on_hold"`
}

type MappedProduct struct {
	NativeID       string `json:"native_id,omitempty"`
	SKU            string `json:"sku"`
	Name           string `json:"name"`
	CategoryPath   string `json:"category_path"`
	IsFeatured     bool   `json:"is_featured"`
	IsSpecialOrder bool   `json:"is_special_order"`
}

type MappedInventory struct {
	NativeProductID string  `json:"native_product_id,omitempty"`
	BranchCode      string  `json:"branch_code"`
	OnHandBase      float64 `json:"on_hand_base"`
	ValuationMethod string  `json:"valuation_method"`
}

type MappedOrder struct {
	NativeID         string `json:"native_id,omitempty"`
	CustomerNativeID string `json:"customer_native_id,omitempty"`
	Status           string `json:"status"`
}
