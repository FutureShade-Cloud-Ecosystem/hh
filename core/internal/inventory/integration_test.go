//go:build integration

// Integration tests run the real pgx Repository + Service against the private W6 inventory DB (never the
// shared hardscapeos). Provision + run:
//
//	psql -h localhost -p 5435 -U postgres -c "DROP DATABASE IF EXISTS hos_w6_inv WITH (FORCE);" \
//	  -c "CREATE DATABASE hos_w6_inv OWNER hardscape;"
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hos_w6_inv?sslmode=disable' \
//	  bash -c 'cd backend && go run ./cmd/migrate && go run ./cmd/seed'
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5435/hos_w6_inv?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/... -count=1 -p 1
//
// Covers the INVCORE ACs that need Postgres: GRNI (idempotent + weighted-avg), COLLECT_NOW SHIP + real
// CostBasis, DELIVERED zero-move boundary, USD fx→CAD, over-receipt 409, negative-stock surface, drift-empty
// (moves ⋈ balances tie), the pending-fulfilment gap, and the inventory.adjusted rule-resolved 1200 leg.
package inventory

import (
	"context"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const branch1 = "5d1b3f6e-0000-4000-9000-000000000001"

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// glBackedPoster + glBackedRules wire inventory's GL seams over a real *gl.Service (PostExternal + ResolveRule).
type glBackedPoster struct{ svc *gl.Service }

func (a glBackedPoster) PostJournal(ctx context.Context, in GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternal(ctx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

func (a glBackedPoster) PostJournalTx(ctx context.Context, tx pgx.Tx, in GLJournal) (string, error) {
	lines := make([]gl.ExternalLine, len(in.Lines))
	for i, l := range in.Lines {
		lines[i] = gl.ExternalLine{AccountCode: l.AccountCode, DebitCents: l.DebitCents, CreditCents: l.CreditCents, Memo: l.Memo}
	}
	return a.svc.PostExternalTx(ctx, tx, gl.ExternalJournal{
		Source: in.Source, SourceRefID: in.SourceRefID, IdempotencyKey: in.IdempotencyKey,
		Memo: in.Memo, BranchID: in.BranchID, Lines: lines,
	})
}

type glBackedRules struct{ svc *gl.Service }

func (a glBackedRules) ResolveRule(ctx context.Context, eventType, lineRole, branchID string) (string, error) {
	return a.svc.ResolveRule(ctx, eventType, lineRole, branchID)
}

func (a glBackedRules) ResolveRuleTx(ctx context.Context, tx pgx.Tx, eventType, lineRole, branchID string) (string, error) {
	return a.svc.ResolveRuleTx(ctx, tx, eventType, lineRole, branchID)
}

func newIntSvc(pool *pgxpool.Pool) *Service {
	glSvc := gl.NewService(gl.NewRepository(pool))
	return NewService(NewRepository(pool)).
		WithGLPoster(glBackedPoster{svc: glSvc}).
		WithRuleResolver(glBackedRules{svc: glSvc})
}

// - fixtures -

func mkProduct(t *testing.T, pool *pgxpool.Pool, sku string) string {
	t.Helper()
	// The SKU is unique per run so parallel/repeat runs never collide. The stock ledger references products
	// ON DELETE RESTRICT, so the product row itself lingers, harmless (nothing asserts on stray products). But
	// the stock_balances / stock_moves this product accumulates are NOT harmless on the SHARED integration DB:
	// TestCollectNowShipCOGSAndNegativeStock deliberately drives on-hand to −2, and a later cross-cutting gate
	// (TestGate_StockReconciles_NoNegative) reads every branch's balances. Clean the ledger rows for this
	// product at teardown so one test's intentional-negative fixture never leaks into another's invariant
	// (count_lines.move_id is ON DELETE SET NULL, so dropping the moves is safe).
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO products (sku, name, base_uom, price_uom, weight_per_unit_kg)
		VALUES ($1 || '-' || substr(md5(random()::text),1,8), $1, 'EA', 'EA', 2.0) RETURNING id::text`, sku).Scan(&id)
	if err != nil {
		t.Fatalf("mk product: %v", err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, `DELETE FROM stock_balances WHERE product_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM stock_moves WHERE product_id = $1`, id)
		_, _ = pool.Exec(ctx, `DELETE FROM product_branch_costs WHERE product_id = $1`, id)
	})
	return id
}

type poLineSpec struct {
	productID string
	qtyBase   float64
	unitCost  int64
}

func mkPO(t *testing.T, pool *pgxpool.Pool, poNo, currency string, fx float64, specs []poLineSpec) (string, []string) {
	t.Helper()
	ctx := context.Background()
	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE vendor_no='V-TECHO'`).Scan(&vendorID); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	var poID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO purchase_orders (po_no, vendor_id, branch_id, status, currency, fx_rate)
		VALUES ($1 || '-' || substr(md5(random()::text),1,8), $2, $3, 'SUBMITTED', $4, $5) RETURNING id::text`,
		poNo, vendorID, branch1, currency, fx).Scan(&poID); err != nil {
		t.Fatalf("po: %v", err)
	}
	var lineIDs []string
	for i, s := range specs {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO po_lines (po_id, line_no, product_id, qty_entered, uom_entered, qty_base, unit_cost_cents, cost_uom)
			VALUES ($1, $2, $3, $4, 'EA', $4, $5, 'EA') RETURNING id::text`,
			poID, i+1, s.productID, s.qtyBase, s.unitCost).Scan(&id); err != nil {
			t.Fatalf("po line: %v", err)
		}
		lineIDs = append(lineIDs, id)
	}
	return poID, lineIDs
}

