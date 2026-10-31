//go:build integration

package purchasing

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The W3 FX-2 world: open USD bills in a past house month, revalued at the month end rate.
//
//	bill A: USD 1,000.00 at 1.35, posted inside the month, unpaid. CAD carried 135,000.
//	bill B: USD 800.00 at 1.30, posted inside the month, part paid USD 300.00 at 1.28
//	        inside the month (2100 clears 39,000, the pro rata CAD share). CAD carried
//	        65,000, doc open USD 500.00.
//	bill C: a CAD bill posted inside the month, never revalued.
//
// The month under test is a PAST month, so the run's begun judgement and the reversal's
// next house day judgement both hold on the real clock the legs run at.
const (
	fx2Month     = "2026-09"
	fx2MonthEnd  = "2026-09-30"
	fx2NextDay   = "2026-10-01"
	fx2RunRate   = 1.40
	fx2RunDiff   = 10_000 // revalued 210,000 less carried 200,000
	fx2BillARate = 1.35
	fx2BillBCent = 80_000
	fx2BillBRate = 1.30
)

// fx2Loc is the dealer's zone the legs run in.
func fx2Loc() *time.Location {
	loc, err := time.LoadLocation("America/Toronto")
	if err != nil {
		panic(err)
	}
	return loc
}

// fx2Instant is an instant inside the month under test, in the dealer's zone.
func fx2Instant(day, hour, min int) time.Time {
	return time.Date(2026, 9, day, hour, min, 0, 0, fx2Loc())
}

// fx2Wipe removes every revaluation run record and journal in either test branch, before the
// world is posted and again after the test ends, so a rerun against the same database starts
// clean. The records go first (they point at the journals), the lines before their headers, and
// every delete fails the test naming its table.
func fx2Wipe(t *testing.T, rig *apAgingTestRig) {
	t.Helper()
	bg := context.Background()
	for _, step := range []struct{ table, sql string }{
		{"ap_revaluation_runs", `DELETE FROM ap_revaluation_runs`},
		{"gl_journal_lines", `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key LIKE 'purchasing.ap.revaluation:%')`},
		{"gl_journals", `DELETE FROM gl_journals WHERE idempotency_key LIKE 'purchasing.ap.revaluation:%'`},
	} {
		if _, err := rig.pool.Exec(bg, step.sql); err != nil {
			t.Errorf("cleanup: delete from %s: %v", step.table, err)
		}
	}
}

// fx2Rig is the aging rig with the revaluation journals' cleanup registered.
func fx2Rig(t *testing.T) *apAgingTestRig {
	t.Helper()
	rig := newAPAgingRig(t)
	rig.ensurePeriod(t, 2026, 9)
	rig.ensurePeriod(t, 2026, 10)
	fx2Wipe(t, rig)
	t.Cleanup(func() { fx2Wipe(t, rig) })
	return rig
}

