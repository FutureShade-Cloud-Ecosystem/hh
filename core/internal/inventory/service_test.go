package inventory

// service_test.go, the INV-CORE unit suite (DB-free, via the in-memory fakeStore). Covers the ACs that don't
// need Postgres: the CostBasis found=false COGS gate, weighted-average maintenance across receipts, the USD→CAD
// conversion at receipt, the over-receipt 409 guard, the COLLECT_NOW SHIP moves + negative-stock surface, the
// DELIVERED zero-move boundary, and the po.received / inventory.adjusted posting consumers (1200 leg resolved
// through the rule seam, GRNI idempotency key).

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func svcWith(f *fakeStore) *Service {
	return NewService(nil).withStore(f).WithGLPoster(&fakeGLPoster{}).WithRuleResolver(defaultRules())
}

// AC: CostBasis found=false gates COGS, no cost row → (0,false,nil), byte-identical to NoopCostBasis.
func TestUnitCost_FoundFalseGatesCOGS(t *testing.T) {
	f := newFakeStore()
	s := svcWith(f)
	cost, found, err := s.UnitCostCents(context.Background(), "prod-1", 10)
	if err != nil || found || cost != 0 {
		t.Fatalf("want (0,false,nil), got (%d,%v,%v)", cost, found, err)
	}
}

// AC: real CostBasis, the extended weighted-avg cost (unit × qty) drives the COGS leg.
func TestUnitCost_ExtendedCost(t *testing.T) {
	f := newFakeStore()
	f.productCost["prod-1"] = 1100 // $11.00 weighted-avg
	s := svcWith(f)
	cost, found, err := s.UnitCostCents(context.Background(), "prod-1", 5)
	if err != nil || !found || cost != 5500 {
		t.Fatalf("want (5500,true,nil), got (%d,%v,%v)", cost, found, err)
	}
}

// AC: GRNI avg-cost, two receipts of the same product build the weighted average 100@$10 then 100@$12 → $11.
func TestReceive_WeightedAverageAcrossReceipts(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.hdr = poHeader{ID: "po-1", PONo: "PO-1", BranchID: "br-1", Status: "SUBMITTED", Currency: "CAD", FxRate: 1}
	f.lines = []poLine{{ID: "l1", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: 100, ReceivedBase: 0, UnitCostCents: 1000}}
	s := svcWith(f)

	if _, err := s.applyReceipt(ctx, nil, "po-1", nil, ""); err != nil {
		t.Fatalf("receipt 1: %v", err)
	}
	if got := f.cost[key("p", "br-1")]; got != 1000 {
		t.Fatalf("after receipt 1 want avg 1000, got %d", got)
	}
	// Second PO, same product/branch, dearer.
	f.hdr = poHeader{ID: "po-2", PONo: "PO-2", BranchID: "br-1", Status: "SUBMITTED", Currency: "CAD", FxRate: 1}
	f.lines = []poLine{{ID: "l2", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: 100, ReceivedBase: 0, UnitCostCents: 1200}}
	if _, err := s.applyReceipt(ctx, nil, "po-2", nil, ""); err != nil {
		t.Fatalf("receipt 2: %v", err)
	}
	if got := f.cost[key("p", "br-1")]; got != 1100 {
		t.Fatalf("after receipt 2 want blended avg 1100, got %d", got)
	}
	if got := f.onhand[key("p", "br-1")]; got != 200 {
		t.Fatalf("want on-hand 200, got %v", got)
	}
}

// AC: USD fx→CAD, a USD PO converts unit cost to CAD before it touches the ledger or GRNI.
func TestReceive_USDConvertsToCAD(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.hdr = poHeader{ID: "po-1", PONo: "PO-USD", BranchID: "br-1", Status: "SUBMITTED", Currency: "USD", FxRate: 1.35}
	f.lines = []poLine{{ID: "l1", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: 10, ReceivedBase: 0, UnitCostCents: 1000}}
	s := svcWith(f)

	res, err := s.applyReceipt(ctx, nil, "po-1", nil, "")
	if err != nil {
		t.Fatalf("receipt: %v", err)
	}
	// 1000 USD × 1.35 = 1350 CAD/unit; 10 units = 13500 CAD.
	if res.ValueCadCents != 13500 {
		t.Fatalf("want GRNI value 13500 CAD, got %d", res.ValueCadCents)
	}
	if got := f.cost[key("p", "br-1")]; got != 1350 {
		t.Fatalf("want CAD avg 1350, got %d", got)
	}
	if len(f.moves) != 1 || f.moves[0].UnitCostCents != 1350 || f.moves[0].ValueCents != 13500 {
		t.Fatalf("stock move not in CAD: %+v", f.moves)
	}
}