func receiveAll(t *testing.T, svc *Service, pool *pgxpool.Pool, poID string) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if err := svc.ReceivePOTx(ctx, tx, poID); err != nil {
		t.Fatalf("ReceivePOTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func branchCost(t *testing.T, pool *pgxpool.Pool, productID string) int64 {
	t.Helper()
	var c int64
	if err := pool.QueryRow(context.Background(),
		`SELECT avg_unit_cost_cents FROM product_branch_costs WHERE product_id=$1 AND branch_id=$2`,
		productID, branch1).Scan(&c); err != nil {
		t.Fatalf("branch cost: %v", err)
	}
	return c
}

func onHand(t *testing.T, pool *pgxpool.Pool, productID string) float64 {
	t.Helper()
	var q float64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(on_hand_base),0) FROM stock_balances WHERE product_id=$1 AND branch_id=$2`,
		productID, branch1).Scan(&q); err != nil {
		t.Fatalf("on-hand: %v", err)
	}
	return q
}

// lastPOReceivedEvent returns the payload of the most recent po.received outbox row for a PO.
func lastPOReceivedEvent(t *testing.T, pool *pgxpool.Pool, poID string) []byte {
	t.Helper()
	var payload []byte
	err := pool.QueryRow(context.Background(), `
		SELECT payload FROM events_outbox WHERE topic='po.received'
		AND payload->>'po_id' = $1 ORDER BY created_at DESC LIMIT 1`, poID).Scan(&payload)
	if err != nil {
		t.Fatalf("no po.received event for %s: %v", poID, err)
	}
	return payload
}

// - tests -

// AC: GRNI idempotent + weighted-avg-cost. Two receipts blend to $11; the GRNI journal posts once per receipt
// and a redelivered po.received event does not double-post.
func TestGRNIIdempotentAndWeightedAvg(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "INVCORE-AVG-1")

	po1, _ := mkPO(t, pool, "INVCORE-PO-A1", "CAD", 1.0, []poLineSpec{{p, 100, 1000}})
	receiveAll(t, svc, pool, po1)
	if got := branchCost(t, pool, p); got != 1000 {
		t.Fatalf("after receipt 1 want avg 1000, got %d", got)
	}
	if got := onHand(t, pool, p); got != 100 {
		t.Fatalf("want on-hand 100, got %v", got)
	}

	po2, _ := mkPO(t, pool, "INVCORE-PO-A2", "CAD", 1.0, []poLineSpec{{p, 100, 1200}})
	receiveAll(t, svc, pool, po2)
	if got := branchCost(t, pool, p); got != 1100 {
		t.Fatalf("after receipt 2 want blended avg 1100, got %d", got)
	}

	// GRNI: post the first receipt's event, twice. Idempotent → one journal for that receipt.
	payload := lastPOReceivedEvent(t, pool, po1)
	if err := svc.postEvent(ctx, notify.Event{Topic: "po.received", Payload: payload}); err != nil {
		t.Fatalf("postEvent 1: %v", err)
	}
	if err := svc.postEvent(ctx, notify.Event{Topic: "po.received", Payload: payload}); err != nil {
		t.Fatalf("postEvent 2 (idempotent): %v", err)
	}
	var receiptID string
	_ = pool.QueryRow(ctx, `SELECT payload->>'receipt_id' FROM events_outbox WHERE topic='po.received' AND payload->>'po_id'=$1 ORDER BY created_at LIMIT 1`, po1).Scan(&receiptID)
	var journals int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key=$1`, "po.received:"+receiptID).Scan(&journals); err != nil {
		t.Fatalf("count journals: %v", err)
	}
	if journals != 1 {
		t.Fatalf("GRNI not idempotent: want 1 journal, got %d", journals)
	}
	// The GRNI legs are DR 1200 / CR 2150 for 100000 CAD (100 × $10).
	assertJournalLeg(t, pool, "po.received:"+receiptID, "1200", 100000, 0)
	assertJournalLeg(t, pool, "po.received:"+receiptID, "2150", 0, 100000)
}