// fx2PostUSDBillInBranch is postUSDBillAt with the branch named, so the zero difference
// leg keeps its own world in the second branch.
func fx2PostUSDBillInBranch(t *testing.T, rig *apAgingTestRig, tag, branch, billDate, dueDate string, usdCents int64, fxRate float64, postedAt time.Time) BillDetail {
	t.Helper()
	if d, err := time.Parse("2006-01-02", billDate); err == nil {
		rig.ensurePeriod(t, d.Year(), int(d.Month()))
	}
	prod := rig.newProduct(t, tag)
	po, err := rig.svc.CreatePO(rig.ctx, CreatePORequest{
		VendorID: rig.vendor,
		BranchID: branch,
		Currency: "USD",
		FXRate:   fxRate,
		Lines:    []AddLineParams{{ProductID: prod, QtyOrderedBase: 1, UOM: "EA", UnitCostCents: usdCents}},
	})
	if err != nil {
		t.Fatalf("CreatePO(%s): %v", tag, err)
	}
	rig.cleanupPO(t, po.ID)
	if _, err := rig.svc.Transition(rig.ctx, po.ID, StatusOrdered); err != nil {
		t.Fatalf("Transition PO(%s): %v", tag, err)
	}
	lineID := po.Lines[0].ID
	if _, err := rig.svc.Receive(rig.ctx, po.ID, ReceiveRequest{
		Lines: []ReceiveLineParams{{POLineID: lineID, QtyBase: 1}},
	}); err != nil {
		t.Fatalf("Receive PO(%s): %v", tag, err)
	}
	created, err := rig.svc.CreateBill(rig.ctx, CreateBillRequest{
		BillNo:   billNo(tag),
		POID:     po.ID,
		BillDate: billDate,
		DueDate:  dueDate,
		Currency: "USD",
		FXRate:   fxRate,
		Lines:    []BillLineParams{{POLineID: lineID, QtyBilledBase: 1, UnitCostCents: usdCents}},
	})
	if err != nil {
		t.Fatalf("CreateBill(%s): %v", tag, err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE source = 'PAYMENT' AND source_ref_id = $1::uuid)`, created.ID)
		_, _ = rig.pool.Exec(bg, `DELETE FROM gl_journals WHERE source = 'PAYMENT' AND source_ref_id = $1::uuid`, created.ID)
	})
	posted, err := rig.svc.PostBill(rig.ctx, created.ID, PostBillRequest{AcceptVariance: true, Note: "W3 FX-2 test bill"})
	if err != nil {
		t.Fatalf("PostBill(%s): %v", tag, err)
	}
	if !postedAt.IsZero() && posted.GLJournalID != "" {
		if _, err := rig.pool.Exec(rig.ctx, `UPDATE gl_journals SET posted_at = $1 WHERE id = $2`, postedAt, posted.GLJournalID); err != nil {
			t.Fatalf("stamp bill journal posted_at: %v", err)
		}
	}
	return posted
}

// fx2World posts the three bills of the month under test and returns them.
func fx2World(t *testing.T, rig *apAgingTestRig) (billA, billB, billC BillDetail) {
	t.Helper()
	billA = fx2PostUSDBillInBranch(t, rig, "w3fx2-a", branchTRT, "2026-09-10", "2026-09-25", 100_000, fx2BillARate, fx2Instant(10, 9, 0))
	billB = fx2PostUSDBillInBranch(t, rig, "w3fx2-b", branchTRT, "2026-09-12", "2026-10-05", fx2BillBCent, fx2BillBRate, fx2Instant(12, 9, 0))
	// The part payment inside the month: USD 300.00 at 1.28 clears the pro rata CAD share
	// 104000 * 30000 / 80000 = 39,000, cash 38,400, a 600 cent realised gain.
	if _, err := rig.svc.PayBill(rig.ctx, billB.ID, PayBillRequest{
		AmountCents: 30_000, FXRate: 1.28, PaidAt: fx2Instant(20, 10, 0),
		Note: "part payment inside the month", IdempotencyKey: "w3fx2-b-pay-" + billB.ID,
	}); err != nil {
		t.Fatalf("part pay bill B: %v", err)
	}
	_, billC = rig.createReceivedPOAndPostBill(t, "w3fx2-c", 50_000, "2026-09-10", "2026-09-25")
	if billC.GLJournalID != "" {
		if _, err := rig.pool.Exec(rig.ctx, `UPDATE gl_journals SET posted_at = $1 WHERE id = $2`, fx2Instant(10, 9, 0), billC.GLJournalID); err != nil {
			t.Fatalf("stamp the CAD bill's journal inside the month: %v", err)
		}
	}
	return billA, billB, billC
}

// fx2JournalLegs reads a journal by its full idempotency key, as map[code][2]int64{dr, cr}.
func fx2JournalLegs(t *testing.T, rig *apAgingTestRig, key string) map[string][2]int64 {
	t.Helper()
	rows, err := rig.pool.Query(rig.ctx, `
		SELECT a.code::text, COALESCE(l.debit_cents, 0), COALESCE(l.credit_cents, 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = $1
		   AND j.status = 'POSTED'`,
		key,
	)
	if err != nil {
		t.Fatalf("query journal legs (%s): %v", key, err)
	}
	defer rows.Close()
	legs := make(map[string][2]int64)
	for rows.Next() {
		var code string
		var dr, cr int64
		if err := rows.Scan(&code, &dr, &cr); err != nil {
			t.Fatalf("scan journal leg: %v", err)
		}
		cur := legs[code]
		cur[0] += dr
		cur[1] += cr
		legs[code] = cur
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("journal legs rows: %v", err)
	}
	if len(legs) == 0 {
		t.Fatalf("no POSTED journal found for key %q", key)
	}
	return legs
}

func fx2JournalCount(t *testing.T, rig *apAgingTestRig, key string) int {
	t.Helper()
	var n int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM gl_journals WHERE idempotency_key = $1`, key).Scan(&n); err != nil {
		t.Fatalf("count journals under %s: %v", key, err)
	}
	return n
}

// fx2BranchNetAcct reads the branch's net credit balance on a GL account (credits less
// debits across every journal stamped with the branch), the side the tie legs read.
func fx2BranchNetAcct(t *testing.T, rig *apAgingTestRig, branchID, code string) int64 {
	t.Helper()
	var net int64
	if err := rig.pool.QueryRow(rig.ctx, `
		SELECT COALESCE(SUM(COALESCE(l.credit_cents, 0) - COALESCE(l.debit_cents, 0)), 0)
		  FROM gl_journals j
		  JOIN gl_journal_lines l ON l.journal_id = j.id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.status = 'POSTED'
		   AND j.branch_id = $1::uuid
		   AND a.code = $2`,
		branchID, code).Scan(&net); err != nil {
		t.Fatalf("read branch %s net: %v", code, err)
	}
	return net
}

func TestIntegration_W3FX2_RevaluationRun(t *testing.T) {
	rig := fx2Rig(t)
	fx2World(t, rig)
	runKey := "purchasing.ap.revaluation:" + branchTRT + ":" + fx2Month

	t.Run("1_the_run_revalues_the_open_document_amount", func(t *testing.T) {
		run, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: fx2RunRate})
		if err != nil {
			t.Fatalf("RevalueAP: %v", err)
		}
		if run.Bills != 2 {
			t.Fatalf("W3-FX2 red: run revalued %d bills, want the 2 open USD bills (a stray bill leaked into the branch's month)", run.Bills)
		}
		if run.MonthEnd != fx2MonthEnd {
			t.Fatalf("W3-FX2 red: month end = %q, want %q", run.MonthEnd, fx2MonthEnd)
		}
		if run.DocOpenCents != 150_000 {
			t.Fatalf("W3-FX2 red: doc open = %d, want USD 1,500.00 (1,000 + 500)", run.DocOpenCents)
		}
		if run.CarriedCadCents != 200_000 {
			t.Fatalf("W3-FX2 red: carried CAD = %d, want 200,000 (135,000 + 65,000)", run.CarriedCadCents)
		}
		if run.RevaluedCadCents != 210_000 {
			t.Fatalf("W3-FX2 red: revalued CAD = %d, want 210,000 (USD 1,500.00 at 1.40)", run.RevaluedCadCents)
		}
		if run.DifferenceCents != fx2RunDiff {
			t.Fatalf("W3-FX2 red: difference = %d, want %d", run.DifferenceCents, fx2RunDiff)
		}
		if !run.Posted || run.JournalID == "" {
			t.Fatalf("W3-FX2 red: run did not post its journal (%+v)", run)
		}

		legs := fx2JournalLegs(t, rig, runKey)
		if legs["2100"] != ([2]int64{0, fx2RunDiff}) {
			t.Fatalf("W3-FX2 red: journal 2100 = %v, want CR %d (the liability grew at the month end rate)", legs["2100"], fx2RunDiff)
		}
		if legs["6315"] != ([2]int64{fx2RunDiff, 0}) {
			t.Fatalf("W3-FX2 red: journal FX = %v, want DR %d against 2100 on the unrealised account 6315", legs["6315"], fx2RunDiff)
		}

		var memo, postedAtUTC, fxMemo, apMemo string
		if err := rig.pool.QueryRow(rig.ctx, `
			SELECT j.memo,
			       to_char(j.posted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'),
			       (SELECT l.memo FROM gl_journal_lines l WHERE l.journal_id = j.id AND l.line_no = 1),
			       (SELECT l.memo FROM gl_journal_lines l WHERE l.journal_id = j.id AND l.line_no = 2)
			  FROM gl_journals j WHERE j.id = $1`, run.JournalID).Scan(&memo, &postedAtUTC, &apMemo, &fxMemo); err != nil {
			t.Fatalf("read the run journal: %v", err)
		}
		if memo != "AP revaluation "+fx2Month+" at 1.4" {
			t.Fatalf("W3-FX2 red: journal memo = %q", memo)
		}
		if apMemo != "AP revaluation "+fx2Month {
			t.Fatalf("W3-FX2 red: the 2100 leg's memo = %q", apMemo)
		}
		if fxMemo != "Unrealised FX "+fx2Month {
			t.Fatalf("W3-FX2 red: the FX leg's memo = %q, want the month's unrealised words", fxMemo)
		}
		// The month end instant is the last nanosecond of the last HOUSE day, which is
		// already the next UTC day in the dealer's zone.
		if postedAtUTC != "2026-10-01 03:59:59.999999" {
			t.Fatalf("W3-FX2 red: journal posted_at (UTC) = %q, want the month end instant 2026-10-01 03:59:59.999999 UTC", postedAtUTC)
		}
	})

	t.Run("2_idempotent_per_month_by_its_key", func(t *testing.T) {
		first, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: fx2RunRate})
		if err != nil {
			t.Fatalf("first run: %v", err)
		}
		replay, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: fx2RunRate})
		if err != nil {
			t.Fatalf("re-press under the same key and rate: %v", err)
		}
		if replay.JournalID != first.JournalID || !replay.Posted {
			t.Fatalf("W3-FX2 red: a re-press answered journal %q (first %q), want the posted run replayed", replay.JournalID, first.JournalID)
		}
		if n := fx2JournalCount(t, rig, runKey); n != 1 {
			t.Fatalf("W3-FX2 red: %d journals carry the month's key, want 1", n)
		}

		_, err = rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: 1.41})
		if err == nil {
			t.Fatal("W3-FX2 red: a different rate under the spent key must be refused")
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusConflict {
			t.Fatalf("W3-FX2 red: different rate under the spent key error = %v, want 409", err)
		}
		for _, want := range []string{"already revalued", "one key is one run"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("W3-FX2 red: refusal %q does not say %q", err.Error(), want)
			}
		}
		if n := fx2JournalCount(t, rig, runKey); n != 1 {
			t.Fatalf("W3-FX2 red: %d journals carry the month's key after the refused press, want 1", n)
		}
	})

	t.Run("3_the_rate_is_bounded_like_a_payment_rate", func(t *testing.T) {
		// The band is judged on the books the run would revalue, so its own month carries
		// its own bill: an August USD bill at 1.30, never revalued (the run is refused
		// before any journal exists).
		fx2PostUSDBillInBranch(t, rig, "w3fx2-band", branchKGN, "2026-08-20", "2026-09-20", fx2BillBCent, fx2BillBRate,
			time.Date(2026, 8, 20, 9, 0, 0, 0, fx2Loc()))
		_, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-08", FXRate: 138})
		if err == nil {
			t.Fatal("W3-FX2 red: a typed 138 for 1.38 must be refused")
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusBadRequest {
			t.Fatalf("W3-FX2 red: typed 138 error = %v, want 400", err)
		}
		for _, want := range []string{"more than 2x away from bill", "check it was typed right"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("W3-FX2 red: refusal %q does not say %q", err.Error(), want)
			}
		}
		// The band refuses the other side too: half a 1.30 bill's rate is as far away as
		// a hundredfold of it.
		if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-08", FXRate: 0.5}); err == nil {
			t.Fatal("W3-FX2 red: a rate half the bill's own must be refused")
		} else if !errors.As(err, &ce) || ce.status != http.StatusBadRequest {
			t.Fatalf("W3-FX2 red: half rate error = %v, want 400", err)
		}

		// The decimals refusal is judged before the books are read, even on a spent key.
		_, err = rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: 1.4000001})
		if err == nil {
			t.Fatal("W3-FX2 red: seven decimals must be refused")
		}
		if !errors.As(err, &ce) || ce.status != http.StatusBadRequest || !strings.Contains(err.Error(), "7 decimals") {
			t.Fatalf("W3-FX2 red: seven decimals error = %v, want 400 in words", err)
		}
	})

	t.Run("4_no_open_usd_bills_is_refused_in_words", func(t *testing.T) {
		_, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: fx2Month, FXRate: fx2RunRate})
		if err == nil {
			t.Fatal("W3-FX2 red: a branch with no open USD bills must be refused")
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusUnprocessableEntity {
			t.Fatalf("W3-FX2 red: nothing to revalue error = %v, want 422", err)
		}
		if !strings.Contains(err.Error(), "nothing to revalue") {
			t.Fatalf("W3-FX2 red: refusal %q does not say there is nothing to revalue", err.Error())
		}
	})

	t.Run("5_a_zero_difference_run_posts_nothing", func(t *testing.T) {
		// A single USD bill at its own rate, revalued at that rate: the open document
		// amount at the month end rate equals the CAD carried, so the run is complete and
		// posts no journal.
		fx2PostUSDBillInBranch(t, rig, "w3fx2-zero", branchKGN, "2026-09-15", "2026-10-15", 100_000, fx2BillARate, fx2Instant(15, 9, 0))
		run, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: fx2Month, FXRate: fx2BillARate})
		if err != nil {
			t.Fatalf("RevalueAP on the zero difference branch: %v", err)
		}
		if run.Bills != 1 || run.DifferenceCents != 0 || run.Posted || run.JournalID != "" {
			t.Fatalf("W3-FX2 red: zero difference run = %+v, want one bill, zero difference, no journal", run)
		}
		if n := fx2JournalCount(t, rig, "purchasing.ap.revaluation:"+branchKGN+":"+fx2Month); n != 0 {
			t.Fatalf("W3-FX2 red: %d journals carry the zero difference key, want 0", n)
		}
	})
}

