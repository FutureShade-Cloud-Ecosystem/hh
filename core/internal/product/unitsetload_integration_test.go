//go:build integration

// unitsetload_integration_test.go proves the load doors' unit set door
// (HH-LOAD-1 LD-3): the atomic replace of a product's set (exactly one
// base row at factor one, every factor positive and at scale eight or
// under, beyond that refused naming the legacy key and field, never a
// value, never rounded), the product's base and price units inside the
// set, no unit removed while a price, break, rule or stock row still
// names it (the check runs through the owning modules' read seams, whose
// real implementations carry their own suites beside their tables), the
// decimal round trip on every conversion, and the product resolved by
// legacy key through the registry only, a name not yet loaded refusing
// as a missing reference naming the kind and the key.
//
// Fixtures are invented. Run (the database URL rides the command line):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5671/ld3sets?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run TestUnitSet
package product

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/purchasing"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The unit set door's fixture constants, invented.
const (
	fxSetOrgID   = "4642a800-0000-4000-8000-00000000d201"
	fxSetActorID = "4642a800-0000-4000-8000-00000000d20a"
	fxSetSource  = "testsuite"
	fxSetProdOne = "4642a800-0000-4000-8000-00000000d211"
	fxSetProdTwo = "4642a800-0000-4000-8000-00000000d212"
)

// fakeUnitUseReader is the seam double the stock and vendor readers
// stand behind in this file: it records the questions the writer asks
// and answers from a switch. fakePricingUseReader is the pricing seam's
// double, its question carrying the product's category beside the
// product. The real implementations (pricing.LoadedUnitUseReader,
// inventory.LoadedUnitUseReader, purchasing.LoadedUnitUseReader) carry
// their own suites beside their own tables; the wiring test in cmd/core
// proves the real triple is what the door is built with.
type fakeUnitUseReader struct {
	inUse bool
	asked []string
}

func (f *fakeUnitUseReader) ProductUnitInUseTx(_ context.Context, _ pgx.Tx, productID, uom string) (bool, error) {
	f.asked = append(f.asked, productID+":"+uom)
	return f.inUse, nil
}

type fakePricingUseReader struct {
	inUse bool
	asked []string
}

func (f *fakePricingUseReader) ProductUnitInUseTx(_ context.Context, _ pgx.Tx, productID string, categoryID *string, uom string) (bool, error) {
	cat := ""
	if categoryID != nil {
		cat = *categoryID
	}
	f.asked = append(f.asked, productID+":"+cat+":"+uom)
	return f.inUse, nil
}

// unitSetEnv is one test's fixtures: the pool, a door over the seam
// doubles, the product's category id.
type unitSetEnv struct {
	pool       *pgxpool.Pool
	door       *load.Door
	uses       *fakeUnitUseReader
	puses      *fakePricingUseReader
	categoryID string
}

// fxSetCategoryID and fxSetParentCategoryID are the fixture product's
// category and its parent, invented.
const (
	fxSetCategoryID      = "4642a800-0000-4000-8000-00000000d2c1"
	fxSetParentCategory  = "4642a800-0000-4000-8000-00000000d2c0"
	fxSetProdOneCategory = fxSetCategoryID
)