// AC: USD PO cost converts to CAD before it touches the ledger + GRNI (DX-19 scoped exception).
func TestUSDReceiptConvertsToCAD(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	p := mkProduct(t, pool, "INVCORE-USD-1")
	po, _ := mkPO(t, pool, "INVCORE-PO-USD", "USD", 1.35, []poLineSpec{{p, 10, 1000}})
	receiveAll(t, svc, pool, po)

	if got := branchCost(t, pool, p); got != 1350 {
		t.Fatalf("want CAD avg 1350, got %d", got)
	}
	var value int64
	if err := pool.QueryRow(context.Background(),
		`SELECT value_cents FROM stock_moves WHERE product_id=$1 AND move_type='RECEIPT'`, p).Scan(&value); err != nil {
		t.Fatalf("move value: %v", err)
	}
	if value != 13500 { // 10 × 1000 × 1.35
		t.Fatalf("want CAD move value 13500, got %d", value)
	}
}

// AC: over-receipt 409, receiving more than a line's outstanding qty is a conflict.
func TestOverReceiptConflict(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "INVCORE-OVER-1")
	po, lines := mkPO(t, pool, "INVCORE-PO-OVER", "CAD", 1.0, []poLineSpec{{p, 50, 1000}})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	_, err = svc.applyReceipt(ctx, tx, po, map[string]float64{lines[0]: 80}, "")
	var ce clientError
	if err == nil || !asClientErr(err, &ce) || ce.status != 409 {
		t.Fatalf("want 409 over-receipt, got %v", err)
	}
}

