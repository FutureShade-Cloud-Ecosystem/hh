//go:build integration

// receiving_integration_test.go, the acceptance evidence for item 1: the PO lifecycle end to end, partial
// then full receiving, and the two halves of the load-bearing claim that stock is written ONLY through the
// seam.
package purchasing

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// TestIntegration_POLifecycle_DraftOrderReceivePartialThenRest is the whole flow, with the stock ledger
// checked at every step so "the PO says PARTIAL" is never accepted as evidence that stock moved.
func TestIntegration_POLifecycle_DraftOrderReceivePartialThenRest(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "lifecycle")

	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT, ExpectedAt: "2026-09-15", Notes: "walkthrough",
		Lines: []AddLineParams{{ProductID: product, QtyOrderedBase: 10, UOM: "EA", UnitCostCents: 2500}},
	})
	if err != nil {
		t.Fatalf("create PO: %v", err)
	}
	h.cleanupPO(t, po.ID)
	if po.Status != StatusDraft {
		t.Fatalf("a new PO is %s, want DRAFT", po.Status)
	}
	// The branch-scoped number (0120 next_po_no), not a global counter.
	if !strings.HasPrefix(po.PONo, "#PO-TRT-") {
		t.Fatalf("po_no = %q, want the #PO-TRT-n branch-scoped form", po.PONo)
	}

	// A DRAFT cannot be received against, goods cannot arrive on an order never placed.
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 1}},
	}); err == nil {
		t.Fatal("receiving against a DRAFT must be refused")
	}

	placed, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered)
	if err != nil {
		t.Fatalf("order PO: %v", err)
	}
	if placed.Status != StatusOrdered {
		t.Fatalf("status = %s, want ORDERED", placed.Status)
	}
	if placed.OrderedAt == nil {
		t.Fatal("ordering a PO must stamp ordered_at")
	}
	// The line set is frozen once placed.
	if _, err := h.svc.AddLine(h.ctx, po.ID, AddLineParams{ProductID: product, QtyOrderedBase: 1, UOM: "EA"}); err == nil {
		t.Fatal("adding a line to an ORDERED PO must be refused")
	}

	lineID := placed.Lines[0].ID
	before := h.onHand(t, product, branchTRT)

	// - partial -
	rc1, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 4}}, Notes: "first truck",
	})
	if err != nil {
		t.Fatalf("partial receive: %v", err)
	}
	if rc1.Seq != 1 || rc1.GRNNo != po.PONo+"/R1" {
		t.Fatalf("receipt = seq %d %q, want seq 1 %q", rc1.Seq, rc1.GRNNo, po.PONo+"/R1")
	}
	if rc1.ValueCadCents != 10000 {
		t.Fatalf("receipt value = %d, want 10000 (4 × 2500)", rc1.ValueCadCents)
	}
	mid, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mid.Status != StatusPartial {
		t.Fatalf("status after a partial receipt = %s, want PARTIAL", mid.Status)
	}
	if mid.Lines[0].QtyReceivedBase != 4 || mid.Lines[0].QtyOutstanding != 6 {
		t.Fatalf("line rollups = received %v outstanding %v, want 4 / 6",
			mid.Lines[0].QtyReceivedBase, mid.Lines[0].QtyOutstanding)
	}
	// THE STOCK CHECK: the seam actually moved stock, and it moved exactly the received quantity.
	if got := h.onHand(t, product, branchTRT); got != before+4 {
		t.Fatalf("on-hand after a partial receipt = %v, want %v, the stock seam did not move the goods", got, before+4)
	}
	// And the weighted-average cost inventory maintains is the PO's unit cost.
	var avg int64
	if err := h.pool.QueryRow(h.ctx,
		`SELECT avg_unit_cost_cents FROM product_branch_costs WHERE product_id = $1 AND branch_id = $2`,
		product, branchTRT).Scan(&avg); err != nil {
		t.Fatalf("weighted-average cost row missing after a receipt: %v", err)
	}
	if avg != 2500 {
		t.Fatalf("weighted-average = %d, want 2500", avg)
	}

	// Over-receiving the remainder is refused with both numbers named.
	_, err = h.svc.Receive(h.ctx, po.ID, ReceiveRequest{Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 7}}})
	if err == nil {
		t.Fatal("over-receipt must be refused")
	}
	if !strings.Contains(err.Error(), "over-receipt") {
		t.Fatalf("refusal = %q, want an over-receipt message", err)
	}

	// - the rest -
	rc2, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 6}}, Notes: "second truck",
	})
	if err != nil {
		t.Fatalf("final receive: %v", err)
	}
	if rc2.Seq != 2 {
		t.Fatalf("second receipt seq = %d, want 2", rc2.Seq)
	}
	done, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != StatusReceived {
		t.Fatalf("status after the remainder = %s, want RECEIVED", done.Status)
	}
	if got := h.onHand(t, product, branchTRT); got != before+10 {
		t.Fatalf("on-hand after full receipt = %v, want %v", got, before+10)
	}
	if len(done.Receipts) != 2 {
		t.Fatalf("PO holds %d receipts, want 2", len(done.Receipts))
	}
	// A fully-received PO leaves the receiving worklist.
	items, err := h.svc.Worklist(h.ctx, branchTRT)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range items {
		if w.POID == po.ID {
			t.Fatal("a fully-received PO must not stay on the receiving worklist")
		}
	}
	// Receiving again is refused.
	if _, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 1}}}); err == nil {
		t.Fatal("receiving against a RECEIVED PO must be refused")
	}
}