func TestIntegration_W3FX2_ReversalOnTheNextHouseDay(t *testing.T) {
	rig := fx2Rig(t)
	fx2World(t, rig)
	if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: fx2RunRate}); err != nil {
		t.Fatalf("RevalueAP: %v", err)
	}
	runKey := "purchasing.ap.revaluation:" + branchTRT + ":" + fx2Month
	revKey := runKey + ":reverse"

	// fx2NetAcrossKeys sums a set of journals' legs per account, the net the pair reads.
	fx2NetAcrossKeys := func(keys ...string) map[string][2]int64 {
		t.Helper()
		rows, err := rig.pool.Query(rig.ctx, `
			SELECT a.code::text, COALESCE(SUM(COALESCE(l.debit_cents, 0)), 0), COALESCE(SUM(COALESCE(l.credit_cents, 0)), 0)
			  FROM gl_journals j
			  JOIN gl_journal_lines l ON l.journal_id = j.id
			  JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.idempotency_key = ANY($1)
			 GROUP BY a.code`,
			keys)
		if err != nil {
			t.Fatalf("query net across keys: %v", err)
		}
		defer rows.Close()
		out := make(map[string][2]int64)
		for rows.Next() {
			var code string
			var legs [2]int64
			if err := rows.Scan(&code, &legs[0], &legs[1]); err != nil {
				t.Fatalf("scan net: %v", err)
			}
			out[code] = legs
		}
		return out
	}

	t.Run("1_the_reversal_swaps_the_run_legs_on_the_next_house_day", func(t *testing.T) {
		rev, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchTRT, fx2Month)
		if err != nil {
			t.Fatalf("ReverseAPRevaluation: %v", err)
		}
		if rev.NextHouseDay != fx2NextDay {
			t.Fatalf("W3-FX2 red: next house day = %q, want %q", rev.NextHouseDay, fx2NextDay)
		}
		if rev.DifferenceCents != fx2RunDiff {
			t.Fatalf("W3-FX2 red: reversal difference = %d, want the run's %d", rev.DifferenceCents, fx2RunDiff)
		}
		if rev.ReversalJournalID == "" || rev.JournalID == "" {
			t.Fatalf("W3-FX2 red: the answer names no journals (%+v)", rev)
		}

		legs := fx2JournalLegs(t, rig, revKey)
		if legs["2100"] != ([2]int64{fx2RunDiff, 0}) {
			t.Fatalf("W3-FX2 red: reversal 2100 = %v, want DR %d (the run's credit swapped)", legs["2100"], fx2RunDiff)
		}
		if legs["6315"] != ([2]int64{0, fx2RunDiff}) {
			t.Fatalf("W3-FX2 red: reversal FX = %v, want CR %d on 6315", legs["6315"], fx2RunDiff)
		}

		var memo, postedAtUTC, periodMonth string
		if err := rig.pool.QueryRow(rig.ctx, `
			SELECT j.memo, to_char(j.posted_at AT TIME ZONE 'UTC', 'YYYY-MM-DD HH24:MI:SS.US'), to_char(fp.month, '00')
			  FROM gl_journals j
			  JOIN fiscal_periods fp ON fp.id = j.period_id
			 WHERE j.id = $1`, rev.ReversalJournalID).Scan(&memo, &postedAtUTC, &periodMonth); err != nil {
			t.Fatalf("read the reversal journal: %v", err)
		}
		if memo != "AP revaluation reversal "+fx2Month {
			t.Fatalf("W3-FX2 red: reversal memo = %q", memo)
		}
		// The next house day's first instant in the dealer's zone: October's midnight in
		// Toronto is four hours into the UTC day.
		if postedAtUTC != "2026-10-01 04:00:00.000000" {
			t.Fatalf("W3-FX2 red: reversal posted_at (UTC) = %q, want the next house day's first instant 2026-10-01 04:00:00 UTC", postedAtUTC)
		}
		if strings.TrimSpace(periodMonth) != "10" {
			t.Fatalf("W3-FX2 red: reversal filed in month %s of the period, want 10 (the next house day's month)", periodMonth)
		}

		// The revaluation and its reversal net to zero in every account.
		net := fx2NetAcrossKeys(runKey, revKey)
		if len(net) == 0 {
			t.Fatal("W3-FX2 red: no journals carry the run and reversal keys")
		}
		for code, legs := range net {
			if legs[0] != legs[1] {
				t.Fatalf("W3-FX2 red: the run and its reversal leave %s at DR %d CR %d, want them equal (net zero in every account)", code, legs[0], legs[1])
			}
		}
	})

	t.Run("2_the_reversal_replays_under_its_key", func(t *testing.T) {
		first, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchTRT, fx2Month)
		if err != nil {
			t.Fatalf("re-press the reversal: %v", err)
		}
		again, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchTRT, fx2Month)
		if err != nil {
			t.Fatalf("re-press the reversal again: %v", err)
		}
		if again.ReversalJournalID != first.ReversalJournalID {
			t.Fatalf("W3-FX2 red: a re-press answered reversal journal %q (first %q)", again.ReversalJournalID, first.ReversalJournalID)
		}
		if n := fx2JournalCount(t, rig, revKey); n != 1 {
			t.Fatalf("W3-FX2 red: %d journals carry the reversal key, want 1", n)
		}
	})

	t.Run("3_the_reversal_waits_for_the_next_house_day", func(t *testing.T) {
		rig.svc.WithClock(func() time.Time { return fx2Instant(29, 12, 0) })
		t.Cleanup(func() { rig.svc.WithClock(time.Now) })
		_, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchTRT, fx2Month)
		if err == nil {
			t.Fatal("W3-FX2 red: a reversal pressed before the next house day must be refused")
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusUnprocessableEntity {
			t.Fatalf("W3-FX2 red: early reversal error = %v, want 422", err)
		}
		for _, want := range []string{"has not begun", "next house day"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("W3-FX2 red: refusal %q does not say %q", err.Error(), want)
			}
		}
		// Restore the real clock and the reversal lands.
		rig.svc.WithClock(time.Now)
		if _, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchTRT, fx2Month); err != nil {
			t.Fatalf("the reversal on the real clock: %v", err)
		}
	})

	t.Run("4_no_run_journal_is_refused_in_words", func(t *testing.T) {
		_, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchKGN, fx2Month)
		if err == nil {
			t.Fatal("W3-FX2 red: reversing a month that was never revalued must be refused")
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusUnprocessableEntity {
			t.Fatalf("W3-FX2 red: nothing to reverse error = %v, want 422", err)
		}
		if !strings.Contains(err.Error(), "nothing to reverse") {
			t.Fatalf("W3-FX2 red: refusal %q does not say there is nothing to reverse", err.Error())
		}
	})

	t.Run("5_the_unwired_reader_refuses_the_reversal", func(t *testing.T) {
		reader := rig.svc.journalReader
		rig.svc.journalReader = nil
		t.Cleanup(func() { rig.svc.journalReader = reader })
		_, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchTRT, fx2Month)
		if err == nil {
			t.Fatal("W3-FX2 red: an unwired journal reader must refuse the reversal")
		}
		var ce clientError
		if !errors.As(err, &ce) || ce.status != http.StatusServiceUnavailable {
			t.Fatalf("W3-FX2 red: unwired reader error = %v, want 503", err)
		}
		if !strings.Contains(err.Error(), "cannot be read") {
			t.Fatalf("W3-FX2 red: refusal %q does not say the legs cannot be read", err.Error())
		}
	})
}

