//go:build integration

// integrity_integration_test.go: bank reconciliation integrity before any real statement (SF-2j).
//
// A manual JOURNAL match used to take any id and mark the line MATCHED; GROUP did the same with a target nothing
// resolves; a manual LODGEMENT match posted the clearing for the LINE's amount whatever the lodgement was; and an
// unmatch could reopen a line inside a COMPLETED reconciliation's month. The match is the reconciliation's audit
// trail, the record of what explains each bank line, so each of these lets a month close with an explanation that
// does not exist. The legs drive the real handler with a real caller authority over the real service and rows.
package bankrec

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// plantJournal writes a journal straight into the GL tables (a POSTED or DRAFT journal with the given lines), with
// an optional branch, in the seeded 2026 June period the test poster also uses.
func (w *reachWorld) plantJournal(t *testing.T, key, branch, status string, lines []struct {
	code   string
	dr, cr int64
}) string {
	t.Helper()
	ctx := context.Background()
	var periodID string
	var year int
	if err := w.pool.QueryRow(ctx, `SELECT id::text, year FROM fiscal_periods WHERE year = 2026 AND month = 6`).Scan(&periodID, &year); err != nil {
		t.Skipf("no seeded 2026-06 period: %v", err)
	}
	var branchArg any
	if branch != "" {
		branchArg = branch
	}
	var id string
	if err := w.pool.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, idempotency_key, branch_id)
		VALUES ($1, (SELECT COALESCE(MAX(entry_no),0)+1 FROM gl_journals WHERE fiscal_year=$1), $2, 'ADJUSTMENT', 'integrity leg', 'DRAFT', $3, $4::uuid)
		RETURNING id::text`,
		year, periodID, key, branchArg).Scan(&id); err != nil {
		t.Fatalf("plant journal: %v", err)
	}
	for i, l := range lines {
		var dr, cr any
		if l.dr > 0 {
			dr = l.dr
		} else {
			cr = l.cr
		}
		if _, err := w.pool.Exec(ctx, `INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents)
			VALUES ($1::uuid, $2, (SELECT id FROM gl_accounts WHERE code = $3), $4, $5)`, id, i+1, l.code, dr, cr); err != nil {
			t.Fatalf("plant journal line: %v", err)
		}
	}
	if status == "POSTED" {
		if _, err := w.pool.Exec(ctx, `UPDATE gl_journals SET status = 'POSTED', posted_at = now() WHERE id = $1::uuid`, id); err != nil {
			t.Fatalf("post the planted journal: %v", err)
		}
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id = $1::uuid`, id)
		_, _ = w.pool.Exec(c, `DELETE FROM gl_journals WHERE id = $1::uuid`, id)
	})
	return id
}

type jl = struct {
	code   string
	dr, cr int64
}

func (w *reachWorld) manualOf(line, targetType, ref string, cents int64) map[string]any {
	return map[string]any{"mode": "manual", "statement_line_id": line, "target_type": targetType, "target_ref_id": ref, "amount_cents": cents}
}

func (w *reachWorld) liveMatches(t *testing.T, line string) int {
	t.Helper()
	var n int
	_ = w.pool.QueryRow(context.Background(), `SELECT count(*) FROM bank_matches WHERE statement_line_id = $1::uuid AND unmatched_at IS NULL`, line).Scan(&n)
	return n
}

// (1) The JOURNAL target must be a real, posted journal that touches the bank account's GL code for the line's
// amount and has no other live match.
func TestIntegration_Integrity_AJournalTargetMustExplainTheLine(t *testing.T) {
	w := newReachWorld(t)
	stamp := time.Now().UnixNano()
	good := w.plantJournal(t, "sf2j-good-"+itoa(stamp), "", "POSTED", []jl{{"1000", 5000, 0}, {"4950", 0, 5000}})
	other := w.plantJournal(t, "sf2j-other-"+itoa(stamp), "", "POSTED", []jl{{"1000", 3000, 0}, {"4950", 0, 3000}})
	noBank := w.plantJournal(t, "sf2j-nobank-"+itoa(stamp), "", "POSTED", []jl{{"1100", 5000, 0}, {"4950", 0, 5000}})
	draft := w.plantJournal(t, "sf2j-draft-"+itoa(stamp), "", "DRAFT", []jl{{"1000", 5000, 0}, {"4950", 0, 5000}})
	branchB := w.plantJournal(t, "sf2j-branchb-"+itoa(stamp), w.branchB, "POSTED", []jl{{"1000", 5000, 0}, {"4950", 0, 5000}})
	branchA := w.plantJournal(t, "sf2j-brancha-"+itoa(stamp), w.branchA, "POSTED", []jl{{"1000", 5000, 0}, {"4950", 0, 5000}})
	const invented = "66666666-6666-4666-8666-666666666666"

	refused := func(name, ref string, caller func() (int, string)) {
		t.Helper()
		line := w.line(t, "JRN "+name, 5000)
		code, body := w.post(w.callerA, w.manualOf(line, "JOURNAL", ref, 5000))
		ls, _, _ := w.written(t, line, reachPayA1)
		t.Logf("%s: %d %s | line %s live matches %d", name, code, body, ls, w.liveMatches(t, line))
		if code == http.StatusCreated || ls != "UNMATCHED" || w.liveMatches(t, line) != 0 {
			t.Errorf("%s: a journal that does not explain the line was matched (answered %d, line %s)", name, code, ls)
		}
	}
	refused("invented id", invented, nil)
	refused("wrong amount", other, nil)
	refused("no bank leg", noBank, nil)
	refused("not posted", draft, nil)
	refused("another branch's journal", branchB, nil)

	// The good one matches, once: a second line cannot claim it.
	line := w.line(t, "JRN GOOD", 5000)
	if code, body := w.post(w.callerA, w.manualOf(line, "JOURNAL", good, 5000)); code != http.StatusCreated {
		t.Errorf("control: a posted journal that touches the bank for the line's amount was refused: %d %s", code, body)
	}
	again := w.line(t, "JRN GOOD AGAIN", 5000)
	if code, _ := w.post(w.callerA, w.manualOf(again, "JOURNAL", good, 5000)); code == http.StatusCreated {
		t.Errorf("a journal already explaining one line was matched to a second")
	}
	// A journal of the caller's own branch matches; a journal with no branch is dealer wide, like the bank line.
	own := w.line(t, "JRN OWN BRANCH", 5000)
	if code, body := w.post(w.callerA, w.manualOf(own, "JOURNAL", branchA, 5000)); code != http.StatusCreated {
		t.Errorf("control: a branch A journal for a branch A manager was refused: %d %s", code, body)
	}
}