// TestIntegration_Receive_StockSeamUnwired_RefusesAndWritesNothing is THE acceptance test for the module's
// central claim. Unwire the stock seam and receiving must refuse honestly, and, critically, must leave no
// trace: no receipt row, no receiving document, no line rollup, no burned GRN number. A module that recorded
// the paperwork and skipped the stock would put goods on the yard that the balance sheet does not know about.
func TestIntegration_Receive_StockSeamUnwired_RefusesAndWritesNothing(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "unwired")
	po := h.orderedPO(t, product, 10, 2500)
	before := h.onHand(t, product, branchTRT)

	// Exactly the production wiring, minus the ONE line that supplies the stock seam
	// (cmd/server/main.go: .WithStockReceiver(w4cStockReceiver{svc: inventorySvc})).
	svc := NewService(NewRepository(h.pool)).
		WithVendors(itVendors{pool: h.pool}).
		withCaller(ctxCaller(h.user))

	_, err := svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 4}},
	})
	if err == nil {
		t.Fatal("receiving with the stock seam unwired must be refused")
	}
	if !errors.Is(err, ErrStockSeamUnwired) {
		t.Fatalf("refusal = %v, want ErrStockSeamUnwired", err)
	}
	if got := statusOf(err); got != 503 {
		t.Fatalf("status = %d, want 503, an unwired seam is a configuration fault, not a caller error", got)
	}
	if !strings.Contains(err.Error(), "move no stock") {
		t.Fatalf("the refusal must say WHY: %q", err)
	}

	// Nothing written, anywhere.
	after, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Receipts) != 0 {
		t.Fatalf("a refused receipt wrote %d receipt row(s)", len(after.Receipts))
	}
	if after.Lines[0].QtyReceivedBase != 0 {
		t.Fatalf("a refused receipt advanced the line rollup to %v", after.Lines[0].QtyReceivedBase)
	}
	if after.Status != StatusOrdered {
		t.Fatalf("a refused receipt moved the PO to %s", after.Status)
	}
	if got := h.onHand(t, product, branchTRT); got != before {
		t.Fatalf("on-hand changed from %v to %v with no stock seam wired", before, got)
	}
	var docs int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM purchase_orders WHERE metadata->>'purchasing_po_id' = $1`, po.ID).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Fatalf("a refused receipt left %d receiving document(s) behind", docs)
	}
}

// TestIntegration_Receive_StockSeamFails_RollsBackEverything is the other half: the seam is wired but ERRORS
// mid-flight. Everything purchasing wrote before calling it must roll back with it, so there is no state in
// which purchasing believes goods arrived and the stock ledger does not.
func TestIntegration_Receive_StockSeamFails_RollsBackEverything(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "seamfail")
	po := h.orderedPO(t, product, 10, 2500)
	before := h.onHand(t, product, branchTRT)

	boom := errors.New("inventory is down")
	svc := NewService(NewRepository(h.pool)).
		WithStockReceiver(failingStock{err: boom}).
		WithVendors(itVendors{pool: h.pool}).
		withCaller(ctxCaller(h.user))

	_, err := svc.Receive(h.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 4}},
	})
	if err == nil {
		t.Fatal("a failing stock seam must fail the receipt")
	}
	if !strings.Contains(err.Error(), "inventory is down") {
		t.Fatalf("the seam's error must surface, got %q", err)
	}

	after, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Receipts) != 0 || after.Lines[0].QtyReceivedBase != 0 || after.Status != StatusOrdered {
		t.Fatalf("the receipt did not roll back: %d receipts, received %v, status %s",
			len(after.Receipts), after.Lines[0].QtyReceivedBase, after.Status)
	}
	if got := h.onHand(t, product, branchTRT); got != before {
		t.Fatalf("on-hand changed despite the seam failing: %v → %v", before, got)
	}
	var docs int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM purchase_orders WHERE metadata->>'purchasing_po_id' = $1`, po.ID).Scan(&docs); err != nil {
		t.Fatal(err)
	}
	if docs != 0 {
		t.Fatalf("the receiving document survived the rollback (%d rows)", docs)
	}
}

