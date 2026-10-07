//go:build integration

// The yard's skid count on a real order: recorded, changeable with a reason, the deposit following
// the count, and every change kept.

package order

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func seedSkidOrder(t *testing.T, pool *pgxpool.Pool, status string, ratePerSkid int64) string {
	t.Helper()
	ord := seedYardOrder(t, pool, status)
	if ratePerSkid > 0 {
		// The rate is the office's to set; no endpoint sets it yet, so the test plants it.
		if _, err := pool.Exec(context.Background(), `
INSERT INTO order_skids (order_id, skid_count, deposit_per_skid_cents, deposit_cents)
VALUES ($1, 3, $2::bigint, 3 * $2::bigint)`, ord, ratePerSkid); err != nil {
			t.Fatalf("plant skids: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_skid_changes WHERE order_id = $1`, ord)
		_, _ = pool.Exec(c, `DELETE FROM order_skids WHERE order_id = $1`, ord)
	})
	return ord
}

func skidIn(ord string, n int, reason string) YardSkidInput {
	return YardSkidInput{OrderID: ord, ActorID: "3b175000-0000-4000-9000-000000000002", SkidCount: n, Reason: reason, Note: "yard count"}
}

func TestIntegration_YardSkids_FirstRecordHasNoBefore(t *testing.T) {
	pool := intPool(t)
	ord := seedSkidOrder(t, pool, "READY", 0)
	svc, _ := editSvc(t, pool)
	out, err := svc.RecordYardSkids(context.Background(), skidIn(ord, 4, "COUNT_CORRECTION"))
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if out.CountBefore != nil || out.CountAfter != 4 || !out.Changed || out.DepositAfterCents != 0 {
		t.Fatalf("no count was recorded before, so there is no before, and no rate means no deposit: %+v", out)
	}
	if out.Order.OrderNo == "" || out.Order.CustomerID == "" {
		t.Fatalf("the notice facts ride along: %+v", out.Order)
	}
}

func TestIntegration_YardSkids_ChangeAdjustsTheDepositAndKeepsHistory(t *testing.T) {
	pool := intPool(t)
	ord := seedSkidOrder(t, pool, "READY", 5000)
	svc, _ := editSvc(t, pool)
	svc.WithInvoiceStanding(fixedStanding{InvoiceStanding{Found: true, InvoiceNo: "#INV-7", TotalCents: 5650, PaidCents: 5650}})
	out, err := svc.RecordYardSkids(context.Background(), skidIn(ord, 2, "CONDENSED_SKIDS"))
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	if out.CountBefore == nil || *out.CountBefore != 3 || out.CountAfter != 2 ||
		out.DepositBeforeCents != 15000 || out.DepositAfterCents != 10000 {
		t.Fatalf("three skids at 50.00 become two: %+v", out)
	}
	if out.Order.OwedBackCents != 5000 || out.Order.DueCents != 0 || out.Order.PaidCents != 5650 {
		t.Fatalf("on a paid order the lost skid's deposit is owed back: %+v", out.Order)
	}
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM order_skid_changes WHERE order_id = $1`, ord).Scan(&n); err != nil || n != 1 {
		t.Fatalf("one history row for one change, got %d (%v)", n, err)
	}

	up, err := svc.RecordYardSkids(context.Background(), skidIn(ord, 3, "COUNT_CORRECTION"))
	if err != nil {
		t.Fatalf("raise: %v", err)
	}
	if up.Order.DueCents != 5000 || up.Order.OwedBackCents != 0 {
		t.Fatalf("a skid added back is a deposit due: %+v", up.Order)
	}
}

func TestIntegration_YardSkids_SameCountAgainChangesNothing(t *testing.T) {
	pool := intPool(t)
	ord := seedSkidOrder(t, pool, "READY", 5000)
	svc, _ := editSvc(t, pool)
	out, err := svc.RecordYardSkids(context.Background(), skidIn(ord, 3, "COUNT_CORRECTION"))
	if err != nil {
		t.Fatalf("same: %v", err)
	}
	if out.Changed {
		t.Fatal("the same count is not a change and must not be told to anyone")
	}
	var n int
	_ = pool.QueryRow(context.Background(), `SELECT count(*) FROM order_skid_changes WHERE order_id = $1`, ord).Scan(&n)
	if n != 0 {
		t.Fatalf("no history for a non-change, got %d", n)
	}
}

func TestIntegration_YardSkids_RefusalsAndStatuses(t *testing.T) {
	pool := intPool(t)
	svc, _ := editSvc(t, pool)
	ctx := context.Background()
	ord := seedSkidOrder(t, pool, "READY", 0)
	if _, err := svc.RecordYardSkids(ctx, skidIn(ord, -1, "DAMAGED")); err == nil {
		t.Fatal("a negative count is not a count")
	}
	for _, status := range []string{"DRAFT", "FULFILLED", "INVOICED", "CLOSED", "CANCELLED"} {
		o := seedSkidOrder(t, pool, status, 0)
		var ne *NotEditableError
		if _, err := svc.RecordYardSkids(ctx, skidIn(o, 2, "DAMAGED")); !errors.As(err, &ne) {
			t.Fatalf("a %s order must refuse a skid change, got %v", status, err)
		}
	}
	if _, err := svc.RecordYardSkids(ctx, skidIn("3b175000-0000-4000-9000-0000000000ff", 2, "DAMAGED")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown order is not found, got %v", err)
	}
}