// (2) GROUP is refused in manual mode until a member set exists.
func TestIntegration_Integrity_AGroupMatchIsRefusedInManualMode(t *testing.T) {
	w := newReachWorld(t)
	line := w.line(t, "GROUP", 5000)
	code, body := w.post(w.callerAB, w.manualOf(line, "GROUP", "77777777-7777-4777-8777-777777777777", 5000))
	ls, _, _ := w.written(t, line, reachPayA1)
	t.Logf("a manual GROUP match: %d %s | line %s", code, body, ls)
	if code == http.StatusCreated || ls != "UNMATCHED" || w.liveMatches(t, line) != 0 {
		t.Errorf("a GROUP target nothing resolves was matched (answered %d, line %s)", code, ls)
	}
}

// (3) A manual LODGEMENT match explains the line with the lodgement's own amount. A 50.00 lodgement matched to a
// 5,000.00 line used to clear 5,000.00 from undeposited funds.
func TestIntegration_Integrity_ALodgementOfAnotherAmountDoesNotClearTheLine(t *testing.T) {
	w := newReachWorld(t)
	d := time.Now().UTC().Truncate(24 * time.Hour)
	w.lodg.open = []Lodgement{{MovementID: reachLodgA, BranchID: w.branchA, AmountCents: 5000, Reference: "LDG-50", OccurredAt: d}}
	line := w.line(t, "BIG DEPOSIT", 500000)
	code, body := w.post(w.callerA, w.manualOf(line, "LODGEMENT", reachLodgA, 500000))
	var posted int64
	_ = w.pool.QueryRow(context.Background(), `SELECT COALESCE(SUM(l.credit_cents), 0) FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		JOIN gl_accounts a ON a.id = l.account_id WHERE a.code = '1020' AND j.memo LIKE '%'||$1||'%'`, "BIG DEPOSIT").Scan(&posted)
	ls, _, _ := w.written(t, line, reachPayA1)
	t.Logf("a 50.00 lodgement matched to a 5,000.00 line: %d %s | line %s, undeposited cleared %d", code, body, ls, posted)
	if code == http.StatusCreated || ls != "UNMATCHED" || posted != 0 {
		t.Errorf("a lodgement of another amount cleared the line: answered %d, line %s, 1020 credited %d", code, ls, posted)
	}
	// Control: the same lodgement against a line of ITS amount matches.
	okLine := w.line(t, "FIFTY DEPOSIT", 5000)
	if code, body := w.post(w.callerA, w.manualOf(okLine, "LODGEMENT", reachLodgA, 5000)); code != http.StatusCreated {
		t.Errorf("control: a lodgement matched to a line of its own amount was refused: %d %s", code, body)
	}
}

// (4) An unmatch cannot reopen a line inside a COMPLETED reconciliation's month.
func TestIntegration_Integrity_AnUnmatchInsideACompletedMonthIsRefused(t *testing.T) {
	w := newReachWorld(t)
	ctx := context.Background()
	line := w.line(t, "CLOSED MONTH", 5000)
	code, body := w.post(w.callerAB, w.manual(line, reachPayA1))
	if code != http.StatusCreated {
		t.Fatalf("setup: %d %s", code, body)
	}
	var m Match
	_ = json.Unmarshal([]byte(body), &m)
	var year, month int
	if err := w.pool.QueryRow(ctx, `SELECT EXTRACT(YEAR FROM txn_date)::int, EXTRACT(MONTH FROM txn_date)::int FROM bank_statement_lines WHERE id = $1::uuid`, line).Scan(&year, &month); err != nil {
		t.Fatalf("line month: %v", err)
	}
	if _, err := w.pool.Exec(ctx, `INSERT INTO bank_reconciliations (bank_account_id, year, month, status, completed_at) VALUES ($1::uuid, $2, $3, 'COMPLETED', now())
		ON CONFLICT (bank_account_id, year, month) DO UPDATE SET status = 'COMPLETED'`, w.account, year, month); err != nil {
		t.Fatalf("complete the month: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), `DELETE FROM bank_reconciliations WHERE bank_account_id = $1::uuid AND year = $2 AND month = $3`, w.account, year, month)
	})
	code, body = w.unmatch(w.callerAB, m.ID)
	ls, ps, _ := w.written(t, line, reachPayA1)
	t.Logf("an unmatch inside the completed month: %d %s | line %s payment %s", code, body, ls, ps)
	if code == http.StatusOK || ls != "MATCHED" || ps != "DEPOSITED" {
		t.Errorf("an unmatch reopened a line inside a COMPLETED month: answered %d, line %s, payment %s", code, ls, ps)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
