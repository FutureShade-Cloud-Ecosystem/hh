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

func TestFW1_07_PreReceiptSaleFallbackExceptionsAndTrueUp(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()

	// Product A: no receipt at branch1, but has a known cost (900 cents) at branch2 -> LAST_KNOWN_COST fallback.
	prodLastKnown := mkProduct(t, pool, "FW107-PRE-LASTKNOWN")
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
		VALUES ($1, $2, 900)`, prodLastKnown, branch2); err != nil {
		t.Fatalf("seed branch2 cost for prodLastKnown: %v", err)
	}

	// Product B: no receipt or cost anywhere, but carries catalogue_cost_cents = 800 on products.metadata -> CATALOGUE_COST fallback.
	prodCatalogue := mkProduct(t, pool, "FW107-PRE-CATALOGUE")
	if _, err := pool.Exec(ctx, `
		UPDATE products SET metadata = '{"catalogue_cost_cents": 800}'::jsonb WHERE id = $1`, prodCatalogue); err != nil {
		t.Fatalf("set catalogue_cost_cents on prodCatalogue: %v", err)
	}

	var custID, orderID1, orderID2 string
	suffix := fmt.Sprintf("fw107-pre-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name)
		VALUES ('CUST-'||$1, 'FW1-07 Pre-Receipt Customer')
		RETURNING id::text`, suffix).Scan(&custID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('#PRE1-'||$1, $2, $3, 'FULFILLED', 'PICKUP', 2000, 2000)
		RETURNING id::text`, suffix, custID, branch1).Scan(&orderID1); err != nil {
		t.Fatalf("seed order 1: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
		VALUES ('#PRE2-'||$1, $2, $3, 'FULFILLED', 'PICKUP', 4000, 4000)
		RETURNING id::text`, suffix, custID, branch1).Scan(&orderID2); err != nil {
		t.Fatalf("seed order 2: %v", err)
	}

	poID, _ := mkPO(t, pool, "PO-FW107-TRUEUP", "CAD", 1.0, []poLineSpec{
		{productID: prodCatalogue, qtyBase: 5, unitCost: 1050},
	})

	t.Cleanup(func() {
		c := context.Background()
		for _, oid := range []string{orderID1, orderID2} {
			key := "order.fulfilled:" + oid
			_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key = $1`, key)
			_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = $1`, key)
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'order.fulfilled' AND payload->>'order_id' = $1`, oid)
			_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE source_ref_id = $1::uuid`, oid)
			_, _ = pool.Exec(c, `DELETE FROM orders WHERE id = $1`, oid)
		}
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.idempotency_key LIKE 'pre_receipt_trueup:%'`)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key LIKE 'pre_receipt_trueup:%'`)
		_, _ = pool.Exec(c, `DELETE FROM gl_journal_lines l USING gl_journals j WHERE l.journal_id = j.id AND j.source_ref_id IN (SELECT id FROM po_receipts WHERE po_id = $1::uuid)`, poID)
		_, _ = pool.Exec(c, `DELETE FROM gl_journals WHERE source_ref_id IN (SELECT id FROM po_receipts WHERE po_id = $1::uuid)`, poID)
		_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE topic = 'po.received' AND payload->>'po_id' = $1`, poID)
		_, _ = pool.Exec(c, `DELETE FROM stock_moves WHERE product_id IN ($1::uuid, $2::uuid)`, prodLastKnown, prodCatalogue)
		_, _ = pool.Exec(c, `DELETE FROM po_receipts WHERE po_id = $1`, poID)
		_, _ = pool.Exec(c, `DELETE FROM po_lines WHERE po_id = $1`, poID)
		_, _ = pool.Exec(c, `DELETE FROM purchase_orders WHERE id = $1`, poID)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, custID)
	})

	invSvc := newIntSvc(pool)
	glSvc := gl.NewService(gl.NewRepository(pool)).WithCostBasis(invSvc)
	dispatcher := notify.NewDispatcher(notify.NewPgOutboxStore(pool), nil)
	glSvc.RegisterPostingHandlers(dispatcher)
	invSvc.RegisterPostingHandlers(dispatcher)

	// 1. Sell 1 unit of prodLastKnown and 2 units of prodCatalogue at branch1 BEFORE any receipt.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := invSvc.FulfilOrderLinesTx(ctx, tx, orderID1, branch1, []string{prodLastKnown}, []float64{1}); err != nil {
		t.Fatalf("FulfilOrderLinesTx prodLastKnown: %v", err)
	}
	if err := invSvc.FulfilOrderLinesTx(ctx, tx, orderID2, branch1, []string{prodCatalogue}, []float64{2}); err != nil {
		t.Fatalf("FulfilOrderLinesTx prodCatalogue: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit pre-receipt sales: %v", err)
	}

	for i := 0; i < 8; i++ {
		if dispatcher.DrainOnce(ctx) == 0 {
			break
		}
	}

	// Verify fallback COGS posted (900 cents for orderID1 via LAST_KNOWN_COST, 2*800 = 1600 cents for orderID2 via CATALOGUE_COST).
	glDebit := func(idemKey, code string) int64 {
		var v int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(COALESCE(l.debit_cents,0) - COALESCE(l.credit_cents,0)), 0)
			  FROM gl_journal_lines l
			  JOIN gl_journals j ON j.id = l.journal_id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.idempotency_key = $1 AND a.code = $2`, idemKey, code).Scan(&v); err != nil {
			t.Fatalf("glDebit %s %s: %v", idemKey, code, err)
		}
		return v
	}

	cogs1 := glDebit("order.fulfilled:"+orderID1, "5000")
	cogs2 := glDebit("order.fulfilled:"+orderID2, "5000")
	if cogs1 != 900 || cogs2 != 1600 {
		t.Fatalf("pre-receipt sale COGS skipped or wrong: order1 COGS=%d (want 900 from last known cost), order2 COGS=%d (want 1600 from catalogue cost)",
			cogs1, cogs2)
	}

	// 2. Verify both pre-receipt sales appear on the Controller exceptions read for branch1.
	excs, err := invSvc.ControllerExceptions(ctx, branch1)
	if err != nil {
		t.Fatalf("ControllerExceptions before receipt: %v", err)
	}
	foundLastKnown, foundCatalogue := false, false
	for _, ex := range excs {
		if ex.ProductID == prodLastKnown && ex.FallbackSource == "LAST_KNOWN_COST" && ex.ProvisionalValueCents == 900 {
			foundLastKnown = true
		}
		if ex.ProductID == prodCatalogue && ex.FallbackSource == "CATALOGUE_COST" && ex.ProvisionalValueCents == 1600 {
			foundCatalogue = true
		}
	}
	if !foundLastKnown || !foundCatalogue {
		t.Fatalf("ControllerExceptions missing pre-receipt sales: foundLastKnown=%v foundCatalogue=%v rows=%+v",
			foundLastKnown, foundCatalogue, excs)
	}

	// 3. Receive 5 units of prodCatalogue at branch1 at 1050 cents ($10.50).
	// The 2 pre-receipt sold units were booked at 800 cents (1600 cents); actual cost is 2*1050 = 2100 cents -> +500 cents true-up to COGS (DR 5000 / CR 1200).
	// Remaining on-hand at branch1 is 5 - 2 = 3 units at 1050 cents = 3150 cents.
	if err := invSvc.ReceivePOTx(ctx, nil, poID); err != nil {
		t.Fatalf("ReceivePOTx: %v", err)
	}
	for i := 0; i < 8; i++ {
		if dispatcher.DrainOnce(ctx) == 0 {
			break
		}
	}

	var rcptID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM po_receipts WHERE po_id = $1 LIMIT 1`, poID).Scan(&rcptID); err != nil {
		t.Fatalf("scan receipt id: %v", err)
	}
	trueUpKey := "pre_receipt_trueup:" + rcptID + ":" + prodCatalogue
	trueUpCOGS := glDebit(trueUpKey, "5000")
	trueUpInv := glDebit(trueUpKey, "1200")
	if trueUpCOGS != 500 || trueUpInv != -500 {
		t.Fatalf("first-receipt true-up journal mismatch: DR 5000=%d (want 500), CR 1200=%d (want -500)", trueUpCOGS, trueUpInv)
	}

	// Verify prodCatalogue is now cleared from active ControllerExceptions at branch1.
	excsAfter, err := invSvc.ControllerExceptions(ctx, branch1)
	if err != nil {
		t.Fatalf("ControllerExceptions after receipt: %v", err)
	}
	for _, ex := range excsAfter {
		if ex.ProductID == prodCatalogue {
			t.Fatalf("prodCatalogue still listed on ControllerExceptions after first receipt true-up: %+v", ex)
		}
	}
}
