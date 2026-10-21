//go:build integration

// seqtrigger_drawbeforelock_integration_test.go: the draw before lock leg for count_sheets (PR 556, round 3c).
//
// The sheet's single row writers (freeze, count, approve, cancel, post) used to draw the cycle_counts counter
// in a CTE and then lock the row. A writer that relies on the sync seq trigger takes the row first and the
// counter after. Two such writers on one sheet crossed. The writers now rely on the trigger too.
//
// The leg parks the trigger relying writer (a plain touch of the row) between its row lock and its draw with a
// BEFORE INSERT trigger on scope_sequences (testkit.DrawPark), then runs the real CancelSheet on the same
// sheet. Red at 099f2e27: CancelSheet draws the counter, waits for the row, the park releases, the touch wants
// the counter, 40P01.
package invcount

import (
	"context"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestIntegration_DrawBeforeLock_CancelSheetBesideASheetWrite(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	sheet, err := repo.CreateSheet(context.Background(), branchTRT, "FULL", "")
	if err != nil {
		t.Fatalf("create sheet: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM count_sheets WHERE id = $1`, sheet.ID) })

	park := testkit.NewDrawPark(t, pool, "cycle_counts", 55010001)
	touch := park.TouchRow("count_sheets", "updated_at = now()", sheet.ID)
	park.WaitParked("the sheet touch stands between its row lock and its draw")

	cancel := make(chan error, 1)
	go func() { cancel <- repo.CancelSheet(context.Background(), sheet.ID) }()
	park.WaitBlocked("the cancel waits")

	park.Answers(map[string]chan error{"touch": touch, "cancel": cancel})
}
