//go:build integration

// edit_freight_integration_test.go, the pre pick line edit keeps the delivery fee in the order total.
//
// A quote converted order's total_cents already contains the delivery fee, and quoted_freight_cents carries
// the same figure (quote/convert.go: convertInsertSQL). The staff line edit recomputed
// total = Σ lines + tax and dropped the fee, so editing one line of an untaxed delivery order took 15000 to
// 12000 where 17000 is right. recomputeTotals serves the quantity edit, the add and the remove; each is held
// here. The edit's own tax guard (assertEditableMoney) is not touched: a taxed order still refuses.
package order

import (
	"context"
	"errors"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// setDeliveryFee gives a seeded untaxed order a quoted delivery fee the way a quote conversion leaves it:
// total = subtotal + fee, the fee also in quoted_freight_cents (and freight_cents, the opening actual).
func setDeliveryFee(t *testing.T, pool *pgxpool.Pool, ord string, fee int64) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE orders SET quoted_freight_cents = $2, freight_cents = $2, total_cents = subtotal_cents + $2
		 WHERE id = $1`, ord, fee); err != nil {
		t.Fatalf("set the delivery fee: %v", err)
	}
}

// TestIntegration_EditKeepsTheDeliveryFeeInTheTotal walks all three edits that recompute the total on an
// untaxed order with a 5000 fee. The seeded line is 2 units at 100 (subtotal 200, total 5200).
func TestIntegration_EditKeepsTheDeliveryFeeInTheTotal(t *testing.T) {
	pool := intPool(t)
	_, ord, prod, uom := seedEditOrder(t, pool, "CONFIRMED", 0)
	setDeliveryFee(t, pool, ord, 5000)
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	if sub, tot := liveTotals(t, pool, ord); sub != 200 || tot != 5200 {
		t.Fatalf("fixture money = (%d, %d), want (200, 5200)", sub, tot)
	}
	if _, err := svc.EditLineQty(ctx, ord, 1, "7"); err != nil {
		t.Fatalf("qty edit: %v", err)
	}
	if sub, tot := liveTotals(t, pool, ord); sub != 700 || tot != 5700 {
		t.Errorf("after the quantity edit = (%d, %d), want (700, 5700): the 5000 fee stays in the total", sub, tot)
	}
	if _, err := svc.AddLine(ctx, ord, prod, "3", uom, "second line"); err != nil {
		t.Fatalf("add line: %v", err)
	}
	if sub, tot := liveTotals(t, pool, ord); sub != 1000 || tot != 6000 {
		t.Errorf("after the add = (%d, %d), want (1000, 6000)", sub, tot)
	}
	if _, err := svc.RemoveLine(ctx, ord, 2); err != nil {
		t.Fatalf("remove line: %v", err)
	}
	if sub, tot := liveTotals(t, pool, ord); sub != 700 || tot != 5700 {
		t.Errorf("after the remove = (%d, %d), want (700, 5700)", sub, tot)
	}
	var fee int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(quoted_freight_cents, 0) FROM orders WHERE id = $1`, ord).Scan(&fee); err != nil || fee != 5000 {
		t.Errorf("the quoted fee after the edits = %d (%v), want it untouched at 5000", fee, err)
	}
}

// TestIntegration_EditWithNoQuotedFeeIsUnchanged: an order with no quoted freight (a counter order, a
// reorder) totals as it always did.
func TestIntegration_EditWithNoQuotedFeeIsUnchanged(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())
	if _, err := svc.EditLineQty(ctx, ord, 1, "7"); err != nil {
		t.Fatalf("qty edit: %v", err)
	}
	if sub, tot := liveTotals(t, pool, ord); sub != 700 || tot != 700 {
		t.Errorf("order money = (%d, %d), want (700, 700)", sub, tot)
	}
}

// TestIntegration_EditStillRefusesATaxedDeliveryOrder: the line edit's own tax guard stands; this change
// does not teach it to re-assess tax.
func TestIntegration_EditStillRefusesATaxedDeliveryOrder(t *testing.T) {
	pool := intPool(t)
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 130)
	setDeliveryFee(t, pool, ord, 5000)
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())
	if _, err := svc.EditLineQty(ctx, ord, 1, "7"); !errors.Is(err, ErrEditTaxAssessed) {
		t.Fatalf("edit on a taxed order = %v, want ErrEditTaxAssessed", err)
	}
}
