//go:build integration

// reversal_unmatch_card_integration_test.go: the unmatch of a reversed payment's deposit (SF-2d round 7).
//
// Under the 2310 rule a reversal moves no money at the bank. A reversed CARD is claimable again (round 4), so a
// wrong manual match of one must be undoable: unmatching only moves Dr 1000 / Cr 1010 back and leaves 2310 alone.
// A reversed cheque, cash, EFT or other payment can never be claimed again, so its unmatch stays refused, and the
// words say why truthfully.
package payments_test

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
)

func (w *reversalGLWorld) bankSvc() *bankrec.Service {
	return bankrec.NewService(bankrec.NewRepository(w.r.pool)).WithJournalPoster(glPosterOver{w.gl})
}

func (w *reversalGLWorld) matchPayment(t *testing.T, bank *bankrec.Service, line, orig string) (bankrec.Match, error) {
	t.Helper()
	return bank.ManualMatch(context.Background(), bankrec.MatchInput{CanAccessBranch: bankrec.AllBranches,
		Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetPayment, TargetRefID: orig,
	}, w.r.userID)
}

func TestIntegration_ReversedCard_AWrongMatchCanBeUndoneAndMatchedAgain(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.receipt("CARD", 10_000)
	w.drain()
	rev := w.reverse(orig)
	line := w.r.statementLine(10_000)
	bank := w.bankSvc()
	// The wrong match: a deposit of a REVERSED card should not be booked without a look, but a hand match
	// cannot tell, so it books, and the point of the leg is that a wrong booking undoes and the line can be
	// matched again. The one cent wrong amount this leg once used is refused at match time now (FW1-05
	// item 1 checks the claimed payment's amount against the line), so the wrongness is the booking itself.
	m1, err := w.matchPayment(t, bank, line, orig)
	if err != nil {
		t.Fatalf("the (wrong) match of the reversed card: %v", err)
	}
	keys := append(caseKeys(orig, rev), "bankrec:match:"+m1.ID, "bankrec:unmatch:"+m1.ID)
	if err := bank.UnmatchReach(ctx, m1.ID, w.r.userID, bankrec.AllBranches); err != nil {
		t.Fatalf("the wrong match of a reversed card could not be undone: %v", err)
	}
	var payStatus, lineStatus string
	_ = w.r.obs.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1::uuid`, orig).Scan(&payStatus)
	_ = w.r.obs.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id = $1::uuid`, line).Scan(&lineStatus)
	if payStatus != "RECEIVED" || lineStatus != "UNMATCHED" {
		t.Errorf("after the undo: payment %s, line %s; want RECEIVED and UNMATCHED", payStatus, lineStatus)
	}
	m2, err := w.matchPayment(t, bank, line, orig)
	if err != nil {
		t.Fatalf("the corrective match of the reversed card: %v", err)
	}
	keys = append(keys, "bankrec:match:"+m2.ID)
	w.cleanupJournals(keys, []string{line})
	wantNets(t, "reversed card: wrong match, undo, right match", w.walkNets(keys, []string{line}),
		map[string]int64{"1000": 10_000, "1010": 0, "1100": 0, "2310": -10_000})
}

func TestIntegration_ReversedCheque_TheUnmatchIsRefusedInTrueWordsWithNothingWritten(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.receipt("CHEQUE", 10_000)
	w.drain()
	line := w.r.statementLine(10_000)
	bank := w.bankSvc()
	m, err := w.matchPayment(t, bank, line, orig)
	if err != nil {
		t.Fatalf("the deposit: %v", err)
	}
	rev := w.reverse(orig)
	keys := append(caseKeys(orig, rev), "bankrec:match:"+m.ID, "bankrec:unmatch:"+m.ID)
	w.cleanupJournals(keys, []string{line})
	err = bank.UnmatchReach(ctx, m.ID, w.r.userID, bankrec.AllBranches)
	if err == nil {
		t.Fatalf("the unmatch of a reversed cheque's deposit was accepted")
	}
	t.Logf("refused: %v", err)
	if !strings.Contains(err.Error(), "a reversed cheque or cash payment cannot be matched again") || strings.Contains(err.Error(), "already left") {
		t.Errorf("the refusal reads %q, want the true words", err.Error())
	}
	var payStatus, lineStatus string
	var journals int
	_ = w.r.obs.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1::uuid`, orig).Scan(&payStatus)
	_ = w.r.obs.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id = $1::uuid`, line).Scan(&lineStatus)
	_ = w.r.obs.QueryRow(ctx, `SELECT count(*) FROM gl_journals WHERE idempotency_key = 'bankrec:unmatch:'||$1`, m.ID).Scan(&journals)
	if payStatus != "DEPOSITED" || lineStatus != "MATCHED" || journals != 0 {
		t.Errorf("after the refusal: payment %s, line %s, unmatch journals %d; want DEPOSITED, MATCHED, 0", payStatus, lineStatus, journals)
	}
}
