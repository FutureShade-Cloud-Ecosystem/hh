//go:build integration

package bankrec

// integrity_round2_integration_test.go (SF-2j round 2, S3's P2-2 and section 7): the JOURNAL target must be a journal
// that still explains the line.

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// A journal that has been reversed (the GL keeps the original POSTED and adds a reversing journal naming it), a
// journal that is itself a reversal, a journal already used as another live match's clearing, and a journal bankrec
// posted itself (its clearing or an unmatch's reversal) all fail to explain a line.
func TestIntegration_Round2_AJournalThatNoLongerExplainsALineIsRefused(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	stamp := time.Now().UnixNano()

	// (1) reversed: a POSTED original touching 1000 for 7,100 and a POSTED reversing journal that names it.
	orig := w.plantJournal(t, "sf2j2-orig-"+itoa(stamp), "", "POSTED", []jl{{"1000", 7100, 0}, {"4950", 0, 7100}})
	rev := w.plantJournal(t, "sf2j2-rev-"+itoa(stamp), "", "POSTED", []jl{{"1000", 0, 7100}, {"4950", 7100, 0}})
	if _, err := w.pool.Exec(ctx, `UPDATE gl_journals SET reverses_journal_id = $1::uuid WHERE id = $2::uuid`, orig, rev); err != nil {
		t.Fatalf("name the reversal: %v", err)
	}
	line := w.line(t, "R2 REVERSED", 7100)
	code, body := w.post(w.callerAB, w.manualOf(line, "JOURNAL", orig, 7100))
	t.Logf("a reversed journal: %d %s", code, body)
	if code == http.StatusCreated || w.liveMatches(t, line) != 0 {
		t.Errorf("a journal that has been reversed explained a line (answered %d)", code)
	}
	// (2) the reversing journal itself, for the line it would match by amount.
	revLine := w.line(t, "R2 REVERSAL", -7100)
	code, body = w.post(w.callerAB, w.manualOf(revLine, "JOURNAL", rev, -7100))
	t.Logf("a reversing journal: %d %s", code, body)
	if code == http.StatusCreated || w.liveMatches(t, revLine) != 0 {
		t.Errorf("a reversing journal explained a line (answered %d)", code)
	}

	// (3) another live match's clearing journal: a 7,200 lodgement matched, then its clearing journal offered as a
	// JOURNAL target for a second 7,200 line.
	d := time.Now().UTC().Truncate(24 * time.Hour)
	w.lodg.open = []Lodgement{{MovementID: reachLodgA, BranchID: w.branchA, AmountCents: 7200, Reference: "LDG-72", OccurredAt: d}}
	first := w.line(t, "R2 LODGED", 7200)
	code, body = w.post(w.callerA, w.manualOf(first, "LODGEMENT", reachLodgA, 7200))
	if code != http.StatusCreated {
		t.Fatalf("setup: the lodgement match answered %d %s", code, body)
	}
	var clearing, matchID string
	if err := w.pool.QueryRow(ctx, `SELECT journal_id::text, id::text FROM bank_matches WHERE statement_line_id = $1::uuid AND unmatched_at IS NULL`, first).Scan(&clearing, &matchID); err != nil {
		t.Fatalf("read the clearing journal: %v", err)
	}
	second := w.line(t, "R2 SECOND", 7200)
	code, body = w.post(w.callerAB, w.manualOf(second, "JOURNAL", clearing, 7200))
	t.Logf("another match's clearing journal: %d %s", code, body)
	if code == http.StatusCreated || w.liveMatches(t, second) != 0 {
		t.Errorf("a journal already spent as another match's clearing explained a second line (answered %d)", code)
	}

	// (4) bankrec's own journal after its match is undone: the clearing journal stays POSTED, but it is bankrec's own
	// posting, never a target.
	if code, body := w.unmatch(w.callerAB, matchID); code != http.StatusOK {
		t.Fatalf("setup: the unmatch answered %d %s", code, body)
	}
	third := w.line(t, "R2 THIRD", 7200)
	code, body = w.post(w.callerAB, w.manualOf(third, "JOURNAL", clearing, 7200))
	t.Logf("bankrec's own journal: %d %s", code, body)
	if code == http.StatusCreated || w.liveMatches(t, third) != 0 {
		t.Errorf("a journal bankrec posted itself explained a line (answered %d)", code)
	}
	if code != http.StatusCreated && !strings.Contains(body, "journal") {
		t.Errorf("the refusal %q does not say it is about the journal", body)
	}
}

// Completion rechecks every live JOURNAL match in the month through the GL seam (S3 section 7): a matched journal
// that has been reversed since, or no longer qualifies, refuses the sign off and names the line.
func TestIntegration_Round2_CompletionRechecksTheJournalMatchesOfTheMonth(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	stamp := time.Now().UnixNano()
	j := w.plantJournal(t, "sf2j2-cmp-"+itoa(stamp), "", "POSTED", []jl{{"1000", 7300, 0}, {"4950", 0, 7300}})
	line := w.line(t, "R2 COMPLETION", 7300)
	if code, body := w.post(w.callerAB, w.manualOf(line, "JOURNAL", j, 7300)); code != http.StatusCreated {
		t.Fatalf("setup: the journal match answered %d %s", code, body)
	}
	svc := NewService(NewRepository(w.pool)).WithJournals(w.journals).WithGLBalances(&fakeBalances{val: 7300})
	var year, month int
	_ = w.pool.QueryRow(ctx, `SELECT EXTRACT(YEAR FROM txn_date)::int, EXTRACT(MONTH FROM txn_date)::int FROM bank_statement_lines WHERE id = $1::uuid`, line).Scan(&year, &month)
	rec, err := svc.OpenReconciliation(ctx, ReconInput{BankAccountID: w.account, Year: year, Month: month, StatementBalanceCents: 7300})
	if err != nil {
		t.Fatalf("open the month: %v", err)
	}
	t.Cleanup(func() { _, _ = w.pool.Exec(context.Background(), `DELETE FROM bank_reconciliations WHERE id = $1::uuid`, rec.ID) })
	// The journal is reversed after the match, before the sign off.
	rev := w.plantJournal(t, "sf2j2-cmp-rev-"+itoa(stamp), "", "POSTED", []jl{{"1000", 0, 7300}, {"4950", 7300, 0}})
	if _, err := w.pool.Exec(ctx, `UPDATE gl_journals SET reverses_journal_id = $1::uuid WHERE id = $2::uuid`, j, rev); err != nil {
		t.Fatalf("name the reversal: %v", err)
	}
	_, err = svc.CompleteReconciliation(ctx, rec.ID, "")
	t.Logf("completion with a reversed journal match: %v", err)
	if err == nil {
		t.Fatalf("a month holding a journal match whose journal was reversed was signed off")
	}
	if !strings.Contains(err.Error(), "R2 COMPLETION") {
		t.Errorf("the refusal %q does not name the line", err.Error())
	}
	var status string
	_ = w.pool.QueryRow(ctx, `SELECT status FROM bank_reconciliations WHERE id = $1::uuid`, rec.ID).Scan(&status)
	if status != ReconOpen {
		t.Errorf("the month reads %s after the refused sign off, want OPEN", status)
	}
}
