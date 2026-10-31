//go:build integration

package purchasing

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// W3 FX-2b: the run record (migration 0476) and the guards that read it.

// fx2bRunRow is one ap_revaluation_runs row as the legs read it.
type fx2bRunRow struct {
	Found       bool
	FXRate      float64
	Difference  int64
	JournalID   string
	ReversalID  string
	CreatedBy   string
	ReversedBy  string
	HasReversed bool
}

func fx2bRun(t *testing.T, rig *apAgingTestRig, branch, month string) fx2bRunRow {
	t.Helper()
	var r fx2bRunRow
	rows, err := rig.pool.Query(rig.ctx, `
		SELECT fx_rate::float8, difference_cents, COALESCE(journal_id::text, ''), COALESCE(reversal_journal_id::text, ''),
		       created_by, reversed_by, reversed_at IS NOT NULL
		  FROM ap_revaluation_runs WHERE branch_id = $1::uuid AND month = $2`, branch, month)
	if err != nil {
		t.Fatalf("read the run record: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		r.Found = true
		if err := rows.Scan(&r.FXRate, &r.Difference, &r.JournalID, &r.ReversalID, &r.CreatedBy, &r.ReversedBy, &r.HasReversed); err != nil {
			t.Fatalf("scan the run record: %v", err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("run record rows: %v", err)
	}
	return r
}

func fx2bWantStatus(t *testing.T, label string, err error, status int, words ...string) {
	t.Helper()
	var ce clientError
	if err == nil || !errors.As(err, &ce) || ce.status != status {
		t.Fatalf("%s: error = %v, want status %d", label, err, status)
	}
	for _, w := range words {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("%s: refusal %q does not say %q", label, err.Error(), w)
		}
	}
}

// A zero run is recorded with no journal, so a second press at another rate is refused as
// already run; a re-press at the same rate answers the same zero run.
func TestIntegration_W3FX2B_ZeroRunIsRecorded(t *testing.T) {
	rig := fx2Rig(t)
	fx2PostUSDBillInBranch(t, rig, "w3fx2b-zero", branchKGN, "2026-09-15", "2026-10-15", 100_000, fx2BillARate, fx2Instant(15, 9, 0))
	key := "purchasing.ap.revaluation:" + branchKGN + ":" + fx2Month

	run, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: fx2Month, FXRate: fx2BillARate})
	if err != nil || run.Posted || run.DifferenceCents != 0 {
		t.Fatalf("W3-FX2b red: the zero run = %+v, %v, want a complete run with no journal", run, err)
	}
	row := fx2bRun(t, rig, branchKGN, fx2Month)
	if !row.Found || row.Difference != 0 || row.JournalID != "" || row.FXRate != fx2BillARate {
		t.Fatalf("W3-FX2b red: the zero run's record = %+v, want one row, difference 0, no journal, rate %v", row, fx2BillARate)
	}
	if row.CreatedBy == "" {
		t.Fatalf("W3-FX2b red: the record names nobody who pressed the run")
	}

	again, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: fx2Month, FXRate: fx2BillARate})
	if err != nil || again.Posted || again.DifferenceCents != 0 || again.Bills != 1 {
		t.Fatalf("W3-FX2b red: a re-press at the same rate = %+v, %v, want the zero run answered again", again, err)
	}

	_, err = rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: fx2Month, FXRate: 1.45})
	fx2bWantStatus(t, "W3-FX2b red: a second press at another rate", err, http.StatusConflict, "already", "one key is one run")
	if n := fx2JournalCount(t, rig, key); n != 0 {
		t.Fatalf("W3-FX2b red: the refused second press left %d journals", n)
	}
	// A zero run needs no reversal and does not hold the next month.
	if _, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchKGN, fx2Month); err == nil {
		t.Fatal("W3-FX2b red: reversing a zero run must be refused, it posted nothing")
	} else {
		fx2bWantStatus(t, "W3-FX2b red: reversing a zero run", err, http.StatusUnprocessableEntity, "nothing to reverse")
	}
}

// The run record carries the posted run's figures, and the reversal stamps it.
func TestIntegration_W3FX2B_RecordCarriesRunAndReversal(t *testing.T) {
	rig := fx2Rig(t)
	fx2World(t, rig)
	run, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchTRT, Month: fx2Month, FXRate: fx2RunRate})
	if err != nil {
		t.Fatalf("RevalueAP: %v", err)
	}
	row := fx2bRun(t, rig, branchTRT, fx2Month)
	if !row.Found || row.JournalID != run.JournalID || row.Difference != fx2RunDiff || row.FXRate != fx2RunRate || row.CreatedBy == "" || row.HasReversed {
		t.Fatalf("W3-FX2b red: the run record = %+v, want journal %s, difference %d, rate %v, a presser, not reversed", row, run.JournalID, fx2RunDiff, fx2RunRate)
	}
	rev, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchTRT, fx2Month)
	if err != nil {
		t.Fatalf("ReverseAPRevaluation: %v", err)
	}
	row = fx2bRun(t, rig, branchTRT, fx2Month)
	if row.ReversalID != rev.ReversalJournalID || !row.HasReversed || row.ReversedBy == "" {
		t.Fatalf("W3-FX2b red: after the reversal the record = %+v, want reversal journal %s, stamped, with who", row, rev.ReversalJournalID)
	}
	// The unrealised leg rides 6315 and the realised account is untouched by the pair.
	legs := fx2JournalLegs(t, rig, "purchasing.ap.revaluation:"+branchTRT+":"+fx2Month)
	if _, has := legs["6310"]; has {
		t.Fatalf("W3-FX2b red: the revaluation posted to the realised account 6310: %v", legs)
	}
	if legs["6315"] != ([2]int64{fx2RunDiff, 0}) {
		t.Fatalf("W3-FX2b red: the unrealised leg = %v, want DR %d on 6315", legs["6315"], fx2RunDiff)
	}
}

