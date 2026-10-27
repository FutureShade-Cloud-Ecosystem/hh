//go:build integration

package bankrec

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// P1-B (S5's review of PR 632, round 2): the unmatch of a hand match must return the money through the
// clearing account the match booked, which follows the tender (item 1: 1010 card, 1020 cash and cheque),
// not always 1010. One leg per tender: match by hand, unmatch, and 1000, 1010 and 1020 are all back to
// zero across the clearing journal and its reversal.
func TestIntegration_FW1_05_R2_UnmatchRestoresEachTendersClearingAccount(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may24 := time.Date(year, 5, 24, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may24)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may24 }}).
		WithGLBalances(glSvc)

	for i, tt := range []struct {
		method   string
		payID    string
		lineDesc string
		lineRef  string
		clearing string
	}{
		{method: "CARD", payID: "87057702-0000-4000-8000-000000000001", lineDesc: "CARD DEPOSIT R2 UNMATCH", lineRef: "DEP-R2-CARD", clearing: AcctCardClearing},
		{method: "CASH", payID: "87057702-0000-4000-8000-000000000002", lineDesc: "CASH DEPOSIT R2 UNMATCH", lineRef: "DEP-R2-CASH", clearing: AcctUndeposited},
		{method: "CHEQUE", payID: "87057702-0000-4000-8000-000000000003", lineDesc: "CHEQUE DEPOSIT R2 UNMATCH", lineRef: "DEP-R2-CHQ", clearing: AcctUndeposited},
	} {
		t.Run(tt.method, func(t *testing.T) {
			const amount = int64(25000)
			insertPayment(t, pool, tt.payID, branch, tt.method, "", amount)
			lineID := fw104InsertLineAt(t, pool, acct, may24.AddDate(0, 0, i), tt.lineDesc, tt.lineRef, amount)

			m, err := svc.ManualMatch(ctx, MatchInput{
				Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: tt.payID,
				AmountCents: amount, CanAccessBranch: AllBranches,
			}, "")
			if err != nil {
				t.Fatalf("%s hand match: %v", tt.method, err)
			}
			// The hand match books DR 1000 / CR <the tender's clearing account>.
			assertJournalLegs(t, pool, m.JournalID, map[string][2]int64{
				AcctBank:    {amount, 0},
				tt.clearing: {0, amount},
			})

			if err := svc.UnmatchReach(ctx, m.ID, "", AllBranches); err != nil {
				t.Fatalf("%s unmatch: %v", tt.method, err)
			}

			// The reversal exists under its own key and mirrors the ORIGINAL's clearing account.
			var reversalID string
			if err := pool.QueryRow(ctx,
				`SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(m.ID)).Scan(&reversalID); err != nil {
				t.Fatalf("%s reversal journal lookup: %v", tt.method, err)
			}
			assertJournalLegs(t, pool, reversalID, map[string][2]int64{
				tt.clearing: {amount, 0},
				AcctBank:    {0, amount},
			})

			// And every one of the three accounts is back to zero across clearing + reversal.
			var wrong []string
			for _, code := range []string{AcctBank, AcctCardClearing, AcctUndeposited} {
				var net int64
				if err := pool.QueryRow(ctx, `
					SELECT COALESCE(SUM(l.debit_cents),0) - COALESCE(SUM(l.credit_cents),0)
					FROM gl_journal_lines l
					JOIN gl_accounts a ON a.id = l.account_id
					JOIN gl_journals j ON j.id = l.journal_id
					WHERE j.idempotency_key IN ($1, $2) AND a.code = $3`,
					clearingKey(m.ID), unmatchKey(m.ID), code).Scan(&net); err != nil {
					t.Fatalf("%s net for %s: %v", tt.method, code, err)
				}
				if net != 0 {
					wrong = append(wrong, fmt.Sprintf("account %s legs = (net %d), want 0", code, net))
				}
			}
			if len(wrong) > 0 {
				t.Fatalf("P1-B %s: after the unmatch the clearing accounts are not restored: %v", tt.method, wrong)
			}
		})
	}
}

// P3-1 (S5's review of PR 632, round 2): the hand match's sign check is pinned on its own. Item 1's
// withdrawal leg is refused by the amount equality too (a payment is never negative), so this leg sends a
// negative line WITH a matching negative caller amount, which the amount check accepts; the refusal must be
// the sign check's own words, so the leg fails if the check is removed and the refusal moves downstream.
func TestIntegration_FW1_05_R2_AWithdrawalLineIsRefusedByTheSignCheckAlone(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may26 := time.Date(year, 5, 26, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may26)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	const payID = "87057703-0000-4000-8000-000000000001"
	insertPayment(t, pool, payID, branch, "CARD", "", 50000)
	lineID := fw104InsertLineAt(t, pool, acct, may26, "WIRE WITHDRAWAL R2 SIGN", "WDR-R2-SIGN", -50000)

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may26 }}).
		WithGLBalances(glSvc)

	// The caller amount matches the line's sign and size exactly, so only the sign check can refuse.
	_, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID,
		AmountCents: -50000, CanAccessBranch: AllBranches,
	}, "")
	if err == nil {
		t.Fatalf("P3-1: a negative line with a matching negative caller amount was accepted for a payment")
	}
	if !strings.Contains(err.Error(), "only a deposit (a positive line) can be matched to a payment") {
		t.Fatalf("P3-1: the refusal is not the sign check's own (got %q), so the sign check is not what refuses", err)
	}
	assertLineStatus(t, pool, lineID, LineUnmatched)
	if st := paymentStatus(t, pool, payID); st != "RECEIVED" {
		t.Fatalf("P3-1: payment status = %s, want RECEIVED", st)
	}
}

// P3-2 (S5's review of PR 632, round 2): item 6's negative remainder guard is pinned. A settlement line
// ABOVE the payment (a deposit the payment cannot explain) inside the rule's tolerance must be refused by
// bankrec itself (gross >= line in the gateway exact pair, and settleCardPayment refusing a negative fee),
// not merely by the GL's own balance check downstream.
func TestIntegration_FW1_05_R2_ALineAboveThePaymentInsideToleranceIsRefused(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may28 := time.Date(year, 5, 28, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may28)
	t.Cleanup(func() {
		// Restore gateway-ref-exact to 0 tolerance after the test.
		svcRestore := NewService(NewRepository(pool))
		_, _ = svcRestore.ApproveRule(context.Background(), RuleInput{
			Code: "gateway-ref-exact", Name: "Card settlement - gateway reference exact", Priority: 10,
			Spec: map[string]any{"kind": "gateway_ref_exact", "target": "PAYMENT", "tolerance_cents": float64(0)},
		}, "")
		fw104CleanYear(t, pool, acct, year)
	})

	const payID = "87057704-0000-4000-8000-000000000001"
	insertPayment(t, pool, payID, branch, "CARD", "GTW-R2-NEG", 10000)
	// The line is 30 cents ABOVE the payment, inside the 50 cent tolerance.
	lineID := fw104InsertLineAt(t, pool, acct, may28, "GATEWAY SETTLEMENT ABOVE PAYMENT", "GTW-R2-NEG", 10030)
	paySeam := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, BranchID: branch, GatewayRef: "GTW-R2-NEG", AmountCents: 10000, ReceivedAt: may28}}}

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithPayments(paySeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may28 }}).
		WithGLBalances(glSvc)

	if _, err := svc.ApproveRule(ctx, RuleInput{
		Code: "gateway-ref-exact", Name: "Gateway exact 50c tolerance", Priority: 10,
		Spec: map[string]any{"kind": "gateway_ref_exact", "target": "PAYMENT", "tolerance_cents": float64(50), "fee_account_code": "6300"},
	}, ""); err != nil {
		t.Fatalf("approve gateway-ref-exact with 50c tolerance: %v", err)
	}

	sum, err := svc.AutoMatchReach(ctx, acct, may28.AddDate(0, 0, -1), may28.AddDate(0, 0, 1), "", AllBranches)
	if err != nil {
		t.Fatalf("P3-2: AutoMatchReach errored on a line 30 cents above its payment (bankrec, not the GL, must refuse it): %v", err)
	}
	if sum.Matched != 0 {
		t.Fatalf("P3-2: a line 30 cents above the payment auto-matched inside a 50c tolerance (Matched=%d, want 0)", sum.Matched)
	}
	assertLineStatus(t, pool, lineID, LineUnmatched)
	if st := paymentStatus(t, pool, payID); st != "RECEIVED" {
		t.Fatalf("P3-2: payment status = %s, want RECEIVED", st)
	}
}