// AC: COLLECT_NOW → SHIP move + real CostBasis; the sale drives on-hand negative (the detection surface); the
// on-hand read model reports the negative available; drift-empty (moves net == balance).
func TestCollectNowShipCOGSAndNegativeStock(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "INVCORE-SELL-1")
	po, _ := mkPO(t, pool, "INVCORE-PO-SELL", "CAD", 1.0, []poLineSpec{{p, 3, 900}})
	receiveAll(t, svc, pool, po)

	// Real CostBasis: extended weighted-avg for the fulfilled qty.
	cost, found, err := svc.UnitCostCents(ctx, p, 3)
	if err != nil || !found || cost != 2700 {
		t.Fatalf("CostBasis want (2700,true), got (%d,%v,%v)", cost, found, err)
	}

	// Sell 5 (only 3 on hand) COLLECT_NOW → one SHIP move, on-hand −2.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := svc.DecrementForSaleTx(ctx, tx, DecrementInput{
		OrderID: newOrderID(t, pool, "DELIVERY"), BranchID: branch1, SaleType: SaleCollectNow,
		Lines: []SaleLine{{ProductID: p, QtyBase: 5}},
	}); err != nil {
		t.Fatalf("decrement: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if got := onHand(t, pool, p); got != -2 {
		t.Fatalf("want negative on-hand −2, got %v", got)
	}
	var available float64
	if err := pool.QueryRow(ctx,
		`SELECT available_base FROM v_stock_on_hand WHERE product_id=$1 AND branch_id=$2`, p, branch1).Scan(&available); err != nil {
		t.Fatalf("read model: %v", err)
	}
	if available != -2 {
		t.Fatalf("read model want available −2, got %v", available)
	}
	// drift-empty: SUM(IN − OUT) over the move ledger equals the projected balance.
	var netMoves float64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN to_location_id IS NOT NULL THEN qty_base ELSE 0 END)
		     - SUM(CASE WHEN from_location_id IS NOT NULL THEN qty_base ELSE 0 END), 0)
		FROM stock_moves WHERE product_id=$1`, p).Scan(&netMoves); err != nil {
		t.Fatalf("net moves: %v", err)
	}
	if netMoves != onHand(t, pool, p) {
		t.Fatalf("drift: ledger net %v != balance %v", netMoves, onHand(t, pool, p))
	}
}

// AC: DELIVERED boundary, a deferred-fulfilment sale books ZERO moves (DX-01 PARTIAL).
func TestDeliveredBoundaryZeroMoves(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "INVCORE-DELIV-1")
	po, _ := mkPO(t, pool, "INVCORE-PO-DELIV", "CAD", 1.0, []poLineSpec{{p, 10, 900}})
	receiveAll(t, svc, pool, po)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := svc.DecrementForSaleTx(ctx, tx, DecrementInput{
		OrderID: newOrderID(t, pool, "DELIVERY"), BranchID: branch1, SaleType: SaleDelivered,
		Lines: []SaleLine{{ProductID: p, QtyBase: 5}},
	}); err != nil {
		t.Fatalf("decrement: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	var shipMoves int
	if err := pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM stock_moves WHERE product_id=$1 AND move_type='SHIP'`, p).Scan(&shipMoves); err != nil {
		t.Fatalf("count: %v", err)
	}
	if shipMoves != 0 {
		t.Fatalf("DELIVERED must book zero SHIP moves, got %d", shipMoves)
	}
	if got := onHand(t, pool, p); got != 10 {
		t.Fatalf("on-hand must be untouched at 10, got %v", got)
	}
}

// AC: the pending-fulfilment surface quantifies the DX-01 gap (the seeded DELIVERY order appears).
func TestPendingFulfilmentSurface(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	rows, err := svc.PendingFulfilments(context.Background(), branch1)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	// The seed carries SO-2026-0001, a CONFIRMED DELIVERY order at branch 1.
	found := false
	for _, r := range rows {
		if r.SaleType == "DELIVERED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want at least one DELIVERED pending line, got %+v", rows)
	}
}

// AC: inventory.adjusted posts via ResolveRule, the 1200 inventory leg is rule-resolved against real GL rules.
func TestInventoryAdjustedRuleResolved1200Leg(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	// Loss of $25 → DR 5200 (rule default) / CR 1200 (rule-resolved). source_ref_id is a UUID (a count line
	// id in production); generate one for isolation.
	var ref string
	if err := pool.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&ref); err != nil {
		t.Fatalf("uuid: %v", err)
	}
	payload := []byte(`{"source_ref_id":"` + ref + `","branch_id":"` + branch1 + `","value_cents":-2500}`)
	if err := svc.postEvent(ctx, notify.Event{Topic: "inventory.adjusted", Payload: payload}); err != nil {
		t.Fatalf("postEvent: %v", err)
	}
	assertJournalLeg(t, pool, "inventory.adjusted:"+ref, "1200", 0, 2500)
	assertJournalLeg(t, pool, "inventory.adjusted:"+ref, "5200", 2500, 0)
}

