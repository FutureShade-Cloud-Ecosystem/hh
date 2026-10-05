//go:build integration

// productload_integration_test.go proves the load doors' product writer
// (HH-LOAD-1 LD-5) through the frame: the master record by legacy key
// (the SKU the table's own natural key, the names, the category named
// by legacy key, the base and price units by catalogue code, the 0018
// promoted attributes, the dimensions and weight, the metadata), the
// stamps a loaded row carries (org, seq from the products scope,
// idempotency key), the replay arms (unchanged draws nothing, a changed
// field updates that row alone and draws exactly one), the key
// conflicts naming both legacy keys in both directions, a category not
// yet loaded refusing as a missing reference, a unit code the catalogue
// does not carry refusing naming the field, and a row another org owns
// refusing as org_mismatch. Exit line 6 closes the file: the loaded
// product appears in the staff sync pull of its org and not another's.
//
// Fixtures are invented: the fxpr skus, the org and actor ids. The
// fixture rows leave with the test.
//
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5680/integration_gate?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run TestProductLoad
package product

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The product door's fixture constants, invented.
const (
	fxProdOrgID    = "4642a800-0000-4000-8000-00000000f001"
	fxProdOtherOrg = "4642a800-0000-4000-8000-00000000f0ff"
	fxProdActorID  = "4642a800-0000-4000-8000-00000000f00a"
	fxProdSource   = "testsuite"
)

// productLoadEnv is one test's fixtures: the pool, the door, the spy.
type productLoadEnv struct {
	pool *pgxpool.Pool
	door *load.Door
	spy  *loadAuditSpy
}

func productLoadSetup(t *testing.T) *productLoadEnv {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM load_key_registry WHERE org_id = ANY($1::uuid[])`, []any{[]string{fxProdOrgID, fxProdOtherOrg}}},
		{`DELETE FROM load_batch WHERE org_id = ANY($1::uuid[])`, []any{[]string{fxProdOrgID, fxProdOtherOrg}}},
		{`DELETE FROM products WHERE sku LIKE 'FXPR-%'`, nil},
		{`DELETE FROM product_categories WHERE code LIKE 'fxprc-%'`, nil},
		{`DELETE FROM uoms WHERE code LIKE 'FXPRU-%'`, nil},
		{`DELETE FROM scope_sequences WHERE scope = 'products'`, nil},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("product fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = ANY($1::uuid[])`, []string{fxProdOrgID, fxProdOtherOrg})
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = ANY($1::uuid[])`, []string{fxProdOrgID, fxProdOtherOrg})
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE sku LIKE 'FXPR-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM product_categories WHERE code LIKE 'fxprc-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM uoms WHERE code LIKE 'FXPRU-%'`)
		_, _ = pool.Exec(ctx, `DELETE FROM scope_sequences WHERE scope = 'products'`)
	})
	spy := &loadAuditSpy{}
	store := load.NewStore(pool)
	return &productLoadEnv{
		pool: pool,
		door: load.NewDoor(store, load.KindProduct, ProductLoadWriter{}, spy),
		spy:  spy,
	}
}

// prItem is one product batch item; the derived key is a canonical
// fixture uuid keyed off the legacy id.
func prItem(legacyID, sku, name string, extra map[string]any, requires ...load.Ref) load.Item {
	rec := map[string]any{"sku": sku, "name": name, "base_uom": "EA", "price_uom": "EA"}
	for k, v := range extra {
		rec[k] = v
	}
	suffix := map[string]string{
		"PR-1":  "000000000201",
		"PR-2":  "000000000202",
		"PR-3":  "000000000203",
		"PR-X":  "000000000204",
		"PR-Y":  "000000000205",
		"PR-Z":  "000000000206",
		"PR-P1": "000000000207",
		"PR-P2": "000000000208",
		"PR-D1": "000000000209",
		"PR-D2": "00000000020a",
		"PR-I1": "00000000020b",
		"PR-I2": "00000000020c",
		"PR-I3": "00000000020d",
	}[legacyID]
	return load.Item{
		LegacyID:   legacyID,
		DerivedKey: "f6000000-0000-4000-8000-" + suffix,
		Record:     rec,
		Requires:   requires,
	}
}