func unitSetSetup(t *testing.T) *unitSetEnv {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM load_key_registry WHERE org_id = $1::uuid`, []any{fxSetOrgID}},
		{`DELETE FROM load_batch WHERE org_id = $1::uuid`, []any{fxSetOrgID}},
		{`DELETE FROM product_uoms WHERE product_id = ANY($1::uuid[])`, []any{[]string{fxSetProdOne, fxSetProdTwo}}},
		{`DELETE FROM products WHERE id = ANY($1::uuid[])`, []any{[]string{fxSetProdOne, fxSetProdTwo}}},
		{`DELETE FROM product_rounding_rules WHERE product_id = ANY($1::uuid[])`, []any{[]string{fxSetProdOne, fxSetProdTwo}}},
		{`DELETE FROM products WHERE category_id = $1::uuid`, []any{fxSetCategoryID}},
		{`DELETE FROM product_categories WHERE id = ANY($1::uuid[])`, []any{[]string{fxSetCategoryID, fxSetParentCategory}}},
	} {
		if _, err := pool.Exec(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("unit set fixture clean: %v", err)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM load_key_registry WHERE org_id = $1::uuid`, fxSetOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM load_batch WHERE org_id = $1::uuid`, fxSetOrgID)
		_, _ = pool.Exec(ctx, `DELETE FROM product_uoms WHERE product_id = ANY($1::uuid[])`, []string{fxSetProdOne, fxSetProdTwo})
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE id = ANY($1::uuid[])`, []string{fxSetProdOne, fxSetProdTwo})
		_, _ = pool.Exec(ctx, `DELETE FROM product_rounding_rules WHERE product_id = ANY($1::uuid[])`, []string{fxSetProdOne, fxSetProdTwo})
		_, _ = pool.Exec(ctx, `DELETE FROM products WHERE category_id = $1::uuid`, fxSetCategoryID)
		_, _ = pool.Exec(ctx, `DELETE FROM product_categories WHERE id = ANY($1::uuid[])`, []string{fxSetCategoryID, fxSetParentCategory})
		pool.Close()
	})
	// The fixture category stands under a parent, so the category
	// question carries a real answer.
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_categories (id, code, name)
		VALUES ($1::uuid, 'FX-SET-CAT-P', 'Fixture Set Category Parent')`, fxSetParentCategory); err != nil {
		t.Fatalf("fixture parent category: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_categories (id, code, name, parent_id)
		VALUES ($1::uuid, 'FX-SET-CAT', 'Fixture Set Category', $2::uuid)`, fxSetCategoryID, fxSetParentCategory); err != nil {
		t.Fatalf("fixture category: %v", err)
	}
	// Two fixture products, base SF, price PLT, each with a seeded set of
	// SF (base), PLT and BOX, in the fixture category.
	for _, pid := range []string{fxSetProdOne, fxSetProdTwo} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO products (id, sku, name, base_uom, price_uom, org_id, category_id)
			VALUES ($1::uuid, $2, 'Fixture Set Product', 'SF', 'PLT', $3::uuid, $4::uuid)`,
			pid, "FX-SET-"+pid[len(pid)-4:], fxSetOrgID, fxSetCategoryID); err != nil {
			t.Fatalf("fixture product: %v", err)
		}
		for _, row := range []struct {
			code string
			qty  string
			base bool
		}{{"SF", "1", true}, {"PLT", "60", false}, {"BOX", "20", false}} {
			if _, err := pool.Exec(ctx, `
				INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base, org_id)
				VALUES ($1::uuid, $2, $3::numeric, $4, $5::uuid)`,
				pid, row.code, row.qty, row.base, fxSetOrgID); err != nil {
				t.Fatalf("fixture set row: %v", err)
			}
		}
	}
	// The products are named by legacy key through the registry, the
	// only door a product name has until LD-5 builds the product door.
	for _, n := range []struct {
		legacy string
		pid    string
	}{{"UP-1", fxSetProdOne}, {"UP-2", fxSetProdTwo}} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
			VALUES ($1::uuid, $2, 'product', $3, $4::uuid, repeat('b', 64))
			ON CONFLICT DO NOTHING`, fxSetOrgID, fxSetSource, n.legacy, n.pid); err != nil {
			t.Fatalf("fixture product name: %v", err)
		}
	}
	uses := &fakeUnitUseReader{}
	puses := &fakePricingUseReader{}
	store := load.NewStore(pool)
	return &unitSetEnv{
		pool: pool,
		door: load.NewDoor(store, load.KindUnitSet,
			UnitSetLoadWriter{Pricing: puses, Stock: uses, Vendor: uses}, &loadAuditSpy{}),
		uses:       uses,
		puses:      puses,
		categoryID: fxSetProdOneCategory,
	}
}

// setRow is one row of a set record.
func setRow(code, qty string, base bool) map[string]any {
	return map[string]any{"code": code, "qty_in_base": qty, "is_base": base}
}

// setItem is one unit set batch item over the fixture product names.
func setItem(legacyID string, rows ...map[string]any) load.Item {
	return load.Item{
		LegacyID:   legacyID,
		DerivedKey: "d3000000-0000-4000-8000-000000000301",
		Record:     map[string]any{"rows": rows},
		Requires:   []load.Ref{{Kind: "product", LegacyID: "UP-1"}},
	}
}

// setOutcome picks one item's outcome out of a batch result.
func setOutcome(t *testing.T, r load.BatchResult, legacyID string) load.Outcome {
	t.Helper()
	for _, o := range r.Items {
		if o.LegacyID == legacyID {
			return o
		}
	}
	t.Fatalf("batch answered no outcome for %s: %+v", legacyID, r.Items)
	return load.Outcome{}
}

// setCodes reads the product's set as code to factor text.
func (e *unitSetEnv) setCodes(t *testing.T, productID string) map[string]string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(),
		`SELECT uom_code::text, qty_in_base::text FROM product_uoms WHERE product_id = $1::uuid ORDER BY uom_code`, productID)
	if err != nil {
		t.Fatalf("set read: %v", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, qty string
		if err := rows.Scan(&code, &qty); err != nil {
			t.Fatalf("set scan: %v", err)
		}
		out[code] = qty
	}
	return out
}

