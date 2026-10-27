//go:build integration

package inventory

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
)

func TestFW1_07_BranchCostBasisMatchesStockMoveAndJournal(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	productID := mkProduct(t, pool, "FW107-BRANCH-WAC")

	// Branch 1 holds 10 units at 1000 cents ($10.00).
	// Branch 2 holds 10 units at 2000 cents ($20.00).
	if _, err := pool.Exec(ctx, `
		INSERT INTO stock_balances (product_id, branch_id, location_id, on_hand_base, qty_reserved)
		VALUES ($1, $2, $2, 10, 0), ($1, $3, $3, 10, 0)`, productID, branch1, branch2); err != nil {
		t.Fatalf("seed stock_balances: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, 1000), ($1, $3, 2000)`, productID, branch1, branch2); err != nil {
		t.Fatalf("seed product_branch_costs: %v", err)
	}

	var custID string
	suffix := fmt.Sprintf("fw107-b-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name)
		VALUES ('CUST-'||$1, 'FW1-07 Branch Cost Customer')
		RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	var orderID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('#ORD-'||$1, $2, $3, 'FULFILLED', 'PICKUP', 2500, 2500)
		RETURNING id::text`, suffix, custID, branch1).Scan(&orderID); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		key := "order.fulfilled:" + orderID
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key = $1`, key)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, key)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'order.fulfilled' AND payload->>'order_id' = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE source_ref_id = $1::uuid`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, orderID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})

	invSvc := newIntSvc(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).WithCostBasis(invSvc)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)

	// Fulfil 2 units at branch1 (branch average = 1000 cents, so total cost = 2000 cents).
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := invSvc.FulfilOrderLinesTx(ctx, tx, orderID, branch1, []string{productID}, []float64{2}); err != nil {
		t.Fatalf("FulfilOrderLinesTx: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	for i := 0; i < 8; i++ {
		if dispatcher.DrainOnce(ctx) == 0 {
			break
		}
	}

	var moveValueCents int64
	if err := pool.QueryRow(ctx, `
		SELECT value_cents FROM stock_moves WHERE source_ref_id = $1::uuid AND move_type = 'SHIP'`, orderID,
	).Scan(&moveValueCents); err != nil {
		t.Fatalf("read ship move value_cents: %v", err)
	}

	var gl1200CreditCents, gl5000DebitCents int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.credit_cents), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND a.code = '1200'`, "order.fulfilled:"+orderID,
	).Scan(&gl1200CreditCents); err != nil {
		t.Fatalf("read GL 1200 credit: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(l.debit_cents), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND a.code = '5000'`, "order.fulfilled:"+orderID,
	).Scan(&gl5000DebitCents); err != nil {
		t.Fatalf("read GL 5000 debit: %v", err)
	}

	const wantBranch1CostCents = 2 * 1000 // 2000 cents, NOT the cross-branch blended 3000 cents
	if moveValueCents != wantBranch1CostCents || gl1200CreditCents != wantBranch1CostCents || gl5000DebitCents != wantBranch1CostCents {
		t.Fatalf("branch cost basis mismatch: stock_move value_cents=%d, GL 1200 credit=%d, GL 5000 debit=%d (want %d for all three)",
			moveValueCents, gl1200CreditCents, gl5000DebitCents, wantBranch1CostCents)
	}
}
