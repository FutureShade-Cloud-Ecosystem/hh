//go:build integration

package bankrec

// recon_variance_integration_test.go (SF-2s, S3's probe of PR 593): a month completes only on a variance recomputed at
// completion. CompleteReconciliation checked the variance OpenReconciliation stored when the month was opened, so a
// posting to the bank account after the open (the ledger moving from 70,000 to 60,000) still completed the month.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

type movingBalance struct{ val int64 }

func (m *movingBalance) GLBalanceCents(_ context.Context, _ string, _, _ int, _ string) (int64, error) {
	return m.val, nil
}

func varianceWorld(t *testing.T) (*reachWorld, *Service, *movingBalance, Reconciliation, string) {
	t.Helper()
	w := newReachWorld(t)
	ctx := context.Background()
	line := w.line(t, "SF2S DEPOSIT", 70000)
	if code, body := w.post(w.callerAB, w.manualOf(line, "JOURNAL", w.plantJournal(t, "sf2s-"+itoa(time.Now().UnixNano()), "", "POSTED", []jl{{"1000", 70000, 0}, {"4950", 0, 70000}}), 70000)); code != http.StatusCreated {
		t.Fatalf("setup: %d %s", code, body)
	}
	y, m := w.monthOf(t, line)
	bal := &movingBalance{val: 70000}
	svc := NewService(NewRepository(w.pool)).WithJournals(w.journals).WithGLBalances(bal)
	rec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: w.account, Year: y, Month: m, StatementBalanceCents: 70000})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if rec.VarianceCents != 0 {
		t.Fatalf("setup: variance %d, want 0", rec.VarianceCents)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM bank_reconciliations WHERE id = $1::uuid`, rec.ID)
	})
	return w, svc, bal, rec, line
}

// S3's probe: open at statement 70,000 and ledger 70,000, then the ledger moves to 60,000 (a posting to the bank
// account inside the month), then complete. It must be refused in dollars, and the row stays OPEN with its stored figures.
func TestIntegration_ReconVariance_ALedgerThatMovedAfterTheOpenRefusesTheSignOff(t *testing.T) {
	w, svc, bal, rec, _ := varianceWorld(t)
	ctx := context.Background()
	bal.val = 60000
	_, err := svc.CompleteReconciliation(ctx, rec.ID, "")
	t.Logf("complete after the ledger moved: %v", err)
	if err == nil {
		t.Fatalf("a month whose ledger moved after the open was signed off")
	}
	if !strings.Contains(err.Error(), "$700.00") || !strings.Contains(err.Error(), "$600.00") {
		t.Errorf("the refusal %q does not name both figures in dollars", err.Error())
	}
	var status string
	_ = w.pool.QueryRow(ctx, `SELECT status FROM bank_reconciliations WHERE id = $1::uuid`, rec.ID).Scan(&status)
	if status != ReconOpen {
		t.Errorf("the month reads %s after the refusal, want OPEN", status)
	}
	// The control: with the ledger back at the statement the month signs off, and the row stores the recomputed figures.
	bal.val = 70000
	done, err := svc.CompleteReconciliation(ctx, rec.ID, "")
	if err != nil {
		t.Fatalf("control: the balanced month was refused: %v", err)
	}
	if done.StatementBalanceCents != 70000 || done.GLBalanceCents != 70000 || done.VarianceCents != 0 {
		t.Errorf("the completed row stores statement %d, ledger %d, variance %d; want 70000, 70000, 0", done.StatementBalanceCents, done.GLBalanceCents, done.VarianceCents)
	}
}

// A month opened with a derived statement balance (a zero typed balance: the net of the month's lines) goes stale when
// a line is imported after the open: the statement figure is recomputed from the lines at completion.
func TestIntegration_ReconVariance_ADerivedStatementBalanceIsRecomputedFromTheLines(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	line := w.line(t, "SF2S DERIVED", 50000)
	if code, body := w.post(w.callerAB, w.manualOf(line, "JOURNAL", w.plantJournal(t, "sf2s-d-"+itoa(time.Now().UnixNano()), "", "POSTED", []jl{{"1000", 50000, 0}, {"4950", 0, 50000}}), 50000)); code != http.StatusCreated {
		t.Fatalf("setup: %d %s", code, body)
	}
	y, m := w.monthOf(t, line)
	bal := &movingBalance{val: 50000}
	svc := NewService(NewRepository(w.pool)).WithJournals(w.journals).WithGLBalances(bal)
	rec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: w.account, Year: y, Month: m}) // zero: derived
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM bank_reconciliations WHERE id = $1::uuid`, rec.ID)
	})
	if rec.StatementBalanceCents != 50000 || rec.VarianceCents != 0 {
		t.Fatalf("setup: statement %d variance %d, want 50000 and 0", rec.StatementBalanceCents, rec.VarianceCents)
	}
	time.Sleep(20 * time.Millisecond)
	late := w.line(t, "SF2S LATE", 1234)
	if code, body := w.post(w.callerAB, w.manualOf(late, "JOURNAL", w.plantJournal(t, "sf2s-l-"+itoa(time.Now().UnixNano()), "", "POSTED", []jl{{"1000", 1234, 0}, {"4950", 0, 1234}}), 1234)); code != http.StatusCreated {
		t.Fatalf("setup: %d %s", code, body)
	}
	_, err = svc.CompleteReconciliation(ctx, rec.ID, "")
	t.Logf("complete after a line was imported: %v", err)
	if err == nil {
		t.Fatalf("a month whose derived statement balance went stale was signed off")
	}
	if !strings.Contains(err.Error(), "$512.34") || !strings.Contains(err.Error(), "$500.00") {
		t.Errorf("the refusal %q does not name the statement $512.34 and the ledger $500.00", err.Error())
	}
}

