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

// fakeUnitUseReader is the seam double both readers stand behind in this
// file: it records the questions the writer asks and answers from a
// switch. The real implementations (pricing.LoadedUnitUseReader and
// inventory.LoadedUnitUseReader) carry their own suites beside their own
// tables; the wiring test in cmd/core proves the real pair is what the
// door is built with.
type fakeUnitUseReader struct {
	inUse bool
	asked []string
}

func (f *fakeUnitUseReader) ProductUnitInUseTx(_ context.Context, _ pgx.Tx, productID, uom string) (bool, error) {
	f.asked = append(f.asked, productID+":"+uom)
	return f.inUse, nil
}

// unitSetEnv is one test's fixtures: the pool and a door over the seam
// doubles.
type unitSetEnv struct {
	pool *pgxpool.Pool
	door *load.Door
	uses *fakeUnitUseReader
}

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
		pool.Close()
	})
	// Two fixture products, base SF, price PLT, each with a seeded set of
	// SF (base), PLT and BOX.
	for _, pid := range []string{fxSetProdOne, fxSetProdTwo} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO products (id, sku, name, base_uom, price_uom, org_id)
			VALUES ($1::uuid, $2, 'Fixture Set Product', 'SF', 'PLT', $3::uuid)`,
			pid, "FX-SET-"+pid[len(pid)-4:], fxSetOrgID); err != nil {
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
	store := load.NewStore(pool)
	return &unitSetEnv{
		pool: pool,
		door: load.NewDoor(store, load.KindUnitSet,
			UnitSetLoadWriter{Pricing: uses, Stock: uses}, &loadAuditSpy{}),
		uses: uses,
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
	// The reader names BOX: the drop refuses as unit_in_use, naming the
	// field, and nothing of the replace stands.
	e.uses.inUse = true
	e.uses.asked = nil
	r, err = e.door.UpsertBatch(ctx, load.Batch{OrgID: fxSetOrgID, SourceSystem: fxSetSource,
		BatchID: "b-set-drop-blocked", Caller: fxSetActorID, Items: []load.Item{dropsBOX}})
	if err != nil {
		t.Fatalf("blocked drop batch: %v", err)
	}
	o := setOutcome(t, r, "US-1")
	if o.Status != load.OutcomeRefused || o.Reason != "unit_in_use" {
		t.Fatalf("the blocked drop answered %+v, want unit_in_use", o)
	}
	if o.Field != "code" {
		t.Fatalf("the blocked drop names field %q, want code", o.Field)
	}
	got := e.setCodes(t, fxSetProdOne)
	if len(got) != 3 {
		t.Fatalf("the blocked drop left %d rows: %+v, want the three seeded rows untouched", len(got), got)
	}
	askedWant := fxSetProdOne + ":BOX"
	found := false
	for _, a := range e.uses.asked {
		if a == askedWant {
			found = true
		}
	}
	if !found {
		t.Fatalf("the writer asked the readers %v, want the dropped code %s", e.uses.asked, askedWant)
	}
	// The readers answer clear: the same drop lands, and the kept rows
	// are never asked about.
	e.uses.inUse = false
	e.uses.asked = nil
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
	store := load.NewStore(pool)
	door := load.NewDoor(store, load.KindUnitSet,
		UnitSetLoadWriter{Pricing: uses, Stock: uses}, &loadAuditSpy{})
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
