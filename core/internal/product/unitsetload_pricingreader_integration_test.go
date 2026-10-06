//go:build integration

// unitsetload_pricingreader_integration_test.go is the unit set door's
// removal probe through the REAL pricing reader (HH-LOAD-1 LD-3 round
// 3, the delta review's customer category override finding): the
// reviewer's probe is a MARKUP_ON_COST override for a customer on the
// product's category, priced in a unit of the product's set. The
// resolver serves that override by customer and category, its price_uom
// becomes the declared unit, and the result conversion factor needs it,
// so a set that drops the unit fails every later resolve for that
// customer. The door must refuse the drop while the override stands and
// land it once the override goes. This file is an external test package
// because the real reader lives in the pricing module, which imports
// this one.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5675/ld3sets?sslmode=disable' \
//	  go test -tags integration ./internal/product/ -count=1 -run TestUnitSetOverrideProbe
package product_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	fxProbeOrgID      = "4542a800-0000-4000-8000-00000000d401"
	fxProbeProductID  = "4542a800-0000-4000-8000-00000000d411"
	fxProbeCategoryID = "4542a800-0000-4000-8000-00000000d4c1"
	fxProbeCustomerID = "4542a800-0000-4000-8000-00000000d4a1"
)

// probeSpy is the audit sink the probe's door records through.
type probeSpy struct {
	mu   sync.Mutex
	rows int
}

func (s *probeSpy) RecordBatchTx(_ context.Context, _ pgx.Tx, _ load.BatchAuditRow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows++
	return nil
}