// fx2bTwoMonthWorld is one USD bill posted in October in the Kingston branch, open at both
// the October and the November month end, with the clock at a December morning.
func fx2bTwoMonthWorld(t *testing.T, rig *apAgingTestRig) {
	t.Helper()
	rig.ensurePeriod(t, 2026, 11)
	rig.ensurePeriod(t, 2026, 12)
	loc := fx2Loc()
	fx2PostUSDBillInBranch(t, rig, "w3fx2b-two", branchKGN, "2026-10-10", "2026-12-20", 100_000, fx2BillARate, time.Date(2026, 10, 10, 9, 0, 0, 0, loc))
	rig.svc.WithClock(func() time.Time { return time.Date(2026, 12, 2, 9, 0, 0, 0, loc) })
	t.Cleanup(func() { rig.svc.WithClock(time.Now) })
}

// The backward guard reads the record: a run is refused while any earlier month of the branch
// is unreversed, however far back, and a zero earlier run holds nothing.
func TestIntegration_W3FX2B_BackwardGuard(t *testing.T) {
	rig := fx2Rig(t)
	fx2bTwoMonthWorld(t, rig)
	if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-10", FXRate: fx2RunRate}); err != nil {
		t.Fatalf("October run: %v", err)
	}
	_, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-11", FXRate: fx2RunRate})
	fx2bWantStatus(t, "W3-FX2b red: November with October unreversed", err, http.StatusUnprocessableEntity, "2026-10", "reversal")
	if row := fx2bRun(t, rig, branchKGN, "2026-11"); row.Found {
		t.Fatalf("W3-FX2b red: the refused November run left a record: %+v", row)
	}
	if _, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchKGN, "2026-10"); err != nil {
		t.Fatalf("October reversal: %v", err)
	}
	if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-11", FXRate: fx2RunRate}); err != nil {
		t.Fatalf("November after October's reversal: %v", err)
	}
}

// The forward guard: a run is refused while a later month has a posted run, so an earlier
// month is never revalued after a later one.
func TestIntegration_W3FX2B_ForwardGuard(t *testing.T) {
	rig := fx2Rig(t)
	fx2bTwoMonthWorld(t, rig)
	if _, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-11", FXRate: fx2RunRate}); err != nil {
		t.Fatalf("November run: %v", err)
	}
	_, err := rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-10", FXRate: fx2RunRate})
	fx2bWantStatus(t, "W3-FX2b red: October after November", err, http.StatusUnprocessableEntity, "2026-11", "earlier month")
	if row := fx2bRun(t, rig, branchKGN, "2026-10"); row.Found {
		t.Fatalf("W3-FX2b red: the refused October run left a record: %+v", row)
	}
	if n := fx2JournalCount(t, rig, "purchasing.ap.revaluation:"+branchKGN+":2026-10"); n != 0 {
		t.Fatalf("W3-FX2b red: the refused October run left %d journals", n)
	}
	// Even once November is reversed the order holds: October is behind November.
	if _, err := rig.svc.ReverseAPRevaluation(rig.ctx, branchKGN, "2026-11"); err != nil {
		t.Fatalf("November reversal: %v", err)
	}
	_, err = rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: "2026-10", FXRate: fx2RunRate})
	fx2bWantStatus(t, "W3-FX2b red: October after a reversed November", err, http.StatusUnprocessableEntity, "2026-11")
}

// Two runs for adjacent months pressed at once: one lock per branch, so exactly one posts.
func TestIntegration_W3FX2B_ConcurrentAdjacentRunsOnePosts(t *testing.T) {
	rig := fx2Rig(t)
	fx2bTwoMonthWorld(t, rig)
	months := []string{"2026-10", "2026-11"}
	errs := make([]error, len(months))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, m := range months {
		wg.Add(1)
		go func(i int, m string) {
			defer wg.Done()
			<-start
			_, errs[i] = rig.svc.RevalueAP(rig.ctx, RevalueAPRequest{BranchID: branchKGN, Month: m, FXRate: fx2RunRate})
		}(i, m)
	}
	close(start)
	wg.Wait()
	posted := 0
	for i, err := range errs {
		if err == nil {
			posted++
			continue
		}
		fx2bWantStatus(t, "W3-FX2b red: the refused concurrent run for "+months[i], err, http.StatusUnprocessableEntity)
	}
	if posted != 1 {
		t.Fatalf("W3-FX2b red: %d of two concurrent adjacent runs posted (%v, %v), want exactly one", posted, errs[0], errs[1])
	}
	var n int
	if err := rig.pool.QueryRow(rig.ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key LIKE $1`,
		"purchasing.ap.revaluation:"+branchKGN+":%").Scan(&n); err != nil || n != 1 {
		t.Fatalf("W3-FX2b red: %d run journals in the branch (%v), want 1", n, err)
	}
}
