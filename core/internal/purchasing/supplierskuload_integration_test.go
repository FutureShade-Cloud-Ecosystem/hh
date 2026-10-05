//go:build integration

// supplierskuload_integration_test.go proves the load doors' supplier
// SKU writer (HH-LOAD-1 LD-5) through the frame: the vendor's own code
// and selling unit for one of our products (0197's
// purchasing_vendor_skus, one row per vendor and product), the product
// and the supplier each named by legacy key and resolved against the
// registry before any write, the pack pair all or nothing (a unit with
// no factor and a factor with no unit refuse naming the field), a unit
// code the catalogue does not carry refusing naming the field, a
// missing reference refusing naming the kind and the key, another org's
// supplier row refusing as org_mismatch, and the replay arms. The row
// carries no 0213 stamps: the table has no org, seq or idempotency
// column (0213's lists), so the org rides through the vendor row the
// supplier door stamped and the registry carries the name. The registry
// names the row by a v5 style uuid derived from the vendor and product
// pair, the units door's derived row id precedent for a table with no
// uuid column.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5680/integration_gate?sslmode=disable' \
//	  go test -tags integration ./internal/purchasing/... -count=1 -run TestSupplierSKULoad
package purchasing

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The supplier SKU door's fixture constants, invented.
const (
	fxSkuOrgID   = "4742a800-0000-4000-8000-00000000a001"
	fxSkuActorID = "4742a800-0000-4000-8000-00000000a00a"
	fxSkuSource  = "testsuite"
)

// supplierSkuEnv is one test's fixtures.
type supplierSkuEnv struct {
	pool *pgxpool.Pool
	door *load.Door
	spy  *loadAuditSpy
}

func supplierSkuSetup(t *testing.T) *supplierSkuEnv {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM load_key_registry WHERE org_id = $1::uuid`, []any{fxSkuOrgID}},
		{`DELETE FROM load_batch WHERE org_id = $1::uuid`, []any{fxSkuOrgID}},
		{`DELETE FROM purchasing_vendor_skus WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no LIKE 'FXV-SK%')`, nil},
		{`DELETE FROM purchasing_vendor_contacts WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no LIKE 'FXV-SK%')`, nil},
		{`DELETE FROM vendors WHERE vendor_no LIKE 'FXV-SK%'`, nil},
		{`DELETE FROM products WHERE sku LIKE 'FXSK-%'`, nil},
		{`DELETE FROM uoms WHERE code LIKE 'FXSKU-%'`, nil},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("supplier sku fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid`, fxSkuOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid`, fxSkuOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM purchasing_vendor_skus WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no LIKE 'FXV-SK%')`)
		_, _ = pool.Exec(ctx, `DELETE FROM purchasing_vendor_contacts WHERE vendor_id IN (SELECT id FROM vendors WHERE vendor_no LIKE 'FXV-SK%')`)
		_, _ = pool.Exec(ctx, `DELETE FROM vendors WHERE vendor_no LIKE 'FXV-SK%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE sku LIKE 'FXSK-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM uoms WHERE code LIKE 'FXSKU-%'`)
	})
	spy := &loadAuditSpy{}
	store := load.NewStore(pool)
	return &supplierSkuEnv{
		pool: pool,
		door: load.NewDoor(store, load.KindSupplierSku, SupplierSKULoadWriter{Units: product.UnitCatalogueReader{}}, spy),
		spy:  spy,
	}
}

// skuItem is one supplier SKU batch item; the derived key is a
// canonical fixture uuid keyed off the legacy id.
func skuItem(legacyID string, vendorSKU string, extra map[string]any, requires ...load.Ref) load.Item {
	rec := map[string]any{"vendor_sku": vendorSKU}
	for k, v := range extra {
		rec[k] = v
	}
	return load.Item{
		LegacyID:   legacyID,
		DerivedKey: "a7000000-0000-4000-8000-" + skuSuffix(legacyID),
		Record:     rec,
		Requires:   requires,
	}
}

func skuSuffix(legacyID string) string {
	return map[string]string{
		"SK-1":  "000000000301",
		"SK-2":  "000000000302",
		"SK-3":  "000000000303",
		"SK-X":  "000000000304",
		"SK-DL": "000000000305",
	}[legacyID]
}

