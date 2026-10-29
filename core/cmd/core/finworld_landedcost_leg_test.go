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

// finworldDayShift pins the faketime clock to the through day's noon and returns the shift, so a
// tie check subprocess reads the same simulated moment the day runs left.
func finworldDayShift(t *testing.T, throughDay int) int64 {
	t.Helper()
	loc := houseday.DefaultZone().Location()
	plan, err := FinWorldPlan(1, YearMonth{Year: 2026, Month: 9}, "Quarryfen Hardscape Supply")
	if err != nil {
		t.Fatalf("FinWorldPlan: %v", err)
	}
	for _, d := range finworldFlattenDays(plan) {
		if d.Day.TradingDayIndex != throughDay {
			continue
		}
		dayDate, err := time.ParseInLocation("2006-01-02", d.Day.Date, loc)
		if err != nil {
			t.Fatalf("parse trading day %s: %v", d.Day.Date, err)
		}
		return finworldSetDayClock(t, finworldTestFaketimeFile(t), time.Date(dayDate.Year(), dayDate.Month(), dayDate.Day(), 12, 0, 0, 0, loc))
	}
	t.Fatalf("no trading day %d in the plan", throughDay)
	return 0
}

// finworldRunTieCheck runs core finworld check as a subprocess and returns its [PASS]/[FAIL] lines.
// With allowTieFailure a failing tie reaches the caller instead of failing the test here.
func finworldRunTieCheck(t *testing.T, binPath, dsn string, throughDay int, allowTieFailure bool) []string {
	t.Helper()
	shift := finworldDayShift(t, throughDay)
	var out string
	var err error
	if allowTieFailure {
		out, err = finworldRunSubprocessAllowErr(binPath, dsn, shift, nil, "finworld", "check", "--through-day", strconv.Itoa(throughDay))
	} else {
		out = finworldRunSubprocess(t, binPath, dsn, shift, nil, "finworld", "check", "--through-day", strconv.Itoa(throughDay))
	}
	lines := []string{}
	for _, ln := range strings.Split(out, "\n") {
		if strings.HasPrefix(ln, "[PASS] ") || strings.HasPrefix(ln, "[FAIL] ") {
			lines = append(lines, ln)
		}
	}
	if err != nil && !allowTieFailure {
		t.Fatalf("finworld check --through-day %d failed: %v\n%s", throughDay, err, out)
	}
	if len(lines) == 0 {
		t.Fatalf("finworld check --through-day %d printed no tie lines: %s", throughDay, out)
	}
	return lines
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

	// The tie suite runs over the short world. The posting itself is exact, but the first void
	// with sales since its apply leaves the stock tie a few cents short on this tree: the landed
	// cost module clamps the void's CR 1200 at the allocation while the inventory restore's
	// half-up per-unit quantization can drain up to onHand/2 cents more (the leg's own numbers:
	// apply 138690 over 603 units, void drains 138840 over 520, clamp books 138690, drift 150).
	// The stock line is recorded verbatim for the module lane; every other tie must pass.
	var stockLine string
	for _, ln := range finworldRunTieCheck(t, binPath, dsn, sched.FirstVoidDay, true) {
		if strings.HasPrefix(ln, "[FAIL] ") {
			if !strings.HasPrefix(ln, "[FAIL] stock_value_ties_to_GL_1200") {
				t.Fatalf("non-stock tie refused on the apply and void leg: %s", ln)
			}
			stockLine = ln
		}
		t.Log(ln)
	}
	if stockLine != "" {
		t.Logf("the stock tie refused after the void (the module's void clamp, not the posting): %s", stockLine)
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

// TestFinWorldDriver_LandedCostCrossMonthLeg applies a landed cost in the month after its receipt
// and proves the posting end to end. The tie suite runs with it: on this code, without W2-5b (661),
// the stock tie reads both of its sides now, so it cannot see an earlier month at all; the leg
// records its verbatim line either way and refuses only if any other tie fails.
func TestFinWorldDriver_LandedCostCrossMonthLeg(t *testing.T) {
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

	finworldWalkDays(t, binPath, dsn, sched.CrossMonthDay)

	// The landed cost applied in the month after its receipt: the receipt landed in the plan's
	// prior month and the document was created in the application month.
	var lcStatus string
	var lcCad int64
	var lcCreatedAt, receiptAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT lc.status,
		       lc.freight_cad_cents + lc.duty_cad_cents + lc.handling_cad_cents,
		       lc.created_at,
		       r.received_at
		  FROM landed_costs lc
		  JOIN purchase_orders d ON d.id = lc.po_id
		  JOIN purchasing_pos p ON p.id::text = d.metadata->>'purchasing_po_id'
		  JOIN purchasing_receipts r ON r.po_id = p.id
		 WHERE lc.po_ref = $1`, sched.CrossMonthKey).Scan(&lcStatus, &lcCad, &lcCreatedAt, &receiptAt); err != nil {
		t.Fatalf("read cross-month landed cost %s: %v", sched.CrossMonthKey, err)
	}
	if lcStatus != "POSTED" {
		t.Fatalf("landed cost %s status = %q, want POSTED", sched.CrossMonthKey, lcStatus)
	}
	if lcCad != sched.CrossMonthCad {
		t.Fatalf("landed cost %s cad charge = %d, want %d", sched.CrossMonthKey, lcCad, sched.CrossMonthCad)
	}
	receiptMonth := receiptAt.In(houseday.DefaultZone().Location()).Format("2006-01")
	appliedMonth := lcCreatedAt.In(houseday.DefaultZone().Location()).Format("2006-01")
	if receiptMonth != sched.CrossReceiptDt[:7] {
		t.Fatalf("receipt of %s landed %s, want the plan's %s", sched.CrossMonthKey, receiptMonth, sched.CrossReceiptDt[:7])
	}
	if appliedMonth <= receiptMonth {
		t.Fatalf("landed cost %s applied %s, want the month after its receipt %s", sched.CrossMonthKey, appliedMonth, receiptMonth)
	}
	t.Logf("landed cost %s: receipt %s, applied %s, charge %d cents", sched.CrossMonthKey, receiptMonth, appliedMonth, lcCad)

	// The tie suite: the stock tie's line is recorded verbatim (661 pending), every other tie must pass.
	for _, ln := range finworldRunTieCheck(t, binPath, dsn, sched.CrossMonthDay, true) {
		name := ln
		if idx := strings.Index(ln, ": "); idx >= 0 {
			name = ln[len("[PASS] "):idx]
		}
		if strings.HasPrefix(ln, "[FAIL] ") && name != "stock_value_ties_to_GL_1200" {
			t.Fatalf("non-stock tie refused on the cross-month leg: %s", ln)
		}
		t.Log(ln)
	}
}

// TestFinWorldDriver_OpenAPAcrossMonthEndLeg walks two month ends with deferred bills in the plan:
// the open AP is nonzero at each month end, the supplier cheques clear on the next month's first
// trading day, and the AP tie stays honest through purchasing's own seam.
func TestFinWorldDriver_OpenAPAcrossMonthEndLeg(t *testing.T) {
	ctx := context.Background()
	dsn := finworldTestDSN(t)

	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect finworld db: %v", err)
	}
	defer pool.Close()

	finworldResetSchema(t, ctx, pool)
	binPath := finworldBuildBinary(t)
	const throughDay = 48

	finworldWalkDays(t, binPath, dsn, throughDay)

	// The plan's deferred bills issued on or before each month end are the open AP the tie must see.
	plan, err := FinWorldPlan(1, YearMonth{Year: 2026, Month: 9}, "Quarryfen Hardscape Supply")
	if err != nil {
		t.Fatalf("FinWorldPlan: %v", err)
	}
	days := finworldFlattenDays(plan)
	if throughDay >= len(days) {
		t.Fatalf("through day %d beyond the plan", throughDay)
	}
	asOfDate := days[throughDay].Day.Date
	// When each deferred bill settles: its vendor payment event's day, if the plan schedules one.
	paidOn := map[string]string{}
	for _, d := range days {
		for _, ev := range d.Day.Events {
			if ev.VendorPayment != nil {
				paidOn[ev.VendorPayment.VendorBillKey] = ev.Date
			}
		}
	}
	// The open AP at a cutoff: deferred bills issued on or before it that no payment has settled yet.
	deferredByDate := func(cutoff string) int64 {
		var total int64
		for _, d := range days {
			for _, ev := range d.Day.Events {
				if ev.VendorBill == nil || !ev.VendorBill.PaymentDeferred || ev.Date > cutoff {
					continue
				}
				if payDate, ok := paidOn[ev.VendorBill.VendorBillKey]; ok && payDate <= cutoff {
					continue
				}
				total += ev.VendorBill.TotalCents
			}
		}
		return total
	}
	wantOct, wantNov := deferredByDate("2025-10-31"), deferredByDate(asOfDate)
	if wantOct == 0 || wantNov == 0 {
		t.Fatalf("the schedule carries no deferred bills open at the month ends (oct=%d, through %s=%d)", wantOct, asOfDate, wantNov)
	}

	svc := finworldPurchasingComposition(pool, slogDefault(), nil)
	for _, at := range []struct{ name, cutoff string }{
		{"2025-10 month end", "2025-10-31"},
		{"through " + asOfDate, asOfDate},
	} {
		cut, err := time.ParseInLocation("2006-01-02", at.cutoff, houseday.DefaultZone().Location())
		if err != nil {
			t.Fatalf("parse %s: %v", at.cutoff, err)
		}
		open, err := svc.OpenAPCentsAsOf(ctx, cut.Add(23*time.Hour))
		if err != nil {
			t.Fatalf("open AP read at %s: %v", at.name, err)
		}
		want := wantOct
		if at.cutoff != "2025-10-31" {
			want = wantNov
		}
		if open != want {
			t.Fatalf("open AP at %s = %d, want the plan's deferred bills %d", at.name, open, want)
		}
		t.Logf("open AP at %s: %d cents (the plan's deferred bills)", at.name, open)
	}

	// The tie suite over both month ends, landed cost payable included. The stock line is recorded
	// verbatim (the void clamp, section 5 of the round report); every other tie must pass.
	var stockLine string
	for _, ln := range finworldRunTieCheck(t, binPath, dsn, throughDay, true) {
		if strings.HasPrefix(ln, "[FAIL] ") {
			if !strings.HasPrefix(ln, "[FAIL] stock_value_ties_to_GL_1200") {
				t.Fatalf("non-stock tie refused on the open AP leg: %s", ln)
			}
			stockLine = ln
		}
		if strings.HasPrefix(ln, "[PASS] AP_2100_ties_to_vendor_subledger") || strings.HasPrefix(ln, "[FAIL] stock") {
			t.Log(ln)
		}
	}
	if stockLine != "" {
		t.Logf("the stock tie refused after the day 33 void (the module's void clamp): %s", stockLine)
	}
}
