//go:build integration

// productload_window_integration_test.go closes the review's round 2 P2
// (HH-LOAD-1 LD-5 round 3): the product reload and the unit set replace
// are two different registry kinds, so the frame's registry locks do not
// serialise them, and the two doors' reads of the shared row (the
// product's unit set, the product's base unit) raced on MVCC snapshots.
// The legs run two real concurrent transactions, one per door, and hold
// the first open: a raw pool.Begin whose writer has answered and whose
// commit has not run, so every row it touched stays locked and every
// row it wrote stays invisible to the other transaction until the test
// commits it. Order (a): the set replace stands open, the reload starts;
// the reload must wait, then answer against the committed set (the
// unit_set_mismatch refusal, or a landing consistent with it). Order
// (b): the reload stands open, the set replace starts; the replace must
// wait, then check against the committed product. The final state must
// never be a base unit over a set whose base row is another unit. The
// third leg runs the two doors as two real concurrent batch submissions
// over two products in opposite orders, the deadlock shape the new row
// locks can form: whatever Postgres rules, each batch either finishes or
// answers the ErrBatchContended sentinel with nothing of it standing,
// and the resubmission converges.
//
// Fixtures are invented: the fxpr window skus, the org and actor ids.
// Run (the database URL rides the command line, never a default):
//
//	DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5684/probe_gate?sslmode=disable' \
//	  go test -tags integration ./internal/product/... -count=1 -run Window
package product

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
)

// prPlantWindowProduct plants one product row with no unit set, base and
// price EA, and answers its id.
func (e *productLoadEnv) prPlantWindowProduct(t *testing.T, sku string) string {
	t.Helper()
	var id string
	if err := e.pool.QueryRow(context.Background(),
		`INSERT INTO products (sku, name, base_uom, price_uom, org_id)
		 VALUES ($1, 'Fixture Window Product', 'EA', 'EA', $2::uuid) RETURNING id::text`,
		sku, fxProdOrgID).Scan(&id); err != nil {
		t.Fatalf("plant window product %s: %v", sku, err)
	}
	return id
}

// prPlantWindowNames plants the registry names both doors resolve: the
// product's own name for the reload, and the unit set's name for the
// replace, both against the product row, with a recorded hash no
// submission carries so both doors run their writers.
func (e *productLoadEnv) prPlantWindowNames(t *testing.T, productLegacy, setLegacy, productID string) {
	t.Helper()
	for _, n := range []struct{ kind, legacy string }{
		{load.KindProduct, productLegacy},
		{load.KindUnitSet, setLegacy},
	} {
		if _, err := e.pool.Exec(context.Background(), `
			INSERT INTO load_key_registry (org_id, source_system, kind, legacy_id, hh_id, content_hash)
			VALUES ($1::uuid, $2, $3, $4, $5::uuid, repeat('a', 64))`,
			fxProdOrgID, fxProdSource, n.kind, n.legacy, productID); err != nil {
			t.Fatalf("plant window name %s: %v", n.legacy, err)
		}
	}
}

// prWindowSetWrite is the set replace's direct writer input: the rows of
// the new set, named by the product reference the frame resolves.
func prWindowSetWrite(setLegacy, productLegacy, productID string, rows ...map[string]any) load.Write {
	return load.Write{
		OrgID:      fxProdOrgID,
		LegacyID:   setLegacy,
		DerivedKey: "a8000000-0000-4000-8000-00000000a101",
		Record:     map[string]any{"rows": rows},
		Refs:       []load.RefMatch{{Kind: "product", LegacyID: productLegacy, HHID: productID}},
	}
}

// prWindowReloadWrite is the reload's direct writer input: the update
// arm (the row id named), base and price moved to the box.
func prWindowReloadWrite(sku, productLegacy, productID, name string) load.Write {
	return load.Write{
		OrgID:      fxProdOrgID,
		LegacyID:   productLegacy,
		RowID:      productID,
		DerivedKey: "f6000000-0000-4000-8000-00000000a201",
		Record: map[string]any{
			"sku":       sku,
			"name":      name,
			"base_uom":  "FXPRU-BOX",
			"price_uom": "FXPRU-BOX",
		},
	}
}

