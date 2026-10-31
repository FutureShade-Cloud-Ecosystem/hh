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
	"context"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
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

// The job is scheduled by name: migration 0477 seeds the row the worker registers, DISABLED, so a
// Controller opts in (the job raises customer facing invoices on its own).
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
	if kind != "NIGHTLY" || minute != 540 || enabled {
		t.Fatalf("W3-FC1b red: the schedule is %s at minute %d enabled=%v, want nightly 09:00 UTC, seeded off", kind, minute, enabled)
	}
}

// While the row is disabled the worker never runs the job, even when it is due; enabling the row makes
// the next tick run it and charge. The sweep is the real one over the rig's own branch (so no other
// leg's customers are charged), with the clock at the house midnight after the month.
func TestIntegration_W3FC1B_EnablingTheJobMakesTheWorkerRun(t *testing.T) {
	r := fcRigNew(t)
	cust := r.customer(r.branch, "ENABLE")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "ENABLE")

	// Put the row back exactly as it was, and drop the runs this leg made.
	var wasEnabled bool
	var wasNext, wasLast *time.Time
	var wasStatus, wasErr string
	if err := r.pool.QueryRow(r.ctx, `SELECT enabled, next_run_at, last_run_at, last_status, last_error FROM job_schedules WHERE name = $1`, FinanceChargeJobName).
		Scan(&wasEnabled, &wasNext, &wasLast, &wasStatus, &wasErr); err != nil {
		t.Fatalf("W3-FC1b red: no schedule row named %q: %v", FinanceChargeJobName, err)
	}
	if wasEnabled {
		t.Fatalf("W3-FC1b red: the row is enabled before the leg started, it must seed disabled")
	}
	// The newest run on record before the leg (none on a fresh database): the leg's own runs are the later ones.
	var baseline time.Time
	if err := r.pool.QueryRow(r.ctx, `SELECT COALESCE(MAX(created_at), TIMESTAMPTZ 'epoch') FROM job_runs WHERE job_name = $1`, FinanceChargeJobName).Scan(&baseline); err != nil {
		t.Fatalf("W3-FC1b red: read the job's run baseline: %v", err)
	}
	t.Cleanup(func() {
		fcDelete(t, r.pool, "job_runs", `DELETE FROM job_runs WHERE job_name = $1 AND created_at > $2`, FinanceChargeJobName, baseline)
		fcDelete(t, r.pool, "job_schedules", `UPDATE job_schedules SET enabled = $2, next_run_at = $3, last_run_at = $4, last_status = $5, last_error = $6, updated_at = now() WHERE name = $1`,
			FinanceChargeJobName, wasEnabled, wasNext, wasLast, wasStatus, wasErr)
	})
	// Nothing else may be due, or the tick would run it too.
	var other int
	if err := r.pool.QueryRow(r.ctx, `SELECT COUNT(*) FROM job_schedules WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= now() AND name <> $1`, FinanceChargeJobName).Scan(&other); err != nil || other != 0 {
		t.Fatalf("W3-FC1b red: %d other schedules are due on the shared database (%v), the leg cannot tick in isolation", other, err)
	}

	w := r.writer(func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, fc1Loc()) })
	runner := jobs.NewRunner(jobs.NewRepository(r.pool), nil).Register(FinanceChargeJobName, func(ctx context.Context) (jobs.Result, error) {
		sweep, err := w.runDueFinanceCharges(ctx, []string{r.branch})
		return jobs.Result{RowsRead: int64(sweep.Branches), RowsWritten: int64(sweep.Charged)}, err
	})

	// Due, but disabled: the tick runs nothing.
	fcDelete(t, r.pool, "job_schedules", `UPDATE job_schedules SET next_run_at = now() - interval '1 hour' WHERE name = $1`, FinanceChargeJobName)
	if n := runner.RunDue(r.ctx); n != 0 {
		t.Fatalf("W3-FC1b red: a disabled job ran %d times", n)
	}
	if rows := r.chargeRows(r.branch); len(rows) != 0 {
		t.Fatalf("W3-FC1b red: the disabled job charged %v", rows)
	}

	// Enabled (the operator's one statement): the next tick runs it and the customer is charged.
	fcDelete(t, r.pool, "job_schedules", `UPDATE job_schedules SET enabled = TRUE WHERE name = $1`, FinanceChargeJobName)
	if n := runner.RunDue(r.ctx); n != 1 {
		t.Fatalf("W3-FC1b red: the enabled job ran %d times, want 1", n)
	}
	if rows := r.chargeRows(r.branch); rows[cust] != 1_500 {
		t.Fatalf("W3-FC1b red: the enabled job's charge rows = %v, want 1500 for %s", rows, cust)
	}
	var status string
	var written int64
	if err := r.pool.QueryRow(r.ctx, `SELECT status, rows_written FROM job_runs WHERE job_name = $1 AND created_at > $2 ORDER BY created_at DESC LIMIT 1`, FinanceChargeJobName, baseline).Scan(&status, &written); err != nil || status != "SUCCEEDED" || written != 1 {
		t.Fatalf("W3-FC1b red: the run record = %s, %d written (%v), want SUCCEEDED with 1", status, written, err)
	}
}
