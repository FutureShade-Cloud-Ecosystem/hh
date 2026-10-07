//go:build integration

// The backorder's surface: the staff order read carries each line's backorder and the order's
// has-backorder flag, and the customer's own order list and detail carry them too, so the rep, the
// office and the customer can all see what is still owed.

package order

import (
	"context"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func TestIntegration_YardPick_TheBackorderIsVisibleOnTheOrderSurfaces(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	var cust string
	if err := pool.QueryRow(context.Background(), `SELECT customer_id::text FROM orders WHERE id = $1`, ord).Scan(&cust); err != nil {
		t.Fatal(err)
	}
	svc, _ := editSvc(t, pool)
	ctx := identity.ContextWithStaff(context.Background(), edIntManagerCtx())

	view, err := svc.OrderRead(ctx, ord)
	if err != nil || view.HasBackorder || view.Lines[0].BackorderEntered != "0.0000" {
		t.Fatalf("a whole order has no backorder: %+v %v", view, err)
	}
	list, err := svc.ListOrders(ctx, cust, Filter{Limit: 10})
	if err != nil || len(list) != 1 || list[0].HasBackorder {
		t.Fatalf("and its list entry says so: %+v %v", list, err)
	}

	in := yardPick("3", "CONDENSED_SKIDS")
	in.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, in); err != nil {
		t.Fatal(err)
	}

	view, err = svc.OrderRead(ctx, ord)
	if err != nil || !view.HasBackorder || view.Lines[0].BackorderEntered != "2.0000" || view.Lines[0].BackorderBase != "2.0000" || view.Lines[0].QtyEntered != "3.0000" {
		t.Fatalf("the staff read carries the flag and the line's backorder beside the quantity to fulfil: %+v %v", view, err)
	}
	list, err = svc.ListOrders(ctx, cust, Filter{Limit: 10})
	if err != nil || len(list) != 1 || !list[0].HasBackorder {
		t.Fatalf("the customer's list says the order has a backorder: %+v %v", list, err)
	}
	d, err := svc.GetOrder(ctx, cust, ord)
	if err != nil || !d.HasBackorder || d.Lines[0].BackorderEntered != "2.0000" {
		t.Fatalf("and the customer's detail carries the line's backorder: %+v %v", d, err)
	}
}