// prRow reads one fixture product row's id, org, seq, key and category.
func (e *productLoadEnv) prRow(t *testing.T, sku string) (id, org string, seq int64, key, category string) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT id::text, coalesce(org_id::text, ''), coalesce(seq, 0),
		       coalesce(idempotency_key::text, ''), coalesce(category_id::text, '')
		FROM products WHERE sku = $1`, sku).
		Scan(&id, &org, &seq, &key, &category)
	if err != nil {
		t.Fatalf("product row %s: %v", sku, err)
	}
	return id, org, seq, key, category
}

// prCol reads one text column of a fixture product row, the empty
// string for a null.
func (e *productLoadEnv) prCol(t *testing.T, sku, col string) string {
	t.Helper()
	var s string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT coalesce(`+col+`::text, '') FROM products WHERE sku = $1`, sku).Scan(&s); err != nil {
		t.Fatalf("product column %s of %s: %v", col, sku, err)
	}
	return s
}

func prOutcome(t *testing.T, r load.BatchResult, legacyID string) load.Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("batch carries no outcome for %s: %+v", legacyID, r.Items)
	return load.Outcome{}
}

// prSeedCategory and prSeedUnit plant the reference rows one test needs:
// the category row the category reference names, the catalogue units the
// unit codes name.
func (e *productLoadEnv) prSeedCategory(t *testing.T, code, name string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO product_categories (code, name) VALUES ($1, $2) RETURNING id::text`, code, name).Scan(&id); err != nil {
		t.Fatalf("seed category %s: %v", code, err)
	}
	return id
}

func (e *productLoadEnv) prSeedUnit(t *testing.T, code, name, dimension string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(),
		`INSERT INTO uoms (code, name, dimension) VALUES ($1, $2, $3) ON CONFLICT (code) DO NOTHING`, code, name, dimension); err != nil {
		t.Fatalf("seed unit %s: %v", code, err)
	}
}

// prRecordName plants a registry name against an existing product row,
// the shape a first load leaves behind, for the conflict legs.
func (e *productLoadEnv) prRecordName(t *testing.T, legacyID, productID string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `
		INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
		VALUES ($1::uuid, $2, $3, $4, $5::uuid, repeat('c', 64))`,
		fxProdOrgID, fxProdSource, load.KindCategory, legacyID, productID); err != nil {
		t.Fatalf("record name %s: %v", legacyID, err)
	}
}

func TestProductLoadUpsertsTheMasterByLegacyKey(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	e.prSeedUnit(t, "SF", "Square Foot", "area")
	catID := e.prSeedCategory(t, "fxprc-1", "Fixture Category")

	// The full record, shared by every arm of this test so the replay's
	// hash matches the create's.
	full := func(name string) map[string]any {
		return map[string]any{
			"sku":          "FXPR-0001",
			"name":         name,
			"description":  "Fixture Description",
			"manufacturer": "Fixture Mfr",
			"collection":   "Fixture Collection",
			"color":        "Fixture Grey",
			"finish":       "Fixture Smooth",
			"application":  "Fixture Patio",
			// the 0018 promoted attributes
			"color_family":                      "Grey",
			"texture":                           "Smooth",
			"product_size":                      "60x60",
			"sold_by":                           "SF",
			"is_featured":                       true,
			"is_special_order":                  true,
			"contractor_pricing_event_discount": true,
			"full_skid_quantity":                240,
			"layer_quantity":                    24,
			"minimum_order_qty":                 "10",
			// dimensions and weight
			"dimensions_lwh":     "60x60x60",
			"pallet_count":       120,
			"weight_per_unit_kg": "18.5",
			// metadata
			"metadata":  map[string]any{"fixture_note": "invented"},
			"base_uom":  "EA",
			"price_uom": "EA",
		}
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-1", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-1", "FXPR-0001", "Fixture Paver One", full("Fixture Paver One"), load.Ref{Kind: "category", LegacyID: "PRC-1"})}})
	if err != nil {
		t.Fatalf("product batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-1"); o.Status != load.OutcomeRefused || o.Reason != load.ReasonMissingReference {
		t.Fatalf("the product naming an unloaded category answered %+v, want refused as missing_reference", o)
	}
	// The load order rule: the category door loads first, so its name
	// stands in the registry before the product names it. The same
	// record now lands created, and the row carries the category the
	// legacy key names.
	e.prRecordName(t, "PRC-1", catID)
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-1b", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-1", "FXPR-0001", "Fixture Paver One", full("Fixture Paver One"), load.Ref{Kind: "category", LegacyID: "PRC-1"})}})
	if err != nil {
		t.Fatalf("product batch after the category name stood: %v", err)
	}
	if o := prOutcome(t, r, "PR-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the first sight answered %+v, want created", o)
	}
	id, org, seq, key, category := e.prRow(t, "FXPR-0001")
	if org != fxProdOrgID || seq == 0 || key == "" {
		t.Fatalf("the loaded product carries org %q seq %d key %q, want the load stamps", org, seq, key)
	}
	if category != catID {
		t.Fatalf("the product's category is %q, want the row the legacy key names (%q)", category, catID)
	}
	if got := e.prCol(t, "FXPR-0001", "description"); got != "Fixture Description" {
		t.Fatalf("the description is %q, want the record's", got)
	}
	if got := e.prCol(t, "FXPR-0001", "color_family"); got != "Grey" {
		t.Fatalf("the 0018 color_family is %q, want the record's", got)
	}
	if got := e.prCol(t, "FXPR-0001", "sold_by"); got != "SF" {
		t.Fatalf("the 0018 sold_by is %q, want the record's", got)
	}
	if got := e.prCol(t, "FXPR-0001", "is_featured"); got != "true" {
		t.Fatalf("the 0018 is_featured is %q, want the record's", got)
	}
	if got := e.prCol(t, "FXPR-0001", "full_skid_quantity"); got != "240" {
		t.Fatalf("the 0018 full_skid_quantity is %q, want the record's", got)
	}
	if got := e.prCol(t, "FXPR-0001", "minimum_order_qty"); got != "10.0000" {
		t.Fatalf("the 0018 minimum_order_qty is %q, want the record's", got)
	}
	if got := e.prCol(t, "FXPR-0001", "dimensions_lwh"); got != "60x60x60" {
		t.Fatalf("the dimensions are %q, want the record's", got)
	}
	if got := e.prCol(t, "FXPR-0001", "weight_per_unit_kg"); got != "18.5000" {
		t.Fatalf("the weight is %q, want the record's", got)
	}
	var meta map[string]any
	if err := e.pool.QueryRow(ctx,
		`SELECT metadata::jsonb FROM products WHERE id = $1::uuid`, id).Scan(&meta); err != nil {
		t.Fatalf("metadata read: %v", err)
	}
	if meta["fixture_note"] != "invented" {
		t.Fatalf("the metadata is %v, want the record's", meta)
	}

	// The identical replay answers unchanged and draws nothing; a
	// changed field updates that row alone and draws exactly one.
	var seqBefore int64
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'products'), 0)`).Scan(&seqBefore); err != nil {
		t.Fatalf("scope read: %v", err)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-2", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-1", "FXPR-0001", "Fixture Paver One", full("Fixture Paver One"), load.Ref{Kind: "category", LegacyID: "PRC-1"})}})
	if err != nil {
		t.Fatalf("replay batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-1"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the identical replay answered %q, want unchanged", o.Status)
	}
	var seqAfter int64
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'products'), 0)`).Scan(&seqAfter); err != nil {
		t.Fatalf("scope read: %v", err)
	}
	if seqAfter != seqBefore {
		t.Fatalf("the unchanged replay drew sequences: %d over %d", seqAfter, seqBefore)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-3", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-1", "FXPR-0001", "Fixture Paver One Renamed", map[string]any{
			"description": "Fixture Description",
			"base_uom":    "EA",
			"price_uom":   "EA",
		}, load.Ref{Kind: "category", LegacyID: "PRC-1"})}})
	if err != nil {
		t.Fatalf("update batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-1"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed name answered %q, want updated", o.Status)
	}
	var drew int64
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce((SELECT next_value FROM scope_sequences WHERE scope = 'products'), 0)`).Scan(&drew); err != nil {
		t.Fatalf("scope read: %v", err)
	}
	if drew != seqBefore+1 {
		t.Fatalf("the update drew %d over %d, want exactly one new draw", drew, seqBefore)
	}
	id2, _, _, _, _ := e.prRow(t, "FXPR-0001")
	if id2 != id {
		t.Fatalf("the update landed on %s, want the same row %s", id2, id)
	}
}