// plantSupplierSkuRefs plants the registry names the items reference:
// one supplier (a fixture vendor row the supplier door's shape carries)
// and one product, both as a first load leaves them, answering the rows'
// ids.
func (e *supplierSkuEnv) plantSupplierSkuRefs(t *testing.T) (vendorID, productID string) {
	t.Helper()
	ctx := context.Background()
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO vendors (vendor_no, name) VALUES ('FXV-SK1', 'Fixture SKU Supplier') RETURNING id::text`).Scan(&vendorID); err != nil {
		t.Fatalf("plant vendor: %v", err)
	}
	if err := e.pool.QueryRow(ctx,
		`INSERT INTO products (sku, name, base_uom, price_uom) VALUES ('FXSK-0001', 'Fixture SKU Product', 'EA', 'EA') RETURNING id::text`).Scan(&productID); err != nil {
		t.Fatalf("plant product: %v", err)
	}
	for _, name := range []struct{ kind, legacy, id string }{
		{"supplier", "SUP-1", vendorID},
		{"product", "PROD-1", productID},
	} {
		if _, err := e.pool.Exec(ctx, `
			INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
			VALUES ($1::uuid, $2, $3, $4, $5::uuid, repeat('b', 64))`,
			fxSkuOrgID, fxSkuSource, name.kind, name.legacy, name.id); err != nil {
			t.Fatalf("plant name %s: %v", name.legacy, err)
		}
	}
	return vendorID, productID
}

func skuOutcome(t *testing.T, r load.BatchResult, legacyID string) load.Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("batch carries no outcome for %s: %+v", legacyID, r.Items)
	return load.Outcome{}
}

func (e *supplierSkuEnv) skuRow(t *testing.T, vendorID, productID string) (vendorSKU string, uom string, qty string) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT vendor_sku, coalesce(vendor_uom::text, ''), coalesce(qty_per_vendor_uom::text, '')
		FROM purchasing_vendor_skus WHERE vendor_id = $1::uuid AND product_id = $2::uuid`,
		vendorID, productID).Scan(&vendorSKU, &uom, &qty)
	if err != nil {
		t.Fatalf("vendor sku row: %v", err)
	}
	return vendorSKU, uom, qty
}

func TestSupplierSKULoadLandsThe0197RowByKeys(t *testing.T) {
	e := supplierSkuSetup(t)
	ctx := context.Background()
	vendorID, productID := e.plantSupplierSkuRefs(t)
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-1", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-1", "FXV-SKU-9", map[string]any{
			"vendor_uom":         "PLT",
			"qty_per_vendor_uom": "60",
		}, load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := skuOutcome(t, r, "SK-1")
	if o.Status != load.OutcomeCreated || o.HHID == "" {
		t.Fatalf("the first sight answered %+v, want created with the derived row id", o)
	}
	if want := SupplierSKURowID(vendorID, productID); o.HHID != want {
		t.Fatalf("the name's row id is %s, want the derived pair id %s", o.HHID, want)
	}
	vendorSKU, uom, qty := e.skuRow(t, vendorID, productID)
	if vendorSKU != "FXV-SKU-9" || uom != "PLT" || qty != "60.0000" {
		t.Fatalf("the row carries %q %q %q, want the record's code and pack pair", vendorSKU, uom, qty)
	}
	// The identical replay answers unchanged; a changed code updates.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-2", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-1", "FXV-SKU-9", map[string]any{
			"vendor_uom":         "PLT",
			"qty_per_vendor_uom": "60",
		}, load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("replay batch: %v", err)
	}
	if o := skuOutcome(t, r, "SK-1"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the identical replay answered %q, want unchanged", o.Status)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-3", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-1", "FXV-SKU-10", nil, load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if o := skuOutcome(t, r, "SK-1"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed code answered %q, want updated", o.Status)
	}
	vendorSKU, uom, qty = e.skuRow(t, vendorID, productID)
	if vendorSKU != "FXV-SKU-10" || uom != "" || qty != "" {
		t.Fatalf("the reload carries %q %q %q, want the new code and the pair cleared: the record is the whole truth", vendorSKU, uom, qty)
	}
}

