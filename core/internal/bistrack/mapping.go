package bistrack

// mapping.go, DIB-53 phase 0 the PURE mapping layer: BisTrack recorded-schema rows → native shapes,
// applying the transformation rules the A-18 map records (docs/legacy/bistrack_map.json domain_mappings).
// No I/O, every function is a total function of its inputs, so it is exhaustively fixture-testable
// (mapping_test.go). The cardinal rule (supervisor-praised): an input the layer cannot faithfully map is
// SURFACED as a SyncDiscrepancy, never silently dropped.
//
// Each Map* returns (mapped, discrepancy). A non-nil discrepancy with a zero mapped value means "not
// mapped, and here's why" (excluded / unmapped / rule violation). A non-nil discrepancy WITH a mapped
// value means "mapped, but note this" (e.g. no native counterpart yet → MISSING_IN_NATIVE, which a pull
// resolves by creating). Callers persist whatever discrepancies come back.

// IDResolver resolves a BisTrack id to its native uuid via the bistrack_id_map crosswalk. It is the seam
// the mapping layer uses to decide "does this row already have a native counterpart?", satisfied by the
// repository in production and by an in-memory map in tests. ok=false means no crosswalk row (yet).
type IDResolver interface {
	Resolve(entity EntityType, bistrackID string) (nativeID string, ok bool)
}

// mapIDResolver is a trivial in-memory IDResolver (tests; and any caller that already holds the crosswalk).
type mapIDResolver map[string]string

func (m mapIDResolver) Resolve(entity EntityType, bistrackID string) (string, bool) {
	id, ok := m[string(entity)+"|"+bistrackID]
	return id, ok
}

// discrep builds a discrepancy for one source row.
func discrep(entity EntityType, bistrackID string, kind DiscrepancyKind, detail map[string]any) *SyncDiscrepancy {
	return &SyncDiscrepancy{EntityType: entity, BisTrackID: bistrackID, Kind: kind, Detail: detail}
}

// MapCustomer maps a dbo.Customer row. Rules: the stale Customer_BAK_241031 backup is EXCLUDED (a blind
// union would near-double the customer base), surfaced as UNMAPPED, not silently skipped; a row missing
// its account/name is a MAPPING_ERROR; credit-hold (OnHold) carries 1:1; an id with no crosswalk entry is
// MISSING_IN_NATIVE (a pull creates it) but still maps its fields.
func MapCustomer(bt BTCustomer, ids IDResolver) (MappedCustomer, *SyncDiscrepancy) {
	if bt.SourceTable == "Customer_BAK_241031" {
		return MappedCustomer{}, discrep(EntityCustomer, bt.BisTrackID, KindUnmapped,
			map[string]any{"reason": "excluded_backup_table", "source_table": bt.SourceTable})
	}
	if bt.AccountNo == "" && bt.Name == "" {
		return MappedCustomer{}, discrep(EntityCustomer, bt.BisTrackID, KindMappingError,
			map[string]any{"reason": "missing_account_and_name"})
	}
	m := MappedCustomer{AccountNo: bt.AccountNo, Name: bt.Name, OnHold: bt.OnHold}
	if nativeID, ok := ids.Resolve(EntityCustomer, bt.BisTrackID); ok {
		m.NativeID = nativeID
		return m, nil
	}
	return m, discrep(EntityCustomer, bt.BisTrackID, KindMissingInNative, map[string]any{"account_no": bt.AccountNo})
}

// MapProduct maps a dbo.Product row: ProductGroup → category path, promoted UDFs (is_featured,
// is_special_order) carry, SKU is required (MAPPING_ERROR if absent). Unmapped id → MISSING_IN_NATIVE.
func MapProduct(bt BTProduct, ids IDResolver) (MappedProduct, *SyncDiscrepancy) {
	if bt.SKU == "" {
		return MappedProduct{}, discrep(EntityProduct, bt.BisTrackID, KindMappingError,
			map[string]any{"reason": "missing_sku"})
	}
	m := MappedProduct{
		SKU: bt.SKU, Name: bt.Name, CategoryPath: bt.ProductGroup,
		IsFeatured: bt.IsFeatured, IsSpecialOrder: bt.IsSpecialOrder,
	}
	if nativeID, ok := ids.Resolve(EntityProduct, bt.BisTrackID); ok {
		m.NativeID = nativeID
		return m, nil
	}
	return m, discrep(EntityProduct, bt.BisTrackID, KindMissingInNative, map[string]any{"sku": bt.SKU})
}

// MapInventory maps a stock position. Rule from the map ("resolve negative on-hand BEFORE snapshot"): a
// negative on-hand is a MAPPING_ERROR to fix at source, not a value to migrate. The stock row is keyed to
// a BisTrack PRODUCT, so the crosswalk lookup is on the product id; an unmapped product → MISSING_IN_NATIVE.
func MapInventory(bt BTInventory, ids IDResolver) (MappedInventory, *SyncDiscrepancy) {
	if bt.OnHandBase < 0 {
		return MappedInventory{}, discrep(EntityInventory, bt.BisTrackID, KindMappingError,
			map[string]any{"reason": "negative_on_hand", "on_hand_base": bt.OnHandBase, "branch": bt.BranchCode})
	}
	m := MappedInventory{BranchCode: bt.BranchCode, OnHandBase: bt.OnHandBase, ValuationMethod: bt.ValuationMethod}
	if nativeProductID, ok := ids.Resolve(EntityProduct, bt.ProductBisTrackID); ok {
		m.NativeProductID = nativeProductID
		return m, nil
	}
	return m, discrep(EntityInventory, bt.BisTrackID, KindMissingInNative,
		map[string]any{"reason": "product_not_mapped", "product_bistrack_id": bt.ProductBisTrackID})
}

// MapOrder maps a dbo.OrderHeader row. BisTrack unifies quotes + orders under OrderType; anything that is
// not an ORDER is filtered out (surfaced as UNMAPPED, never the empty Quote tables). The order's customer
// must be in the crosswalk; an unmapped customer is MISSING_IN_NATIVE (the customer pull must land first).
func MapOrder(bt BTOrder, ids IDResolver) (MappedOrder, *SyncDiscrepancy) {
	if !isOrderType(bt.OrderType) {
		return MappedOrder{}, discrep(EntityOrder, bt.BisTrackID, KindUnmapped,
			map[string]any{"reason": "not_an_order", "order_type": bt.OrderType})
	}
	m := MappedOrder{Status: bt.Status}
	custNative, custOK := ids.Resolve(EntityCustomer, bt.CustomerBisTrackID)
	if custOK {
		m.CustomerNativeID = custNative
	}
	if nativeID, ok := ids.Resolve(EntityOrder, bt.BisTrackID); ok {
		m.NativeID = nativeID
		if !custOK {
			// The order maps, but its customer does not, a real reconciliation gap to record.
			return m, discrep(EntityOrder, bt.BisTrackID, KindMissingInNative,
				map[string]any{"reason": "customer_not_mapped", "customer_bistrack_id": bt.CustomerBisTrackID})
		}
		return m, nil
	}
	return m, discrep(EntityOrder, bt.BisTrackID, KindMissingInNative, map[string]any{"order_type": bt.OrderType})
}

// isOrderType reports whether a BisTrack OrderType is a real order (vs a quote/other). Case-insensitive
// on the canonical value; the exact OrderType vocabulary is confirmed at first live connection.
func isOrderType(t string) bool {
	switch t {
	case "ORDER", "order", "Order":
		return true
	default:
		return false
	}
}
