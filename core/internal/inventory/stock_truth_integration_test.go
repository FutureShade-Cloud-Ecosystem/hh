//go:build integration

// stock_truth_integration_test.go, the Wave 4 integration suite (DIB-60/71/73/61/72) against real
// Postgres. Run with:
//
//	DATABASE_URL='postgres://hardscape:hardscape@127.0.0.1:5432/<db>?sslmode=disable' \
//	  go test -tags=integration ./internal/inventory/... -count=1 -run StockTruth -v
//
// DIB-396 LEASES. Two rules, applied here:
//
//   - Any fixture that transiently moves stock (a transfer ship leaves the source down and the
//     destination not yet up; a hand-written balance) takes dib396LeaseShared(3960001) BEFORE the
//     first write, so the whole-DB drift gates in internal/gate and internal/platform/db cannot read
//     the window as a real breach.
//   - The ONE test here that asserts a whole-stock invariant, total on-hand is conserved across a
//     completed transfer, takes dib396LeaseExclusive(3960001), so it waits for every drifting
//     fixture in every package to finish first.
//
// The lease is taken FIRST in each test so its release (t.Cleanup is LIFO) runs LAST, after the
// fixture has restored what it touched.
package inventory

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const branch2 = "5d1b3f6e-0000-4000-9000-000000000002" // Kingston

// - fixtures -

// seedBalance writes an on-hand balance and a matching RECEIPT move so the (product, location) row
// reconciles against v_stock_balance_drift on its own. Writing the balance WITHOUT the move is the
// classic fixture drift; writing both means this fixture never needs the shared lease, and the tests
// that DO move stock take it for the window that genuinely drifts.
func seedBalance(t *testing.T, pool *pgxpool.Pool, productID, branchID string, qty float64, unitCostCents int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_moves (product_id, to_location_id, qty_base, qty_entered, uom_entered,
		                         move_type, unit_cost_cents, value_cents, reason, branch_id)
		VALUES ($1, $2, $3, $3, (SELECT base_uom FROM products WHERE id=$1), 'RECEIPT', $4::bigint,
		        ROUND($3::numeric * $4::bigint)::bigint, 'w4b fixture opening', $2)`,
		productID, branchID, qty, unitCostCents); err != nil {
		t.Fatalf("seed move: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, location_id, branch_id, on_hand_base)
		VALUES ($1, $2, $2, $3)
		ON CONFLICT (product_id, location_id) DO UPDATE SET on_hand_base = stock_balances.on_hand_base + EXCLUDED.on_hand_base`,
		productID, branchID, qty); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, $3) ON CONFLICT (product_id, branch_id) DO UPDATE SET avg_unit_cost_cents = EXCLUDED.avg_unit_cost_cents`,
		productID, branchID, unitCostCents); err != nil {
		t.Fatalf("seed cost: %v", err)
	}
	t.Cleanup(func() {
		// mkProduct's cleanup already drops stock_balances / stock_moves / product_branch_costs for
		// this product; the reservation + transfer rows are this file's to clear.
		_, _ = pool.Exec(context.Background(), `DELETE FROM stock_reservations WHERE product_id = $1`, productID)
		_, _ = pool.Exec(context.Background(), `
			DELETE FROM stock_transfer_lines WHERE product_id = $1`, productID)
	})
}

func reservedAt(t *testing.T, pool *pgxpool.Pool, productID, branchID string) float64 {
	t.Helper()
	var q float64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(qty_reserved),0) FROM stock_balances WHERE product_id=$1 AND branch_id=$2`,
		productID, branchID).Scan(&q); err != nil {
		t.Fatalf("reserved: %v", err)
	}
	return q
}