func TestUnitSetReplacesAtomicallyAndAnswersTheArms(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-1", Caller: fxSetActorID,
		Items: []load.Item{setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "60", false), setRow("LYR", "0.25", false))}})
	if err != nil {
		t.Fatalf("set batch: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the first set answered %+v, want created", o)
	}
	got := e.setCodes(t, fxSetProdOne)
	if len(got) != 3 || got["SF"] != "1.00000000" || got["PLT"] != "60.00000000" || got["LYR"] != "0.25000000" {
		t.Fatalf("the replaced set is %+v, want SF 1, PLT 60, LYR 0.25 at the column's exact scale", got)
	}
	// The replaced rows carry the load's stamps.
	var org, idem string
	if err := e.pool.QueryRow(ctx,
		`SELECT coalesce(org_id::text, ''), coalesce(idempotency_key::text, '')
		 FROM product_uoms WHERE product_id = $1::uuid AND uom_code = 'SF'`, fxSetProdOne).Scan(&org, &idem); err != nil {
		t.Fatalf("set stamp read: %v", err)
	}
	if org != fxSetOrgID || idem == "" {
		t.Fatalf("the set rows carry org %q idempotency %q: the load's stamps are missing", org, idem)
	}
	// The replay arm: the identical record answers unchanged.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-2", Caller: fxSetActorID,
		Items: []load.Item{setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "60", false), setRow("LYR", "0.25", false))}})
	if err != nil {
		t.Fatalf("set replay: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeUnchanged {
		t.Fatalf("the replayed set answered %+v, want unchanged", o)
	}
	// A changed factor answers updated and the set moves as a whole: the
	// seeded three row set becomes two rows in one transaction.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-3", Caller: fxSetActorID,
		Items: []load.Item{setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "55", false))}})
	if err != nil {
		t.Fatalf("set update: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the changed set answered %+v, want updated", o)
	}
	got = e.setCodes(t, fxSetProdOne)
	if len(got) != 2 || got["PLT"] != "55.00000000" {
		t.Fatalf("the updated set is %+v, want SF and PLT 55 only", got)
	}
}