// - helpers -

func newOrderID(t *testing.T, pool *pgxpool.Pool, fulfillment string) string {
	t.Helper()
	ctx := context.Background()
	var custID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM customers LIMIT 1`).Scan(&custID); err != nil {
		t.Fatalf("customer: %v", err)
	}
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
		VALUES ('INVCORE-ORD-'||substr(md5(random()::text),1,8), $1, $2, 'CONFIRMED', $3) RETURNING id::text`,
		custID, branch1, fulfillment).Scan(&id); err != nil {
		t.Fatalf("order: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM orders WHERE id=$1`, id) })
	return id
}

func assertJournalLeg(t *testing.T, pool *pgxpool.Pool, idemKey, code string, debit, credit int64) {
	t.Helper()
	var d, c int64
	err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(jl.debit_cents,0), COALESCE(jl.credit_cents,0)
		FROM gl_journals j
		JOIN gl_journal_lines jl ON jl.journal_id = j.id
		JOIN gl_accounts a ON a.id = jl.account_id
		WHERE j.idempotency_key = $1 AND a.code = $2`, idemKey, code).Scan(&d, &c)
	if err != nil {
		t.Fatalf("leg %s for %s: %v", code, idemKey, err)
	}
	if d != debit || c != credit {
		t.Fatalf("leg %s: want DR %d/CR %d, got DR %d/CR %d", code, debit, credit, d, c)
	}
}

// TestCapitalizeSnapRestore_RoundTripRestoresWAC_DB is the L8 MAJOR regression against real Postgres: an
// Apply(capitalizeLandedSnap)/Void(restoreLandedCost) round-trip with NO sale between must restore the persisted
// product_branch_costs.avg_unit_cost_cents EXACTLY, including sub-unit fractional on-hand where round(oldAvg ×
// on-hand) quantizes oldAvg away and the legacy reverse-via-negate Void drifted. It seeds a real prior WAC + a
// sub-unit on-hand, captures the snapshot at Apply, restores it at Void, and asserts the DB WAC is byte-identical
// to the pre-Apply value (drift 0). FAILS if Void recomputes the WAC via round() instead of restoring the snapshot.
func TestCapitalizeSnapRestore_RoundTripRestoresWAC_DB(t *testing.T) {
	pool := intPool(t)
	// DIB-396: this test seeds stock_balances DIRECTLY (no matching stock_moves row) to exercise
	// the WAC seam, which drifts v_stock_balance_drift until mkProduct's teardown. Declare the
	// window so the whole-DB reconciliation gates wait it out (see dib396_lease_test.go).
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	ctx := context.Background()

	type state struct {
		onHand  float64
		oldAvg  int64
		freight int64
	}
	for _, c := range []state{
		{0.25, 1, 2700}, // spec repro: WAC 1 → 10800 → 0 under legacy negate (drift -1)
		{0.1, 5, 50},    // spec repro: WAC 5 → 510 → 10 under legacy negate (drift +5)
		{0.05, 9, 4},
		{0.5, 1, 5000},
	} {
		p := mkProduct(t, pool, "LC-SNAP")
		// Seed a sub-unit on-hand and a prior weighted-average directly (no receipt needed for the seam test).
		if _, err := pool.Exec(ctx,
			`INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base) VALUES ($1,$2,$2,$3)`,
			p, branch1, c.onHand); err != nil {
			t.Fatalf("%+v: seed on-hand: %v", c, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents) VALUES ($1,$2,$3)`,
			p, branch1, c.oldAvg); err != nil {
			t.Fatalf("%+v: seed cost: %v", c, err)
		}

		// Apply: capitalize + capture the snapshot (the value landedcost persists on the allocation row).
		absorbed, snapAvg, hadCost, err := svc.capitalizeLandedSnap(ctx, nil, p, branch1, c.freight)
		if err != nil {
			t.Fatalf("%+v: apply: %v", c, err)
		}
		if !hadCost || snapAvg != c.oldAvg {
			t.Fatalf("%+v: snapshot=%d hadCost=%v, want %d/true", c, snapAvg, hadCost, c.oldAvg)
		}

		// Void: remove exactly `absorbed`; on-hand unchanged since Apply → drift-0 verbatim restore of the snapshot.
		reversed, err := svc.restoreLandedCost(ctx, nil, p, branch1, snapAvg, absorbed)
		if err != nil {
			t.Fatalf("%+v: void: %v", c, err)
		}
		if -reversed != absorbed {
			t.Errorf("%+v: reversed %d != absorbed %d (CR1200 != DR1200)", c, -reversed, absorbed)
		}
		if got := branchCost(t, pool, p); got != c.oldAvg {
			t.Errorf("%+v: DB WAC after apply+void = %d, want %d (round-trip drift %d)", c, got, c.oldAvg, got-c.oldAvg)
		}
	}
}