// TestIntegration_Receive_MultiLinePartial exercises the shape the receiving screen actually produces: some
// lines received, some not, one of them only in part.
func TestIntegration_Receive_MultiLinePartial(t *testing.T) {
	h := newHarness(t)
	p1 := h.newProduct(t, "multi1")
	p2 := h.newProduct(t, "multi2")
	p3 := h.newProduct(t, "multi3")

	po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
		VendorID: h.vendor, BranchID: branchTRT,
		Lines: []AddLineParams{
			{ProductID: p1, QtyOrderedBase: 10, UOM: "EA", UnitCostCents: 1000},
			{ProductID: p2, QtyOrderedBase: 5, UOM: "EA", UnitCostCents: 2000},
			{ProductID: p3, QtyOrderedBase: 8, UOM: "EA", UnitCostCents: 500},
		},
	})
	if err != nil {
		t.Fatalf("create PO: %v", err)
	}
	h.cleanupPO(t, po.ID)
	if _, err := h.svc.Transition(h.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("order: %v", err)
	}

	rc, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{Lines: []ReceiveLineParams{
		{POLineID: po.Lines[0].ID, QtyBase: 10}, // full
		{POLineID: po.Lines[1].ID, QtyBase: 2},  // partial
		// line 3 not on this truck at all
	}})
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	if len(rc.Lines) != 2 {
		t.Fatalf("receipt holds %d lines, want 2, a line not on the truck must not be recorded", len(rc.Lines))
	}
	if rc.ValueCadCents != 10*1000+2*2000 {
		t.Fatalf("receipt value = %d, want %d", rc.ValueCadCents, 10*1000+2*2000)
	}
	if got := h.onHand(t, p1, branchTRT); got != 10 {
		t.Fatalf("p1 on-hand = %v, want 10", got)
	}
	if got := h.onHand(t, p2, branchTRT); got != 2 {
		t.Fatalf("p2 on-hand = %v, want 2", got)
	}
	if got := h.onHand(t, p3, branchTRT); got != 0 {
		t.Fatalf("p3 on-hand = %v, want 0, it was not on the truck", got)
	}
	detail, err := h.svc.GetPO(h.ctx, po.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != StatusPartial {
		t.Fatalf("status = %s, want PARTIAL", detail.Status)
	}
}