// P2-1 (PR 607 round 2): a derived month (opened with 0), a line imported after the open, then a refresh, then
// completion. The refresh must keep the month derived, and completion must refuse while the ledger matches only the
// old figure ($500.00 against $512.34 of lines).
func TestIntegration_ReconVariance_ADerivedBalanceStaysDerivedAfterARefresh(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	line := w.line(t, "SF2S P21 OPEN", 50000)
	if code, body := w.post(w.callerAB, w.manualOf(line, "JOURNAL", w.plantJournal(t, "sf2s-p21-o-"+itoa(time.Now().UnixNano()), "", "POSTED", []jl{{"1000", 50000, 0}, {"4950", 0, 50000}}), 50000)); code != http.StatusCreated {
		t.Fatalf("setup: %d %s", code, body)
	}
	y, m := w.monthOf(t, line)
	bal := &movingBalance{val: 50000}
	svc := NewService(NewRepository(w.pool)).WithJournals(w.journals).WithGLBalances(bal)
	rec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: w.account, Year: y, Month: m}) // zero: derived
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM bank_reconciliations WHERE id = $1::uuid`, rec.ID)
	})
	time.Sleep(20 * time.Millisecond)
	late := w.line(t, "SF2S P21 LATE", 1234)
	if code, body := w.post(w.callerAB, w.manualOf(late, "JOURNAL", w.plantJournal(t, "sf2s-p21-l-"+itoa(time.Now().UnixNano()), "", "POSTED", []jl{{"1000", 1234, 0}, {"4950", 0, 1234}}), 1234)); code != http.StatusCreated {
		t.Fatalf("setup: %d %s", code, body)
	}
	// Refresh the month with the stored derived figure (or with 0): the derived flag must hold, so completion
	// re-derives from the lines ($512.34) and refuses while the ledger matches only the old figure ($500.00).
	refreshed, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: w.account, Year: y, Month: m, StatementBalanceCents: rec.StatementBalanceCents})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	_, err = svc.CompleteReconciliation(ctx, refreshed.ID, "")
	t.Logf("complete after late import and refresh: %v", err)
	if err == nil {
		t.Fatalf("a derived month refreshed after a late import was signed off while the ledger matched only the old figure")
	}
	if !strings.Contains(err.Error(), "$512.34") || !strings.Contains(err.Error(), "$500.00") {
		t.Errorf("the refusal %q does not name the statement $512.34 and the ledger $500.00", err.Error())
	}
	// Refreshing with 0 (what the desk's Refresh figures sends for a derived month) keeps the flag TRUE and updates
	// the stored statement figure to $512.34; completion still refuses while the ledger is $500.00.
	refreshedZero, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: w.account, Year: y, Month: m})
	if err != nil {
		t.Fatalf("refresh with zero: %v", err)
	}
	if !refreshedZero.StatementBalanceDerived || refreshedZero.StatementBalanceCents != 51234 {
		t.Errorf("refresh with zero: derived=%v statement=%d, want true and 51234", refreshedZero.StatementBalanceDerived, refreshedZero.StatementBalanceCents)
	}
	if _, err := svc.CompleteReconciliation(ctx, refreshedZero.ID, ""); err == nil {
		t.Fatalf("completion succeeded after refresh with zero while ledger is $500.00 and statement is $512.34")
	}
	// A fresh open with a typed closing balance sets statement_balance_derived FALSE.
	stated, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: w.account, Year: y, Month: m, StatementBalanceCents: 51234})
	if err != nil {
		t.Fatalf("open with typed balance: %v", err)
	}
	if stated.StatementBalanceDerived {
		t.Errorf("a fresh open with a typed balance left StatementBalanceDerived true")
	}
}

// P2-2 (PR 607 round 2): a stated closing balance that happens to equal the lines' net at the open ($700.00), then an
// extra line (+$50.00) imported and matched so the ledger and the lines both reach $750.00. Completion must keep the
// stated $700.00 and refuse against it ($700.00 versus $750.00).
func TestIntegration_ReconVariance_AStatedBalanceEqualToTheLinesAtOpenRefusesAnExtraLine(t *testing.T) {
	w, svc, bal, rec, _ := varianceWorld(t)
	ctx := context.Background()
	time.Sleep(20 * time.Millisecond)
	extra := w.line(t, "SF2S P22 EXTRA", 5000)
	if code, body := w.post(w.callerAB, w.manualOf(extra, "JOURNAL", w.plantJournal(t, "sf2s-p22-e-"+itoa(time.Now().UnixNano()), "", "POSTED", []jl{{"1000", 5000, 0}, {"4950", 0, 5000}}), 5000)); code != http.StatusCreated {
		t.Fatalf("setup: %d %s", code, body)
	}
	bal.val = 75000
	_, err := svc.CompleteReconciliation(ctx, rec.ID, "")
	t.Logf("complete stated balance after an extra line matched: %v", err)
	if err == nil {
		t.Fatalf("a month opened with a stated $700.00 was signed off at $750.00 after an extra line was matched")
	}
	if !strings.Contains(err.Error(), "$700.00") || !strings.Contains(err.Error(), "$750.00") {
		t.Errorf("the refusal %q does not name the stated $700.00 and the ledger $750.00", err.Error())
	}
}

// S3's P3: the auto run skips a line inside a COMPLETED month (errMonthCompleted) and goes on; the line stays unmatched.
func TestIntegration_ReconVariance_TheAutoRunSkipsALineInACompletedMonth(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	d := time.Now().UTC().Truncate(24 * time.Hour)
	w.lodg.open = []Lodgement{{MovementID: reachLodgA, BranchID: w.branchA, AmountCents: 7700, Reference: "SF2S-AUTO", OccurredAt: d}}
	line := insertLine(t, w.pool, w.account, d, "SF2S AUTO", "SF2S-AUTO", 7700)
	y, m := w.monthOf(t, line)
	if _, err := w.pool.Exec(ctx, `INSERT INTO bank_reconciliations (bank_account_id, year, month, status, completed_at) VALUES ($1::uuid, $2, $3, 'COMPLETED', now())
		ON CONFLICT (bank_account_id, year, month) DO UPDATE SET status = 'COMPLETED'`, w.account, y, m); err != nil {
		t.Fatalf("complete the month: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2 AND month = $3`, w.account, y, m)
	})
	svc := NewService(NewRepository(w.pool)).WithLodgements(w.lodg).WithJournalPoster(w.poster)
	sum, err := svc.AutoMatchReach(ctx, w.account, d.AddDate(0, 0, -3), d.AddDate(0, 0, 3), "", AllBranches)
	if err != nil {
		t.Fatalf("the auto run stopped instead of skipping: %v", err)
	}
	ls, _, n := w.written(t, line, reachPayA1)
	t.Logf("auto run: %+v | line %s matches %d", sum, ls, n)
	if ls != "UNMATCHED" || n != 0 || sum.Matched != 0 {
		t.Errorf("the line in the completed month was matched: line %s, summary %+v", ls, sum)
	}
}
