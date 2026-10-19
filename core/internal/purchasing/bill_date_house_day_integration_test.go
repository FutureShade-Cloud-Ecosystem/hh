//go:build integration

package purchasing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// A bill entered without a bill date is dated the dealer's day. Between the UTC day change and the
// house zone's own, the database session's CURRENT_DATE is the next day: the bill would be dated
// a day the yard has not reached, and age and pay a day off.
func TestIntegration_InsertBill_DefaultBillDateIsTheHouseDay(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var branch string
	if err := h.pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branch); err != nil {
		t.Fatalf("no seeded BRANCH: %v", err)
	}
	repo := NewRepository(h.pool)
	id, err := repo.InsertBill(ctx, nil, insertBillParams{
		BillNo: fmt.Sprintf("HOUSEDAY-%d", time.Now().UnixNano()), VendorID: h.vendor, BranchID: branch,
		Match: "UNMATCHED", Currency: "CAD", FXRate: 1,
	})
	if err != nil {
		t.Fatalf("insert bill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM purchasing_bills WHERE id = $1::uuid`, id)
	})
	var got string
	if err := h.pool.QueryRow(ctx, `SELECT to_char(bill_date,'YYYY-MM-DD') FROM purchasing_bills WHERE id = $1::uuid`, id).Scan(&got); err != nil {
		t.Fatalf("read bill date: %v", err)
	}
	if want := houseday.Today(nil); got != want {
		t.Fatalf("a bill with no date is dated the house zone's day: got %s, want %s", got, want)
	}
}

// The service decides the dealer's day before its transaction opens and binds it: a bill with no date
// is dated that day, whatever the process zone and the Ontario default say (item 3b slice 3).
func TestIntegration_InsertBill_DefaultBillDateIsTheDayTheServiceBound(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var branch string
	if err := h.pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branch); err != nil {
		t.Fatalf("no seeded BRANCH: %v", err)
	}
	repo := NewRepository(h.pool)
	id, err := repo.InsertBill(ctx, nil, insertBillParams{
		BillNo: fmt.Sprintf("ZONEDAY-%d", time.Now().UnixNano()), VendorID: h.vendor, BranchID: branch,
		Match: "UNMATCHED", Currency: "CAD", FXRate: 1, HouseDay: "2020-02-29",
	})
	if err != nil {
		t.Fatalf("insert bill: %v", err)
	}
	t.Cleanup(func() {
		_, _ = h.pool.Exec(context.Background(), `DELETE FROM purchasing_bills WHERE id = $1::uuid`, id)
	})
	var got string
	if err := h.pool.QueryRow(ctx, `SELECT to_char(bill_date,'YYYY-MM-DD') FROM purchasing_bills WHERE id = $1::uuid`, id).Scan(&got); err != nil {
		t.Fatalf("read bill date: %v", err)
	}
	if got != "2020-02-29" {
		t.Fatalf("a bill with no date is dated the day the service bound: got %s, want 2020-02-29", got)
	}
}