// prWindowBaseAndSet reads the row's base unit and the set's base row,
// so a final-state assertion can name the broken shape directly.
func (e *productLoadEnv) prWindowBaseAndSet(t *testing.T, productID string) (baseUOM, setBase string, setRows int) {
	t.Helper()
	err := e.pool.QueryRow(context.Background(), `
		SELECT p.base_uom::text,
		       coalesce((SELECT u.uom_code::text FROM product_uoms u
		                  WHERE u.product_id = p.id AND u.is_base LIMIT 1), ''),
		       (SELECT count(*) FROM product_uoms u WHERE u.product_id = p.id)
		  FROM products p WHERE p.id = $1::uuid`, productID).
		Scan(&baseUOM, &setBase, &setRows)
	if err != nil {
		t.Fatalf("window state read: %v", err)
	}
	return baseUOM, setBase, setRows
}

// TestProductLoadReloadWaitsForTheOpenSetReplace is order (a), the
// set replace first: transaction one runs the set writer on a raw
// pool.Begin (insert the EA base and the box) and stands open and
// uncommitted; transaction two runs the product writer's reload, base
// and price moved to the box, on another raw begin. The reload must not
// answer while the replace stands (the red line), must be provably
// parked on the row lock, and after the replace commits must refuse as
// unit_set_mismatch naming base_uom: the committed set's base row is EA
// and the record moves the base off it. The row is untouched.
func TestProductLoadReloadWaitsForTheOpenSetReplace(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	e.prSeedUnit(t, "FXPRU-BOX", "Fixture Box", "count")
	pid := e.prPlantWindowProduct(t, "FXPR-0902")
	e.prPlantWindowNames(t, "PR-W1", "US-W1", pid)

	// Transaction one: the set replace, open and uncommitted. The
	// product carries no set rows yet, so the replace's removal checks
	// never run and the writer needs no readers; its rows and their
	// foreign key's row lock stand invisible until the commit below.
	tx1, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("set replace begin: %v", err)
	}
	defer func() { _ = tx1.Rollback(ctx) }()
	setWrite := prWindowSetWrite("US-W1", "PR-W1", pid,
		map[string]any{"code": "EA", "qty_in_base": "1", "is_base": true},
		map[string]any{"code": "FXPRU-BOX", "qty_in_base": "10", "is_base": false})
	if _, err := (UnitSetLoadWriter{}).Apply(ctx, tx1, setWrite); err != nil {
		t.Fatalf("the open set replace answered an error %v", err)
	}

	// Transaction two: the reload, started while one stands.
	done := make(chan error, 1)
	go func() {
		tx2, err := e.pool.Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = tx2.Rollback(ctx) }()
		_, _, err = (ProductLoadWriter{}).ApplyAttached(ctx, tx2,
			prWindowReloadWrite("FXPR-0902", "PR-W1", pid, "Fixture Window Product Reloaded"))
		if err != nil {
			done <- err
			return
		}
		// The writer never commits: a landed answer commits the item's
		// transaction, a refused one leaves the rollback standing.
		done <- tx2.Commit(ctx)
	}()
	// The red line: an answer here means the reload read the set while
	// the replace stood uncommitted, the race the review's probe showed.
	select {
	case err := <-done:
		t.Fatalf("the reload answered while the set replace stood open (err %v): the two doors race on the product row", err)
	case <-time.After(300 * time.Millisecond):
	}
	waitForLockWait(t, e.pool)
	if err := tx1.Commit(ctx); err != nil {
		t.Fatalf("set replace commit: %v", err)
	}
	var reloadErr error
	select {
	case reloadErr = <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("the reload never answered after the replace committed")
	}
	var refused *load.Refusal
	if !errors.As(reloadErr, &refused) || refused.Code != "unit_set_mismatch" || refused.Field != "base_uom" {
		t.Fatalf("the reload after the committed set answered %v, want refused as unit_set_mismatch on base_uom", reloadErr)
	}
	baseUOM, setBase, setRows := e.prWindowBaseAndSet(t, pid)
	if baseUOM != "EA" || setBase != "EA" || setRows != 2 {
		t.Fatalf("after the refusal the row carries base %s over a set whose base row is %s (%d rows), want the row untouched with the set's base row EA", baseUOM, setBase, setRows)
	}
}