func TestIntegration_W3FX2_Ties(t *testing.T) {
	rig := fx2Rig(t)
	billA, _, _ := fx2World(t, rig)
	monthEnd := time.Date(2026, 9, 30, 23, 59, 59, 999999999, fx2Loc())
	runRateMicros := int64(math.Round(fx2RunRate * 1_000_000))

	// agingAt reads the aging as of an instant; atRate values it in CAD at the month end
	// rate (a USD item's doc open taken at the rate, a CAD item's own open cents), the
	// figure 2100 must carry once the run has posted, and carried values it in the bills'
	// own rates (every item's own open cents), the figure 2100 carries before and after.
	agingAt := func(asOf time.Time, atRate bool) int64 {
		t.Helper()
		page, err := rig.svc.APAging(rig.ctx, asOf, BranchReach{BranchIDs: []string{branchTRT}})
		if err != nil {
			t.Fatalf("APAging: %v", err)
		}
		var total int64
		for _, row := range page.Items {
			for _, item := range row.OpenItems {
				if item.Kind != APItemKindBill {
					t.Fatalf("unexpected aging item kind %q in the tie world", item.Kind)
				}
				if atRate && strings.EqualFold(item.Currency, "USD") {
					total += proRataHalfToEven(item.DocOpenCents, runRateMicros, 1_000_000)
				} else {
					total += item.OpenCents
				}
			}
		}
		return total
	}

	t.Run("1_at_month_end_the_aging_in_cad_at_the_rate_ties_to_2100", func(t *testing.T) {
		// Before the run, 2100 carries the bills' own CAD (the W29A tie).
		if got, want := agingAt(monthEnd, false), int64(250_000); got != want {
			t.Fatalf("W3-FX2 red: the aging in the bills' own CAD before the run = %d, want %d (200,000 USD carried + 50,000 CAD bill)", got, want)
		}
		if got := fx2BranchNetAcct(t, rig, branchTRT, "2100"); got != 250_000 {
			t.Fatalf("W3-FX2 red: 2100 before the run = %d, want 250,000", got)
		}

		run, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: fx2RunRate})
		if err != nil {
			t.Fatalf("RevalueAP: %v", err)
		}
		if run.DifferenceCents != fx2RunDiff {
			t.Fatalf("run difference = %d, want %d", run.DifferenceCents, fx2RunDiff)
		}
		gl2100 := fx2BranchNetAcct(t, rig, branchTRT, "2100")
		if got := agingAt(monthEnd, true); got != gl2100 {
			t.Fatalf("W3-FX2 red: at month end the aging in CAD at the month end rate = %d, but 2100 carries %d; they must tie", got, gl2100)
		}
		if gl2100 != 260_000 {
			t.Fatalf("W3-FX2 red: 2100 at month end = %d, want 260,000 (250,000 carried + the 10,000 revaluation)", gl2100)
		}
	})

	t.Run("2_after_the_reversal_2100_is_back_to_the_bills_carried_cad", func(t *testing.T) {
		if _, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchTRT, fx2Month); err != nil {
			t.Fatalf("ReverseAPRevaluation: %v", err)
		}
		gl2100 := fx2BranchNetAcct(t, rig, branchTRT, "2100")
		if gl2100 != 250_000 {
			t.Fatalf("W3-FX2 red: 2100 after the reversal = %d, want the bills' carried 250,000", gl2100)
		}
		// The aging's own read (in the bills' own rates) ties again, as of the month end
		// and as of a later instant alike: nothing moved but the temporary entry.
		if got, want := agingAt(monthEnd, false), int64(250_000); got != want {
			t.Fatalf("W3-FX2 red: the aging after the reversal = %d, want %d", got, want)
		}
		after := time.Date(2026, 10, 20, 23, 59, 59, 999999999, fx2Loc())
		if got, want := agingAt(after, false), int64(250_000); got != want {
			t.Fatalf("W3-FX2 red: the aging as of a later instant after the reversal = %d, want %d", got, want)
		}
	})

	t.Run("3_a_payment_after_the_reversal_posts_its_realised_fx_against_the_bill_own_rate", func(t *testing.T) {
		// Pay bill A in full at 1.33, after the reversal: 2100 clears the bill's own CAD
		// 135,000, the bank pays 133,000, and the 2,000 difference is a realised gain
		// against the bill's own 1.35, none of the revaluation's 10,000 in it.
		key := "w3fx2-a-pay-" + billA.ID
		if _, err := rig.svc.PayBill(rig.ctx, billA.ID, PayBillRequest{
			AmountCents: 100_000, FXRate: 1.33, PaidAt: time.Date(2026, 10, 15, 10, 0, 0, 0, fx2Loc()),
			Note: "after the reversal", IdempotencyKey: key,
		}); err != nil {
			t.Fatalf("pay bill A after the reversal: %v", err)
		}
		legs := rig.paymentJournalLegs(t, key)
		if legs["2100"] != ([2]int64{135_000, 0}) {
			t.Fatalf("W3-FX2 red: payment 2100 = %v, want DR 135,000 (the bill's own CAD at its own 1.35)", legs["2100"])
		}
		if legs["1000"] != ([2]int64{0, 133_000}) {
			t.Fatalf("W3-FX2 red: payment 1000 = %v, want CR 133,000 (USD 1,000.00 at 1.33)", legs["1000"])
		}
		if legs["6310"] != ([2]int64{0, 2_000}) {
			t.Fatalf("W3-FX2 red: payment 6310 = %v, want CR 2,000 realised against the bill's own rate (all legs: %v)", legs["6310"], legs)
		}

		// And the books still tie: 2100 and the aging are both at the remaining bills'
		// carried CAD, the revaluation pair having left no residue.
		gl2100 := fx2BranchNetAcct(t, rig, branchTRT, "2100")
		if gl2100 != 115_000 {
			t.Fatalf("W3-FX2 red: 2100 after the payment = %d, want 115,000 (65,000 + 50,000)", gl2100)
		}
		after := time.Date(2026, 10, 20, 23, 59, 59, 999999999, fx2Loc())
		page, err := rig.svc.APAging(rig.ctx, after, BranchReach{BranchIDs: []string{branchTRT}})
		if err != nil {
			t.Fatalf("APAging after the payment: %v", err)
		}
		if page.TotalCents != 115_000 {
			t.Fatalf("W3-FX2 red: the aging after the payment = %d, want 115,000 tied to 2100", page.TotalCents)
		}
	})
}