// TestUnitSetRefusesAResolvedReferenceOfAnotherKind: a reference of a
// kind the set record takes none of refuses as the wrong reference once
// it resolves; an unrecorded name of any kind is the frame's missing
// reference before the writer runs.
func TestUnitSetRefusesAResolvedReferenceOfAnotherKind(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
		VALUES ($1::uuid, $2, 'category', 'UP-1', gen_random_uuid(), repeat('e', 64))`,
		fxSetOrgID, fxSetSource); err != nil {
		t.Fatalf("plant category name: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx,
			`DELETE FROM load_key_registry WHERE org_id = $1::uuid AND kind = 'category'`, fxSetOrgID)
	})
	it := setItem("US-1", setRow("SF", "1", true), setRow("PLT", "60", false))
	it.Requires = []load.Ref{{Kind: "category", LegacyID: "UP-1"}}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-wrongref", Caller: fxSetActorID, Items: []load.Item{it}})
	if err != nil {
		t.Fatalf("wrong reference batch: %v", err)
	}
	o := setOutcome(t, r, "US-1")
	if o.Status != load.OutcomeRefused || o.Reason != "wrong_reference" || o.Field != "category" {
		t.Fatalf("the wrong reference answered %+v, want wrong_reference on category", o)
	}
}

func TestUnitSetRefusesShapesBeyondTheSetRule(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	cases := []struct {
		name        string
		item        load.Item
		wantReason  string
		wantField   string
		detailHas   string
		detailHasnt []string
	}{
		{"two base rows", setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "60", true)), "unit_set_base", "rows", "", nil},
		{"no base row", setItem("US-1",
			setRow("SF", "1", false), setRow("PLT", "60", false)), "unit_set_base", "rows", "", nil},
		{"base factor not one", setItem("US-1",
			setRow("SF", "2", true), setRow("PLT", "60", false)), "unit_set_base", "rows", "", nil},
		{"zero factor", setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "0", false)), "factor_negative", "qty_in_base", "", nil},
		{"negative factor", setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "-60", false)), "factor_negative", "qty_in_base", "", nil},
		{"scale nine", setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "0.123456789", false)), "factor_scale", "qty_in_base", "US-1",
			[]string{"0.123456789"}},
		{"junk factor", setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "wider", false)), "record_shape", "qty_in_base", "", nil},
		{"unknown code", setItem("US-1",
			setRow("SF", "1", true), setRow("FXU-NOPE", "5", false)), "no_unit_row", "code", "", nil},
		{"base unit absent", setItem("US-1",
			setRow("LYR", "1", true), setRow("BOX", "20", false)), "unit_set_base", "base_uom", "", nil},
		{"price unit absent", setItem("US-1",
			setRow("SF", "1", true), setRow("BOX", "20", false)), "unit_set_base", "price_uom", "", nil},
		{"no product reference", func() load.Item {
			it := setItem("US-1", setRow("SF", "1", true), setRow("PLT", "60", false))
			it.Requires = nil
			return it
		}(), "record_shape", "product", "", nil},
	}
	for _, c := range cases {
		r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
			BatchID: "b-set-shape-" + c.name, Caller: fxSetActorID, Items: []load.Item{c.item}})
		if err != nil {
			t.Fatalf("%s: batch: %v", c.name, err)
		}
		o := setOutcome(t, r, "US-1")
		if o.Status != load.OutcomeRefused {
			t.Fatalf("%s: answered %+v, want refused", c.name, o)
		}
		if o.Reason != c.wantReason || o.Field != c.wantField {
			t.Fatalf("%s: refusal carries reason %q field %q, want %q on %q", c.name, o.Reason, o.Field, c.wantReason, c.wantField)
		}
		if c.detailHas != "" && !strings.Contains(o.Detail, c.detailHas) {
			t.Fatalf("%s: refusal detail %q names not the legacy key", c.name, o.Detail)
		}
		for _, absent := range c.detailHasnt {
			if strings.Contains(o.Detail, absent) {
				t.Fatalf("%s: refusal detail %q carries a value", c.name, o.Detail)
			}
		}
	}
	// Nothing of any refused shape landed: the seeded set still stands
	// whole.
	got := e.setCodes(t, fxSetProdOne)
	if len(got) != 3 {
		t.Fatalf("after the shape refusals the seeded set is %+v, want the three seeded rows untouched", got)
	}
}

func TestUnitSetRefusesAMissingProductName(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	it := setItem("US-1", setRow("SF", "1", true), setRow("PLT", "60", false))
	it.Requires = []load.Ref{{Kind: "product", LegacyID: "UP-MISSING"}}
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-missing", Caller: fxSetActorID, Items: []load.Item{it}})
	if err != nil {
		t.Fatalf("missing name batch: %v", err)
	}
	o := setOutcome(t, r, "US-1")
	if o.Status != load.OutcomeRefused || o.Reason != load.ReasonMissingReference {
		t.Fatalf("the missing product answered %+v, want missing_reference", o)
	}
	if o.Field != "product" || !strings.Contains(o.Detail, "UP-MISSING") {
		t.Fatalf("the missing reference names not the kind and the key: field %q detail %q", o.Field, o.Detail)
	}
}

// TestUnitSetRefusesARemovalAReaderNames: the set that drops a unit
// refuses while a reader of the price, break, rule or stock families
// names the dropped unit, and lands once the readers answer clear. The
// writer must ask about every dropped code, nothing else.
func TestUnitSetRefusesARemovalAReaderNames(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	// The loaded set keeps SF and PLT but drops BOX, which the seeded
	// set carries.
	dropsBOX := setItem("US-1", setRow("SF", "1", true), setRow("PLT", "60", false))
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-load-box", Caller: fxSetActorID,
		Items: []load.Item{setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "60", false), setRow("BOX", "20", false))}})
	if err != nil {
		t.Fatalf("box seed batch: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the box set answered %+v, want created", o)
	}
	// A different recorded hash so the drop runs the update arm.
	if _, err := e.pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('c', 64)
		 WHERE org_id = $1::uuid AND source_system = $2 AND legacy_id = 'US-1'`, fxSetOrgID, fxSetSource); err != nil {
		t.Fatalf("drop hash plant: %v", err)
	}
	// Each seam in turn names BOX: the drop refuses as unit_in_use,
	// naming the field, and nothing of the replace stands. The writer
	// short circuits at the first naming seam, so the rounds prove each
	// seam's ask: the pricing ask carries the product's category beside
	// the product (the category scoped families ride the same ask), the
	// stock and vendor asks the dropped code alone.
	askedWant := fxSetProdOne + ":BOX"
	askedCategoryWant := fxSetProdOne + ":" + e.categoryID + ":BOX"
	e.uses.asked = nil
	e.puses.asked = nil
	e.puses.inUse = true
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-drop-blocked-pricing", Caller: fxSetActorID, Items: []load.Item{dropsBOX}})
	if err != nil {
		t.Fatalf("pricing blocked drop batch: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeRefused || o.Reason != "unit_in_use" || o.Field != "code" {
		t.Fatalf("the pricing blocked drop answered %+v, want unit_in_use on code", o)
	}
	if len(e.puses.asked) != 1 || e.puses.asked[0] != askedCategoryWant {
		t.Fatalf("the writer asked the pricing seam %v, want the dropped code with the product's category (%s)", e.puses.asked, askedCategoryWant)
	}
	if len(e.uses.asked) != 0 {
		t.Fatalf("the pricing seam named the unit and the writer still asked %v", e.uses.asked)
	}
	e.puses.inUse = false
	e.uses.inUse = true
	e.uses.asked = nil
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-drop-blocked-stock", Caller: fxSetActorID, Items: []load.Item{dropsBOX}})
	if err != nil {
		t.Fatalf("stock blocked drop batch: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeRefused || o.Reason != "unit_in_use" {
		t.Fatalf("the stock blocked drop answered %+v, want unit_in_use", o)
	}
	if len(e.uses.asked) != 1 || e.uses.asked[0] != askedWant {
		t.Fatalf("the writer asked the stock or vendor seam %v, want the dropped code %s", e.uses.asked, askedWant)
	}
	e.uses.inUse = false
	got := e.setCodes(t, fxSetProdOne)
	if len(got) != 3 {
		t.Fatalf("the blocked drops left %d rows: %+v, want the three seeded rows untouched", len(got), got)
	}
	// The readers answer clear: the same drop lands, and the kept rows
	// are never asked about.
	e.uses.asked = nil
	e.puses.asked = nil
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-drop-free", Caller: fxSetActorID, Items: []load.Item{dropsBOX}})
	if err != nil {
		t.Fatalf("free drop batch: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the free drop answered %+v, want updated", o)
	}
	for _, a := range e.uses.asked {
		if a != askedWant {
			t.Fatalf("the writer asked the readers about %s, want only the dropped code", a)
		}
	}
}

// TestUnitSetBaseRowIsTheProductsBaseUnit: the set's one base row must
// be the product's own base unit (products.base_uom, compared the way
// CITEXT compares), not merely a set whose base and price units are
// members. The reviewer's probe (a BOX base row on a product whose base
// is SF) refuses; a loaded set answers one for the base unit's own
// conversion.
func TestUnitSetBaseRowIsTheProductsBaseUnit(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	// The probe: BOX carries the base row, SF sits inside at a fraction.
	// Every narrower check passes this set; only the base row equality
	// refuses it.
	probe := setItem("US-1",
		setRow("BOX", "1", true), setRow("SF", "0.5", false), setRow("PLT", "30", false))
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-base-probe", Caller: fxSetActorID, Items: []load.Item{probe}})
	if err != nil {
		t.Fatalf("base probe batch: %v", err)
	}
	o := setOutcome(t, r, "US-1")
	if o.Status != load.OutcomeRefused || o.Reason != "unit_set_base" || o.Field != "base_uom" {
		t.Fatalf("the foreign base row answered %+v, want refused as unit_set_base on base_uom", o)
	}
	// Nothing of the refused replace stands: the seeded set keeps its own
	// base row, and the base unit's own conversion answers one.
	got := e.setCodes(t, fxSetProdOne)
	if got["SF"] != "1.00000000" {
		t.Fatalf("after the refusal the seeded base row is %+v, want SF 1.00000000 untouched", got)
	}
	factor, err := NewRepository(e.pool).ConvertUOM(ctx, fxSetProdOne, "SF")
	if err != nil {
		t.Fatalf("base unit conversion: %v", err)
	}
	if factor != 1 {
		t.Fatalf("the product's own base unit converts by %v, want 1", factor)
	}
	// The honest set, the base row the product's own, lands.
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-base-good", Caller: fxSetActorID,
		Items: []load.Item{setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "30", false), setRow("BOX", "0.5", false))}})
	if err != nil {
		t.Fatalf("honest base batch: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the honest set answered %+v, want created (the first submission that stands)", o)
	}
	factor, err = NewRepository(e.pool).ConvertUOM(ctx, fxSetProdOne, "SF")
	if err != nil || factor != 1 {
		t.Fatalf("after the honest set the base unit converts by %v (%v), want 1", factor, err)
	}
}