// TestIntegration_Receive_DuplicateLineEntriesAreFoldedBeforeGuarding, two entries of 5 against a line with
// 8 outstanding must be refused as 10-against-8, not slip through as two legal 5s.
func TestIntegration_Receive_DuplicateLineEntriesAreFolded(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "dupes")
	po := h.orderedPO(t, product, 8, 100)

	_, err := h.svc.Receive(h.ctx, po.ID, ReceiveRequest{Lines: []ReceiveLineParams{
		{POLineID: po.Lines[0].ID, QtyBase: 5},
		{POLineID: po.Lines[0].ID, QtyBase: 5},
	}})
	if err == nil {
		t.Fatal("two entries totalling more than the outstanding balance must be refused")
	}
	if !strings.Contains(err.Error(), "over-receipt") {
		t.Fatalf("refusal = %q, want an over-receipt message", err)
	}
	if got := h.onHand(t, product, branchTRT); got != 0 {
		t.Fatalf("on-hand = %v after a refused receipt, want 0", got)
	}
}

// TestIntegration_BranchFence_ScopedCallerCannotReachAnotherBranch, the horizontal-access boundary.
func TestIntegration_BranchFence_ScopedCallerCannotReachAnotherBranch(t *testing.T) {
	h := newHarness(t)
	product := h.newProduct(t, "fence")
	po := h.orderedPO(t, product, 4, 100)

	scoped := NewService(NewRepository(h.pool)).
		WithStockReceiver(itStock{svc: h.inv}).
		WithVendors(itVendors{pool: h.pool}).
		withCaller(scopedCaller(h.user, branchKGN))

	if _, err := scoped.GetPO(h.ctx, po.ID); !errors.Is(err, errNotFound) {
		t.Fatalf("a Kingston-scoped caller reading a Trenton PO = %v, want not found (the same answer as a missing PO)", err)
	}
	_, err := scoped.Receive(h.ctx, po.ID, ReceiveRequest{Lines: []ReceiveLineParams{{POLineID: po.Lines[0].ID, QtyBase: 1}}})
	if !errors.Is(err, errNotFound) {
		t.Fatalf("a Kingston-scoped caller receiving a Trenton PO = %v, want not found (the same answer as a missing PO)", err)
	}
	if got := h.onHand(t, product, branchTRT); got != 0 {
		t.Fatalf("a forbidden receipt moved stock: on-hand = %v", got)
	}
}

// TestIntegration_PONumbering_IsBranchScopedAndMonotonic, the 0087 pattern applied to POs: per-branch
// counters, so two branches never contend and a number always names its branch.
func TestIntegration_PONumbering_IsBranchScopedAndMonotonic(t *testing.T) {
	h := newHarness(t)
	p := h.newProduct(t, "numbering")

	mk := func(branch string) string {
		po, err := h.svc.CreatePO(h.ctx, CreatePORequest{
			VendorID: h.vendor, BranchID: branch,
			Lines: []AddLineParams{{ProductID: p, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: 1}},
		})
		if err != nil {
			t.Fatalf("create PO at %s: %v", branch, err)
		}
		h.cleanupPO(t, po.ID)
		return po.PONo
	}
	trt1, trt2 := mk(branchTRT), mk(branchTRT)
	kgn := mk(branchKGN)

	if !strings.HasPrefix(trt1, "#PO-TRT-") || !strings.HasPrefix(kgn, "#PO-KGN-") {
		t.Fatalf("numbers must carry their branch code: %q / %q", trt1, kgn)
	}
	if trt1 == trt2 {
		t.Fatalf("two POs at one branch got the same number %q", trt1)
	}
	if n1, n2 := poOrdinal(t, trt1), poOrdinal(t, trt2); n2 != n1+1 {
		t.Fatalf("consecutive Trenton POs are %d then %d, the counter is not monotonic", n1, n2)
	}
}

func poOrdinal(t *testing.T, poNo string) int {
	t.Helper()
	i := strings.LastIndex(poNo, "-")
	if i < 0 {
		t.Fatalf("po_no %q has no ordinal", poNo)
	}
	n, err := strconv.Atoi(poNo[i+1:])
	if err != nil {
		t.Fatalf("po_no %q ordinal: %v", poNo, err)
	}
	return n
}
