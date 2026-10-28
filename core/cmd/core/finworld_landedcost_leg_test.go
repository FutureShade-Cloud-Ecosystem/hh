//go:build finworld && integration

package main

// The landed-cost legs (W2-1b driver round 3): the plan's LANDED_COST events post through the
// production landed-cost HTTP routes on the same mux every other document rides, manager guard
// included. Leg one walks a short world that applies one landed cost and voids one and asserts the
// documents, their GL legs and the guard. Leg two applies one in the month after its receipt.

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/db"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// finworldLandedCostSchedule is the plan's first landed-cost application, the first void, and the
// first application that lands in the month after its receipt.
type finworldLandedCostSchedule struct {
	FirstApplyDay  int
	FirstApplyKey  string
	FirstApplyCad  int64
	FirstVoidDay   int
	FirstVoidKey   string
	FirstVoidOfKey string
	FirstVoidOfCad int64
	CrossMonthDay  int
	CrossMonthKey  string
	CrossMonthCad  int64
	CrossReceiptDt string
}

func finworldReadLandedCostSchedule(t *testing.T) *finworldLandedCostSchedule {
	t.Helper()
	plan, err := FinWorldPlan(1, YearMonth{Year: 2026, Month: 9}, "Quarryfen Hardscape Supply")
	if err != nil {
		t.Fatalf("FinWorldPlan: %v", err)
	}
	sched := &finworldLandedCostSchedule{FirstApplyDay: -1, FirstVoidDay: -1, CrossMonthDay: -1}
	receiptDate := map[string]string{}
	for _, d := range finworldFlattenDays(plan) {
		for _, ev := range d.Day.Events {
			if ev.Kind == FinWorldEventPurchaseReceipt {
				receiptDate[ev.PurchaseReceipt.PurchaseReceiptKey] = ev.Date
			}
			if ev.Kind != FinWorldEventLandedCost {
				continue
			}
			lc := ev.LandedCost
			switch {
			case lc.Status == "POSTED" && sched.FirstApplyDay < 0:
				sched.FirstApplyDay = d.Day.TradingDayIndex
				sched.FirstApplyKey = lc.LandedCostKey
				// ChargeCadCents carries freight+duty; the document's payable adds handling.
				sched.FirstApplyCad = lc.ChargeCadCents + lc.HandlingCents
			case lc.Status == "VOIDED" && sched.FirstVoidDay < 0:
				sched.FirstVoidDay = d.Day.TradingDayIndex
				sched.FirstVoidKey = lc.LandedCostKey
				sched.FirstVoidOfKey = lc.VoidOfLandedCostKey
				sched.FirstVoidOfCad = lc.ChargeCadCents + lc.HandlingCents
			case lc.Status == "POSTED" && sched.CrossMonthDay < 0:
				if rd, ok := receiptDate[lc.PurchaseReceiptKey]; ok && rd[:7] != ev.Date[:7] {
					sched.CrossMonthDay = d.Day.TradingDayIndex
					sched.CrossMonthKey = lc.LandedCostKey
					sched.CrossMonthCad = lc.ChargeCadCents + lc.HandlingCents
					sched.CrossReceiptDt = rd
				}
			}
		}
	}
	if sched.FirstApplyDay < 0 || sched.FirstVoidDay < 0 || sched.CrossMonthDay < 0 {
		t.Fatalf("plan carries no landed cost schedule: %+v", sched)
	}
	return sched
}

// finworldWalkDays runs migrate, seed and the given trading days as fresh subprocesses under the
// evening-guard clock, one process per simulated day.
func finworldWalkDays(t *testing.T, binPath, dsn string, throughDay int) {
	t.Helper()
	loc := houseday.DefaultZone().Location()
	plan, err := FinWorldPlan(1, YearMonth{Year: 2026, Month: 9}, "Quarryfen Hardscape Supply")
	if err != nil {
		t.Fatalf("FinWorldPlan: %v", err)
	}
	for _, d := range finworldFlattenDays(plan) {
		if d.Day.TradingDayIndex > throughDay {
			break
		}
		dayDate, err := time.ParseInLocation("2006-01-02", d.Day.Date, loc)
		if err != nil {
			t.Fatalf("parse trading day %s: %v", d.Day.Date, err)
		}
		target := time.Date(dayDate.Year(), dayDate.Month(), dayDate.Day(), 12, 0, 0, 0, loc)
		shift := finworldSetDayClock(t, finworldTestFaketimeFile(t), target)
		if d.Day.TradingDayIndex == 0 {
			finworldRunSubprocess(t, binPath, dsn, shift, nil, "migrate")
			finworldRunSubprocess(t, binPath, dsn, shift, nil, "seed")
		}
		finworldRunSubprocess(t, binPath, dsn, shift, nil, "finworld", "day", "--day", strconv.Itoa(d.Day.TradingDayIndex))
	}
}

