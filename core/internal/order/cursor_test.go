//go:build integration

// cursor_test.go, the order list cursor leg, the billing invoice cursor's sibling (row 804).
//
// ListOrders orders created_at DESC, order_no ASC, but its cursor predicate compared the row
// (created_at, order_no) < (cursor created, cursor no), a descending comparison on both keys: a page
// ending past the first order of a timestamp skipped every same-timestamp order with a greater
// number. This leg plants three orders at ONE created_at with numbers ...100, ...101, ...102, pages
// from the first order's cursor pair, and requires the next page to carry the two greater numbers.
//
// Run: DATABASE_URL=... go test -tags=integration ./internal/order/... -run OrderListCursor -count=1
package order

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestIntegration_OrderListCursor_SameTimestampGreaterNumbers(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()

	// A per-run tenant namespace keeps orders.order_no (UNIQUE) collision free across runs on a
	// shared gate. The tiebreak digits ride the shared prefix, so the text comparison of the
	// numbers is the numeric comparison: ...100 < ...101 < ...102.
	n := fmt.Sprintf("ordcur-%d", time.Now().UnixNano()%1_000_000_000)
	var cust string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name) VALUES ($1, 'Order cursor leg tenant') RETURNING id::text`,
		n).Scan(&cust); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM order_lines WHERE order_id IN (SELECT id FROM orders WHERE customer_id = $1)`, cust)
		_, _ = pool.Exec(c, `DELETE FROM orders WHERE customer_id = $1`, cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, cust)
	})

	// One fixed instant for all three rows: rows written by one transaction share created_at, so
	// this is the shape a real page boundary lands on.
	stamp := time.Date(1990, 3, 15, 12, 0, 0, 0, time.UTC)
	for _, no := range []string{"100", "101", "102"} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents, created_at, updated_at)
			 VALUES ($1, $2, $3, 'CONFIRMED', 'PICKUP', 1000, 1000, $4::timestamptz, $4::timestamptz)`,
			n+"-"+no, cust, trentonBranch, stamp); err != nil {
			t.Fatalf("seed order %s: %v", no, err)
		}
	}

	// Page one: the three in tiebreak order, and the cursor is the FIRST row's pair.
	page1, err := repo.ListOrders(ctx, cust, Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("page 1 = %d orders, want the 3 planted in this tenant", len(page1))
	}
	for i, want := range []string{"100", "101", "102"} {
		if page1[i].OrderNo != n+"-"+want {
			t.Fatalf("page 1[%d] = %q, want %q (created_at DESC, order_no ASC)", i, page1[i].OrderNo, n+"-"+want)
		}
	}

	// Page two from the first order's pair: the same-timestamp orders with a GREATER number. The
	// buggy predicate wants both keys strictly less, so it drops exactly these rows (and would
	// re-serve lower ones); the fixed two-part predicate keeps created_at = cursor AND order_no >
	// cursor number.
	cursorCreated := page1[0].CreatedAt.Format(time.RFC3339Nano)
	cursorOrderNo := page1[0].OrderNo
	page2, err := repo.ListOrders(ctx, cust, Filter{
		Limit:         50,
		AfterCursor:   true,
		CursorCreated: cursorCreated,
		CursorOrderNo: cursorOrderNo,
	})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	got := make([]string, 0, len(page2))
	for _, o := range page2 {
		got = append(got, o.OrderNo)
	}
	want := []string{n + "-101", n + "-102"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("cursor at (created_at %s, order_no %s): next page = %v, want [%s %s]; the page lost the same-timestamp orders with the greater order numbers %s and %s",
			cursorCreated, cursorOrderNo, got, want[0], want[1], n+"-101", n+"-102")
	}
}