// TestProductLoadSetReplaceWaitsForTheOpenReload is order (b), the
// reload first: transaction one runs the product writer's reload on a
// raw begin (base and price moved to the box, the guard passing because
// no set stands yet) and stays open and uncommitted; transaction two
// runs the set writer. The replace must not answer while the reload
// stands, must be provably parked, and after the reload commits must
// check against the committed product: a set naming the old base EA
// refuses as unit_set_base naming base_uom with nothing standing, and a
// set naming the committed base the box lands, leaving the base unit
// over its own set's base row.
func TestProductLoadSetReplaceWaitsForTheOpenReload(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	e.prSeedUnit(t, "FXPRU-BOX", "Fixture Box", "count")

	runOne := func(t *testing.T, sku, productLegacy, setLegacy string, wantRefusal bool, rows ...map[string]any) {
		t.Helper()
		pid := e.prPlantWindowProduct(t, sku)
		e.prPlantWindowNames(t, productLegacy, setLegacy, pid)
		// Transaction one: the reload, open and uncommitted. The writer
		// answers (no set stands, the guard passes) and the test holds
		// its transaction: the base move is locked and invisible.
		tx1, err := e.pool.Begin(ctx)
		if err != nil {
			t.Fatalf("reload begin: %v", err)
		}
		defer func() { _ = tx1.Rollback(ctx) }()
		if _, _, err := (ProductLoadWriter{}).ApplyAttached(ctx, tx1,
			prWindowReloadWrite(sku, productLegacy, pid, "Fixture Window Product Reloaded")); err != nil {
			t.Fatalf("the open reload answered an error %v", err)
		}
		// Transaction two: the set replace, started while one stands.
		done := make(chan error, 1)
		go func() {
			tx2, err := e.pool.Begin(ctx)
			if err != nil {
				done <- err
				return
			}
			defer func() { _ = tx2.Rollback(ctx) }()
			_, err = (UnitSetLoadWriter{}).Apply(ctx, tx2,
				prWindowSetWrite(setLegacy, productLegacy, pid, rows...))
			if err != nil {
				done <- err
				return
			}
			// The writer never commits: a landed answer commits the
			// item's transaction, a refused one leaves the rollback
			// standing.
			done <- tx2.Commit(ctx)
		}()
		select {
		case err := <-done:
			t.Fatalf("the set replace answered while the reload stood open (err %v): the two doors race on the product row", err)
		case <-time.After(300 * time.Millisecond):
		}
		waitForLockWait(t, e.pool)
		if err := tx1.Commit(ctx); err != nil {
			t.Fatalf("reload commit: %v", err)
		}
		var setErr error
		select {
		case setErr = <-done:
		case <-time.After(30 * time.Second):
			t.Fatalf("the set replace never answered after the reload committed")
		}
		baseUOM, setBase, setRows := e.prWindowBaseAndSet(t, pid)
		if wantRefusal {
			// The set names the old base EA over the committed base the
			// box: refused, nothing of the replace stands.
			var refused *load.Refusal
			if !errors.As(setErr, &refused) || refused.Code != "unit_set_base" || refused.Field != "base_uom" {
				t.Fatalf("the replace after the committed reload answered %v, want refused as unit_set_base on base_uom", setErr)
			}
			if setBase != "" || setRows != 0 {
				t.Fatalf("the refused replace left %d set rows (base row %s), want none standing", setRows, setBase)
			}
			return
		}
		// The set names the committed base the box: it lands, and the
		// base unit sits over its own set's base row.
		if setErr != nil {
			t.Fatalf("the replace naming the committed base answered %v, want it landed", setErr)
		}
		if baseUOM != "FXPRU-BOX" || setBase != "FXPRU-BOX" || setRows != 2 {
			t.Fatalf("after the landing the row carries base %s over a set whose base row is %s (%d rows), want the box over the box", baseUOM, setBase, setRows)
		}
	}
	// The refusal arm: the committed base is the box, the set names EA.
	runOne(t, "FXPR-0903", "PR-W2", "US-W2", true,
		map[string]any{"code": "EA", "qty_in_base": "1", "is_base": true},
		map[string]any{"code": "FXPRU-BOX", "qty_in_base": "10", "is_base": false})
	// The consistent arm: the set names the committed base the box.
	runOne(t, "FXPR-0904", "PR-W3", "US-W3", false,
		map[string]any{"code": "FXPRU-BOX", "qty_in_base": "1", "is_base": true},
		map[string]any{"code": "EA", "qty_in_base": "10", "is_base": false})
}

