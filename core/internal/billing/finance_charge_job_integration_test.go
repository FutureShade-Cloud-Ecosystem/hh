//go:build integration

// finance_charge_job_integration_test.go, wave 3 FC-1b: the worker's press of the previous month's
// finance charge run. The sweep runs on the house calendar: it presses the month before the dealer's
// current month, only inside the first house days of the new month (a catch-up window, so a worker
// that was down on the first still presses it, and a Controller who clears a waiver late in the month
// is not charged for a month long closed), for every branch, idempotent by the run's own key and safe
// under two instances.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1B_Worker
package billing

import (
	"sync"
	"testing"
	"time"
)

// fcSweep runs the sweep over the rig's own branches (the shared gate database holds other legs'
// branches a test must never charge) at the clock named.
func (r *fcRig) sweep(now time.Time, branches ...string) (FinanceChargeSweep, error) {
	return r.writer(func() time.Time { return now }).runDueFinanceCharges(r.ctx, branches)
}

// The house midnight is the boundary: at 23:59 on the month's last house day the sweep is outside the
// window and presses nothing (and so never September); at the midnight after it, September runs.
func TestIntegration_W3FC1B_WorkerPressesOnTheHouseMidnightAfterTheMonth(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "WORKER")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "WORKER")

	early, err := r.sweep(time.Date(2026, 9, 30, 23, 59, 0, 0, fc1Loc()), r.branch)
	if err != nil || !early.Skipped || early.Charged != 0 || early.Month != "" {
		t.Fatalf("W3-FC1b red: the sweep at 23:59 on the last house day = %+v, %v, want it skipped with nothing charged", early, err)
	}
	if rows := r.chargeRows(r.branch); len(rows) != 0 {
		t.Fatalf("W3-FC1b red: the early sweep charged %v", rows)
	}
	if n := r.chargeInvoiceCount(r.branch); n != 0 {
		t.Fatalf("W3-FC1b red: the early sweep raised %d charge documents", n)
	}

	on, err := r.sweep(time.Date(2026, 10, 1, 0, 0, 0, 0, fc1Loc()), r.branch)
	if err != nil || on.Month != fc1Month || on.Charged != 1 || on.ChargedCents != 1_500 || on.Failed != 0 {
		t.Fatalf("W3-FC1b red: the sweep at the house midnight = %+v, %v, want September, 1 charge of 1500", on, err)
	}
	if rows := r.chargeRows(r.branch); rows[cust] != 1_500 {
		t.Fatalf("W3-FC1b red: the charge rows after the sweep = %v, want %d for %s", rows, 1_500, cust)
	}

	// Every later night of the window replays: nothing is charged twice.
	again, err := r.sweep(time.Date(2026, 10, 2, 5, 0, 0, 0, fc1Loc()), r.branch)
	if err != nil || again.Charged != 0 || again.AlreadyCharged != 1 {
		t.Fatalf("W3-FC1b red: the sweep the next night = %+v, %v, want 1 already charged and none new", again, err)
	}
	if n := r.chargeInvoiceCount(r.branch); n != 1 {
		t.Fatalf("W3-FC1b red: %d charge documents after two sweeps, want 1", n)
	}
}

// The catch-up window is the first three house days: a sweep on the fourth does nothing at all.
func TestIntegration_W3FC1B_WorkerCatchUpWindowIsTheFirstThreeHouseDays(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "WINDOW")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "WINDOW")

	late, err := r.sweep(time.Date(2026, 10, 4, 5, 0, 0, 0, fc1Loc()), r.branch)
	if err != nil || !late.Skipped || late.Charged != 0 || late.Branches != 0 {
		t.Fatalf("W3-FC1b red: the sweep on the fourth house day = %+v, %v, want it skipped", late, err)
	}
	if rows := r.chargeRows(r.branch); len(rows) != 0 {
		t.Fatalf("W3-FC1b red: the late sweep charged %v", rows)
	}
	last, err := r.sweep(time.Date(2026, 10, 3, 23, 59, 0, 0, fc1Loc()), r.branch)
	if err != nil || last.Skipped || last.Charged != 1 {
		t.Fatalf("W3-FC1b red: the sweep on the third house day = %+v, %v, want 1 charge", last, err)
	}
}

// Every branch: the sweep presses each branch's own books, and the branch list it walks holds the
// branches of the books.
func TestIntegration_W3FC1B_WorkerPressesEveryBranch(t *testing.T) {
	r := fcRigNew(t)
	a := r.customer(r.branch, "EVERYA")
	b := r.customer(r.other, "EVERYB")
	r.tradeInvoice(a, r.branch, fc1Due31, 100_000, "EVERYA")
	r.tradeInvoice(b, r.other, fc1Due31, 200_000, "EVERYB")

	got, err := r.writer(nil).financeChargeBranches(r.ctx)
	if err != nil {
		t.Fatalf("W3-FC1b red: the branch list: %v", err)
	}
	seen := map[string]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen[r.branch] || !seen[r.other] {
		t.Fatalf("W3-FC1b red: the branch list %v misses the rig's branches", got)
	}

	res, err := r.sweep(time.Date(2026, 10, 1, 0, 0, 0, 0, fc1Loc()), r.branch, r.other)
	if err != nil || res.Branches != 2 || res.Charged != 2 || res.ChargedCents != 1_500+3_000 {
		t.Fatalf("W3-FC1b red: the sweep over two branches = %+v, %v, want 2 charges of 4500 in all", res, err)
	}
	if r.chargeRows(r.branch)[a] != 1_500 || r.chargeRows(r.other)[b] != 3_000 {
		t.Fatalf("W3-FC1b red: each branch's charge is not its own: %v %v", r.chargeRows(r.branch), r.chargeRows(r.other))
	}
}

// Two worker instances sweeping at the same moment: one run. The charges in both answers add up to
// the one customer.
func TestIntegration_W3FC1B_TwoWorkerInstancesChargeOnce(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "TWO")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "TWO")
	now := time.Date(2026, 10, 1, 0, 0, 0, 0, fc1Loc())

	var wg sync.WaitGroup
	results := make([]FinanceChargeSweep, 2)
	errs := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = r.sweep(now, r.branch)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("W3-FC1b red: instance %d: %v", i, err)
		}
	}
	if got := results[0].Charged + results[1].Charged; got != 1 {
		t.Fatalf("W3-FC1b red: the two instances charged %d in all (%+v), want 1", got, results)
	}
	if n := r.chargeInvoiceCount(r.branch); n != 1 {
		t.Fatalf("W3-FC1b red: %d charge documents after two instances, want 1", n)
	}
}

// The job is scheduled by name: migration 0477 seeds the row the worker registers.
func TestIntegration_W3FC1B_WorkerJobIsScheduled(t *testing.T) {
	r := fcRigNew(t)
	var kind string
	var minute int
	var enabled bool
	if err := r.pool.QueryRow(r.ctx,
		`SELECT kind, nightly_minute, enabled FROM job_schedules WHERE name = $1`, FinanceChargeJobName,
	).Scan(&kind, &minute, &enabled); err != nil {
		t.Fatalf("W3-FC1b red: no schedule row named %q: %v", FinanceChargeJobName, err)
	}
	if kind != "NIGHTLY" || minute != 540 || !enabled {
		t.Fatalf("W3-FC1b red: the schedule is %s at minute %d enabled=%v, want nightly 09:00 UTC, on", kind, minute, enabled)
	}
}