func onHandAt(t *testing.T, pool *pgxpool.Pool, productID, branchID string) float64 {
	t.Helper()
	var q float64
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(on_hand_base),0) FROM stock_balances WHERE product_id=$1 AND branch_id=$2`,
		productID, branchID).Scan(&q); err != nil {
		t.Fatalf("on-hand: %v", err)
	}
	return q
}

// reserve is the HTTP-path equivalent: open a tx, reserve, commit.
func reserve(t *testing.T, svc *Service, pool *pgxpool.Pool, in ReserveInput) ReserveResult {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)
	res, err := svc.ReserveTx(ctx, tx, in)
	if err != nil {
		t.Fatalf("ReserveTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return res
}

// - DIB-60 -

func TestStockTruth_ReservationProjection_HardHoldsOnly(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-RES")
	seedBalance(t, pool, p, branch1, 100, 500)

	// HARD (order), moves the projection.
	hard := reserve(t, svc, pool, ReserveInput{
		ProductID: p, BranchID: branch1, ResType: ResTypeOrder, QtyBase: 30,
		SourceType: "order", Reason: "w4b order hold",
	})
	if !hard.Applied || hard.Reservation.HoldMode != HoldHard {
		t.Fatalf("order hold: applied=%v mode=%q, want applied HARD", hard.Applied, hard.Reservation.HoldMode)
	}
	if got := reservedAt(t, pool, p, branch1); got != 30 {
		t.Fatalf("qty_reserved = %v after a 30-unit HARD hold, want 30", got)
	}

	// SOFT (quote), recorded, expires, does NOT move the projection.
	soft := reserve(t, svc, pool, ReserveInput{
		ProductID: p, BranchID: branch1, ResType: ResTypeQuote, QtyBase: 25, SourceType: "quote",
	})
	if soft.Reservation.HoldMode != HoldSoft {
		t.Fatalf("quote hold mode = %q, want SOFT (the shipped default)", soft.Reservation.HoldMode)
	}
	if soft.Reservation.ExpiresAt == "" {
		t.Fatal("the default quote policy is a 72h TTL, a soft hold with no expiry is a leak")
	}
	if got := reservedAt(t, pool, p, branch1); got != 30 {
		t.Fatalf("qty_reserved = %v after adding a SOFT hold, want 30 (soft must not gate)", got)
	}

	// available reads through the view AND through CheckAvailability.
	var available float64
	if err := pool.QueryRow(ctx,
		`SELECT SUM(available_base) FROM v_stock_on_hand WHERE product_id=$1 AND branch_id=$2`,
		p, branch1).Scan(&available); err != nil {
		t.Fatalf("v_stock_on_hand: %v", err)
	}
	if available != 70 {
		t.Fatalf("v_stock_on_hand.available_base = %v, want 70 (100 on hand − 30 held)", available)
	}
	ok, reason, err := svc.CheckAvailability(ctx, p, branch1, 80)
	if err != nil {
		t.Fatalf("CheckAvailability: %v", err)
	}
	if ok || reason != ReasonOutOfStock {
		t.Fatalf("80 of 100 with 30 held must be OUT_OF_STOCK; got ok=%v reason=%q", ok, reason)
	}

	// Release the hard hold: the projection unwinds.
	tx, _ := pool.Begin(ctx)
	n, err := svc.ReleaseBySourceTx(ctx, tx, "", hard.Reservation.SourceRefID, ResReleased)
	if hard.Reservation.SourceRefID == "" {
		// No source ref on this fixture, release by id instead.
		_ = tx.Rollback(ctx)
		tx, _ = pool.Begin(ctx)
		if err := svc.ReleaseTx(ctx, tx, hard.Reservation.ID, ResReleased); err != nil {
			t.Fatalf("ReleaseTx: %v", err)
		}
		n = 1
	} else if err != nil {
		t.Fatalf("ReleaseBySourceTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit release: %v", err)
	}
	if n != 1 {
		t.Fatalf("released %d holds, want 1", n)
	}
	if got := reservedAt(t, pool, p, branch1); got != 0 {
		t.Fatalf("qty_reserved = %v after release, want 0", got)
	}
}

func TestStockTruth_InStockProductIDs_ExcludesHardReserved(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-INSTOCK")
	seedBalance(t, pool, p, branch1, 12, 400)

	has := func() bool {
		ids, err := svc.InStockProductIDs(ctx, branch1)
		if err != nil {
			t.Fatalf("InStockProductIDs: %v", err)
		}
		for _, id := range ids {
			if id == p {
				return true
			}
		}
		return false
	}
	if !has() {
		t.Fatal("a product with 12 on hand must be in stock")
	}
	// Hold the entire balance. The catalogue in-stock-only filter must stop listing it, a product
	// whose whole balance is promised is not something a browsing customer can buy.
	reserve(t, svc, pool, ReserveInput{ProductID: p, BranchID: branch1, ResType: ResTypeOrder, QtyBase: 12})
	if has() {
		t.Fatal("a fully hard-reserved product is still listed as in stock, the filter reads on-hand, not available")
	}
}

func TestStockTruth_ExpirySweep_ClosesDueSoftHolds(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-EXPIRE")
	seedBalance(t, pool, p, branch1, 40, 300)

	// A branch that hardens quote holds, with a TTL already in the past.
	if _, err := svc.SetAllocationPolicy(ctx, AllocationPolicy{
		BranchID: branch2, OrderMode: HoldHard, QuoteMode: HoldHard, QuoteTTLHours: 1,
	}, ""); err != nil {
		t.Fatalf("SetAllocationPolicy: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `
			UPDATE branch_allocation_policy SET order_mode='HARD', quote_mode='SOFT', quote_ttl_hours=72
			 WHERE branch_id=$1`, branch2)
	})
	seedBalance(t, pool, p, branch2, 40, 300)
	res := reserve(t, svc, pool, ReserveInput{
		ProductID: p, BranchID: branch2, ResType: ResTypeQuote, QtyBase: 9,
	})
	if res.Reservation.HoldMode != HoldHard {
		t.Fatalf("Kingston configured quote_mode=HARD; got %q", res.Reservation.HoldMode)
	}
	if got := reservedAt(t, pool, p, branch2); got != 9 {
		t.Fatalf("qty_reserved = %v, want 9", got)
	}

	// Drag the expiry into the past and sweep.
	if _, err := pool.Exec(ctx,
		`UPDATE stock_reservations SET expires_at = now() - interval '1 hour' WHERE id = $1`,
		res.Reservation.ID); err != nil {
		t.Fatalf("age reservation: %v", err)
	}
	if _, err := svc.ExpireDueReservations(ctx); err != nil {
		t.Fatalf("ExpireDueReservations: %v", err)
	}
	if got := reservedAt(t, pool, p, branch2); got != 0 {
		t.Fatalf("qty_reserved = %v after the sweep, want 0, an expired hold must free its stock", got)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM stock_reservations WHERE id=$1`, res.Reservation.ID).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != ResExpired {
		t.Fatalf("status = %q after the sweep, want EXPIRED", status)
	}
}