// AC: over-receipt 409, receiving more than a line's outstanding qty is a conflict, not a silent over-post.
func TestReceive_OverReceiptConflict(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.hdr = poHeader{ID: "po-1", PONo: "PO-1", BranchID: "br-1", Status: "SUBMITTED", Currency: "CAD", FxRate: 1}
	f.lines = []poLine{{ID: "l1", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: 100, ReceivedBase: 40, UnitCostCents: 1000}}
	s := svcWith(f)

	_, err := s.applyReceipt(ctx, nil, "po-1", map[string]float64{"l1": 80}, "") // only 60 outstanding
	var ce clientError
	if err == nil || !asClientErr(err, &ce) || ce.status != 409 {
		t.Fatalf("want 409 over-receipt, got %v", err)
	}
}

// AC: partial receipt advances status to PARTIALLY_RECEIVED; a full receipt to RECEIVED.
func TestReceive_StatusTransitions(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.hdr = poHeader{ID: "po-1", PONo: "PO-1", BranchID: "br-1", Status: "SUBMITTED", Currency: "CAD", FxRate: 1}
	f.lines = []poLine{{ID: "l1", LineNo: 1, ProductID: "p", UOM: "EA", QtyBase: 100, ReceivedBase: 0, UnitCostCents: 1000}}
	s := svcWith(f)
	if _, err := s.applyReceipt(ctx, nil, "po-1", map[string]float64{"l1": 40}, ""); err != nil {
		t.Fatalf("partial: %v", err)
	}
	if f.poStatus != POPartiallyReceived {
		t.Fatalf("want PARTIALLY_RECEIVED, got %s", f.poStatus)
	}
	f.lines[0].ReceivedBase = 40 // reflect the partial for the next receipt
	if _, err := s.applyReceipt(ctx, nil, "po-1", map[string]float64{"l1": 60}, ""); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if f.poStatus != POReceived {
		t.Fatalf("want RECEIVED, got %s", f.poStatus)
	}
}

// AC: COLLECT_NOW → SHIP moves + on-hand drops (and may go negative, the by-design detection surface).
func TestDecrement_CollectNowShipsAndCanGoNegative(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.cost[key("p", "br-1")] = 900
	f.onhand[key("p", "br-1")] = 3 // only 3 on hand, sell 5 → −2
	s := svcWith(f)

	err := s.DecrementForSaleTx(ctx, nil, DecrementInput{
		OrderID: "o-1", BranchID: "br-1", SaleType: SaleCollectNow,
		Lines: []SaleLine{{ProductID: "p", QtyBase: 5}},
	})
	if err != nil {
		t.Fatalf("decrement: %v", err)
	}
	if len(f.moves) != 1 || f.moves[0].MoveType != MoveShip || f.moves[0].FromLocation != "br-1" {
		t.Fatalf("want one SHIP move out of br-1, got %+v", f.moves)
	}
	if f.moves[0].ValueCents != 4500 { // 5 × 900
		t.Fatalf("want COGS value 4500, got %d", f.moves[0].ValueCents)
	}
	if got := f.onhand[key("p", "br-1")]; got != -2 {
		t.Fatalf("want negative on-hand −2 (detection surface), got %v", got)
	}
}

// AC: DELIVERED / COLLECT_LATER boundary, zero moves (DX-01 PARTIAL). Quantified on pending-fulfilment.
func TestDecrement_DeferredFulfilmentZeroMoves(t *testing.T) {
	ctx := context.Background()
	for _, st := range []string{SaleDelivered, SaleCollectLater} {
		f := newFakeStore()
		f.cost[key("p", "br-1")] = 900
		f.onhand[key("p", "br-1")] = 10
		s := svcWith(f)
		if err := s.DecrementForSaleTx(ctx, nil, DecrementInput{
			OrderID: "o-1", BranchID: "br-1", SaleType: st,
			Lines: []SaleLine{{ProductID: "p", QtyBase: 5}},
		}); err != nil {
			t.Fatalf("%s decrement: %v", st, err)
		}
		if len(f.moves) != 0 {
			t.Fatalf("%s: want zero moves, got %d", st, len(f.moves))
		}
		if got := f.onhand[key("p", "br-1")]; got != 10 {
			t.Fatalf("%s: on-hand must be untouched, got %v", st, got)
		}
	}
}

