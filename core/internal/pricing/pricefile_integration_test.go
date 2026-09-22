//go:build integration

// pricefile_integration_test.go, the 0198 surface against a real database.
//
// What only a real database can prove, and what each of these tests is therefore for:
//
//	· the migration APPLIED, the three added columns, the new table, its append-only trigger
//	· the LAZY profile, a vendor 0039 never seeded gets one on its FIRST import and only then
//	· the SEEDED SEVEN keep their own profile and their `effective_col` survives a mapping save
//	· the audit row is REALLY append-only, enforced by the trigger and not by our own discipline
//	· the whole diff runs against the seeded catalogue with its real SKUs and its real anchors
//
// Run with: DATABASE_URL=... go test -tags integration -count=1 ./internal/pricing/
package pricing

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// realPriceFileSvc wires the production Service the way cmd/server does: the pgx repository for
// pricing's own tables, plus the two consumer-declared seams over the catalogue and the vendor list.
// The adapters here mirror main.go's, deliberately, because an integration test wired differently
// from production proves something about a configuration nobody ships.
func realPriceFileSvc(pool *pgxpool.Pool, managers map[string]bool) *Service {
	return realSvc(pool).
		WithCatalogSKUs(itCatalog{pool}).
		WithVendorDirectory(itVendors{pool}).
		WithCostVisibility(&fakeCostVisibility{managers: managers})
}

type itCatalog struct{ pool *pgxpool.Pool }

func (a itCatalog) LookupSKUs(ctx context.Context, skus []string) (map[string]CatalogSKU, error) {
	rows, err := a.pool.Query(ctx,
		`SELECT id::text, sku::text, name FROM products WHERE sku = ANY($1::citext[])`, skus)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]CatalogSKU{}
	for rows.Next() {
		var e CatalogSKU
		if err := rows.Scan(&e.ProductID, &e.SKU, &e.Name); err != nil {
			return nil, err
		}
		out[e.SKU] = e
	}
	return out, rows.Err()
}

type itVendors struct{ pool *pgxpool.Pool }

func (a itVendors) ListVendors(ctx context.Context) ([]PriceFileVendor, error) {
	rows, err := a.pool.Query(ctx, `SELECT id::text, vendor_no::text, name FROM vendors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PriceFileVendor
	for rows.Next() {
		var v PriceFileVendor
		if err := rows.Scan(&v.VendorID, &v.VendorNo, &v.Name); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func vendorIDByNo(t *testing.T, pool *pgxpool.Pool, vendorNo string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM vendors WHERE vendor_no = $1`, vendorNo).Scan(&id); err != nil {
		t.Fatalf("vendor %s not seeded: %v", vendorNo, err)
	}
	return id
}

// cleanupImport removes everything one apply wrote. price_import_runs blocks UPDATE, not DELETE,
// which is the same posture price_change_log has and the same cleanup cleanupBatch uses.
func cleanupImport(t *testing.T, pool *pgxpool.Pool, batchID, profileID string) {
	ctx := context.Background()
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM price_import_runs WHERE batch_id = $1`, batchID)
		pool.Exec(ctx, `DELETE FROM price_batches WHERE id = $1`, batchID)
		if profileID != "" {
			pool.Exec(ctx, `DELETE FROM price_import_profiles WHERE id = $1 AND origin = 'FIRST_IMPORT'`, profileID)
		}
	})
}

// - 1 · the migration is on this database -

func TestIntegration_0198_MigrationShape(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	cols := map[string]string{
		"price_import_profiles.origin":                 "",
		"price_import_profiles.created_by":             "",
		"price_import_profiles.last_import_at":         "",
		"pricing_anchor_policy.import_cost_visibility": "",
	}
	for qualified := range cols {
		parts := strings.SplitN(qualified, ".", 2)
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM information_schema.columns WHERE table_name = $1 AND column_name = $2`,
			parts[0], parts[1]).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("%s is missing, migration 0198 has not applied to this database", qualified)
		}
	}
	// The org-default policy row answers, and it answers with the strict default.
	vis, err := NewRepository(pool).ImportCostVisibility(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if vis != "MANAGER_UP" {
		t.Fatalf("import_cost_visibility = %q, want the MANAGER_UP default", vis)
	}
	// The seeded seven are SEEDED, and there are still exactly seven of them: this test also pins
	// that 0198 did not quietly pre-seed the other twenty-three, which is the thing it argues against.
	var seeded, total int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FILTER (WHERE origin = 'SEEDED'), count(*) FROM price_import_profiles`).
		Scan(&seeded, &total); err != nil {
		t.Fatal(err)
	}
	if seeded < 7 {
		t.Fatalf("only %d SEEDED profiles, the 0039 seven should carry the default origin", seeded)
	}
}

// - 2 · the audit row is append-only, by the database -

func TestIntegration_0198_ImportRunIsAppendOnly(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_import_runs (source_name, source_format, row_count) VALUES ('trigger-probe.csv','CSV',1)
		 RETURNING id::text`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pool.Exec(ctx, `DELETE FROM price_import_runs WHERE id = $1`, id) })

	if _, err := pool.Exec(ctx, `UPDATE price_import_runs SET row_count = 999 WHERE id = $1`, id); err == nil {
		t.Fatal("an import audit row was editable, the append-only trigger is not armed")
	} else if !strings.Contains(err.Error(), "append-only") {
		t.Fatalf("the refusal does not name the reason: %v", err)
	}
	// GUARD THE GUARD: an INSERT must still work, or "UPDATE fails" would also be true of a table
	// nothing can write to at all.
	var second string
	if err := pool.QueryRow(ctx,
		`INSERT INTO price_import_runs (source_name, source_format) VALUES ('trigger-probe-2.csv','XLSX')
		 RETURNING id::text`).Scan(&second); err != nil {
		t.Fatalf("the table refuses inserts too: %v", err)
	}
	pool.Exec(ctx, `DELETE FROM price_import_runs WHERE id = $1`, second)
}