// TestUnitSetRefusesANumberFactor: the factor arrives as decimal text;
// a JSON number is refused naming factor_type, never re-spelled into
// text and never stored rounded. The reviewer's nineteen significant
// digit number is the probe.
func TestUnitSetRefusesANumberFactor(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	it := setItem("US-1",
		setRow("SF", "1", true),
		map[string]any{"code": "PLT", "qty_in_base": float64(12345678901.12345678), "is_base": false})
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-numfactor", Caller: fxSetActorID, Items: []load.Item{it}})
	if err != nil {
		t.Fatalf("number factor batch: %v", err)
	}
	o := setOutcome(t, r, "US-1")
	if o.Status != load.OutcomeRefused || o.Reason != "factor_type" || o.Field != "factor_type" {
		t.Fatalf("the numeric factor answered %+v, want refused as factor_type on factor_type", o)
	}
	for _, digit := range []string{"12345678901", "123457", "30"} {
		if strings.Contains(o.Detail, digit) {
			t.Fatalf("the numeric factor refusal carries a value: %q", o.Detail)
		}
	}
	got := e.setCodes(t, fxSetProdOne)
	if got["PLT"] != "60.00000000" {
		t.Fatalf("the refused number left PLT %+v, want the seeded 60.00000000 untouched", got["PLT"])
	}
}

// TestUnitSetRefusesAFactorTheResolverWouldCorrupt: the stored decimal
// text must survive the representation the price resolver reads (the
// float parse of repository.go ConvertUOM). A factor of more than
// fifteen significant digits passes the scale gate and the column would
// hold it exactly, and the door refuses it as a round trip failure
// naming the legacy key, never a value.
func TestUnitSetRefusesAFactorTheResolverWouldCorrupt(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	it := setItem("US-1",
		setRow("SF", "1", true),
		map[string]any{"code": "PLT", "qty_in_base": "12345678901.12345678", "is_base": false})
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-floattrip", Caller: fxSetActorID, Items: []load.Item{it}})
	if err != nil {
		t.Fatalf("float trip batch: %v", err)
	}
	o := setOutcome(t, r, "US-1")
	if o.Status != load.OutcomeRefused || o.Reason != "round_trip" {
		t.Fatalf("the wide factor answered %+v, want refused as round_trip: the resolver reads it back differently", o)
	}
	if !strings.Contains(o.Detail, "US-1") {
		t.Fatalf("the round trip refusal names no legacy key: %q", o.Detail)
	}
	if strings.Contains(o.Detail, "12345678901") {
		t.Fatalf("the round trip refusal carries a value: %q", o.Detail)
	}
	got := e.setCodes(t, fxSetProdOne)
	if got["PLT"] != "60.00000000" {
		t.Fatalf("the refused factor left PLT %+v, want the seeded 60.00000000 untouched", got["PLT"])
	}
}