// AC: po.received posts GRNI DR 1200 / CR 2150 with both legs rule-resolved, keyed idempotently on the receipt.
func TestPostPOReceived_GRNIRuleResolved(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	gl := &fakeGLPoster{}
	s := NewService(nil).withStore(f).WithGLPoster(gl).WithRuleResolver(defaultRules())

	payload, _ := json.Marshal(map[string]any{"po_id": "po-1", "receipt_id": "rc-1", "branch_id": "br-1", "value_cents": 13500})
	if err := s.postPOReceived(ctx, payload); err != nil {
		t.Fatalf("postPOReceived: %v", err)
	}
	if len(gl.journals) != 1 {
		t.Fatalf("want 1 journal, got %d", len(gl.journals))
	}
	j := gl.journals[0]
	if j.IdempotencyKey != "po.received:rc-1" {
		t.Fatalf("want idempotency key po.received:rc-1, got %s", j.IdempotencyKey)
	}
	assertLeg(t, j, "1200", 13500, 0)
	assertLeg(t, j, "2150", 0, 13500)
}

// AC: inventory.adjusted posts via ResolveRule, the 1200 inventory leg is rule-resolved (not a literal), and
// the sign selects gain (DR 1200) vs loss (CR 1200).
func TestPostInventoryAdjusted_RuleResolved1200Leg(t *testing.T) {
	ctx := context.Background()
	gl := &fakeGLPoster{}
	s := NewService(nil).withStore(newFakeStore()).WithGLPoster(gl).WithRuleResolver(defaultRules())

	// Loss (shrink): negative value → DR 5200 / CR 1200.
	loss, _ := json.Marshal(map[string]any{"source_ref_id": "cs-1", "branch_id": "br-1", "value_cents": -2500})
	if err := s.postInventoryAdjusted(ctx, loss); err != nil {
		t.Fatalf("loss: %v", err)
	}
	assertLeg(t, gl.journals[0], "5200", 2500, 0)
	assertLeg(t, gl.journals[0], "1200", 0, 2500)

	// Gain (found), reason-coded account override → DR 1200 / CR 4000.
	gain, _ := json.Marshal(map[string]any{"source_ref_id": "cs-2", "branch_id": "br-1", "account_code": "4000", "value_cents": 1800})
	if err := s.postInventoryAdjusted(ctx, gain); err != nil {
		t.Fatalf("gain: %v", err)
	}
	assertLeg(t, gl.journals[1], "1200", 1800, 0)
	assertLeg(t, gl.journals[1], "4000", 0, 1800)
}

// AC: capitalization raises the weighted-average over on-hand (A-16 seam), no stock qty change.
func TestCapitalizeLanded_RaisesAvgOverOnHand(t *testing.T) {
	ctx := context.Background()
	f := newFakeStore()
	f.cost[key("p", "br-1")] = 1000
	f.onhand[key("p", "br-1")] = 100
	s := svcWith(f)
	absorbed, err := s.capitalizeLanded(ctx, nil, "p", "br-1", 5000, time.Now(), "") // $50 over 100 units → +$0.50
	if err != nil {
		t.Fatalf("capitalize: %v", err)
	}
	if absorbed != 5000 {
		t.Fatalf("want absorbed 5000 (GL debit == subledger delta), got %d", absorbed)
	}
	if got := f.cost[key("p", "br-1")]; got != 1050 {
		t.Fatalf("want avg 1050, got %d", got)
	}
	// Capitalization moves no stock. It books ONE value only row (0453, W2-5B): zero quantity, no
	// location, carrying the exact cents the GL 1200 debit books, so the close pre flight's stock
	// tie can unwind it.
	if len(f.moves) != 1 {
		t.Fatalf("capitalization must not move stock, got %d moves", len(f.moves))
	}
	mv := f.moves[0]
	if mv.MoveType != MoveValue || mv.QtyBase != 0 || mv.FromLocation != "" || mv.ToLocation != "" {
		t.Fatalf("the capitalization's row must be a value only move, got %+v", mv)
	}
	if mv.ValueCents != absorbed {
		t.Fatalf("the value move carries %d cents, want the absorbed %d (the exact cents the GL books)", mv.ValueCents, absorbed)
	}
}

// - helpers -

func asClientErr(err error, target *clientError) bool {
	ce, ok := err.(clientError)
	if ok {
		*target = ce
	}
	return ok
}

func assertLeg(t *testing.T, j GLJournal, code string, debit, credit int64) {
	t.Helper()
	for _, l := range j.Lines {
		if l.AccountCode == code {
			if l.DebitCents != debit || l.CreditCents != credit {
				t.Fatalf("leg %s: want DR %d/CR %d, got DR %d/CR %d", code, debit, credit, l.DebitCents, l.CreditCents)
			}
			return
		}
	}
	t.Fatalf("no leg for account %s in %+v", code, j.Lines)
}