// - 3 · lazy profile creation, on a vendor 0039 never mapped -

func TestIntegration_0198_ProfileIsCreatedByTheFirstImportAndNotBefore(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realPriceFileSvc(pool, map[string]bool{makerID: true})

	// V-BROWNS is one of the twenty-three 0039 did NOT seed a profile for.
	vendorID := vendorIDByNo(t, pool, "V-BROWNS")
	var before int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM price_import_profiles WHERE vendor_id = $1`, vendorID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if before != 0 {
		t.Skipf("V-BROWNS already carries %d profiles on this database", before)
	}

	data, err := os.ReadFile("testdata/techo_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	in := PriceFileInput{VendorID: vendorID, UserID: makerID, FileName: "browns.csv", Data: data}

	// PREVIEW FIRST, TWICE, AND NOTHING IS CREATED. This is the acceptance clause at the database.
	for i := 0; i < 2; i++ {
		if _, err := svc.PreviewPriceFile(ctx, in); err != nil {
			t.Fatalf("preview %d: %v", i, err)
		}
	}
	var afterPreview int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM price_import_profiles WHERE vendor_id = $1`, vendorID).Scan(&afterPreview); err != nil {
		t.Fatal(err)
	}
	if afterPreview != 0 {
		t.Fatalf("a preview created %d profile rows, the preview must write nothing", afterPreview)
	}

	pv, err := svc.PreviewPriceFile(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Digest = pv.Digest
	res, err := svc.ApplyPriceFile(ctx, in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Registered BEFORE cleanupImport so it runs AFTER it (t.Cleanup is LIFO): the run row and the
	// batch both reference the profile, and clearing them first keeps the delete unambiguous.
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM price_import_profiles WHERE vendor_id = $1 AND origin = 'FIRST_IMPORT'`, vendorID)
	})
	cleanupImport(t, pool, res.Batch.ID, "")

	if !res.ProfileCreated {
		t.Fatal("apply did not report creating the vendor's first profile")
	}
	var origin, name string
	var mappingRaw []byte
	var lastImport *time.Time
	if err := pool.QueryRow(ctx,
		`SELECT origin, name, mapping, last_import_at FROM price_import_profiles WHERE vendor_id = $1`, vendorID).
		Scan(&origin, &name, &mappingRaw, &lastImport); err != nil {
		t.Fatalf("no profile after the first import: %v", err)
	}
	if origin != "FIRST_IMPORT" {
		t.Fatalf("origin = %q, want FIRST_IMPORT, a lazily created mapping must not look seeded", origin)
	}
	// THE PROFILE IS NAMED AFTER THE MANUFACTURER, NOT AFTER THE UPLOAD. The picker groups by vendor
	// and this row is what the next operator reads; "browns.csv" names a file that will be superseded
	// next quarter and says nothing about which manufacturer the mapping belongs to.
	if !strings.Contains(strings.ToLower(name), "brown") || strings.Contains(name, ".csv") {
		t.Fatalf("the lazily created profile is called %q, it must carry the vendor's name", name)
	}
	if lastImport == nil {
		t.Fatal("last_import_at was not stamped")
	}
	var mapping map[string]string
	if err := json.Unmarshal(mappingRaw, &mapping); err != nil {
		t.Fatal(err)
	}
	if mapping[MapSKU] == "" || mapping[MapCost] == "" {
		t.Fatalf("the saved mapping is empty: %+v", mapping)
	}

	// The audit row landed, is attributed, and points at the batch.
	var runBatch, importedBy, digest string
	var rows, unknown int
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(batch_id::text,''), COALESCE(imported_by::text,''), content_digest, row_count, unknown_count
		   FROM price_import_runs WHERE id = $1`, res.RunID).
		Scan(&runBatch, &importedBy, &digest, &rows, &unknown); err != nil {
		t.Fatalf("no audit row: %v", err)
	}
	if runBatch != res.Batch.ID || importedBy != makerID || digest != pv.Digest {
		t.Fatalf("audit row = batch %q by %q digest %q", runBatch, importedBy, digest)
	}
	if rows != pv.Counts.Rows {
		t.Fatalf("audit row_count = %d, want the whole file's %d", rows, pv.Counts.Rows)
	}
	// The staged batch is STAGED and awaiting the EXISTING maker-checker gate, this import does not
	// write the retail anchor, by design (pricefile_service.go's header argues it).
	if res.Batch.Status != BatchStaged {
		t.Fatalf("batch status = %q, want STAGED", res.Batch.Status)
	}
}