// TestUnitSetRefusesARemovalTheVendorSkuNames: the purchasing module's
// live supplier rows name units too. A unit a vendor sku sells in is
// refused the drop while the sku stands and is free once it goes, read
// by the real purchasing reader at the same seam (its own suite proves
// the read beside its table; this proves the door consults it).
func TestUnitSetRefusesARemovalTheVendorSkuNames(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	vendorDoor := load.NewDoor(load.NewStore(e.pool), load.KindUnitSet,
		UnitSetLoadWriter{Pricing: e.puses, Stock: e.uses, Vendor: purchasing.LoadedUnitUseReader{}},
		&loadAuditSpy{})
	var vendorID string
	if err := e.pool.QueryRow(ctx, `SELECT id FROM vendors ORDER BY vendor_no LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("fixture vendor read: %v", err)
	}
	if _, err := e.pool.Exec(ctx, `
		INSERT INTO purchasing_vendor_skus (vendor_id, product_id, vendor_sku, vendor_uom, qty_per_vendor_uom)
		VALUES ($1::uuid, $2::uuid, 'FXUSE-SKU', 'BOX', 20)`, vendorID, fxSetProdOne); err != nil {
		t.Fatalf("plant vendor sku: %v", err)
	}
	t.Cleanup(func() {
		_, _ = e.pool.Exec(ctx, `DELETE FROM purchasing_vendor_skus WHERE product_id = $1::uuid`, fxSetProdOne)
	})
	// The set stands loaded whole, so the next submission is a real drop.
	r, err := vendorDoor.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-vendor-seed", Caller: fxSetActorID,
		Items: []load.Item{setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "60", false), setRow("BOX", "20", false))}})
	if err != nil {
		t.Fatalf("vendor seed batch: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeCreated {
		t.Fatalf("the vendor seed answered %+v, want created", o)
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('f', 64)
		 WHERE org_id = $1::uuid AND source_system = $2 AND legacy_id = 'US-1'`, fxSetOrgID, fxSetSource); err != nil {
		t.Fatalf("vendor drop hash plant: %v", err)
	}
	// The load drops BOX, which the vendor sku sells in: the drop refuses
	// and nothing of the replace stands.
	dropsBOX := setItem("US-1", setRow("SF", "1", true), setRow("PLT", "60", false))
	r, err = vendorDoor.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-vendor-blocked", Caller: fxSetActorID, Items: []load.Item{dropsBOX}})
	if err != nil {
		t.Fatalf("vendor blocked batch: %v", err)
	}
	o := setOutcome(t, r, "US-1")
	if o.Status != load.OutcomeRefused || o.Reason != "unit_in_use" {
		t.Fatalf("the vendor sku drop answered %+v, want refused as unit_in_use", o)
	}
	got := e.setCodes(t, fxSetProdOne)
	if len(got) != 3 {
		t.Fatalf("the blocked drop left %d rows, want the three seeded rows untouched", len(got))
	}
	// The sku goes: the same drop lands.
	if _, err := e.pool.Exec(ctx, `DELETE FROM purchasing_vendor_skus WHERE product_id = $1::uuid`, fxSetProdOne); err != nil {
		t.Fatalf("drop vendor sku: %v", err)
	}
	r, err = vendorDoor.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-vendor-free", Caller: fxSetActorID, Items: []load.Item{dropsBOX}})
	if err != nil {
		t.Fatalf("vendor free batch: %v", err)
	}
	if o := setOutcome(t, r, "US-1"); o.Status != load.OutcomeUpdated {
		t.Fatalf("the free drop answered %+v, want updated", o)
	}
}