func TestProductLoadRefusesACategoryNotYetLoadedNamingKindAndKey(t *testing.T) {
	e := productLoadSetup(t)
	e.prSeedUnit(t, "EA", "Each", "count")
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-m1", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-X", "FXPR-0008", "Fixture Orphan", nil,
			load.Ref{Kind: "category", LegacyID: "PRC-MISSING"})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := prOutcome(t, r, "PR-X")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonMissingReference ||
		!strings.Contains(o.Detail, "category") || !strings.Contains(o.Detail, "PRC-MISSING") {
		t.Fatalf("the unloaded category answered %+v, want refused as missing_reference naming the kind and the key", o)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM products WHERE sku = 'FXPR-0008'`).Scan(&n); err != nil {
		t.Fatalf("refused row read: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused item wrote %d product rows, want none", n)
	}
}

func TestProductLoadRefusesAUnitCodeTheCatalogueDoesNotCarry(t *testing.T) {
	e := productLoadSetup(t)
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-u1", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-X", "FXPR-0009", "Fixture Bad Unit", map[string]any{
			"base_uom": "FXPRU-NOPE", "price_uom": "EA",
		})}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	o := prOutcome(t, r, "PR-X")
	if o.Status != load.OutcomeRefused || o.Field != "base_uom" {
		t.Fatalf("the unknown unit code answered %+v, want refused on base_uom", o)
	}
	var n int
	if err := e.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM products WHERE sku = 'FXPR-0009'`).Scan(&n); err != nil {
		t.Fatalf("refused row read: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused item wrote %d product rows, want none", n)
	}
}

