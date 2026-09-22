package bistrack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// mapping_test.go, DIB-53 the fixture-backed mapping tests. The BT* row fixtures (testdata/*.json) are
// shaped from the A-18 recorded schema; TestMappingTracesToRecordedSchema pins that the four mapped
// entities are real subsystems in docs/legacy/bistrack_map.json (not invented). The per-entity tests
// drive Map* over the fixtures and assert the transformation rules + that every un-mappable row surfaces
// the RIGHT discrepancy instead of being silently dropped.

func loadFixture[T any](t *testing.T, name string) []T {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var out []T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	return out
}

func loadCrosswalk(t *testing.T) mapIDResolver {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "crosswalk.json"))
	if err != nil {
		t.Fatalf("read crosswalk: %v", err)
	}
	m := mapIDResolver{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode crosswalk: %v", err)
	}
	return m
}

// kindOf returns the discrepancy kind, or "" for a clean map (nil discrepancy).
func kindOf(d *SyncDiscrepancy) DiscrepancyKind {
	if d == nil {
		return ""
	}
	return d.Kind
}

func TestMapCustomer_Fixtures(t *testing.T) {
	ids := loadCrosswalk(t)
	rows := loadFixture[BTCustomer](t, "customers.json")
	byID := map[string]BTCustomer{}
	for _, r := range rows {
		byID[r.BisTrackID] = r
	}

	// C1: mapped clean, crosswalked, credit-hold carries 1:1.
	m, d := MapCustomer(byID["C1"], ids)
	if kindOf(d) != "" {
		t.Errorf("C1: want clean, got %s", kindOf(d))
	}
	if m.NativeID == "" || !m.OnHold || m.AccountNo != "A-100" {
		t.Errorf("C1: mapped = %+v (want native id set, on_hold true, account A-100)", m)
	}
	// C2: valid but no crosswalk → MISSING_IN_NATIVE, fields still mapped.
	if m2, d2 := MapCustomer(byID["C2"], ids); kindOf(d2) != KindMissingInNative || m2.Name != "NewCo" {
		t.Errorf("C2: got kind=%s mapped=%+v, want MISSING_IN_NATIVE + fields", kindOf(d2), m2)
	}
	// C3: the excluded backup table → UNMAPPED, NOT silently skipped, and NOT mapped.
	if m3, d3 := MapCustomer(byID["C3"], ids); kindOf(d3) != KindUnmapped || m3.NativeID != "" {
		t.Errorf("C3: got kind=%s, want UNMAPPED (excluded backup)", kindOf(d3))
	} else if d3.Detail["reason"] != "excluded_backup_table" {
		t.Errorf("C3: want reason excluded_backup_table, got %v", d3.Detail["reason"])
	}
	// C4: no account and no name → MAPPING_ERROR.
	if _, d4 := MapCustomer(byID["C4"], ids); kindOf(d4) != KindMappingError {
		t.Errorf("C4: got %s, want MAPPING_ERROR", kindOf(d4))
	}
}

func TestMapProduct_Fixtures(t *testing.T) {
	ids := loadCrosswalk(t)
	rows := loadFixture[BTProduct](t, "products.json")
	byID := map[string]BTProduct{}
	for _, r := range rows {
		byID[r.BisTrackID] = r
	}
	// P1: clean, crosswalked, UDFs + category carried.
	if m, d := MapProduct(byID["P1"], ids); kindOf(d) != "" || m.NativeID == "" || !m.IsFeatured || m.CategoryPath != "Paver" {
		t.Errorf("P1: kind=%s mapped=%+v, want clean + featured + category Paver", kindOf(d), m)
	}
	// P2: no crosswalk → MISSING_IN_NATIVE, special-order UDF carried.
	if m, d := MapProduct(byID["P2"], ids); kindOf(d) != KindMissingInNative || !m.IsSpecialOrder {
		t.Errorf("P2: kind=%s mapped=%+v, want MISSING_IN_NATIVE + special-order", kindOf(d), m)
	}
	// P3: no SKU → MAPPING_ERROR.
	if _, d := MapProduct(byID["P3"], ids); kindOf(d) != KindMappingError {
		t.Errorf("P3: got %s, want MAPPING_ERROR (missing sku)", kindOf(d))
	}
}