// TestUnitSetRefusesOneCodeTwiceUnderTheDatabaseFold pins the set's
// duplicate code check: two spellings of one code are one row claimed
// twice, folded the way the database folds (lower() through the
// database, case and non ASCII letters alike), and the second would
// overwrite the first silently if the check left. Both pairs refuse as
// a record shape on code; the bite that proves the pin is the fold
// itself (the check's lower(c) read as a case sensitive count turns
// both cases red).
func TestUnitSetRefusesOneCodeTwiceUnderTheDatabaseFold(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	for _, probe := range []struct {
		batch string
		rows  []map[string]any
	}{
		{"b-set-dup-ascii", []map[string]any{
			{"code": "SF", "qty_in_base": "1", "is_base": true},
			{"code": "PLT", "qty_in_base": "60", "is_base": false},
			{"code": "plt", "qty_in_base": "60", "is_base": false}}},
		{"b-set-dup-nonascii", []map[string]any{
			{"code": "SF", "qty_in_base": "1", "is_base": true},
			{"code": "ÆX", "qty_in_base": "4", "is_base": false},
			{"code": "æx", "qty_in_base": "4", "is_base": false}}},
	} {
		r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
			BatchID: probe.batch, Caller: fxSetActorID,
			Items: []load.Item{setItem("US-1", probe.rows...)}})
		if err != nil {
			t.Fatalf("duplicate batch %s: %v", probe.batch, err)
		}
		o := setOutcome(t, r, "US-1")
		if o.Status != load.OutcomeRefused || o.Reason != "record_shape" || o.Field != "code" {
			t.Fatalf("the doubled code answered %+v, want refused as record_shape on code", o)
		}
		if strings.Contains(o.Detail, "PLT") || strings.Contains(o.Detail, "ÆX") {
			t.Fatalf("the duplicate refusal carries a value: %q", o.Detail)
		}
		got := e.setCodes(t, fxSetProdOne)
		if len(got) != 3 {
			t.Fatalf("the refused duplicate left %d rows: %+v, want the three seeded rows untouched", len(got), got)
		}
	}
}

// TestUnitSetRefusesAPaddedCode: a row of the set naming a code with a
// leading or trailing space refuses as a record shape on code, the same
// gate the unit door runs: the padded spelling is not a unit, and the
// catalogue read's no_unit_row answer would name the wrong reason for
// it.
func TestUnitSetRefusesAPaddedCode(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-pad", Caller: fxSetActorID,
		Items: []load.Item{setItem("US-1",
			setRow("SF", "1", true), setRow("PLT", "60", false), setRow("BOX ", "20", false))}})
	if err != nil {
		t.Fatalf("padded code batch: %v", err)
	}
	o := setOutcome(t, r, "US-1")
	if o.Status != load.OutcomeRefused || o.Reason != "record_shape" || o.Field != "code" {
		t.Fatalf("the padded code answered %+v, want refused as record_shape on code", o)
	}
	if strings.Contains(o.Detail, "BOX") {
		t.Fatalf("the padded code refusal carries a value: %q", o.Detail)
	}
	got := e.setCodes(t, fxSetProdOne)
	if len(got) != 3 {
		t.Fatalf("the refused padded code left %d rows: %+v, want the three seeded rows untouched", len(got), got)
	}
}

// TestUnitSetPoolFourThreeContendersEveryCallOnTheBatchTransaction is
// the pool 4 leg for the unit set door (decision row 786).
func TestUnitSetPoolFourThreeContendersEveryCallOnTheBatchTransaction(t *testing.T) {
	unitSetSetup(t)
	pool := poolFour(t)
	ctx := context.Background()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	uses := &fakeUnitUseReader{}
	puses := &fakePricingUseReader{}
	store := load.NewStore(pool)
	door := load.NewDoor(store, load.KindUnitSet,
		UnitSetLoadWriter{Pricing: puses, Stock: uses, Vendor: uses}, &loadAuditSpy{})
	items := []load.Item{setItem("US-1", setRow("SF", "1", true), setRow("PLT", "60", false))}
	var wg sync.WaitGroup
	errs := make([]error, 3)
	created := make([]int, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, poolFourBatchTimeout)
			defer cancel()
			r, err := door.UpsertBatch(cctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
				BatchID: fmt.Sprintf("b-set-pool-%d", k), Caller: fxSetActorID,
				Items: append([]load.Item(nil), items...)})
			errs[k] = err
			for _, o := range r.Items {
				if o.Status == load.OutcomeCreated {
					created[k]++
				}
			}
		}(k)
	}
	wg.Wait()
	totalCreated := 0
	for k, err := range errs {
		if err != nil {
			t.Fatalf("contender %d: %v (a batch needed a connection beyond the transaction it holds)", k, err)
		}
		totalCreated += created[k]
	}
	if totalCreated != 1 {
		t.Fatalf("%d creates across the contenders, want exactly one name created", totalCreated)
	}
}

// straySetWriter is the deliberate violation, the pool leg's bite twin:
// a writer that reaches for the pool while the batch holds its
// transaction. The pool leg is only worth its name if this one fails it.
type straySetWriter struct {
	pool  *pgxpool.Pool
	inner UnitSetLoadWriter
}

func (w straySetWriter) Apply(ctx context.Context, tx pgx.Tx, in load.Write) (string, error) {
	if _, err := w.pool.Exec(ctx, `SELECT 1`); err != nil {
		return "", err
	}
	return w.inner.Apply(ctx, tx, in)
}