func TestFinWorldDriver_LandedCostApplyVoidLeg(t *testing.T) {
	ctx := context.Background()
	dsn := finworldTestDSN(t)

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect finworld db: %v", err)
	}
	defer pool.Close()

	finworldResetSchema(t, ctx, pool)
	binPath := finworldBuildBinary(t)
	sched := finworldReadLandedCostSchedule(t)

	finworldWalkDays(t, binPath, dsn, sched.FirstVoidDay)

	// The applied document carries the event's cad charge and a posted journal.
	var applyID, applyStatus string
	var applyCad int64
	if err := pool.QueryRow(ctx, `
		SELECT id::text, status, freight_cad_cents + duty_cad_cents + handling_cad_cents
		  FROM landed_costs WHERE po_ref = $1`, sched.FirstApplyKey).Scan(&applyID, &applyStatus, &applyCad); err != nil {
		t.Fatalf("read applied landed cost %s: %v", sched.FirstApplyKey, err)
	}
	if applyStatus != "POSTED" {
		t.Fatalf("landed cost %s status = %q, want POSTED", sched.FirstApplyKey, applyStatus)
	}
	if applyCad != sched.FirstApplyCad {
		t.Fatalf("landed cost %s cad charge = %d, want %d", sched.FirstApplyKey, applyCad, sched.FirstApplyCad)
	}

	// The apply journal charges the full cad charge against freight payable 2100.
	var applyCr2100, applyDr int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN a.code = '2100' THEN COALESCE(l.credit_cents, 0) ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN a.code != '2100' THEN COALESCE(l.debit_cents, 0) ELSE 0 END), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND j.status = 'POSTED'`, "landedcost.apply:"+applyID).Scan(&applyCr2100, &applyDr); err != nil {
		t.Fatalf("read apply journal for %s: %v", sched.FirstApplyKey, err)
	}
	if applyCr2100 != sched.FirstApplyCad || applyDr != sched.FirstApplyCad {
		t.Fatalf("apply journal for %s: CR 2100=%d DR rest=%d, want both %d", sched.FirstApplyKey, applyCr2100, applyDr, sched.FirstApplyCad)
	}

	// The voided document is VOIDED and its reversing journal debits the payable back.
	var voidOfID, voidOfStatus string
	if err := pool.QueryRow(ctx, `SELECT id::text, status FROM landed_costs WHERE po_ref = $1`, sched.FirstVoidOfKey).Scan(&voidOfID, &voidOfStatus); err != nil {
		t.Fatalf("read voided landed cost %s: %v", sched.FirstVoidOfKey, err)
	}
	if voidOfStatus != "VOIDED" {
		t.Fatalf("landed cost %s status = %q, want VOIDED", sched.FirstVoidOfKey, voidOfStatus)
	}
	var voidDr2100, voidCr int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(CASE WHEN a.code = '2100' THEN COALESCE(l.debit_cents, 0) ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN a.code != '2100' THEN COALESCE(l.credit_cents, 0) ELSE 0 END), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1 AND j.status = 'POSTED'`, "landedcost.void:"+voidOfID).Scan(&voidDr2100, &voidCr); err != nil {
		t.Fatalf("read void journal for %s: %v", sched.FirstVoidOfKey, err)
	}
	if voidDr2100 != sched.FirstVoidOfCad || voidCr != sched.FirstVoidOfCad {
		t.Fatalf("void journal for %s: DR 2100=%d CR rest=%d, want both %d", sched.FirstVoidOfKey, voidDr2100, voidCr, sched.FirstVoidOfCad)
	}

	// The routes are manager guarded: a frontline clerk is refused closed.
	h, err := newFinWorldHarness(ctx, pool)
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	clerkTok, err := h.minter.MintAt(finworldClerkSub, 12*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("mint clerk token: %v", err)
	}
	_, guardErr := h.callWithToken(clerkTok, "clerk landed cost", http.MethodPost, "/api/v1/landed-costs", map[string]any{
		"branch_id": finworldBranchTRT,
	})
	if guardErr == nil || !strings.Contains(guardErr.Error(), "403") {
		t.Fatalf("clerk create landed cost: want HTTP 403 refusal, got %v", guardErr)
	}

	// The manager role the driver links for approvals reads the same surface.
	if _, err := h.callWithToken(h.managerToken, "manager list landed costs", http.MethodGet, "/api/v1/landed-costs?branch_id="+finworldBranchTRT, nil); err != nil {
		t.Fatalf("manager landed-cost read refused: %v", err)
	}
}

// TestFinWorldDriver_APTieCoversLandedCost pins the AP tie's read: with a landed cost applied and
// outstanding, GL 2100's credit is the vendor bills less supplier payments PLUS the landed-cost
// payable, so the tie's subledger read must carry both.
func TestFinWorldDriver_APTieCoversLandedCost(t *testing.T) {
	ctx := context.Background()
	dsn := finworldTestDSN(t)

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect finworld db: %v", err)
	}
	defer pool.Close()

	finworldResetSchema(t, ctx, pool)
	binPath := finworldBuildBinary(t)
	sched := finworldReadLandedCostSchedule(t)

	finworldWalkDays(t, binPath, dsn, sched.FirstApplyDay)

	plan, err := FinWorldPlan(1, YearMonth{Year: 2026, Month: 9}, "Quarryfen Hardscape Supply")
	if err != nil {
		t.Fatalf("FinWorldPlan: %v", err)
	}
	ties, err := runFinWorldTies(ctx, pool, plan, sched.FirstApplyDay)
	if err != nil {
		t.Fatalf("run ties: %v", err)
	}
	var ap *FinWorldTieResult
	for i := range ties {
		if ties[i].Name == "AP_2100_ties_to_vendor_subledger" {
			ap = &ties[i]
		}
	}
	if ap == nil {
		t.Fatalf("tie suite carries no AP_2100_ties_to_vendor_subledger")
	}
	if !ap.Passed {
		t.Fatalf("AP tie refused with a landed cost outstanding: %s", ap.Detail)
	}
	t.Logf("AP tie with a landed cost outstanding: %s", ap.Detail)
}