// - DIB-211-D: the A-15 count seam, and the DIB-39 → DIB-40 tie -
//
// count_seam.go is the whole boundary inventory exposes to internal/invcount (A-15): the snapshot a count
// sheet freezes against, the weighted-average a variance is VALUED at, and the signed COUNT adjustment a
// posted variance writes. It had no test of any kind, both sides of the seam were covered (landed cost
// moves the WAC; invcount posts variances and their GL legs) and the seam between them was not.
//
// That seam is exactly where DIB-39 and DIB-40 compose: a landed cost raises the weighted-average, and the
// NEXT count values its variance at that new average, so freight/duty absorbed into inventory reaches the
// GL a second time, through the count adjustment. If WeightedAvgCostCents ever went stale, a count would
// silently value its variance at the pre-landed-cost number and the adjustment would post the wrong dollars.
func TestIntegration_A15CountSeam_ValuesVarianceAtPostLandedCostWAC(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "A15-SEAM")

	// 100 units at $10.00 → WAC 1000.
	po, _ := mkPO(t, pool, "A15-SEAM-PO", "CAD", 1.0, []poLineSpec{{p, 100, 1000}})
	receiveAll(t, svc, pool, po)
	if got := branchCost(t, pool, p); got != 1000 {
		t.Fatalf("setup: want WAC 1000 after receipt, got %d", got)
	}

	// The seam reads what the ledger holds, before any landed cost.
	avg, err := svc.WeightedAvgCostCents(ctx, p, branch1)
	if err != nil {
		t.Fatalf("WeightedAvgCostCents: %v", err)
	}
	if avg != 1000 {
		t.Fatalf("A-15 variance valuation = %d, want the ledger WAC 1000", avg)
	}

	// DIB-39: absorb $50.00 of freight across the 100 units on hand → WAC 1000 + 5000/100 = 1050.
	absorbed, err := svc.capitalizeLanded(ctx, nil, p, branch1, 5000)
	if err != nil {
		t.Fatalf("capitalize landed cost: %v", err)
	}
	if absorbed != 5000 {
		t.Fatalf("landed cost absorbed %d of 5000, the tie below is only meaningful at full absorption", absorbed)
	}

	// DIB-40's valuation must now see the NEW average. This is the tie.
	avgAfter, err := svc.WeightedAvgCostCents(ctx, p, branch1)
	if err != nil {
		t.Fatalf("WeightedAvgCostCents after landed cost: %v", err)
	}
	if avgAfter != 1050 {
		t.Fatalf("A-15 would value a count variance at %d, stale. The landed cost moved the WAC to 1050", avgAfter)
	}

	// The freeze basis A-15 snapshots against reports the same on-hand + average for this product.
	snaps, err := svc.SnapshotOnHand(ctx, branch1)
	if err != nil {
		t.Fatalf("SnapshotOnHand: %v", err)
	}
	var found bool
	for _, s := range snaps {
		if s.ProductID != p {
			continue
		}
		found = true
		if s.OnHandBase != 100 {
			t.Errorf("count-sheet freeze basis on-hand = %v, want 100", s.OnHandBase)
		}
		if s.AvgCostCents != 1050 {
			t.Errorf("count-sheet freeze basis WAC = %d, want the post-landed-cost 1050", s.AvgCostCents)
		}
	}
	if !found {
		t.Fatal("SnapshotOnHand omitted a product with on-hand at the branch, a count sheet would not list it")
	}

	// A shrink of 3 units, valued at the post-landed-cost average, writes ONE signed COUNT move and moves
	// the balance. (The GL leg is invcount's, posted separately via the reason-coded rule.)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	moveID, err := svc.PostCountAdjustmentTx(ctx, tx, p, branch1, branch1, -3, avgAfter, "SHRINK")
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("PostCountAdjustmentTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if moveID == "" {
		t.Fatal("a non-zero variance must write a COUNT move")
	}
	var moveType, fromLoc string
	var qty float64
	var value, unitCost int64
	if err := pool.QueryRow(ctx, `
		SELECT move_type, COALESCE(from_location_id::text,''), qty_base, value_cents, unit_cost_cents
		  FROM stock_moves WHERE id=$1`, moveID).Scan(&moveType, &fromLoc, &qty, &value, &unitCost); err != nil {
		t.Fatalf("read count move: %v", err)
	}
	if moveType != string(MoveCount) {
		t.Errorf("move_type = %q, want %q", moveType, MoveCount)
	}
	if fromLoc == "" {
		t.Error("a NEGATIVE variance must leave the location (from_location_id), not enter it")
	}
	if qty != 3 {
		t.Errorf("qty_base = %v, want the absolute variance 3", qty)
	}
	// The dollars are the whole point of the tie: 3 × 1050 = 3150, not 3 × 1000 = 3000.
	if unitCost != 1050 || value != 3150 {
		t.Errorf("count adjustment valued at unit=%d value=%d, want 1050/3150 (the post-landed-cost WAC)", unitCost, value)
	}
	if got := onHand(t, pool, p); got != 97 {
		t.Errorf("on-hand after a -3 count adjustment = %v, want 97", got)
	}
}