func TestProductLoadRefusesAMissingSkuOrName(t *testing.T) {
	e := productLoadSetup(t)
	r, err := e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-n1", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-X", "", "Fixture Nameless SKU", nil)}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-X"); o.Status != load.OutcomeRefused || o.Reason != "field_required" || o.Field != "sku" {
		t.Fatalf("a skuless product answered %+v, want refused as field_required on sku", o)
	}
	r, err = e.door.UpsertBatch(context.Background(), load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-n2", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-X", "FXPR-0010", "", nil)}})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-X"); o.Status != load.OutcomeRefused || o.Reason != "field_required" || o.Field != "name" {
		t.Fatalf("a nameless product answered %+v, want refused as field_required on name", o)
	}
}

func TestProductLoadConflictsRefuseNamingBothKeys(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	// Two rows stand, each named by its own legacy key.
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-c1", Caller: fxProdActorID,
		Items: []load.Item{
			prItem("PR-1", "FXPR-0101", "Fixture One", nil),
			prItem("PR-2", "FXPR-0102", "Fixture Two", nil),
		}}); err != nil {
		t.Fatalf("first batch: %v", err)
	}
	// A SKU that would claim a second HH row: PR-2's key renaming its
	// row onto PR-1's sku refuses naming both keys.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-c2", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-2", "FXPR-0101", "Fixture Two Renamed", nil)}})
	if err != nil {
		t.Fatalf("sku claim batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-2"); o.Status != load.OutcomeRefused || o.Reason != load.ReasonKeyConflict ||
		!strings.Contains(o.Detail, "PR-2") || !strings.Contains(o.Detail, "PR-1") {
		t.Fatalf("the sku claim answered %+v, want a key conflict naming PR-2 and PR-1", o)
	}
	// An HH row a second key would claim: a staff made row already
	// carries the sku. The first key attaching to it answers created;
	// a second key naming the same row refuses naming both keys,
	// nothing merging silently.
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO products (sku, name, base_uom, price_uom) VALUES ('FXPR-0103', 'Planted By Staff', 'EA', 'EA')`); err != nil {
		t.Fatalf("plant product: %v", err)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-c3", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-Y", "FXPR-0103", "Planted By Staff", nil)}})
	if err != nil {
		t.Fatalf("attach batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-Y"); o.Status != load.OutcomeCreated {
		t.Fatalf("the first key's attach answered %+v, want created", o)
	}
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-c3b", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-Z", "FXPR-0103", "Planted By Staff", nil)}})
	if err != nil {
		t.Fatalf("second claim batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-Z"); o.Status != load.OutcomeRefused || o.Reason != load.ReasonKeyConflict ||
		!strings.Contains(o.Detail, "PR-Z") || !strings.Contains(o.Detail, "PR-Y") {
		t.Fatalf("the second claim answered %+v, want a key conflict naming PR-Z and PR-Y", o)
	}
	var n int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM products WHERE sku = 'FXPR-0103'`).Scan(&n); err != nil {
		t.Fatalf("claim count: %v", err)
	}
	if n != 1 {
		t.Fatalf("%d rows carry the sku, want the one attached row", n)
	}
	// The other items of a batch with one conflicted item still land.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-c4", Caller: fxProdActorID,
		Items: []load.Item{
			prItem("PR-2", "FXPR-0101", "Fixture Two Renamed", nil),
			prItem("PR-3", "FXPR-0104", "Fixture Three", nil),
		}})
	if err != nil {
		t.Fatalf("mixed batch: %v", err)
	}
	if o := prOutcome(t, r, "PR-2"); o.Status != load.OutcomeRefused {
		t.Fatalf("the conflicted item answered %+v, want refused", o)
	}
	if o := prOutcome(t, r, "PR-3"); o.Status != load.OutcomeCreated {
		t.Fatalf("the batch's other item answered %+v, want created", o)
	}
}