// TestUnitSetPoolFourBitesAWriterThatUsesThePool is the bite twin of the
// set door's pool 4 leg: the same three contenders over a pool of four,
// the suite holding the fourth connection, against a writer that makes
// one stray pool call beside the transaction. Every contender fails
// fast, and none hangs on the held connection.
func TestUnitSetPoolFourBitesAWriterThatUsesThePool(t *testing.T) {
	unitSetSetup(t)
	pool := poolFour(t)
	ctx := context.Background()
	held, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("hold one connection: %v", err)
	}
	defer held.Release()
	uses := &fakeUnitUseReader{}
	puses := &fakePricingUseReader{}
	door := load.NewDoor(load.NewStore(pool), load.KindUnitSet,
		straySetWriter{pool: pool, inner: UnitSetLoadWriter{Pricing: puses, Stock: uses, Vendor: uses}},
		&loadAuditSpy{})
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for k := 0; k < 3; k++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			_, errs[k] = door.UpsertBatch(cctx, load.Batch{OrgID: fxSetOrgID,
				SourceSystem: fxSetSource, BatchID: fmt.Sprintf("b-set-stray-%d", k), Caller: fxSetActorID,
				Items: []load.Item{setItem("US-1", setRow("SF", "1", true), setRow("PLT", "60", false))}})
		}(k)
	}
	wg.Wait()
	for k, err := range errs {
		if err == nil {
			t.Fatalf("contender %d finished: the pool 4 leg missed a writer that uses the pool", k)
		}
	}
}

// TestUnitSetDeadlockAnswersTheContentionSentinel: the real deadlock leg
// for the set door. Two fixture products' sets stand with names
// recorded; the batch replaces them in one order while an outside
// transaction holds a row of the second set and then wants a row of the
// first, Postgres kills the batch, the sentinel answers, nothing stands,
// and the resubmission converges.
func TestUnitSetDeadlockAnswersTheContentionSentinel(t *testing.T) {
	e := unitSetSetup(t)
	ctx := context.Background()
	for _, n := range []struct {
		batch string
		item  string
		ref   string
		pid   string
	}{{"b-set-dl-seed-1", "US-1", "UP-1", fxSetProdOne}, {"b-set-dl-seed-2", "US-2", "UP-2", fxSetProdTwo}} {
		it := setItem(n.item, setRow("SF", "1", true), setRow("PLT", "60", false))
		it.Requires = []load.Ref{{Kind: "product", LegacyID: n.ref}}
		if _, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
			BatchID: n.batch, Caller: fxSetActorID, Items: []load.Item{it}}); err != nil {
			t.Fatalf("deadlock seed %s: %v", n.batch, err)
		}
	}
	if _, err := e.pool.Exec(ctx,
		`UPDATE load_key_registry SET content_hash = repeat('d', 64)
		 WHERE org_id = $1::uuid AND source_system = $2 AND kind = $3`, fxSetOrgID, fxSetSource, load.KindUnitSet); err != nil {
		t.Fatalf("deadlock hash plant: %v", err)
	}
	outside, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("outside begin: %v", err)
	}
	defer func() { _ = outside.Rollback(ctx) }()
	if _, err := outside.Exec(ctx, `SET LOCAL deadlock_timeout = '30s'`); err != nil {
		t.Fatalf("outside timeout: %v", err)
	}
	// The outside holds one row of the second product's set.
	if _, err := outside.Exec(ctx,
		`UPDATE product_uoms SET qty_in_base = qty_in_base WHERE product_id = $1::uuid AND uom_code = 'SF'`, fxSetProdTwo); err != nil {
		t.Fatalf("outside lock: %v", err)
	}
	one := setItem("US-1", setRow("SF", "1", true), setRow("PLT", "61", false))
	one.Requires = []load.Ref{{Kind: "product", LegacyID: "UP-1"}}
	two := setItem("US-2", setRow("SF", "1", true), setRow("PLT", "62", false))
	two.Requires = []load.Ref{{Kind: "product", LegacyID: "UP-2"}}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := e.door.UpsertBatch(cctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
			BatchID: "b-set-dl", Caller: fxSetActorID, Items: []load.Item{one, two}})
		done <- err
	}()
	waitForLockWait(t, e.pool)
	// The outside now wants a row of the first product's set, which the
	// batch's first item replaced and holds: the cycle closes.
	_, _ = outside.Exec(ctx,
		`UPDATE product_uoms SET qty_in_base = qty_in_base WHERE product_id = $1::uuid AND uom_code = 'SF'`, fxSetProdOne)
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked set batch answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)
	var batches int
	if err := e.pool.QueryRow(ctx,
		`SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-set-dl'`, fxSetOrgID).Scan(&batches); err != nil {
		t.Fatalf("victim remainder read: %v", err)
	}
	if batches != 0 {
		t.Fatalf("the victim left %d batch rows: a contended batch rolls back whole", batches)
	}
	// The resubmission converges.
	r, err := e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-dl", Caller: fxSetActorID, Items: []load.Item{one, two}})
	if err != nil {
		t.Fatalf("deadlock resubmission: %v", err)
	}
	for _, o := range r.Items {
		if o.Status == load.OutcomeRefused {
			t.Fatalf("the resubmission refused %+v", o)
		}
	}
}