func TestMapInventory_Fixtures(t *testing.T) {
	ids := loadCrosswalk(t)
	rows := loadFixture[BTInventory](t, "inventory.json")
	byID := map[string]BTInventory{}
	for _, r := range rows {
		byID[r.BisTrackID] = r
	}
	// S1: product crosswalked, on-hand ≥ 0 → clean, native product id set.
	if m, d := MapInventory(byID["S1"], ids); kindOf(d) != "" || m.NativeProductID == "" || m.OnHandBase != 120 {
		t.Errorf("S1: kind=%s mapped=%+v, want clean + native product + 120", kindOf(d), m)
	}
	// S2: product not crosswalked → MISSING_IN_NATIVE.
	if _, d := MapInventory(byID["S2"], ids); kindOf(d) != KindMissingInNative {
		t.Errorf("S2: got %s, want MISSING_IN_NATIVE (product not mapped)", kindOf(d))
	}
	// S3: negative on-hand → MAPPING_ERROR (fix at source before snapshot).
	if _, d := MapInventory(byID["S3"], ids); kindOf(d) != KindMappingError || d.Detail["reason"] != "negative_on_hand" {
		t.Errorf("S3: got %s, want MAPPING_ERROR/negative_on_hand", kindOf(d))
	}
}

func TestMapOrder_Fixtures(t *testing.T) {
	ids := loadCrosswalk(t)
	rows := loadFixture[BTOrder](t, "orders.json")
	byID := map[string]BTOrder{}
	for _, r := range rows {
		byID[r.BisTrackID] = r
	}
	// O1: real order, customer + order crosswalked → clean.
	if m, d := MapOrder(byID["O1"], ids); kindOf(d) != "" || m.NativeID == "" || m.CustomerNativeID == "" {
		t.Errorf("O1: kind=%s mapped=%+v, want clean + both native ids", kindOf(d), m)
	}
	// O2: a quote, not an order → UNMAPPED (never the empty Quote tables).
	if _, d := MapOrder(byID["O2"], ids); kindOf(d) != KindUnmapped || d.Detail["reason"] != "not_an_order" {
		t.Errorf("O2: got %s, want UNMAPPED/not_an_order", kindOf(d))
	}
	// O3: real order, no order crosswalk → MISSING_IN_NATIVE.
	if _, d := MapOrder(byID["O3"], ids); kindOf(d) != KindMissingInNative {
		t.Errorf("O3: got %s, want MISSING_IN_NATIVE", kindOf(d))
	}
	// O4: order crosswalked but its CUSTOMER is not → MISSING_IN_NATIVE (customer must land first).
	if _, d := MapOrder(byID["O4"], ids); kindOf(d) != KindMissingInNative || d.Detail["reason"] != "customer_not_mapped" {
		t.Errorf("O4: got %s, want MISSING_IN_NATIVE/customer_not_mapped", kindOf(d))
	}
}

// TestMappingTracesToRecordedSchema pins that the four entities the mapping layer handles are real
// subsystems in the A-18 recorded schema (docs/legacy/bistrack_map.json), so the BT* shapes trace to
// recorded BisTrack tables, not invented ones. It reads the in-repo fixture the whole module maps against.
func TestMappingTracesToRecordedSchema(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "bistrack_map.json"))
	if err != nil {
		t.Fatalf("read recorded schema: %v", err)
	}
	var m struct {
		Subsystems []struct {
			Name         string `json:"name"`
			TargetModule string `json:"target_module"`
			KeyTables    []struct {
				Table string `json:"table"`
			} `json:"key_tables"`
		} `json:"subsystems"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode recorded schema: %v", err)
	}
	// Each mapped entity must correspond to a recorded subsystem carrying its key BisTrack table.
	wantTable := map[EntityType]string{
		EntityCustomer:  "Customer",
		EntityProduct:   "Product",
		EntityInventory: "StockValuationData",
		EntityOrder:     "OrderHeader",
	}
	for _, entity := range AllEntities {
		table := wantTable[entity]
		found := false
		for _, s := range m.Subsystems {
			for _, kt := range s.KeyTables {
				if kt.Table == table {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("entity %s: recorded schema has no subsystem carrying key table %q", entity, table)
		}
	}
}