func TestProductLoadRefusesARowOwnedByAnotherOrg(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	if _, err := e.pool.Exec(ctx,
		`INSERT INTO products (sku, name, base_uom, price_uom, org_id) VALUES ('FXPR-0201', 'Other Org Row', 'EA', 'EA', $1::uuid)`,
		fxProdOtherOrg); err != nil {
		t.Fatalf("plant other org row: %v", err)
	}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-o1", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-X", "FXPR-0201", "Renamed By Load", nil)}})
	if err != nil {
		t.Fatalf("org batch: %v", err)
	}
	o := prOutcome(t, r, "PR-X")
	if o.Status != load.OutcomeRefused || o.Reason != "org_mismatch" ||
		!strings.Contains(o.Detail, "product") || !strings.Contains(o.Detail, "PR-X") {
		t.Fatalf("the other org's row answered %+v, want refused as org_mismatch naming the kind and key", o)
	}
	_, org, _, _, _ := e.prRow(t, "FXPR-0201")
	var name string
	if err := e.pool.QueryRow(ctx, `SELECT name FROM products WHERE sku = 'FXPR-0201'`).Scan(&name); err != nil {
		t.Fatalf("name read: %v", err)
	}
	if org != fxProdOtherOrg || name != "Other Org Row" {
		t.Fatalf("the row now carries org %q name %q, want it untouched", org, name)
	}
}

// Exit line 6: a loaded product appears in the staff sync pull of its
// org and not another's, proven by request through the products sync
// Store the staff pull serves.
func TestProductLoadAppearsInTheStaffSyncPullOfItsOrgAlone(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-pr-s1", Caller: fxProdActorID,
		Items: []load.Item{prItem("PR-1", "FXPR-0301", "Fixture Pull Product", nil)}}); err != nil {
		t.Fatalf("load batch: %v", err)
	}
	products := NewProductsStore(e.pool)
	own, _, err := products.Pull(ctx, sync.ScopeFilter{OrgID: fxProdOrgID}, 0, 200)
	if err != nil {
		t.Fatalf("own org pull: %v", err)
	}
	found := false
	for _, it := range own {
		if it.Data["sku"] == "FXPR-0301" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the loaded product is absent from its own org's staff pull: %d rows", len(own))
	}
	other, _, err := products.Pull(ctx, sync.ScopeFilter{OrgID: fxProdOtherOrg}, 0, 200)
	if err != nil {
		t.Fatalf("other org pull: %v", err)
	}
	for _, it := range other {
		if it.Data["sku"] == "FXPR-0301" {
			t.Fatalf("the loaded product leaked into another org's staff pull: %+v", it.Data)
		}
	}
}
