//go:build integration

package bankrec

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// Item 1: A match by hand checks amount, method and sign, and credits the right clearing account
// (1010 for card, 1020 for cash and cheque).
func TestIntegration_FW1_05_ManualMatchChecksAmountMethodSignAndClearingAccount(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may12 := time.Date(year, 5, 12, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may12)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	const (
		smallPayID  = "87050011-0000-4000-8000-000000000001"
		chequePayID = "87050012-0000-4000-8000-000000000002"
		cardPayID   = "87050013-0000-4000-8000-000000000003"
	)
	insertPayment(t, pool, smallPayID, branch, "CARD", "GTW-FW105-SMALL", 5000)
	insertPayment(t, pool, chequePayID, branch, "CHEQUE", "CHQ-FW105-500", 50000)
	insertPayment(t, pool, cardPayID, branch, "CARD", "GTW-FW105-400", 40000)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id IN ($1::uuid, $2::uuid, $3::uuid)`, smallPayID, chequePayID, cardPayID)
	})

	deposit500Line := fw104InsertLineAt(t, pool, acct, may12, "CHQ DEPOSIT 500", "DEP-500", 50000)
	deposit400Line := fw104InsertLineAt(t, pool, acct, may12, "CARD DEPOSIT 400", "DEP-400", 40000)
	withdrawalLine := fw104InsertLineAt(t, pool, acct, may12, "WIRE WITHDRAWAL", "WDR-500", -50000)

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may12 }}).
		WithGLBalances(glSvc)

	// (a) Hand matching a $500.00 deposit line to a $50.00 payment must be refused.
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: deposit500Line, TargetType: TargetPayment, TargetRefID: smallPayID, CanAccessBranch: AllBranches,
	}, ""); err == nil {
		t.Fatalf("Item 1a: hand matching a 50000-cent line to a 5000-cent payment was accepted")
	}
	assertLineStatus(t, pool, deposit500Line, LineUnmatched)
	if st := paymentStatus(t, pool, smallPayID); st != "RECEIVED" {
		t.Fatalf("Item 1a: rejected amount-mismatch payment status = %s, want RECEIVED", st)
	}

	// (b) Hand matching with a caller-supplied AmountCents that disagrees with the statement line must be refused.
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: deposit500Line, TargetType: TargetPayment, TargetRefID: chequePayID, AmountCents: 12345, CanAccessBranch: AllBranches,
	}, ""); err == nil {
		t.Fatalf("Item 1b: hand matching with mismatched input AmountCents=12345 on a 50000-cent line was accepted")
	}

	// (c) Hand matching a negative withdrawal statement line to a payment must be refused.
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: withdrawalLine, TargetType: TargetPayment, TargetRefID: chequePayID, CanAccessBranch: AllBranches,
	}, ""); err == nil {
		t.Fatalf("Item 1c: hand matching a negative withdrawal line (-50000) to a payment was accepted")
	}
	assertLineStatus(t, pool, withdrawalLine, LineUnmatched)

	// (d) Hand matching a $500.00 CHEQUE payment to the $500.00 deposit line must credit 1020 (Undeposited Funds),
	// never 1010 (Card Processor Clearing).
	chqMatch, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: deposit500Line, TargetType: TargetPayment, TargetRefID: chequePayID, AmountCents: 50000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("Item 1d: valid cheque manual match failed: %v", err)
	}
	assertJournalLegs(t, pool, chqMatch.JournalID, map[string][2]int64{
		AcctBank:         {50000, 0},
		AcctUndeposited:  {0, 50000},
		AcctCardClearing: {0, 0},
	})

	// (e) Hand matching a $400.00 CARD payment to the $400.00 deposit line credits 1010 (Card Processor Clearing).
	cardMatch, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: deposit400Line, TargetType: TargetPayment, TargetRefID: cardPayID, AmountCents: 40000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("Item 1e: valid card manual match failed: %v", err)
	}
	assertJournalLegs(t, pool, cardMatch.JournalID, map[string][2]int64{
		AcctBank:         {40000, 0},
		AcctCardClearing: {0, 40000},
		AcctUndeposited:  {0, 0},
	})
}

// Item 2: The match and its journal commit in one transaction (and unmatch and its reversal journal commit in one transaction).
func TestIntegration_FW1_05_MatchAndJournalCommitAtomicallyInOneTransaction(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may14 := time.Date(year, 5, 14, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may14)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE gl_accounts SET is_active = true WHERE code = '1010'`)
		fw104CleanYear(t, pool, acct, year)
	})

	const payID = "87050002-0000-4000-8000-000000000001"
	insertPayment(t, pool, payID, branch, "CARD", "GTW-FW105-TX", 60000)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, payID) })

	lineID := fw104InsertLineAt(t, pool, acct, may14, "CARD SETTLEMENT ATOMIC", "GTW-FW105-TX", 60000)

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may14 }}).
		WithGLBalances(glSvc)

	// Deactivate 1010 so the GL refuses to post the clearing journal.
	if _, err := pool.Exec(ctx, `UPDATE gl_accounts SET is_active = false WHERE code = '1010'`); err != nil {
		t.Fatalf("deactivate 1010: %v", err)
	}
	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID, AmountCents: 60000, CanAccessBranch: AllBranches,
	}, ""); err == nil {
		t.Fatalf("Item 2a: ManualMatch succeeded when GL account 1010 was inactive")
	}

	// Because the match and its journal share one transaction, the line must remain UNMATCHED, the payment must
	// remain RECEIVED, and bank_matches must have 0 live rows.
	assertLineStatus(t, pool, lineID, LineUnmatched)
	if st := paymentStatus(t, pool, payID); st != "RECEIVED" {
		t.Fatalf("Item 2a: after journal failure, payment status = %s, want RECEIVED (atomic rollback)", st)
	}
	var liveMatches int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM bank_matches WHERE statement_line_id = $1::uuid AND unmatched_at IS NULL`, lineID).Scan(&liveMatches)
	if liveMatches != 0 {
		t.Fatalf("Item 2a: after journal failure, found %d live bank_matches rows, want 0", liveMatches)
	}

	// Re-activate 1010 and perform the match cleanly.
	if _, err := pool.Exec(ctx, `UPDATE gl_accounts SET is_active = true WHERE code = '1010'`); err != nil {
		t.Fatalf("reactivate 1010: %v", err)
	}
	m, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID, AmountCents: 60000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("Item 2b setup match: %v", err)
	}
	if m.JournalID == "" {
		t.Fatalf("Item 2b: committed match has empty JournalID")
	}

	// Deactivate 1010 again and attempt UnmatchReach: the reversal journal fails, so the unmatch must roll back
	// atomically, leaving the line MATCHED and the payment DEPOSITED.
	if _, err := pool.Exec(ctx, `UPDATE gl_accounts SET is_active = false WHERE code = '1010'`); err != nil {
		t.Fatalf("deactivate 1010 for unmatch: %v", err)
	}
	if err := svc.UnmatchReach(ctx, m.ID, "", AllBranches); err == nil {
		t.Fatalf("Item 2b: UnmatchReach succeeded when GL account 1010 was inactive")
	}
	assertLineStatus(t, pool, lineID, LineMatched)
	if st := paymentStatus(t, pool, payID); st != "DEPOSITED" {
		t.Fatalf("Item 2b: after unmatch journal failure, payment status = %s, want DEPOSITED", st)
	}
	if _, err := pool.Exec(ctx, `UPDATE gl_accounts SET is_active = true WHERE code = '1010'`); err != nil {
		t.Fatalf("restore 1010: %v", err)
	}
}

// Item 3: Undo reverses the fee leg (1000, 1010, and 6300 all return to zero net movement on unmatch).
func TestIntegration_FW1_05_UnmatchReversesFeeLeg(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may16 := time.Date(year, 5, 16, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may16)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	const payID = "87050003-0000-4000-8000-000000000001"
	insertPayment(t, pool, payID, branch, "CARD", "", 100000)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, payID) })

	lineID := fw104InsertLineAt(t, pool, acct, may16, "RUN BATCH SETTLEMENT FEE UNDO", "", 98000)
	paySeam := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, BranchID: branch, AmountCents: 100000, ReceivedAt: may16}}}

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithPayments(paySeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may16 }}).
		WithGLBalances(glSvc)

	sum, err := svc.AutoMatchReach(ctx, acct, may16.AddDate(0, 0, -1), may16.AddDate(0, 0, 1), "", AllBranches)
	if err != nil {
		t.Fatalf("automatch fee split: %v", err)
	}
	if sum.Matched != 1 {
		t.Fatalf("expected 1 match, got %d", sum.Matched)
	}

	var matchID, clearingJID string
	if err := pool.QueryRow(ctx,
		`SELECT id::text, COALESCE(journal_id::text,'') FROM bank_matches WHERE statement_line_id = $1::uuid AND unmatched_at IS NULL`,
		lineID).Scan(&matchID, &clearingJID); err != nil {
		t.Fatalf("load match: %v", err)
	}
	assertJournalLegs(t, pool, clearingJID, map[string][2]int64{
		AcctBank:         {98000, 0},
		"6300":           {2000, 0},
		AcctCardClearing: {0, 100000},
	})

	if err := svc.UnmatchReach(ctx, matchID, "", AllBranches); err != nil {
		t.Fatalf("unmatch fee split: %v", err)
	}

	var reversalJID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(matchID)).Scan(&reversalJID); err != nil {
		t.Fatalf("lookup reversal journal: %v", err)
	}
	assertJournalLegs(t, pool, reversalJID, map[string][2]int64{
		AcctCardClearing: {100000, 0},
		AcctBank:         {0, 98000},
		"6300":           {0, 2000},
	})
}

// Item 4: Every bankrec journal carries the target/account branch on gl_journals and gl_journal_lines.
func TestIntegration_FW1_05_EveryBankrecJournalCarriesBranch(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may18 := time.Date(year, 5, 18, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may18)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	const (
		payID  = "87050004-0000-4000-8000-000000000001"
		moveID = "87050004-0000-4000-8000-000000000002"
	)
	insertPayment(t, pool, payID, branch, "CARD", "GTW-FW105-BR", 45000)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, payID) })

	payLine := fw104InsertLineAt(t, pool, acct, may18, "MANUAL PAY BRANCH", "BR-PAY", 45000)
	lodgLine := fw104InsertLineAt(t, pool, acct, may18, "MANUAL LODG BRANCH", "BR-LODG", 35000)

	lodgSeam := &fakeLodgements{open: []Lodgement{{MovementID: moveID, BranchID: branch, AmountCents: 35000, Reference: "BR-LODG", OccurredAt: may18}}}
	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithLodgements(lodgSeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may18 }}).
		WithGLBalances(glSvc)

	payMatch, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: payLine, TargetType: TargetPayment, TargetRefID: payID, AmountCents: 45000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("manual match payment: %v", err)
	}
	lodgMatch, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lodgLine, TargetType: TargetLodgement, TargetRefID: moveID, AmountCents: 35000, CanAccessBranch: AllBranches,
	}, "")
	if err != nil {
		t.Fatalf("manual match lodgement: %v", err)
	}
	if err := svc.UnmatchReach(ctx, payMatch.ID, "", AllBranches); err != nil {
		t.Fatalf("unmatch payment: %v", err)
	}
	var revJID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM gl_journals WHERE idempotency_key = $1`, unmatchKey(payMatch.ID)).Scan(&revJID); err != nil {
		t.Fatalf("lookup unmatch journal: %v", err)
	}

	for label, jID := range map[string]string{
		"manual_match_payment":   payMatch.JournalID,
		"manual_match_lodgement": lodgMatch.JournalID,
		"unmatch_reversal":       revJID,
	} {
		var nullLines int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journal_lines WHERE journal_id = $1::uuid AND branch_id IS NULL`, jID).Scan(&nullLines); err != nil {
			t.Fatalf("%s count null lines: %v", label, err)
		}
		if nullLines != 0 {
			t.Errorf("Item 4 (%s): journal %s has %d lines with NULL branch_id, want 0", label, jID, nullLines)
		}
	}
}

// Item 5: Identical lines in one file are kept apart (both land, and re-importing the same file adds zero).
func TestIntegration_FW1_05_IdenticalLinesInOneFileAreKeptApart(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	ctx := context.Background()

	csvBody := strings.Join([]string{
		"Date,Description,Reference,Amount",
		"2026-06-05,COUNTER DEPOSIT,,500.00",
		"2026-06-05,COUNTER DEPOSIT,,500.00",
	}, "\n")

	svc := NewService(NewRepository(pool))
	res1, err := svc.Import(ctx, ImportInput{BankAccountID: acct, Content: csvBody}, "")
	if err != nil {
		t.Fatalf("first import: %v", err)
	}
	if res1.Inserted != 2 || res1.Duplicates != 0 {
		t.Fatalf("Item 5: first import of 2 identical lines in one CSV got Inserted=%d Duplicates=%d, want Inserted=2 Duplicates=0",
			res1.Inserted, res1.Duplicates)
	}

	res2, err := svc.Import(ctx, ImportInput{BankAccountID: acct, Content: csvBody}, "")
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if res2.Inserted != 0 || res2.Duplicates != 2 {
		t.Fatalf("Item 5: re-import of the same CSV got Inserted=%d Duplicates=%d, want Inserted=0 Duplicates=2",
			res2.Inserted, res2.Duplicates)
	}
}

// Item 6: The tolerance never unbalances a journal (gateway tolerance books the remainder to the fee account,
// and lodgement rules refuse non-zero tolerance).
func TestIntegration_FW1_05_ToleranceNeverUnbalancesAJournal(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may20 := time.Date(year, 5, 20, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may20)
	t.Cleanup(func() {
		// Restore gateway-ref-exact to 0 tolerance after the test.
		svcRestore := NewService(NewRepository(pool))
		_, _ = svcRestore.ApproveRule(context.Background(), RuleInput{
			Code: "gateway-ref-exact", Name: "Card settlement - gateway reference exact", Priority: 10,
			Spec: map[string]any{"kind": "gateway_ref_exact", "target": "PAYMENT", "tolerance_cents": float64(0)},
		}, "")
		fw104CleanYear(t, pool, acct, year)
	})

	glSvc := gl.NewService(gl.NewRepository(pool))
	const payID = "87050006-0000-4000-8000-000000000001"
	insertPayment(t, pool, payID, branch, "CARD", "GTW-TOL-50", 100000)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, payID) })

	lineID := fw104InsertLineAt(t, pool, acct, may20, "GATEWAY SETTLEMENT WITH 50C FEE", "GTW-TOL-50", 99950)
	paySeam := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, BranchID: branch, GatewayRef: "GTW-TOL-50", AmountCents: 100000, ReceivedAt: may20}}}

	svc := NewService(NewRepository(pool)).
		WithPayments(paySeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may20 }}).
		WithGLBalances(glSvc)

	// Approving a lodgement_ref_exact rule with non-zero tolerance must be refused.
	if _, err := svc.ApproveRule(ctx, RuleInput{
		Code: "lodgement-ref-exact", Name: "Lodgement with drift", Priority: 20,
		Spec: map[string]any{"kind": "lodgement_ref_exact", "target": "LODGEMENT", "tolerance_cents": float64(50)},
	}, ""); err == nil {
		t.Errorf("Item 6a: approving lodgement-ref-exact with tolerance_cents=50 was accepted")
	}

	// Approve gateway-ref-exact with tolerance_cents = 50.
	if _, err := svc.ApproveRule(ctx, RuleInput{
		Code: "gateway-ref-exact", Name: "Gateway exact 50c tolerance", Priority: 10,
		Spec: map[string]any{"kind": "gateway_ref_exact", "target": "PAYMENT", "tolerance_cents": float64(50), "fee_account_code": "6300"},
	}, ""); err != nil {
		t.Fatalf("approve gateway-ref-exact with 50c tolerance: %v", err)
	}

	sum, err := svc.AutoMatchReach(ctx, acct, may20.AddDate(0, 0, -1), may20.AddDate(0, 0, 1), "", AllBranches)
	if err != nil {
		t.Fatalf("Item 6b: AutoMatchReach failed under 50c gateway tolerance: %v", err)
	}
	if sum.Matched != 1 {
		t.Fatalf("Item 6b: expected 1 match under 50c gateway tolerance, got %d", sum.Matched)
	}
	jID, _ := matchFacts(t, pool, lineID)
	assertJournalLegs(t, pool, jID, map[string][2]int64{
		AcctBank:         {99950, 0},
		"6300":           {50, 0},
		AcctCardClearing: {0, 100000},
	})
}

// Item 7: The card variance has the right sign on a CREDIT_CARD account (2110).
func TestIntegration_FW1_05_CreditCardVarianceHasRightSign(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	ctx := context.Background()

	var visaAcctID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM bank_accounts WHERE gl_account_code = '2110' LIMIT 1`).Scan(&visaAcctID); err != nil {
		t.Fatalf("lookup 2110 Visa bank account: %v", err)
	}

	const year = 2023
	may10 := time.Date(year, 5, 10, 0, 0, 0, 0, time.UTC)
	june05 := time.Date(year, 6, 5, 12, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, visaAcctID, year)
	fw104EnsurePeriods(t, pool, year, may10)
	t.Cleanup(func() { fw104CleanYear(t, pool, visaAcctID, year) })

	glSvc := gl.NewService(gl.NewRepository(pool))
	// A $120.45 card purchase: DR 6300 / CR 2110 = 12045 cents.
	purchaseJID, err := glSvc.PostExternal(ctx, gl.ExternalJournal{
		IdempotencyKey: "fw105-visa-purchase-1",
		Source:         "ADJUSTMENT",
		Memo:           "Supplier card purchase",
		DocumentDate:   may10.Add(12 * time.Hour),
		Lines: []gl.ExternalLine{
			{AccountCode: "6300", DebitCents: 12045, Memo: "supplies"},
			{AccountCode: "2110", CreditCents: 12045, Memo: "corporate visa"},
		},
	})
	if err != nil {
		t.Fatalf("post visa purchase journal: %v", err)
	}

	// On the Visa statement, a card purchase is negative (-12045 cents).
	visaLineID := fw104InsertLineAt(t, pool, visaAcctID, may10, "HOME HARDWARE #4412", "VISA-4412", -12045)

	svc := NewService(NewRepository(pool)).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return june05 }}).
		WithGLBalances(glSvc)

	if _, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: visaLineID, TargetType: TargetJournal, TargetRefID: purchaseJID, AmountCents: -12045, CanAccessBranch: AllBranches,
	}, ""); err != nil {
		t.Fatalf("match visa line to purchase journal: %v", err)
	}

	rec, err := svc.OpenReconciliation(ctx, ReconInput{
		BankAccountID: visaAcctID, Year: year, Month: 5, StatementBalanceCents: -12045,
	})
	if err != nil {
		t.Fatalf("open visa reconciliation: %v", err)
	}
	if rec.VarianceCents != 0 {
		t.Fatalf("Item 7: credit card reconciliation VarianceCents = %d (GLBalanceCents=%d, Adjusted=%d), want 0",
			rec.VarianceCents, rec.GLBalanceCents, rec.AdjustedBankBalanceCents)
	}
}

