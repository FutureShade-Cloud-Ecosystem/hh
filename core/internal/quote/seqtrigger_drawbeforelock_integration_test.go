//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock leg for quotes (PR 556, round 3c). The
// customer side status transition used to draw the quotes counter in a CTE and then lock the quote row; a
// writer that relies on the sync seq trigger takes the row first. The transition now relies on the trigger too.
// The trigger relying writer (a plain touch) is parked between its row lock and its draw with a BEFORE INSERT
// trigger on scope_sequences (testkit.DrawPark); the real transition runs on the same quote. Red at 099f2e27:
// the transition draws the counter, waits for the row, the park releases, the touch wants the counter, 40P01.
// After the fix the quote's seq still moved: the trigger drew it (the row's seq is read before and after).
package quote

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_DrawBeforeLock_QuoteTransitionBesideAQuoteWrite(t *testing.T) {
	pool := intPool(t)
	cust, quoteID, _, _ := seedQuote(t, pool, "SENT", nil)
	_, repo, _ := intSvc(t, pool, time.Now())
	var before int64
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(seq, 0) FROM quotes WHERE id = $1::uuid`, quoteID).Scan(&before); err != nil {
		t.Fatalf("read seq: %v", err)
	}

	park := testkit.NewDrawPark(t, pool, "quotes", 55010007)
	touch := park.TouchRow("quotes", "updated_at = now()", quoteID)
	park.WaitParked("the quote touch stands between its row lock and its draw")

	reject := make(chan error, 1)
	go func() {
		_, err := repo.Reject(context.Background(), cust, quoteID)
		reject <- err
	}()
	park.WaitBlocked("the transition waits")

	park.Answers(map[string]chan error{"touch": touch, "reject": reject})

	var after int64
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT COALESCE(seq, 0), status FROM quotes WHERE id = $1::uuid`, quoteID).Scan(&after, &status); err != nil {
		t.Fatalf("read seq: %v", err)
	}
	if status != "LOST" || after <= before {
		t.Fatalf("after the transition the quote reads status %q seq %d (was %d): the trigger must have drawn", status, after, before)
	}
}