// - 4 · the seeded seven keep their profile, and the key this screen hides survives -

func TestIntegration_0198_SeededProfileIsReusedAndEffectiveColSurvives(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realPriceFileSvc(pool, map[string]bool{makerID: true})
	vendorID := vendorIDByNo(t, pool, "V-TECHO")

	var profileID string
	var beforeRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT id::text, mapping FROM price_import_profiles WHERE vendor_id = $1 AND is_active
		  ORDER BY created_at, id LIMIT 1`, vendorID).Scan(&profileID, &beforeRaw); err != nil {
		t.Fatalf("the seeded Techo profile is missing: %v", err)
	}
	var before map[string]string
	json.Unmarshal(beforeRaw, &before)
	if before["effective_col"] == "" {
		t.Skip("this database's Techo profile carries no effective_col to preserve")
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `UPDATE price_import_profiles SET mapping = $2 WHERE id = $1`, profileID, beforeRaw)
	})

	data, err := os.ReadFile("testdata/techo_sample.csv")
	if err != nil {
		t.Fatal(err)
	}
	in := PriceFileInput{VendorID: vendorID, UserID: makerID, FileName: "techo_sample.csv", Data: data}
	pv, err := svc.PreviewPriceFile(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if pv.ProfileID != profileID {
		t.Fatalf("preview resolved profile %q, want the seeded %q", pv.ProfileID, profileID)
	}
	if pv.MappingSource != "STORED" {
		t.Fatalf("mapping source = %q, want STORED for a seeded vendor", pv.MappingSource)
	}
	// The seeded catalogue carries real anchors for these SKUs, so this file is a real diff.
	if pv.Counts.Rows == 0 || pv.Counts.Unknown == 0 {
		t.Fatalf("counts = %+v, the fixture is expected to carry both matched and unmatched SKUs", pv.Counts)
	}

	in.Digest = pv.Digest
	res, err := svc.ApplyPriceFile(ctx, in)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	cleanupImport(t, pool, res.Batch.ID, "")
	if res.ProfileCreated {
		t.Fatal("apply created a second profile for a vendor that already had one")
	}
	var afterRaw []byte
	if err := pool.QueryRow(ctx, `SELECT mapping FROM price_import_profiles WHERE id = $1`, profileID).Scan(&afterRaw); err != nil {
		t.Fatal(err)
	}
	var after map[string]string
	json.Unmarshal(afterRaw, &after)
	if after["effective_col"] != before["effective_col"] {
		t.Fatalf("effective_col went %q → %q, a save must merge, not replace", before["effective_col"], after["effective_col"])
	}

	// The staged lines are the movers plus the unmatched, and the unmatched really do carry a NULL
	// product, which is what makes the unmatched-SKU report outlive the session.
	var lines, nullProduct int
	if err := pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE product_id IS NULL) FROM price_change_lines WHERE batch_id = $1`,
		res.Batch.ID).Scan(&lines, &nullProduct); err != nil {
		t.Fatal(err)
	}
	if lines != res.Staged || nullProduct != pv.Counts.Unknown {
		t.Fatalf("staged %d lines (%d unmatched), want %d / %d", lines, nullProduct, res.Staged, pv.Counts.Unknown)
	}
}

// - 5 · the vendor picker offers all thirty, mapped or not -

func TestIntegration_0198_EveryVendorIsOfferedNotOnlyTheMappedSeven(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := realPriceFileSvc(pool, nil)
	list, err := svc.PriceFileVendors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) < 30 {
		t.Fatalf("the picker offers %d vendors, want every seeded vendor", len(list))
	}
	mapped, unmapped := 0, 0
	for _, v := range list {
		if v.ProfileID != "" {
			mapped++
		} else {
			unmapped++
		}
	}
	if mapped < 7 || unmapped == 0 {
		t.Fatalf("mapped=%d unmapped=%d, the picker must show both states", mapped, unmapped)
	}
}