// TestProductLoadAndSetBatchesInOppositeOrdersStayConsistent runs the
// two doors as two real concurrent batch submissions over two products
// in opposite item orders, the deadlock shape the row locks can form
// (the set submission holds the first product's share lock while the
// product submission holds the second's update lock, and each wants the
// other's). Whatever Postgres rules: each batch either finishes or
// answers the ErrBatchContended sentinel, a contended batch leaves
// nothing standing (no batch row, no moved name, no set rows), and the
// resubmission converges to the one consistent end state, each product's
// base unit over its own set's base row.
func TestProductLoadAndSetBatchesInOppositeOrdersStayConsistent(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	e.prSeedUnit(t, "FXPRU-BOX", "Fixture Box", "count")
	p1 := e.prPlantWindowProduct(t, "FXPR-0905")
	p2 := e.prPlantWindowProduct(t, "FXPR-0906")
	e.prPlantWindowNames(t, "PR-W1", "US-W1", p1)
	e.prPlantWindowNames(t, "PR-W2", "US-W2", p2)
	uses := &fakeUnitUseReader{}
	puses := &fakePricingUseReader{}
	setDoor := load.NewDoor(load.NewStore(e.pool), load.KindUnitSet,
		UnitSetLoadWriter{Pricing: puses, Stock: uses, Vendor: uses}, &loadAuditSpy{})

	setRows := func() []load.Item {
		return []load.Item{
			{LegacyID: "US-W1", DerivedKey: "d3000000-0000-4000-8000-00000000ad01",
				Record: map[string]any{"rows": []map[string]any{
					{"code": "EA", "qty_in_base": "1", "is_base": true},
					{"code": "FXPRU-BOX", "qty_in_base": "10", "is_base": false}}},
				Requires: []load.Ref{{Kind: "product", LegacyID: "PR-W1"}}},
			{LegacyID: "US-W2", DerivedKey: "d3000000-0000-4000-8000-00000000ad02",
				Record: map[string]any{"rows": []map[string]any{
					{"code": "EA", "qty_in_base": "1", "is_base": true},
					{"code": "FXPRU-BOX", "qty_in_base": "10", "is_base": false}}},
				Requires: []load.Ref{{Kind: "product", LegacyID: "PR-W2"}}},
		}
	}
	productRows := func() []load.Item {
		return []load.Item{
			{LegacyID: "PR-W2", DerivedKey: "f6000000-0000-4000-8000-00000000ae01",
				Record: map[string]any{"sku": "FXPR-0906", "name": "Fixture Window Product Reloaded",
					"base_uom": "EA", "price_uom": "EA"}},
			{LegacyID: "PR-W1", DerivedKey: "f6000000-0000-4000-8000-00000000ae02",
				Record: map[string]any{"sku": "FXPR-0905", "name": "Fixture Window Product Reloaded",
					"base_uom": "EA", "price_uom": "EA"}},
		}
	}
	runBatch := func(door *load.Door, batchID string, items []load.Item) (load.BatchResult, error) {
		return door.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
			BatchID: batchID, Caller: fxProdActorID, Items: items})
	}

	setRes := make(chan load.BatchResult, 1)
	setErrs := make(chan error, 1)
	go func() {
		r, err := runBatch(setDoor, "b-us-x", setRows())
		setRes <- r
		setErrs <- err
	}()
	prRes, prErr := runBatch(e.door, "b-pr-x", productRows())
	setResult, setErr := <-setRes, <-setErrs

	// Each batch either finished or answered the sentinel; anything else
	// is a shape the frame does not own. Whether Postgres rules a
	// deadlock at all is the interleaving's own decision; both answers
	// are recorded so a verbose run shows which one happened.
	if prErr != nil && !errors.Is(prErr, load.ErrBatchContended) {
		t.Fatalf("the product batch answered %v, want it finished or the ErrBatchContended sentinel", prErr)
	}
	if setErr != nil && !errors.Is(setErr, load.ErrBatchContended) {
		t.Fatalf("the set batch answered %v, want it finished or the ErrBatchContended sentinel", setErr)
	}
	t.Logf("the races answered: product batch %v, set batch %v", prErr, setErr)
	// A contended batch leaves nothing standing.
	if prErr != nil {
		var batches int
		var name, hash string
		if err := e.pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-pr-x'),
			       (SELECT name FROM products WHERE id = $2::uuid),
			       (SELECT coalesce(content_hash, '') FROM load_key_registry
			         WHERE org_id = $1::uuid AND source_system = $3 AND kind = $4 AND legacy_id = 'PR-W2')`,
			fxProdOrgID, p2, fxProdSource, load.KindProduct).Scan(&batches, &name, &hash); err != nil {
			t.Fatalf("product victim remainder read: %v", err)
		}
		if batches != 0 || name != "Fixture Window Product" || hash != repeatA64 {
			t.Fatalf("the contended product batch left %d batch rows, name %q, hash %q: a contended batch rolls back whole", batches, name, hash)
		}
	}
	if setErr != nil {
		var batches, setRowCount int
		var hash string
		if err := e.pool.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-us-x'),
			       (SELECT count(*) FROM product_uoms WHERE product_id = $2::uuid),
			       (SELECT coalesce(content_hash, '') FROM load_key_registry
			         WHERE org_id = $1::uuid AND source_system = $3 AND kind = $4 AND legacy_id = 'US-W1')`,
			fxProdOrgID, p1, fxProdSource, load.KindUnitSet).Scan(&batches, &setRowCount, &hash); err != nil {
			t.Fatalf("set victim remainder read: %v", err)
		}
		if batches != 0 || setRowCount != 0 || hash != repeatA64 {
			t.Fatalf("the contended set batch left %d batch rows, %d set rows, hash %q: a contended batch rolls back whole", batches, setRowCount, hash)
		}
	}
	// The resubmission converges: any loser runs again after the winner
	// committed, and both batches end finished with no refused item.
	if prErr != nil {
		r, err := runBatch(e.door, "b-pr-x", productRows())
		if err != nil {
			t.Fatalf("product batch resubmission: %v", err)
		}
		prRes = r
	}
	if setErr != nil {
		r, err := runBatch(setDoor, "b-us-x", setRows())
		if err != nil {
			t.Fatalf("set batch resubmission: %v", err)
		}
		setResult = r
	}
	for _, o := range prRes.Items {
		if o.Status == load.OutcomeRefused {
			t.Fatalf("the product batch refused %+v", o)
		}
	}
	for _, o := range setResult.Items {
		if o.Status == load.OutcomeRefused {
			t.Fatalf("the set batch refused %+v", o)
		}
	}
	// The one consistent end state: each product's base unit over its own
	// set's base row, both names reloaded.
	for _, pid := range []string{p1, p2} {
		baseUOM, setBase, setRowCount := e.prWindowBaseAndSet(t, pid)
		if baseUOM != "EA" || setBase != "EA" || setRowCount != 2 {
			t.Fatalf("the end state carries base %s over a set whose base row is %s (%d rows), want EA over EA with two rows", baseUOM, setBase, setRowCount)
		}
		var name string
		if err := e.pool.QueryRow(ctx,
			`SELECT name FROM products WHERE id = $1::uuid`, pid).Scan(&name); err != nil {
			t.Fatalf("end state name read: %v", err)
		}
		if name != "Fixture Window Product Reloaded" {
			t.Fatalf("the end state's name is %q, want the reload's", name)
		}
	}
}