// TestUnitSetOverrideProbeBlocksTheRemoval: the reviewer's probe end to
// end through the real triple of readers. The override stands: the drop
// of the unit it prices in refuses as unit_in_use and nothing of the
// replace stands. The override goes: the same drop lands.
func TestUnitSetOverrideProbeBlocksTheRemoval(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatalf("probe pool: %v", err)
	}
	t.Cleanup(pool.Close)
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM load_key_registry WHERE org_id = $1::uuid`, []any{fxProbeOrgID}},
		{`DELETE FROM load_batch WHERE org_id = $1::uuid`, []any{fxProbeOrgID}},
		{`DELETE FROM customer_category_overrides WHERE customer_id = $1::uuid`, []any{fxProbeCustomerID}},
		{`DELETE FROM product_uoms WHERE product_id = $1::uuid`, []any{fxProbeProductID}},
		{`DELETE FROM products WHERE id = $1::uuid`, []any{fxProbeProductID}},
		{`DELETE FROM product_categories WHERE id = $1::uuid`, []any{fxProbeCategoryID}},
		{`DELETE FROM customers WHERE id = $1::uuid`, []any{fxProbeCustomerID}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("probe fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid`, fxProbeOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid`, fxProbeOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM customer_category_overrides WHERE customer_id = $1::uuid`, fxProbeCustomerID)
		_, _ = pool.Exec(ctx, `DELETE FROM product_uoms WHERE product_id = $1::uuid`, fxProbeProductID)
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = $1::uuid`, fxProbeProductID)
		_, _ = pool.Exec(ctx, `DELETE FROM product_categories WHERE id = $1::uuid`, fxProbeCategoryID)
		_, _ = pool.Exec(ctx, `DELETE FROM customers WHERE id = $1::uuid`, fxProbeCustomerID)
	})
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_categories (id, code, name)
		VALUES ($1::uuid, 'FX-PROBE-CAT', 'Fixture Probe Category')`, fxProbeCategoryID); err != nil {
		t.Fatalf("probe category: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO products (id, sku, name, base_uom, price_uom, org_id, category_id)
		VALUES ($1::uuid, 'FX-PROBE-1', 'Fixture Probe Product', 'SF', 'PLT', $2::uuid, $3::uuid)`,
		fxProbeProductID, fxProbeOrgID, fxProbeCategoryID); err != nil {
		t.Fatalf("probe product: %v", err)
	}
	for _, row := range []struct {
		code string
		qty  string
		base bool
	}{{"SF", "1", true}, {"PLT", "60", false}, {"BOX", "20", false}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base, org_id)
			VALUES ($1::uuid, $2, $3::numeric, $4, $5::uuid)`,
			fxProbeProductID, row.code, row.qty, row.base, fxProbeOrgID); err != nil {
			t.Fatalf("probe set row: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
		VALUES ($1::uuid, 'testsuite', 'product', 'PRB-1', $2::uuid, repeat('b', 64))`,
		fxProbeOrgID, fxProbeProductID); err != nil {
		t.Fatalf("probe product name: %v", err)
	}
	// The reviewer's probe row: a customer override on the product's
	// category, MARKUP_ON_COST priced in BOX, the calc type whose result
	// stays in the override's own unit.
	if _, err := pool.Exec(ctx, `
		INSERT INTO customers (id, account_no, name)
		VALUES ($1::uuid, 'FX-PROBE-CUST', 'Fixture Probe Customer')`, fxProbeCustomerID); err != nil {
		t.Fatalf("probe customer: %v", err)
	}
	plantOverride := func(t *testing.T) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO customer_category_overrides (customer_id, category_id, calc_type, markup_bps, price_uom)
			VALUES ($1::uuid, $2::uuid, 'MARKUP_ON_COST', 1500, 'BOX')`, fxProbeCustomerID, fxProbeCategoryID); err != nil {
			t.Fatalf("plant override: %v", err)
		}
	}

	door := load.NewDoor(load.NewStore(pool), load.KindUnitSet,
		product.UnitSetLoadWriter{
			Pricing: pricing.LoadedUnitUseReader{},
			Stock:   inventory.LoadedUnitUseReader{},
			Vendor:  purchasing.LoadedUnitUseReader{},
		}, &probeSpy{})
	setWhole := load.Item{
		LegacyID:   "PRB-1",
		DerivedKey: "d4000000-0000-4000-8000-000000000401",
		Record: map[string]any{"rows": []any{
			map[string]any{"code": "SF", "qty_in_base": "1", "is_base": true},
			map[string]any{"code": "PLT", "qty_in_base": "60", "is_base": false},
			map[string]any{"code": "BOX", "qty_in_base": "20", "is_base": false}}},
		Requires: []load.Ref{{Kind: "product", LegacyID: "PRB-1"}},
	}
	dropsBOX := load.Item{
		LegacyID:   "PRB-1",
		DerivedKey: "d4000000-0000-4000-8000-000000000401",
		Record: map[string]any{"rows": []any{
			map[string]any{"code": "SF", "qty_in_base": "1", "is_base": true},
			map[string]any{"code": "PLT", "qty_in_base": "60", "is_base": false}}},
		Requires: []load.Ref{{Kind: "product", LegacyID: "PRB-1"}},
	}
	submit := func(t *testing.T, batchID string, item load.Item) load.Outcome {
		t.Helper()
		r, err := door.UpsertBatch(ctx, load.Batch{OrgID: fxProbeOrgID, SourceSystem: "testsuite",
			BatchID: batchID, Caller: fxProbeOrgID, Items: []load.Item{item}})
		if err != nil {
			t.Fatalf("probe batch %s: %v", batchID, err)
		}
		for _, o := range r.Items {
			if o.LegacyID == "PRB-1" {
				return o
			}
		}
		t.Fatalf("probe batch %s answered no outcome: %+v", batchID, r.Items)
		return load.Outcome{}
	}
	probeCodes := func(t *testing.T) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM product_uoms WHERE product_id = $1::uuid`, fxProbeProductID).Scan(&n); err != nil {
			t.Fatalf("probe set read: %v", err)
		}
		return n
	}

	// The set stands loaded whole, so the next submission is a real drop.
	if o := submit(t, "b-probe-seed", setWhole); o.Status != load.OutcomeCreated {
		t.Fatalf("the seed answered %+v, want created (the first submission that stands)", o)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('c', 64)
		 WHERE org_id = $1::uuid AND source_system = 'testsuite' AND legacy_id = 'PRB-1'`, fxProbeOrgID); err != nil {
		t.Fatalf("probe drop hash plant: %v", err)
	}
	plantOverride(t)
	if o := submit(t, "b-probe-blocked", dropsBOX); o.Status != load.OutcomeRefused || o.Reason != "unit_in_use" || o.Field != "code" {
		t.Fatalf("the override blocked drop answered %+v, want refused as unit_in_use on code", o)
	}
	if got := probeCodes(t); got != 3 {
		t.Fatalf("the blocked drop left %d rows, want the three seeded rows untouched", got)
	}
	// The override goes: the same drop lands, BOX is gone.
	if _, err := pool.Exec(ctx, `DELETE FROM customer_category_overrides WHERE customer_id = $1::uuid`, fxProbeCustomerID); err != nil {
		t.Fatalf("drop override: %v", err)
	}
	if o := submit(t, "b-probe-free", dropsBOX); o.Status != load.OutcomeUpdated {
		t.Fatalf("the free drop answered %+v, want updated", o)
	}
	if got := probeCodes(t); got != 2 {
		t.Fatalf("the free drop left %d rows, want the two kept rows", got)
	}
}
