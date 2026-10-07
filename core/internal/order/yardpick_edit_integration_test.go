//go:build integration

// An order edit on a short line is a NEW order quantity: it clears the backorder and the frozen
// ordered values for that line, records that it did, tells the order's rep, and the money then
// follows the edit's own pricing. A pick after the edit works from the edited quantity.

package order

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedTaxFreeYard is a CONFIRMED Trenton order with one line of 5 at 100 (500), no tax, which is the
// only kind an order edit is allowed on.
func seedTaxFreeYard(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	_, ord, _, _ := seedEditOrder(t, pool, "CONFIRMED", 0)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE order_lines SET qty_entered = 5, qty_base = 5, unit_price_cents = 100, line_total_cents = 500 WHERE order_id = $1`, ord); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE orders SET subtotal_cents = 500, total_cents = 500 WHERE id = $1`, ord); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_shortfall_basis WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM order_line_shortfalls WHERE order_id = $1`, ord)
	})
	return ord
}

type clearedRecorder struct{ got []BackorderClearedNotice }

func (c *clearedRecorder) BackorderCleared(_ context.Context, n BackorderClearedNotice) error {
	c.got = append(c.got, n)
	return nil
}

func TestIntegration_YardPick_AnEditOfAShortLineIsANewOrderQuantity(t *testing.T) {
	pool := intPool(t)
	ord := seedTaxFreeYard(t, pool)
	svc, _ := editSvc(t, pool)
	rec := &clearedRecorder{}
	svc.WithBackorderNotifier(rec)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	short := yardPick("3", "CONDENSED_SKIDS")
	short.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, short); err != nil {
		t.Fatalf("short: %v", err)
	}
	if _, err := svc.EditLineQty(ctx, ord, 1, "10"); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if b := liveBackorder(t, pool, ord); b.entered != "0.0000" || b.base != "0.0000" {
		t.Fatalf("the edit's quantity is the new order quantity, so nothing is left on backorder: %+v", b)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM order_line_shortfalls WHERE order_id = $1`, ord).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the frozen ordered values for the line are cleared: %d %v", n, err)
	}
	if _, tot := liveTotals(t, pool, ord); tot != 1000 {
		t.Fatalf("the money follows the edit's own pricing: %d", tot)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE entity_id = $1 AND changes::text LIKE '%backorder_cleared%'`, ord).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the order's history records that the edit cleared the backorder: %d %v", n, err)
	}
	if len(rec.got) != 1 || rec.got[0].LineNo != 1 || rec.got[0].ClearedQty != "2.0000" || rec.got[0].NewQty != "10.0000" || rec.got[0].Order.OrderNo == "" {
		t.Fatalf("the rep's notice carries the line, what was cleared and the new quantity: %+v", rec.got)
	}

	// The probe: pick 4 of the new 10 bills 4 at the agreed price, not 4/12 of a stale total.
	pick := yardPick("4", "NOT_IN_YARD")
	pick.OrderID = ord
	out, err := svc.ApplyYardPicks(ctx, pick)
	if err != nil {
		t.Fatalf("pick after the edit: %v", err)
	}
	_, _, total := liveLine(t, pool, ord, 1)
	if total != 400 || out.Lines[0].QtyOrdered != "10.0000" || out.Lines[0].QtyBackordered != "6.0000" {
		t.Fatalf("4 at the agreed 100 is 400 against an ordered 10, got total %d %+v", total, out.Lines[0])
	}
}

func TestIntegration_YardPick_RemovingAShortLineClearsItAndRebasesTheOrder(t *testing.T) {
	pool := intPool(t)
	ord := seedTaxFreeYard(t, pool)
	svc, _ := editSvc(t, pool)
	rec := &clearedRecorder{}
	svc.WithBackorderNotifier(rec)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())
	short := yardPick("3", "DAMAGED")
	short.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, short); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddLine(ctx, ord, mustProduct(t, pool), "1", mustUOM(t, pool), "added after the short"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.RemoveLine(ctx, ord, 1); err != nil {
		t.Fatalf("remove the short line: %v", err)
	}
	if len(rec.got) != 1 || rec.got[0].NewQty != "0.0000" {
		t.Fatalf("removing a short line tells the rep its backorder went with it: %+v", rec.got)
	}
	var basis, lines int64
	if err := pool.QueryRow(context.Background(), `SELECT ordered_subtotal_cents FROM order_shortfall_basis WHERE order_id = $1`, ord).Scan(&basis); err != nil {
		t.Fatalf("basis: %v", err)
	}
	sub, _ := liveTotals(t, pool, ord)
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(line_total_cents),0) FROM order_lines WHERE order_id = $1`, ord).Scan(&lines); err != nil {
		t.Fatal(err)
	}
	if basis != lines || sub != lines {
		t.Fatalf("with no short line left the basis is the order as it stands: basis %d lines %d subtotal %d", basis, lines, sub)
	}
}

func mustProduct(t *testing.T, pool *pgxpool.Pool) string {
	id, _ := anchoredProduct(t, pool)
	return id
}

func mustUOM(t *testing.T, pool *pgxpool.Pool) string {
	_, uom := anchoredProduct(t, pool)
	return uom
}
