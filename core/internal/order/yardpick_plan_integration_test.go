//go:build integration

// A short pick on an order with a shipment plan (migration 0126): the groups must still partition the
// order line, so the shortfall is taken from the unshipped groups, newest first, never from a group
// that has gone out, and a correction puts it back. Never a raw constraint error.

package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// plan builds groups on order line 1 from (status, qty) pairs, in group order.
func plan(t *testing.T, pool *pgxpool.Pool, ord string, groups ...struct {
	status string
	qty    string
}) {
	t.Helper()
	ctx := context.Background()
	var lineID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM order_lines WHERE order_id = $1 AND line_no = 1`, ord).Scan(&lineID); err != nil {
		t.Fatal(err)
	}
	// The partition is a deferred constraint: the whole plan goes in one transaction.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for i, g := range groups {
		var gid string
		if err := tx.QueryRow(ctx, `
INSERT INTO shipment_groups (order_id, group_no, status, completed_at)
VALUES ($1, $2, $3, CASE WHEN $3 = 'COMPLETE' THEN now() END) RETURNING id::text`, ord, i+1, g.status).Scan(&gid); err != nil {
			t.Fatalf("group: %v", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base) VALUES ($1, $2, $3::numeric)`, gid, lineID, g.qty); err != nil {
			t.Fatalf("group line: %v", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("plan: %v", err)
	}
}

func groupQtys(t *testing.T, pool *pgxpool.Pool, ord string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
SELECT COALESCE(sgl.qty_base::text, 'none') FROM shipment_groups g
  LEFT JOIN shipment_group_lines sgl ON sgl.shipment_group_id = g.id
 WHERE g.order_id = $1 ORDER BY g.group_no`, ord)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var q string
		_ = rows.Scan(&q)
		out = append(out, q)
	}
	return out
}

type pg = struct {
	status string
	qty    string
}

func TestIntegration_YardPick_ShortOnAPlannedOrderShrinksTheUnshippedGroupsAndKeepsThePartition(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	plan(t, pool, ord, pg{"COMPLETE", "3"}, pg{"PLANNED", "2"})
	svc, _ := editSvc(t, pool)
	ctx := context.Background()

	short := yardPick("4", "NOT_IN_YARD")
	short.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, short); err != nil {
		t.Fatalf("a short on a planned order must record, not fail on the partition: %v", err)
	}
	if got := groupQtys(t, pool, ord); len(got) != 2 || got[0] != "3.0000" || got[1] != "1.0000" {
		t.Fatalf("the shortfall comes off the unshipped group and the shipped one is untouched: %v", got)
	}

	// Below what has already gone out is refused in words, not by the constraint.
	deep := yardPick("2", "NOT_IN_YARD")
	deep.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, deep); !errors.Is(err, ErrLineInUse) {
		t.Fatalf("the part already shipped cannot be taken back, got %v", err)
	}
	if got := groupQtys(t, pool, ord); got[0] != "3.0000" || got[1] != "1.0000" {
		t.Fatalf("a refused pick leaves the plan alone: %v", got)
	}

	// The yard finds the rest: it goes back on the unshipped group.
	restore := yardPick("5", "")
	restore.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, restore); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := groupQtys(t, pool, ord); got[0] != "3.0000" || got[1] != "2.0000" {
		t.Fatalf("a correction restores the unshipped group: %v", got)
	}
}

func TestIntegration_YardPick_AZeroPickEmptiesTheUnshippedGroupAndRestoreRefillsIt(t *testing.T) {
	pool := intPool(t)
	ord := seedYardOrder(t, pool, "CONFIRMED")
	plan(t, pool, ord, pg{"PLANNED", "5"})
	svc, _ := editSvc(t, pool)
	ctx := context.Background()
	zero := yardPick("0", "NOT_IN_YARD")
	zero.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, zero); err != nil {
		t.Fatalf("zero: %v", err)
	}
	if got := groupQtys(t, pool, ord); len(got) != 1 || got[0] != "none" {
		t.Fatalf("an unshipped truck with nothing left on it is empty, not deleted: %v", got)
	}
	back := yardPick("5", "")
	back.OrderID = ord
	if _, err := svc.ApplyYardPicks(ctx, back); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := groupQtys(t, pool, ord); got[0] != "5.0000" {
		t.Fatalf("the restore refills the truck: %v", got)
	}
}

// A dispatch that has not committed yet holds the group's row. A short pick that read the group as
// READY before that must not shrink a truck that is leaving: it waits on the group, re-reads its
// status under the lock, and either refuses (nothing else unshipped to give) or takes another group.
func TestIntegration_YardPick_ADispatchInFlightIsNotShrunk(t *testing.T) {
	for _, tc := range []struct {
		name    string
		groups  []pg
		pick    string
		refused bool
	}{
		{"nothing else to give", []pg{{"READY", "5"}}, "2", true},
		{"another unshipped group gives", []pg{{"PLANNED", "2"}, {"READY", "3"}}, "4", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := intPool(t)
			ord := seedYardOrder(t, pool, "CONFIRMED")
			plan(t, pool, ord, tc.groups...)
			svc, _ := editSvc(t, pool)
			ctx := context.Background()

			// The dispatch click: an UPDATE of the READY group, left open.
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, err := tx.Exec(ctx, `UPDATE shipment_groups SET status = 'DISPATCHED', dispatched_at = now()
				WHERE order_id = $1 AND status = 'READY'`, ord); err != nil {
				t.Fatal(err)
			}

			short := yardPick(tc.pick, "NOT_IN_YARD")
			short.OrderID = ord
			done := make(chan error, 1)
			go func() { _, err := svc.ApplyYardPicks(ctx, short); done <- err }()
			select {
			case err := <-done:
				t.Fatalf("the short must wait on the group the dispatch holds, but returned %v", err)
			case <-time.After(400 * time.Millisecond):
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("dispatch commit: %v", err)
			}
			err = <-done
			if tc.refused != errors.Is(err, ErrLineInUse) || (!tc.refused && err != nil) {
				t.Fatalf("refused=%v want, got %v", tc.refused, err)
			}
			got := groupQtysByStatus(t, pool, ord)
			if tc.refused && got["DISPATCHED"] != "5.0000" || !tc.refused && (got["DISPATCHED"] != "3.0000" || got["PLANNED"] != "1.0000") {
				t.Fatalf("the dispatched truck keeps what it left with: %v", got)
			}
		})
	}
}

func groupQtysByStatus(t *testing.T, pool *pgxpool.Pool, ord string) map[string]string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
SELECT g.status, COALESCE(SUM(sgl.qty_base), 0)::text FROM shipment_groups g
  LEFT JOIN shipment_group_lines sgl ON sgl.shipment_group_id = g.id WHERE g.order_id = $1 GROUP BY g.status`, ord)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var s, q string
		_ = rows.Scan(&s, &q)
		out[s] = q
	}
	return out
}
