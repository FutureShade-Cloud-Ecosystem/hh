//go:build integration

package billing

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// An account-level reminder with no open invoice falls back to today's date for its due date, and
// that day is the dealer's: between the UTC day change and the house zone's own the session's
// CURRENT_DATE is a day the yard has not reached, which shifts the aging bucket the send files under.
func TestIntegrationARReminderContext_NoInvoiceFallbackIsTheHouseDay(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	var branchID, customerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type='BRANCH' LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no seeded BRANCH: %v", err)
	}
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id)
		 VALUES ('HDAY-'||$1, 'House Day '||$1, 'CONTRACTOR', $2::uuid) RETURNING id::text`,
		suffix, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1::uuid`, customerID)
	})
	rc, err := NewARLandingRepository(pool).ReminderContextFor(ctx, customerID, "", ChannelEmail, AllBranches)
	if err != nil {
		t.Fatalf("reminder context: %v", err)
	}
	if got, want := rc.DueDate.Format("2006-01-02"), houseday.Today(nil); got != want {
		t.Fatalf("the no-invoice due date is the house zone's day: got %s, want %s", got, want)
	}
}