// Item 8 (FROM-PRINCIPAL 84): The batch card settlement rule no longer books a whole shortfall as a fee.
func TestIntegration_FW1_05_BatchCardSettlementRuleDoesNotBookWholeShortfallAsFee(t *testing.T) {
	pool := intPool(t)
	cleanTables(t, pool)
	acct := rbcAccountID(t, pool)
	branch := aBranch(t, pool)
	ctx := context.Background()

	const year = 2023
	may22 := time.Date(year, 5, 22, 0, 0, 0, 0, time.UTC)
	fw104CleanYear(t, pool, acct, year)
	fw104EnsurePeriods(t, pool, year, may22)
	t.Cleanup(func() { fw104CleanYear(t, pool, acct, year) })

	// A $6,000.00 card payment in the window and a $4,890.00 RUN batch deposit line ($1,110.00 shortfall).
	// The batch settlement rule must NOT book the $1,110.00 shortfall as a merchant fee to 6300.
	const payID = "87050008-0000-4000-8000-000000000001"
	insertPayment(t, pool, payID, branch, "CARD", "", 600000)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM payments WHERE id = $1::uuid`, payID) })

	lineID := fw104InsertLineAt(t, pool, acct, may22, "RUN BATCH SETTLEMENT SHORTFALL", "", 489000)
	paySeam := &fakePayments{cands: []CandidatePayment{{PaymentID: payID, BranchID: branch, AmountCents: 600000, ReceivedAt: may22}}}

	glSvc := gl.NewService(gl.NewRepository(pool))
	svc := NewService(NewRepository(pool)).
		WithPayments(paySeam).
		WithJournals(glSvc).
		WithJournalPoster(fw104GLPoster{svc: glSvc, clock: func() time.Time { return may22 }}).
		WithGLBalances(glSvc)

	sum, err := svc.AutoMatchReach(ctx, acct, may22.AddDate(0, 0, -1), may22.AddDate(0, 0, 1), "", AllBranches)
	if err != nil {
		t.Fatalf("automatch batch shortfall: %v", err)
	}
	if sum.Matched != 0 {
		t.Fatalf("Item 8: $4,890.00 RUN batch line auto-matched a $6,000.00 payment and booked $1,110.00 shortfall as a fee (Matched=%d, want 0)", sum.Matched)
	}
	assertLineStatus(t, pool, lineID, LineUnmatched)
	if st := paymentStatus(t, pool, payID); st != "RECEIVED" {
		t.Fatalf("Item 8: $6,000.00 payment status = %s, want RECEIVED", st)
	}
}