// - DIB-71 -

func mkTransfer(t *testing.T, svc *Service, productID string, qty float64) Transfer {
	t.Helper()
	tr, err := svc.CreateTransfer(context.Background(), CreateTransferInput{
		FromBranchID: branch1, ToBranchID: branch2, Notes: "w4b test",
		Lines: []CreateTransferLine{{ProductID: productID, QtyBase: qty}},
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	t.Cleanup(func() {
		// stock_transfer_lines cascade from the header.
		_, _ = svc.repo.pool.Exec(context.Background(), `DELETE FROM stock_transfers WHERE id = $1`, tr.ID)
	})
	return tr
}

func TestStockTruth_Transfer_NoOverdraftAtSource(t *testing.T) {
	pool := intPool(t)
	// The refused ship writes nothing (the guard rolls the transaction back), but the SUCCESSFUL
	// ship below leaves the source down and the destination not yet up, a real in-transit window.
	dib396LeaseShared(t, pool, dib396StockDriftLease)

	svc := newIntSvc(pool)
	p := mkProduct(t, pool, "W4B-OVERDRAFT")
	seedBalance(t, pool, p, branch1, 20, 250)

	// Hold 15 of the 20 for a confirmed order → 5 available.
	reserve(t, svc, pool, ReserveInput{
		ProductID: p, BranchID: branch1, ResType: ResTypeOrder, QtyBase: 15, SourceType: "order",
	})

	// A transfer for 10 has plenty of ON-HAND and not enough AVAILABLE. It must be refused.
	tooBig := mkTransfer(t, svc, p, 10)
	if _, err := svc.ShipTransfer(context.Background(), tooBig.ID, ""); err == nil {
		t.Fatal("a 10-unit transfer shipped against 5 available, the guard is reading on-hand")
	}
	// Nothing moved.
	if got := onHandAt(t, pool, p, branch1); got != 20 {
		t.Fatalf("on-hand = %v after a REFUSED ship, want 20, the guard must roll back the whole document", got)
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM stock_transfers WHERE id=$1`, tooBig.ID).Scan(&status); err != nil {
		t.Fatalf("status: %v", err)
	}
	if status != TransferDraft {
		t.Fatalf("refused transfer is %q, want DRAFT", status)
	}

	// Exactly the available quantity ships.
	ok := mkTransfer(t, svc, p, 5)
	shipped, err := svc.ShipTransfer(context.Background(), ok.ID, "")
	if err != nil {
		t.Fatalf("shipping exactly the available balance was refused: %v", err)
	}
	if shipped.Status != TransferInTransit {
		t.Fatalf("status = %q after ship, want IN_TRANSIT", shipped.Status)
	}
	if got := onHandAt(t, pool, p, branch1); got != 15 {
		t.Fatalf("source on-hand = %v after shipping 5, want 15", got)
	}
}

// TestStockTruth_Transfer_DoubleEntryBalances_Leased is the DIB-71 invariant assertion: over a
// COMPLETED transfer, units out of the source equal units into the destination and system-wide
// on-hand for the product is conserved.
//
// It takes the EXCLUSIVE lease because it reads a whole-stock invariant (the sum across every
// location) and must not run while another package's fixture is mid-drift.
func TestStockTruth_Transfer_DoubleEntryBalances_Leased(t *testing.T) {
	pool := intPool(t)
	dib396LeaseExclusive(t, pool, dib396StockDriftLease)

	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-XFER")
	seedBalance(t, pool, p, branch1, 60, 1000)

	systemOnHand := func() float64 {
		var q float64
		if err := pool.QueryRow(ctx,
			`SELECT COALESCE(SUM(on_hand_base),0) FROM stock_balances WHERE product_id=$1`, p).Scan(&q); err != nil {
			t.Fatalf("system on-hand: %v", err)
		}
		return q
	}
	before := systemOnHand()

	tr := mkTransfer(t, svc, p, 24)
	if _, err := svc.ShipTransfer(ctx, tr.ID, ""); err != nil {
		t.Fatalf("ShipTransfer: %v", err)
	}

	// IN TRANSIT: the source is down, the destination is not yet up, and the difference is visible
	// as the in-transit bucket rather than as lost stock.
	if got := onHandAt(t, pool, p, branch1); got != 36 {
		t.Fatalf("source on-hand in transit = %v, want 36", got)
	}
	if got := onHandAt(t, pool, p, branch2); got != 0 {
		t.Fatalf("destination on-hand in transit = %v, want 0, stock must not arrive before the truck", got)
	}
	kgn, err := svc.AvailabilityAtBranch(ctx, p, branch2)
	if err != nil {
		t.Fatalf("AvailabilityAtBranch: %v", err)
	}
	if kgn.InTransitBase != 24 {
		t.Fatalf("Kingston in-transit = %v, want 24", kgn.InTransitBase)
	}

	if _, err := svc.ReceiveTransfer(ctx, tr.ID, ""); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	// THE INVARIANT. Units out == units in, and nothing was created or destroyed.
	if got := onHandAt(t, pool, p, branch1); got != 36 {
		t.Fatalf("source on-hand after receive = %v, want 36", got)
	}
	if got := onHandAt(t, pool, p, branch2); got != 24 {
		t.Fatalf("destination on-hand after receive = %v, want 24, receive must complete the pair", got)
	}
	if after := systemOnHand(); after != before {
		t.Fatalf("system-wide on-hand changed across a transfer: %v → %v (a transfer must conserve stock)", before, after)
	}

	// The pair is also balanced in the LEDGER: one outbound leg and one inbound leg, equal quantity.
	var out, in float64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(qty_base) FILTER (WHERE from_location_id IS NOT NULL), 0),
		       COALESCE(SUM(qty_base) FILTER (WHERE to_location_id   IS NOT NULL), 0)
		  FROM stock_moves WHERE source_type='stock_transfer' AND source_ref_id=$1`, tr.ID).Scan(&out, &in); err != nil {
		t.Fatalf("ledger legs: %v", err)
	}
	if out != in || out != 24 {
		t.Fatalf("transfer legs are unbalanced: %v out, %v in (want 24 / 24)", out, in)
	}

	// And the drift view stays empty for this product's locations, each leg reconciles on its own.
	var drift int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM v_stock_balance_drift WHERE product_id=$1`, p).Scan(&drift); err != nil {
		t.Fatalf("drift: %v", err)
	}
	if drift != 0 {
		t.Fatalf("%d drifting rows after a completed transfer, the balance projection does not tie to the ledger", drift)
	}

	// The in-transit bucket is now empty and the destination carries the cost that travelled.
	kgn, err = svc.AvailabilityAtBranch(ctx, p, branch2)
	if err != nil {
		t.Fatalf("AvailabilityAtBranch: %v", err)
	}
	if kgn.InTransitBase != 0 {
		t.Fatalf("in-transit = %v after receive, want 0", kgn.InTransitBase)
	}
	if kgn.AvgCostCents != 1000 {
		t.Fatalf("destination weighted-average = %d, want 1000, value must travel with the stock", kgn.AvgCostCents)
	}
}

func TestStockTruth_Transfer_GLPairIsBalanced(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-XFERGL")
	seedBalance(t, pool, p, branch1, 30, 750)

	tr := mkTransfer(t, svc, p, 10)
	if _, err := svc.ShipTransfer(ctx, tr.ID, ""); err != nil {
		t.Fatalf("ShipTransfer: %v", err)
	}
	if _, err := svc.ReceiveTransfer(ctx, tr.ID, ""); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT j.id::text, COALESCE(SUM(COALESCE(l.debit_cents,0)),0), COALESCE(SUM(COALESCE(l.credit_cents,0)),0)
		  FROM gl_journals j JOIN gl_journal_lines l ON l.journal_id = j.id
		 WHERE j.source_ref_id = $1 GROUP BY j.id`, tr.ID)
	if err != nil {
		t.Fatalf("journals: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var id string
		var dr, cr int64
		if err := rows.Scan(&id, &dr, &cr); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if dr != cr {
			t.Fatalf("transfer journal %s is unbalanced: %d DR vs %d CR", id, dr, cr)
		}
		if dr != 7500 {
			t.Fatalf("transfer journal %s value = %d, want 7500 (10 × 750)", id, dr)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("%d transfer journals, want 2 (ship + receive)", n)
	}

	// The in-transit account nets to zero once the pair completes, value parked in 1250 only for
	// the duration of the trip.
	var transit int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.source_ref_id = $1 AND a.code = '1250'`, tr.ID).Scan(&transit); err != nil {
		t.Fatalf("in-transit balance: %v", err)
	}
	if transit != 0 {
		t.Fatalf("1250 Stock In Transit nets to %d after a completed transfer, want 0", transit)
	}
}

func TestStockTruth_Transfer_ShippedCannotBeCancelled(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	p := mkProduct(t, pool, "W4B-XFERCANCEL")
	seedBalance(t, pool, p, branch1, 10, 100)

	tr := mkTransfer(t, svc, p, 3)
	if _, err := svc.CancelTransfer(context.Background(), tr.ID); err != nil {
		t.Fatalf("cancelling a DRAFT must be allowed: %v", err)
	}
	tr2 := mkTransfer(t, svc, p, 3)
	if _, err := svc.ShipTransfer(context.Background(), tr2.ID, ""); err != nil {
		t.Fatalf("ShipTransfer: %v", err)
	}
	if _, err := svc.CancelTransfer(context.Background(), tr2.ID); err == nil {
		t.Fatal("a shipped transfer was cancelled, the stock has physically moved and can only be corrected by a transfer back")
	}
	// Receiving twice must also be refused: the truck arrives once.
	if _, err := svc.ReceiveTransfer(context.Background(), tr2.ID, ""); err != nil {
		t.Fatalf("ReceiveTransfer: %v", err)
	}
	if _, err := svc.ReceiveTransfer(context.Background(), tr2.ID, ""); err == nil {
		t.Fatal("a RECEIVED transfer was received a second time, that is a duplicated inbound move")
	}
}

// - DIB-73 -

func TestStockTruth_FourBucketView_EveryBranchAlways(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-BUCKETS")
	seedBalance(t, pool, p, branch1, 50, 600)

	rows, err := svc.AvailabilityAllBranches(ctx, p)
	if err != nil {
		t.Fatalf("AvailabilityAllBranches: %v", err)
	}
	// EVERY branch, even the one with nothing. A missing row reads as a bug, not as "none here".
	if len(rows) != 2 {
		t.Fatalf("%d branch rows, want 2, a branch with no stock must still appear", len(rows))
	}
	byCode := map[string]BranchStock{}
	for _, r := range rows {
		byCode[r.BranchCode] = r
	}
	if byCode["KGN"].OnHandBase != 0 || byCode["KGN"].AvailableBase != 0 {
		t.Fatalf("Kingston should be zeroed, got %+v", byCode["KGN"])
	}

	reserve(t, svc, pool, ReserveInput{ProductID: p, BranchID: branch1, ResType: ResTypeOrder, QtyBase: 12})
	reserve(t, svc, pool, ReserveInput{ProductID: p, BranchID: branch1, ResType: ResTypeQuote, QtyBase: 7})
	tr := mkTransfer(t, svc, p, 8)
	if _, err := svc.ShipTransfer(ctx, tr.ID, ""); err != nil {
		t.Fatalf("ShipTransfer: %v", err)
	}

	rows, err = svc.AvailabilityAllBranches(ctx, p)
	if err != nil {
		t.Fatalf("AvailabilityAllBranches: %v", err)
	}
	byCode = map[string]BranchStock{}
	for _, r := range rows {
		byCode[r.BranchCode] = r
	}
	trt, kgn := byCode["TRT"], byCode["KGN"]

	// Trenton: 50 − 8 shipped = 42 on hand, 12 hard-held, 7 soft (advisory), 30 available.
	if trt.OnHandBase != 42 {
		t.Fatalf("TRT on-hand = %v, want 42", trt.OnHandBase)
	}
	if trt.ReservedBase != 12 {
		t.Fatalf("TRT reserved = %v, want 12 (hard holds only)", trt.ReservedBase)
	}
	if trt.SoftHeldBase != 7 {
		t.Fatalf("TRT soft-held = %v, want 7, soft holds must be VISIBLE even though they do not gate", trt.SoftHeldBase)
	}
	if trt.AvailableBase != 30 {
		t.Fatalf("TRT available = %v, want 30 (42 − 12)", trt.AvailableBase)
	}
	// In-transit is INBOUND only: the 8 units left Trenton, they are not Trenton's in-transit.
	if trt.InTransitBase != 0 {
		t.Fatalf("TRT in-transit = %v, want 0, outbound stock has already left on-hand and must not be shown twice", trt.InTransitBase)
	}
	if kgn.InTransitBase != 8 {
		t.Fatalf("KGN in-transit = %v, want 8", kgn.InTransitBase)
	}
	if kgn.OnHandBase != 0 || kgn.AvailableBase != 0 {
		t.Fatalf("KGN must not count a truck as on-hand: %+v", kgn)
	}
}

// - DIB-72 -

func TestStockTruth_ShortfallAppearsWhenTheReorderPointIsCrossed(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-SHORT")
	seedBalance(t, pool, p, branch1, 100, 900)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id = $1`, p)
	})

	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active ORDER BY vendor_no LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	// Point 50, standard order 60, skid multiple 60.
	if _, err := svc.SetReorderPoint(ctx, ReorderPoint{
		ProductID: p, BranchID: branch1, ReorderPointBase: 50, ReorderQtyBase: 60,
		OrderMultipleBase: 60, PreferredVendorID: vendorID, LeadTimeDays: 5, IsActive: true,
	}, ""); err != nil {
		t.Fatalf("SetReorderPoint: %v", err)
	}

	rowFor := func() (ShortfallRow, bool) {
		wl, err := svc.ShortfallWorklist(ctx, BranchReach{Global: true}, branch1, 500)
		if err != nil {
			t.Fatalf("ShortfallWorklist: %v", err)
		}
		for _, r := range wl.Rows {
			if r.ProductID == p {
				return r, true
			}
		}
		return ShortfallRow{}, false
	}

	// 100 on hand against a point of 50, nothing to do.
	if _, found := rowFor(); found {
		t.Fatal("a product with double its reorder point is on the worklist")
	}

	// Hard-hold 65. Available drops to 35, under the point of 50, the shortfall is REAL even though
	// on-hand never moved. Grading on on-hand would show nothing here.
	reserve(t, svc, pool, ReserveInput{
		ProductID: p, BranchID: branch1, ResType: ResTypeOrder, QtyBase: 65, SourceType: "order",
	})
	row, found := rowFor()
	if !found {
		t.Fatal("available fell to 35 against a point of 50 and no shortfall row appeared, the worklist reads on-hand")
	}
	if row.OnHandBase != 100 || row.ReservedBase != 65 || row.AvailableBase != 35 {
		t.Fatalf("shortfall row buckets wrong: %+v", row)
	}
	if row.ProjectedBase != 35 {
		t.Fatalf("projected = %v, want 35 (nothing inbound yet)", row.ProjectedBase)
	}
	// Gap is 15, standard order is 60, skid multiple is 60 → 60.
	if row.SuggestedQtyBase != 60 {
		t.Fatalf("suggested = %v, want 60 (standard order qty, on the skid multiple)", row.SuggestedQtyBase)
	}
	if row.PreferredVendorName == "" {
		t.Fatal("the worklist row must name the vendor the buyer would order from")
	}
	if row.SuggestedValueCents != 54000 {
		t.Fatalf("suggested value = %d, want 54000 (60 × 900)", row.SuggestedValueCents)
	}

	// Now bring stock in on a transfer INTO Trenton. The projected position must absorb it and the
	// row must LEAVE the worklist, replenishment must never re-order what is already on a truck.
	// 35 available + 20 in transit = 55, which clears the point of 50.
	seedBalance(t, pool, p, branch2, 30, 900)
	back, err := svc.CreateTransfer(ctx, CreateTransferInput{
		FromBranchID: branch2, ToBranchID: branch1,
		Lines: []CreateTransferLine{{ProductID: p, QtyBase: 20}},
	})
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM stock_transfers WHERE id=$1`, back.ID) })
	if _, err := svc.ShipTransfer(ctx, back.ID, ""); err != nil {
		t.Fatalf("ShipTransfer: %v", err)
	}
	if row, found := rowFor(); found {
		t.Fatalf("still short with 20 units in transit (projected %v against point %v), "+
			"the worklist is ignoring inbound stock and would re-order what is already coming",
			row.ProjectedBase, row.ReorderPointBase)
	}
}

func TestStockTruth_ShortfallSQLAndGoAgree(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	wl, err := svc.ShortfallWorklist(context.Background(), BranchReach{Global: true}, "", 200)
	if err != nil {
		t.Fatalf("ShortfallWorklist: %v", err)
	}
	// No OnOrderReader is bound on the integration service, so the worklist must SAY its on-order
	// leg is degraded rather than presenting the zeros as measured (DIB-470).
	if !wl.OnOrderDegraded {
		t.Fatal("no on-order seam is bound and the worklist did not report the leg as degraded")
	}
	for _, r := range wl.Rows {
		if want := AvailableBase(r.OnHandBase, r.ReservedBase); r.AvailableBase != want {
			t.Fatalf("%s available = %v, Go definition says %v", r.SKU, r.AvailableBase, want)
		}
		if want := ProjectedPosition(r.AvailableBase, r.InTransitBase, r.OnOrderBase); r.ProjectedBase != want {
			t.Fatalf("%s projected = %v, Go definition says %v", r.SKU, r.ProjectedBase, want)
		}
		// Every listed row is genuinely short, the SQL predicate and the Go arithmetic agree.
		if r.ProjectedBase >= r.ReorderPointBase {
			t.Fatalf("%s is on the worklist with projected %v >= point %v", r.SKU, r.ProjectedBase, r.ReorderPointBase)
		}
		if r.SuggestedQtyBase <= 0 {
			t.Fatalf("%s is short but suggests %v", r.SKU, r.SuggestedQtyBase)
		}
	}
}

func TestStockTruth_CreatePOFromShortfall_StubReturnsTheDraft(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-POSEAM")
	seedBalance(t, pool, p, branch1, 10, 500)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM product_reorder_points WHERE product_id=$1`, p)
	})
	var vendorID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM vendors WHERE is_active ORDER BY vendor_no LIMIT 1`).Scan(&vendorID); err != nil {
		t.Fatalf("vendor: %v", err)
	}
	if _, err := svc.SetReorderPoint(ctx, ReorderPoint{
		ProductID: p, BranchID: branch1, ReorderPointBase: 40, ReorderQtyBase: 25,
		PreferredVendorID: vendorID, IsActive: true,
	}, ""); err != nil {
		t.Fatalf("SetReorderPoint: %v", err)
	}

	draft, poID, _, err := svc.CreatePOFromShortfall(ctx, BranchReach{Global: true}, branch1, []string{p})
	if err == nil {
		t.Fatal("the unwired purchasing stub reported success, a buyer would be told their PO exists")
	}
	if poID != "" {
		t.Fatalf("stub returned a PO id %q, it must not fabricate one", poID)
	}
	// The draft rides along with the error so the surface can show exactly what WOULD be ordered.
	if draft.VendorID != vendorID || len(draft.Lines) != 1 {
		t.Fatalf("stub did not return a usable draft: %+v", draft)
	}
	if draft.Lines[0].QtyBase != 30 {
		t.Fatalf("draft qty = %v, want 30 (point 40 − available 10)", draft.Lines[0].QtyBase)
	}
}

// - DIB-61 -

func TestStockTruth_BrowseFacets_OwnFacetIsNotSelfFiltered(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()

	base, err := svc.Browse(ctx, BrowseFilter{})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if base.Total == 0 || len(base.Rows) == 0 {
		t.Fatal("the seeded catalogue browsed empty")
	}
	// Pick whatever dimension this environment actually HAS values in, rather than naming one. The
	// A-18 promoted columns are empty on the demo seed (see facetFields) and populated on real
	// BisTrack data, so a test that hard-codes color_family passes on one and skips on the other -
	// which is the same as not testing it. This asserts the SEMANTIC on whichever facet is real here.
	var chosen Facet
	for _, f := range base.Facets {
		if len(f.Values) >= 2 && f.Field != "is_featured" && f.Field != "is_special_order" {
			chosen = f
			break
		}
	}
	if chosen.Field == "" {
		t.Fatal("no facet in this catalogue has two values, the browse panel would be a dead control")
	}
	pick := chosen.Values[0].Value

	filter := BrowseFilter{}
	setFacet(t, &filter, chosen.Field, pick)
	filtered, err := svc.Browse(ctx, filter)
	if err != nil {
		t.Fatalf("Browse filtered: %v", err)
	}
	if filtered.Total >= base.Total {
		t.Fatalf("selecting %s=%q did not narrow the result: %d → %d", chosen.Field, pick, base.Total, filtered.Total)
	}
	if filtered.Total != chosen.Values[0].Count {
		t.Fatalf("the %s=%q badge said %d but the filter returned %d, the count badge is lying",
			chosen.Field, pick, chosen.Values[0].Count, filtered.Total)
	}

	var filteredChosen, filteredOther Facet
	for _, f := range filtered.Facets {
		if f.Field == chosen.Field {
			filteredChosen = f
		}
	}
	// THE SEMANTIC: the OTHER values of the SELECTED facet must still show their counts, or the panel
	// collapses the moment you use it and you can never see what else is there.
	if len(filteredChosen.Values) != len(chosen.Values) {
		t.Fatalf("the %s facet collapsed from %d values to %d when one was selected, "+
			"the facet count is applying its own constraint",
			chosen.Field, len(chosen.Values), len(filteredChosen.Values))
	}
	// An UNRELATED facet, by contrast, MUST narrow, it is counted with the selection applied.
	for _, f := range filtered.Facets {
		if f.Field != chosen.Field && len(f.Values) > 0 {
			filteredOther = f
			break
		}
	}
	if filteredOther.Field != "" {
		var total int
		for _, v := range filteredOther.Values {
			total += v.Count
		}
		if total > filtered.Total {
			t.Fatalf("the %s facet counts sum to %d under a filter matching %d products, "+
				"an unrelated facet is NOT applying the selection", filteredOther.Field, total, filtered.Total)
		}
	}

	// Every row returned really does carry the selected value.
	for _, r := range filtered.Rows {
		if got := facetValueOf(r, chosen.Field); got != pick {
			t.Fatalf("row %s has %s=%q under a %q filter", r.SKU, chosen.Field, got, pick)
		}
	}
}

// setFacet applies a single-value selection to the field the test picked at runtime.
func setFacet(t *testing.T, f *BrowseFilter, field, value string) {
	t.Helper()
	switch field {
	case "color_family":
		f.ColorFamily = []string{value}
	case "texture":
		f.Texture = []string{value}
	case "product_size":
		f.ProductSize = []string{value}
	case "sold_by":
		f.SoldBy = []string{value}
	case "manufacturer":
		f.Manufacturer = []string{value}
	case "collection":
		f.Collection = []string{value}
	case "color":
		f.Color = []string{value}
	case "finish":
		f.Finish = []string{value}
	case "application":
		f.Application = []string{value}
	default:
		t.Fatalf("setFacet: unknown facet %q, the test helper and facetFields have drifted", field)
	}
}

// facetValueOf reads the same field back off a returned row.
func facetValueOf(r BrowseRow, field string) string {
	switch field {
	case "color_family":
		return r.ColorFamily
	case "texture":
		return r.Texture
	case "product_size":
		return r.ProductSize
	case "sold_by":
		return r.SoldBy
	case "manufacturer":
		return r.Manufacturer
	case "collection":
		return r.Collection
	case "color":
		return r.Color
	case "finish":
		return r.Finish
	case "application":
		return r.Application
	}
	return ""
}

func TestStockTruth_BrowseCarriesTheStockPosition(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "W4B-BROWSE")
	seedBalance(t, pool, p, branch1, 18, 450)
	reserve(t, svc, pool, ReserveInput{ProductID: p, BranchID: branch1, ResType: ResTypeOrder, QtyBase: 6})

	res, err := svc.Browse(ctx, BrowseFilter{Text: "W4B-BROWSE", BranchID: branch1})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	var found bool
	for _, r := range res.Rows {
		if r.ProductID != p {
			continue
		}
		found = true
		if r.OnHandBase != 18 || r.ReservedBase != 6 || r.AvailableBase != 12 {
			t.Fatalf("browse row buckets wrong: on-hand %v reserved %v available %v (want 18/6/12)",
				r.OnHandBase, r.ReservedBase, r.AvailableBase)
		}
	}
	if !found {
		t.Fatal("the trigram text filter did not find the product by SKU")
	}

	// in_stock_only grades AVAILABLE: hold the rest and the product must drop out.
	reserve(t, svc, pool, ReserveInput{ProductID: p, BranchID: branch1, ResType: ResTypeOrder, QtyBase: 12})
	res, err = svc.Browse(ctx, BrowseFilter{Text: "W4B-BROWSE", BranchID: branch1, InStockOnly: true})
	if err != nil {
		t.Fatalf("Browse in-stock: %v", err)
	}
	for _, r := range res.Rows {
		if r.ProductID == p {
			t.Fatal("a fully hard-reserved product passed the in-stock-only filter")
		}
	}
}