// repeatA64 is the planted hash no submission carries; a contended
// batch's names must still carry it, the write having rolled back.
const repeatA64 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// TestProductLoadSetBatchDeadlockOnTheShareLockAnswersTheSentinel is the
// new lock shape's own deterministic deadlock leg: an outside
// transaction holds the second product's row FOR UPDATE (the hold a
// product door's guard takes) while the set batch's first item has
// landed its share lock on the first product's row; the batch's second
// item parks on the outside's hold, the outside then wants the row the
// batch holds FOR SHARE, the cycle closes, and Postgres kills the batch.
// The batch answers the ErrBatchContended sentinel with nothing of it
// standing, and the resubmission converges.
func TestProductLoadSetBatchDeadlockOnTheShareLockAnswersTheSentinel(t *testing.T) {
	e := productLoadSetup(t)
	ctx := context.Background()
	e.prSeedUnit(t, "EA", "Each", "count")
	e.prSeedUnit(t, "FXPRU-BOX", "Fixture Box", "count")
	p1 := e.prPlantWindowProduct(t, "FXPR-0907")
	p2 := e.prPlantWindowProduct(t, "FXPR-0908")
	e.prPlantWindowNames(t, "PR-W1", "US-W1", p1)
	e.prPlantWindowNames(t, "PR-W2", "US-W2", p2)
	uses := &fakeUnitUseReader{}
	puses := &fakePricingUseReader{}
	setDoor := load.NewDoor(load.NewStore(e.pool), load.KindUnitSet,
		UnitSetLoadWriter{Pricing: puses, Stock: uses, Vendor: uses}, &loadAuditSpy{})
	setRows := []load.Item{
		{LegacyID: "US-W1", DerivedKey: "d3000000-0000-4000-8000-00000000af01",
			Record: map[string]any{"rows": []map[string]any{
				{"code": "EA", "qty_in_base": "1", "is_base": true},
				{"code": "FXPRU-BOX", "qty_in_base": "10", "is_base": false}}},
			Requires: []load.Ref{{Kind: "product", LegacyID: "PR-W1"}}},
		{LegacyID: "US-W2", DerivedKey: "d3000000-0000-4000-8000-00000000af02",
			Record: map[string]any{"rows": []map[string]any{
				{"code": "EA", "qty_in_base": "1", "is_base": true},
				{"code": "FXPRU-BOX", "qty_in_base": "10", "is_base": false}}},
			Requires: []load.Ref{{Kind: "product", LegacyID: "PR-W2"}}},
	}
	outside, err := e.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("outside begin: %v", err)
	}
	defer func() { _ = outside.Rollback(ctx) }()
	// A long deadlock timeout on the outside session, so the batch's own
	// detection (the default, a second) fires first and the batch is the
	// victim.
	if _, err := outside.Exec(ctx, `SET LOCAL deadlock_timeout = '30s'`); err != nil {
		t.Fatalf("outside timeout: %v", err)
	}
	if _, err := outside.Exec(ctx, `SELECT id::text FROM products WHERE id = $1::uuid FOR UPDATE`, p2); err != nil {
		t.Fatalf("outside hold: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		_, err := setDoor.UpsertBatch(cctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
			BatchID: "b-us-y", Caller: fxProdActorID, Items: setRows})
		done <- err
	}()
	waitForLockWait(t, e.pool)
	// The outside now wants the first product's row the batch holds FOR
	// SHARE: the cycle closes and Postgres kills the batch.
	if _, err := outside.Exec(ctx, `SELECT id::text FROM products WHERE id = $1::uuid FOR UPDATE`, p1); err != nil {
		t.Fatalf("outside cycle: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, load.ErrBatchContended) {
			t.Fatalf("the deadlocked set batch answered %v, want the ErrBatchContended sentinel", err)
		}
	case <-time.After(60 * time.Second):
		t.Fatalf("the deadlock never resolved")
	}
	_ = outside.Rollback(ctx)
	// Nothing of the victim stands: no batch row, no set rows, the
	// planted hashes unmoved.
	var batches, setRowCount int
	var hash1 string
	if err := e.pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM load_batch WHERE org_id = $1::uuid AND batch_id = 'b-us-y'),
		       (SELECT count(*) FROM product_uoms WHERE product_id = ANY($2::uuid[])),
		       (SELECT coalesce(content_hash, '') FROM load_key_registry
		         WHERE org_id = $1::uuid AND source_system = $3 AND kind = $4 AND legacy_id = 'US-W1')`,
		fxProdOrgID, []string{p1, p2}, fxProdSource, load.KindUnitSet).Scan(&batches, &setRowCount, &hash1); err != nil {
		t.Fatalf("victim remainder read: %v", err)
	}
	if batches != 0 || setRowCount != 0 || hash1 != repeatA64 {
		t.Fatalf("the victim left %d batch rows, %d set rows, hash %q: a contended batch rolls back whole", batches, setRowCount, hash1)
	}
	// The resubmission converges: the outside is gone, so both items
	// land, each base over its own set's base row.
	r, err := setDoor.UpsertBatch(ctx, load.Batch{OrgID: fxProdOrgID, SourceSystem: fxProdSource,
		BatchID: "b-us-y", Caller: fxProdActorID, Items: setRows})
	if err != nil {
		t.Fatalf("set batch resubmission: %v", err)
	}
	for _, o := range r.Items {
		if o.Status != load.OutcomeUpdated {
			t.Fatalf("the resubmission answered %+v, want updated", o)
		}
	}
	for _, pid := range []string{p1, p2} {
		baseUOM, setBase, setRowCount := e.prWindowBaseAndSet(t, pid)
		if baseUOM != "EA" || setBase != "EA" || setRowCount != 2 {
			t.Fatalf("after the resubmission the row carries base %s over a set whose base row is %s (%d rows), want EA over EA with two rows", baseUOM, setBase, setRowCount)
		}
	}
}