func TestSupplierSKULoadRefusesAMissingReferenceNamingKindAndKey(t *testing.T) {
	e := supplierSkuSetup(t)
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-m1", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-X", "FXV-SKU-8", nil, load.Ref{Kind: "supplier", LegacyID: "SUP-GONE"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := skuOutcome(t, r, "SK-X")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonMissingReference ||
		!strings.Contains(o.Detail, "supplier") || !strings.Contains(o.Detail, "SUP-GONE") {
		t.Fatalf("the unloaded supplier answered %+v, want refused as missing_reference naming the kind and the key", o)
	}
}

func TestSupplierSKULoadRefusesAWrongReferenceShape(t *testing.T) {
	e := supplierSkuSetup(t)
	e.plantSupplierSkuRefs(t)
	// No supplier reference: the record shape needs exactly one of each.
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-w1", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-X", "FXV-SKU-7", nil, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := skuOutcome(t, r, "SK-X"); o.Status != load.OutcomeRefused || o.Field != "supplier" {
		t.Fatalf("the supplierless item answered %+v, want refused on the supplier field", o)
	}
	// A doubled reference: the record shape takes exactly one of each.
	r, err = e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-w2", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-X", "FXV-SKU-7", nil,
			load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"},
			load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := skuOutcome(t, r, "SK-X"); o.Status != load.OutcomeRefused || o.Field != "product" {
		t.Fatalf("the doubled reference answered %+v, want refused on the product field", o)
	}
}

func TestSupplierSKULoadRefusesABrokenPackPairAndAnUnknownUnit(t *testing.T) {
	e := supplierSkuSetup(t)
	e.plantSupplierSkuRefs(t)
	// The pair is all or nothing: a factor with no unit refuses.
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-p1", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-X", "FXV-SKU-6", map[string]any{"qty_per_vendor_uom": "60"},
			load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := skuOutcome(t, r, "SK-X"); o.Status != load.OutcomeRefused || o.Field != "vendor_uom" {
		t.Fatalf("a factor with no unit answered %+v, want refused on vendor_uom (the missing member)", o)
	}
	// A unit code the catalogue does not carry refuses naming the field.
	r, err = e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-p2", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-X", "FXV-SKU-6", map[string]any{"vendor_uom": "FXSKU-NOPE"},
			load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := skuOutcome(t, r, "SK-X"); o.Status != load.OutcomeRefused || o.Field != "vendor_uom" {
		t.Fatalf("an unknown unit answered %+v, want refused on vendor_uom", o)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM purchasing_vendor_skus m
		 JOIN vendors v ON v.id = m.vendor_id WHERE v.vendor_no LIKE 'FXV-SK%' AND m.vendor_sku = 'FXV-SKU-6'`).Scan(&n); err != nil {
		t.Fatalf("refused row read: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused items wrote %d rows, want none", n)
	}
}

func TestSupplierSKULoadRefusesAnOverLongCodeNamingTheField(t *testing.T) {
	e := supplierSkuSetup(t)
	e.plantSupplierSkuRefs(t)
	long := strings.Repeat("K", 65)
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-l1", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-X", long, nil,
			load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := skuOutcome(t, r, "SK-X"); o.Status != load.OutcomeRefused || o.Field != "vendor_sku" {
		t.Fatalf("an over long code answered %+v, want refused on vendor_sku", o)
	}
}

func TestSupplierSKULoadRefusesAnotherOrgsSupplier(t *testing.T) {
	e := supplierSkuSetup(t)
	vendorID, productID := e.plantSupplierSkuRefs(t)
	const otherOrg = "4742a800-0000-4000-8000-00000000a0ff"
	if _, err := e.pool.Exec(context.Background(),
		`UPDATE vendors SET org_id = $1::uuid WHERE id = $2::uuid`, otherOrg, vendorID); err != nil {
		t.Fatalf("re-stamp vendor: %v", err)
	}
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxSkuOrgID, SourceSystem: fxSkuSource,
		BatchID: "b-sk-o1", Caller: fxSkuActorID,
		Items: []load.Item{skuItem("SK-X", "FXV-SKU-5", nil,
			load.Ref{Kind: "supplier", LegacyID: "SUP-1"}, load.Ref{Kind: "product", LegacyID: "PROD-1"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := skuOutcome(t, r, "SK-X")
	if o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" ||
		!strings.Contains(o.Detail, "supplier") || !strings.Contains(o.Detail, "SUP-1") {
		t.Fatalf("the other org's supplier answered %+v, want refused as org_mismatch naming the kind and key", o)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM purchasing_vendor_skus WHERE vendor_id = $1::uuid AND product_id = $2::uuid`,
		vendorID, productID).Scan(&n); err != nil {
		t.Fatalf("refused row read: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused item wrote %d rows, want none", n)
	}
}
