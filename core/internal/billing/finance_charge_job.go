package billing

// finance_charge_job.go, wave 3 FC-1b: THE WORKER'S PRESS OF THE FINANCE CHARGE RUN.
//
// -- THE SCHEDULE -----------------------------------------------------------------------
// Migration 0477 seeds the nightly row at 09:00 UTC. Each night the sweep reads the dealer's house day
// and presses the month BEFORE the dealer's current month, for every branch. 09:00 UTC is 05:00 under
// Eastern daylight time and 04:00 under standard time, so it always lands after the Toronto house
// midnight, in summer and in winter and across both clock changes; it is also after the house midnight
// of every North American zone from Newfoundland to the Pacific coast (the latest, Pacific standard
// time, is 08:00 UTC). A dealer further west (Alaska, Hawaii) sees the sweep arrive before its midnight
// on the first, and the catch-up window below makes that a day's delay, never a missed month.
//
// -- THE MONTH CANNOT BE EARLY ------------------------------------------------------------
// The month pressed is derived from the dealer's day, so it is always a month that has ended; the run's
// own next-house-day guard (RunFinanceCharges) stands behind that and refuses any month not yet over.
// On the last house day at 23:59 the sweep's previous month is the month before the one ending, never
// the one about to end.
//
// -- THE CATCH-UP WINDOW ----------------------------------------------------------------------
// The sweep presses only on the first financeChargeCatchUpDays house days of the new month. A worker
// that was down on the first, or a dealer west of the schedule, still presses on the second or third
// night. Past the window the sweep does nothing: every press after the first is a replay (the run's
// claims are spent), except for a customer whose waiver the Controller cleared after the first, who
// would otherwise be charged by a night job weeks after the month closed. That customer is charged by
// the Controller pressing the month by hand, a deliberate act.
//
// -- IDEMPOTENT AND SAFE UNDER TWO INSTANCES -------------------------------------------------
// Each branch is its own run on its own transaction under the run's advisory lock on the branch and
// month, with FC-1's claim per customer and month behind it: a second sweep, or a second instance, finds
// every claim spent and charges nothing. The jobs runner also claims a schedule row with FOR UPDATE
// SKIP LOCKED, so two instances rarely overlap at all. One failing branch does not stop the others; the
// sweep counts it and the job reports it.

import (
	"context"
	"fmt"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/jobs"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// FinanceChargeJobName is the schedule row migration 0477 seeds and the worker registers.
const FinanceChargeJobName = "billing.finance_charge_run"

// financeChargeCatchUpDays is how many house days of the new month the sweep may still press the
// month before it.
const financeChargeCatchUpDays = 3

// FinanceChargeSweep is one sweep's answer.
type FinanceChargeSweep struct {
	// Month is the house month the sweep pressed (empty when it was skipped).
	Month string
	// Skipped is true outside the catch-up window: nothing was read or pressed.
	Skipped bool
	// Branches is how many branches were pressed; Failed is how many of those failed.
	Branches int
	Failed   int
	// Charged and ChargedCents are what this sweep charged; AlreadyCharged is what earlier presses had.
	Charged        int
	AlreadyCharged int
	ChargedCents   int64
}

// RunDueFinanceCharges is the worker's sweep over every branch.
func (w *InvoiceWriter) RunDueFinanceCharges(ctx context.Context) (FinanceChargeSweep, error) {
	return w.runDueFinanceCharges(ctx, nil)
}

// runDueFinanceCharges presses the previous house month for the named branches, or every branch when
// none are named (tests name their own, so a sweep never charges another leg's branch).
func (w *InvoiceWriter) runDueFinanceCharges(ctx context.Context, only []string) (FinanceChargeSweep, error) {
	if w.pool == nil {
		return FinanceChargeSweep{}, errNoDB
	}
	zone, err := houseday.Resolve(ctx, w.zone)
	if err != nil {
		return FinanceChargeSweep{}, err
	}
	today := w.now().In(zone.Location())
	if today.Day() > financeChargeCatchUpDays {
		return FinanceChargeSweep{Skipped: true}, nil
	}
	prev := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, zone.Location()).AddDate(0, -1, 0)
	month := prev.Format("2006-01")

	branches := only
	if branches == nil {
		if branches, err = w.financeChargeBranches(ctx); err != nil {
			return FinanceChargeSweep{}, err
		}
	}
	sweep := FinanceChargeSweep{Month: month}
	for _, b := range branches {
		sweep.Branches++
		res, err := w.RunFinanceCharges(ctx, FinanceChargeRequest{BranchID: b, Month: month})
		if err != nil {
			sweep.Failed++
			continue
		}
		sweep.Charged += res.Charged
		sweep.AlreadyCharged += res.AlreadyCharged
		sweep.ChargedCents += res.ChargedCents
	}
	if sweep.Failed > 0 {
		return sweep, fmt.Errorf("billing: the finance charge sweep for %s failed on %d of %d branches", month, sweep.Failed, sweep.Branches)
	}
	return sweep, nil
}

// FinanceChargeJob builds the worker's JobFunc. The caller wraps the context in the system identity the
// jobs runner uses, as every sweep does.
func FinanceChargeJob(w *InvoiceWriter) jobs.JobFunc {
	return func(ctx context.Context) (jobs.Result, error) {
		sweep, err := w.RunDueFinanceCharges(ctx)
		return jobs.Result{
			RowsRead:    int64(sweep.Branches),
			RowsWritten: int64(sweep.Charged),
			Skipped:     sweep.Skipped,
			Detail: map[string]any{
				"month": sweep.Month, "charged": sweep.Charged, "charged_cents": sweep.ChargedCents,
				"already_charged": sweep.AlreadyCharged, "failed": sweep.Failed,
			},
		}, err
	}
}