// TestIntegration_W3FX2_NextRunWaitsForThePreviousReversal proves a run refuses while the
// branch's previous revalued month is unreversed: the run measures each bill against its
// document, not against 2100, so a second run on top of an unreversed first would carry the
// first's difference twice.
func TestIntegration_W3FX2_NextRunWaitsForThePreviousReversal(t *testing.T) {
	rig := fx2Rig(t)
	rig.ensurePeriod(t, 2026, 11)
	rig.ensurePeriod(t, 2026, 12)
	loc := fx2Loc()
	fx2PostUSDBillInBranch(t, rig, "w3fx2-seq", branchKGN, "2026-10-10", "2026-12-10", 100_000, fx2BillARate, time.Date(2026, 10, 10, 9, 0, 0, 0, loc))
	t.Cleanup(func() { rig.svc.WithClock(time.Now) })
	rig.svc.WithClock(func() time.Time { return time.Date(2026, 12, 2, 9, 0, 0, 0, loc) })

	if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-10", FXRate: fx2RunRate}); err != nil {
		t.Fatalf("October run: %v", err)
	}
	novKey := "purchasing.ap.revaluation:" + branchKGN + ":2026-11"
	_, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-11", FXRate: fx2RunRate})
	var ce clientError
	if err == nil || !errors.As(err, &ce) || ce.status != http.StatusUnprocessableEntity {
		t.Fatalf("W3-FX2 fix red: November run with October unreversed = %v, want a 422 refusal", err)
	}
	if !strings.Contains(err.Error(), "2026-10") || !strings.Contains(err.Error(), "reversal") {
		t.Fatalf("W3-FX2 fix red: refusal %q does not name month 2026-10 and its reversal", err.Error())
	}
	if n := fx2JournalCount(t, rig, novKey); n != 0 {
		t.Fatalf("W3-FX2 fix red: the refused November run left %d journals", n)
	}
	// A re-press of October itself is still answered as a replay.
	if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-10", FXRate: fx2RunRate}); err != nil {
		t.Fatalf("W3-FX2 fix red: re-pressing October: %v", err)
	}
	if _, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchKGN, "2026-10"); err != nil {
		t.Fatalf("October reversal: %v", err)
	}
	nov, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-11", FXRate: fx2RunRate})
	if err != nil || !nov.Posted || nov.DifferenceCents != 5_000 {
		t.Fatalf("W3-FX2 fix red: November run after October's reversal = %+v, %v, want accepted with difference 5000", nov, err)
	}
	// The other branch's books are not held by this branch's unreversed month.
	if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: "2026-11", FXRate: fx2RunRate}); err != nil && strings.Contains(err.Error(), "reversal") {
		t.Fatalf("W3-FX2 fix red: another branch's run was held by this branch's month: %v", err)
	}
}

