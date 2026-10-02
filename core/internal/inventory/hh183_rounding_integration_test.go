//go:build integration

// hh183_rounding_integration_test.go, HH-183 against real Postgres: the persisted legs of an exact
// half-cent receipt (0.29 at 7.50 is 217.5 cents) must store 218, agree with the SQL
// ROUND(received_base * unit_cost_cents) leg the landed-cost read computes, and agree with the
// po.received GRNI value the GL consumes. On main the Go leg stores 217 while the SQL leg stores
// ROUND(0.2900 * 750) = 218, one cent apart on the same line.

package inventory

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// receiptMoveValue reads the one RECEIPT move's value_cents for a product.
func receiptMoveValue(t *testing.T, pool *pgxpool.Pool, productID string) int64 {
	t.Helper()
	var v int64
	if err := pool.QueryRow(context.Background(),
		`SELECT value_cents FROM stock_moves WHERE product_id=$1 AND move_type='RECEIPT'`, productID).Scan(&v); err != nil {
		t.Fatalf("receipt move: %v", err)
	}
	return v
}

// sqlRoundLeg reads the landed-cost read's own arithmetic, ROUND(received_base * unit_cost_cents),
// for every line of the PO.
func sqlRoundLeg(t *testing.T, pool *pgxpool.Pool, poID string) int64 {
	t.Helper()
	var v int64
	if err := pool.QueryRow(context.Background(),
		`SELECT ROUND(pl.received_base * pl.unit_cost_cents)::bigint FROM po_lines pl WHERE pl.po_id=$1`, poID).Scan(&v); err != nil {
		t.Fatalf("sql round leg: %v", err)
	}
	return v
}

// AC: receiving 0.29 at 750 cents persists 218 on the move, the WAC, the GRNI event, and the SQL
// ROUND leg of the same PO line, all agreeing to the cent.
func TestHH183_ReceiptHalfCent_PersistedLegAgreesWithSQLRound(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "HH183-RCP")
	po, _ := mkPO(t, pool, "HH183-PO", "CAD", 1.0, []poLineSpec{{p, 0.29, 750}})
	receiveAll(t, svc, pool, po)

	moveValue := receiptMoveValue(t, pool, p)
	if moveValue != 218 {
		t.Errorf("stock move must carry 218 (0.29 x 750 = 217.5 rounds up), got %d", moveValue)
	}
	if leg := sqlRoundLeg(t, pool, po); leg != moveValue {
		t.Errorf("the Go leg (%d) and the SQL ROUND leg (%d) must agree on the same line", moveValue, leg)
	}

	var event struct {
		ValueCents int64 `json:"value_cents"`
	}
	if err := json.Unmarshal(lastPOReceivedEvent(t, pool, po), &event); err != nil {
		t.Fatalf("po.received payload: %v", err)
	}
	if event.ValueCents != 218 {
		t.Errorf("the po.received GRNI value must carry 218, got %d", event.ValueCents)
	}

	if got := branchCost(t, pool, p); got != 750 {
		t.Errorf("the branch average must blend to exactly the unit cost 750 (single rounding over the exact numerator), got %d", got)
	}

	lines, err := svc.ReceivedLinesForPO(ctx, po)
	if err != nil {
		t.Fatalf("ReceivedLinesForPO: %v", err)
	}
	if len(lines) != 1 || lines[0].ValueCents != 218 {
		t.Errorf("the landed-cost allocation leg must read 218, got %+v", lines)
	}
}

// AC: the USD receipt leg converts through the exact rate, 75 cents at fx 1.38 is 103.5 CAD cents
// and the persisted unit cost is 104, so 2 units value at 208.
func TestHH183_FXReceiptHalfCent_PersistsHalfUp(t *testing.T) {
	pool := intPool(t)
	svc := newIntSvc(pool)
	p := mkProduct(t, pool, "HH183-FX")
	po, _ := mkPO(t, pool, "HH183-PO-FX", "USD", 1.38, []poLineSpec{{p, 2, 75}})
	receiveAll(t, svc, pool, po)

	var unit, value int64
	if err := pool.QueryRow(context.Background(),
		`SELECT unit_cost_cents, value_cents FROM stock_moves WHERE product_id=$1 AND move_type='RECEIPT'`, p).
		Scan(&unit, &value); err != nil {
		t.Fatalf("receipt move: %v", err)
	}
	if unit != 104 {
		t.Errorf("75 x 1.38 = 103.5 must persist as 104 CAD cents, got %d", unit)
	}
	if value != 208 {
		t.Errorf("2 x 104 must persist as 208, got %d", value)
	}
}

// branchCostAt reads the WAC for a product at an arbitrary branch (branchCost is branch1-only).
func branchCostAt(t *testing.T, pool *pgxpool.Pool, productID, branchID string) int64 {
	t.Helper()
	var c int64
	if err := pool.QueryRow(context.Background(),
		`SELECT avg_unit_cost_cents FROM product_branch_costs WHERE product_id=$1 AND branch_id=$2`,
		productID, branchID).Scan(&c); err != nil {
		t.Fatalf("branch cost: %v", err)
	}
	return c
}

// AC: both legs of a transfer value 0.29 at a 750-cent average at 218, and the destination's WAC
// is exactly the travelled unit cost 750 (the blend rounds once over the exact numerator).
func TestHH183_TransferLegs_HalfCentPersisted(t *testing.T) {
	pool := intPool(t)
	dib396LeaseShared(t, pool, dib396StockDriftLease)
	svc := newIntSvc(pool)
	ctx := context.Background()
	p := mkProduct(t, pool, "HH183-TRF")
	seedBalance(t, pool, p, branch1, 10, 750)

	tr, err := svc.CreateTransfer(ctx, CreateTransferInput{
		FromBranchID: branch1, ToBranchID: branch2,
		Lines: []CreateTransferLine{{ProductID: p, QtyBase: 0.29}},
	})
	if err != nil {
		t.Fatalf("create transfer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM stock_transfer_lines WHERE transfer_id = $1`, tr.ID)
		_, _ = pool.Exec(c, `DELETE FROM stock_transfers WHERE id = $1`, tr.ID)
	})
	if _, err := svc.ShipTransfer(ctx, tr.ID, ""); err != nil {
		t.Fatalf("ship transfer: %v", err)
	}

	var shipValue int64
	if err := pool.QueryRow(ctx,
		`SELECT value_cents FROM stock_moves WHERE product_id=$1 AND branch_id=$2 AND move_type='TRANSFER'`,
		p, branch1).Scan(&shipValue); err != nil {
		t.Fatalf("ship move: %v", err)
	}
	if shipValue != 218 {
		t.Errorf("the ship leg must carry 218 (0.29 x 750 = 217.5 rounds up), got %d", shipValue)
	}

	if _, err := svc.ReceiveTransfer(ctx, tr.ID, ""); err != nil {
		t.Fatalf("receive transfer: %v", err)
	}
	var recvValue int64
	if err := pool.QueryRow(ctx,
		`SELECT value_cents FROM stock_moves WHERE product_id=$1 AND branch_id=$2 AND move_type='TRANSFER'`,
		p, branch2).Scan(&recvValue); err != nil {
		t.Fatalf("receive move: %v", err)
	}
	if recvValue != 218 {
		t.Errorf("the receive leg must carry 218, got %d", recvValue)
	}
	if got := branchCostAt(t, pool, p, branch2); got != 750 {
		t.Errorf("the destination average must blend to exactly the travelled cost 750 (single rounding over the exact numerator), got %d", got)
	}
}