// A zero (within-epsilon) variance must write NOTHING, a count that matches the ledger is not an
// adjustment, and a zero-qty move would still create a GL journal downstream.
func TestIntegration_A15CountSeam_ZeroVarianceWritesNoMove(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "A15-SEAM-ZERO")
	po, _ := mkPO(t, pool, "A15-SEAM-ZERO-PO", "CAD", 1.0, []poLineSpec{{p, 10, 500}})
	receiveAll(t, svc, pool, po)

	before := onHand(t, pool, p)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	moveID, err := svc.PostCountAdjustmentTx(ctx, tx, p, branch1, branch1, 0, 500, "RECOUNT")
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("PostCountAdjustmentTx(0): %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if moveID != "" {
		t.Errorf("a zero variance wrote move %s, a matching count is not an adjustment", moveID)
	}
	if got := onHand(t, pool, p); got != before {
		t.Errorf("on-hand moved on a zero variance: %v -> %v", before, got)
	}
}

// - DIB-404: the bulk per-branch on-hand read behind the catalogue row detail -

// TestIntegration_DIB404_OnHandByBranch runs the real SQL. The unit tests in internal/product prove
// what the catalogue DOES with the map; only Postgres can prove the map is right, the grouping, the
// two-branch split, the id filter, and the zero-drop that keeps a fully-drained product rendering
// "-" instead of "TRT 0 · KGN 0".
func TestIntegration_DIB404_OnHandByBranch(t *testing.T) {
	pool := intPool(t)
	// DIB-396: seeds stock_balances directly (no matching stock_moves), declare the drift window.
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	ctx := context.Background()
	const branch2 = "5d1b3f6e-0000-4000-9000-000000000002"
	repo := NewRepository(pool)

	stocked := mkProduct(t, pool, "D404-STOCKED")
	oneBranch := mkProduct(t, pool, "D404-ONEBRANCH")
	drained := mkProduct(t, pool, "D404-DRAINED")
	untouched := mkProduct(t, pool, "D404-UNTOUCHED")

	// stock_balances is keyed (product_id, location_id), a branch can hold several locations, which
	// is exactly why the read SUMs per branch rather than selecting a row. The demo DB has one
	// location per branch, sharing its id.
	seed := func(productID, branchID string, qty float64) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
			VALUES ($1::uuid, $2::uuid, $2::uuid, $3)
			ON CONFLICT (product_id, location_id) DO UPDATE SET on_hand_base = EXCLUDED.on_hand_base`,
			productID, branchID, qty); err != nil {
			t.Fatalf("seed balance: %v", err)
		}
	}
	seed(stocked, branch1, 200)
	seed(stocked, branch2, 50)
	seed(oneBranch, branch2, 12)
	seed(drained, branch1, 0) // a real row, at zero

	ids := []string{stocked, oneBranch, drained, untouched}
	got, err := repo.OnHandByBranch(ctx, ids)
	if err != nil {
		t.Fatalf("OnHandByBranch: %v", err)
	}

	t.Run("both branches are reported for a product stocked in both", func(t *testing.T) {
		if got[stocked][branch1] != 200 || got[stocked][branch2] != 50 {
			t.Errorf("stocked = %v, want {%s:200, %s:50}", got[stocked], branch1, branch2)
		}
	})

	t.Run("a branch with no ledger row is absent, not zero", func(t *testing.T) {
		// The catalogue prints what it is given. A fabricated 0 for Trenton is a claim nobody made.
		if _, ok := got[oneBranch][branch1]; ok {
			t.Errorf("oneBranch has no Trenton row; got %v", got[oneBranch])
		}
		if got[oneBranch][branch2] != 12 {
			t.Errorf("oneBranch Kingston = %v, want 12", got[oneBranch][branch2])
		}
	})

	t.Run("a zero balance row is dropped rather than rendered as a count", func(t *testing.T) {
		if _, ok := got[drained]; ok {
			t.Errorf("a drained product must carry no branches; got %v", got[drained])
		}
	})

	t.Run("a product with no ledger rows at all is absent from the map", func(t *testing.T) {
		if _, ok := got[untouched]; ok {
			t.Errorf("untouched must be absent; got %v", got[untouched])
		}
	})

	t.Run("the read is scoped to the requested ids", func(t *testing.T) {
		// The seeded demo DB has stock on many products. Asking for one must not return the rest.
		only, err := repo.OnHandByBranch(ctx, []string{stocked})
		if err != nil {
			t.Fatalf("OnHandByBranch: %v", err)
		}
		if len(only) != 1 {
			t.Errorf("asked for 1 id, got %d products back", len(only))
		}
	})

	t.Run("an empty id set is a no-op, not a full-table scan", func(t *testing.T) {
		empty, err := repo.OnHandByBranch(ctx, nil)
		if err != nil {
			t.Fatalf("OnHandByBranch(nil): %v", err)
		}
		if len(empty) != 0 {
			t.Errorf("empty request must return an empty map; got %d products", len(empty))
		}
	})

	t.Run("the service seam returns the repository read unchanged", func(t *testing.T) {
		// *inventory.Service is what satisfies product.BranchStockReader in main.go; if the method
		// disappears or diverges, the catalogue silently loses the column.
		via, err := newIntSvc(pool).OnHandByBranch(ctx, []string{stocked})
		if err != nil {
			t.Fatalf("service OnHandByBranch: %v", err)
		}
		if via[stocked][branch1] != 200 || via[stocked][branch2] != 50 {
			t.Errorf("service read = %v, want the repository's {200,50}", via[stocked])
		}
	})
}