// TestIntegration_W3FX2_EveningBoundary is the evening guard's leg for the month end and the
// next day: the dealer's last evening of the month is already the next UTC day, so a month end
// or a next house day read off the UTC day counts the wrong bills and lets the reversal in
// three and a half hours early. A bill posted at 22:30 on the last house day (02:30 UTC the
// next month) is in the run, a bill posted ten minutes after the house midnight is not, and
// the reversal is refused at 23:30 on the last house day and lands at the house midnight.
func TestIntegration_W3FX2_EveningBoundary(t *testing.T) {
	rig := fx2Rig(t)
	loc := fx2Loc()
	lastEvening := time.Date(2026, 9, 30, 22, 30, 0, 0, loc)
	afterMidnight := time.Date(2026, 10, 1, 0, 10, 0, 0, loc)
	if lastEvening.UTC().Month() != time.October {
		t.Fatalf("fixture: the last house evening %s must already be October in UTC", lastEvening.UTC())
	}
	fx2PostUSDBillInBranch(t, rig, "w3fx2-eve-in", branchKGN, "2026-09-30", "2026-10-30", 100_000, fx2BillARate, lastEvening)
	fx2PostUSDBillInBranch(t, rig, "w3fx2-eve-out", branchKGN, "2026-10-01", "2026-10-31", 70_000, fx2BillARate, afterMidnight)

	// The run waits for the month to end, on the dealer's day: a press mid month and at 23:30
	// on the last house day (already October in UTC) are refused in words and post nothing;
	// the house midnight after the month accepts.
	t.Cleanup(func() { rig.svc.WithClock(time.Now) })
	for _, early := range []time.Time{time.Date(2026, 9, 15, 12, 0, 0, 0, loc), time.Date(2026, 9, 30, 23, 30, 0, 0, loc)} {
		rig.svc.WithClock(func() time.Time { return early })
		if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: fx2Month, FXRate: fx2RunRate}); err == nil {
			t.Fatalf("W3-FX2 evening red: a run pressed at %s, before the month's last house day has ended, must be refused", early.Format("2006-01-02 15:04"))
		} else if !strings.Contains(err.Error(), "next house day") {
			t.Fatalf("W3-FX2 evening red: refusal %q does not name the next house day", err.Error())
		}
		if n := fx2JournalCount(t, rig, "purchasing.ap.revaluation:"+branchKGN+":"+fx2Month); n != 0 {
			t.Fatalf("W3-FX2 evening red: the refused press at %s left %d journals under the month's key", early.Format("15:04"), n)
		}
	}
	rig.svc.WithClock(func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, loc) })
	run, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: fx2Month, FXRate: fx2RunRate})
	if err != nil {
		t.Fatalf("RevalueAP at the house midnight: %v", err)
	}
	// Only the bill posted on the last house evening is open at the house month end:
	// USD 1,000.00 at 1.40 is 140,000 against the 135,000 it carries.
	if run.Bills != 1 || run.CarriedCadCents != 135_000 || run.DifferenceCents != 5_000 {
		t.Fatalf("W3-FX2 evening red: run counted %d bills, carried %d, difference %d; want 1 bill, 135000, 5000 (the bill ten minutes after the house midnight is October's)",
			run.Bills, run.CarriedCadCents, run.DifferenceCents)
	}

	rig.svc.WithClock(func() time.Time { return time.Date(2026, 9, 30, 23, 30, 0, 0, loc) })
	if _, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchKGN, fx2Month); err == nil {
		t.Fatal("W3-FX2 evening red: a reversal pressed at 23:30 on the last house day (already October in UTC) must be refused")
	} else if !strings.Contains(err.Error(), "next house day") {
		t.Fatalf("W3-FX2 evening red: refusal %q does not name the next house day", err.Error())
	}
	rig.svc.WithClock(func() time.Time { return time.Date(2026, 10, 1, 0, 0, 0, 0, loc) })
	rev, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchKGN, fx2Month)
	if err != nil {
		t.Fatalf("W3-FX2 evening red: the reversal at the house midnight: %v", err)
	}
	if rev.NextHouseDay != fx2NextDay || rev.DifferenceCents != 5_000 {
		t.Fatalf("W3-FX2 evening red: reversal = %+v, want next house day %s and difference 5000", rev, fx2NextDay)
	}
}
