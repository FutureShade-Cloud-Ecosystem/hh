//go:build integration

package bankrec

// FW1-05b (S4's review of sync stack 1, P1): a CASH payment taken in a till session reaches the bank
// only by its lodgement (the lodgement journal debits 1000 at lodgement, FROM-PRINCIPAL 117 and 120),
// so it is never a PAYMENT target. A slipless cash lodgement leaves its cash payments RECEIVED, and
// today one of them can be hand matched alone as a PAYMENT, which debits 1000 a second time and books
// 1010's cash shape. The match is refused in words, with nothing written. Cash or cheques taken
// outside a till (office receipts, no till session) stay matchable as PAYMENT.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
)

// TestIntegration_FW1_05b_TillCashPaymentIsNeverAPaymentTarget is S4's $33.00 case: a cash payment
// taken in a till session, a slipless $33.00 cash lodgement (DR 1000 / CR 1020 posted at lodgement,
// the payment still RECEIVED because no slip named it), then a $33.00 line on 1000 hand matched to
// the payment alone: refused in words, nothing written.
func TestIntegration_FW1_05b_TillCashPaymentIsNeverAPaymentTarget(t *testing.T) {
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

	var userID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY created_at, id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("seeded user: %v", err)
	}
	var sessionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO till_sessions (branch_id, station, status, opened_by)
		VALUES ($1::uuid, 'FW105B', 'OPEN', $2::uuid) RETURNING id::text`, branch, userID).Scan(&sessionID); err != nil {
		t.Fatalf("till session: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM till_sessions WHERE id = $1::uuid`, sessionID) })

	const amount = int64(3300)
	payID := "87057702-0000-4000-8000-000000055551"
	insertPayment(t, pool, payID, branch, "CASH", "", amount)
	if _, err := pool.Exec(ctx, `UPDATE payments SET till_session_id = $1::uuid WHERE id = $2::uuid`, sessionID, payID); err != nil {
		t.Fatalf("tie payment to its till session: %v", err)
	}

	// The slipless cash lodgement: a till movement with no slip reference, its DR 1000 / CR 1020
	// journal posted at lodgement under the till's own key. The payment above is NOT on the slip,
	// so it stays RECEIVED while 1000 already holds the cash.
	var movementID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO till_movements (till_session_id, movement_type, method, amount_cents, reason, reference, created_by)
		VALUES ($1::uuid, 'LODGEMENT', 'CASH', $2, 'cash drop', '', $3::uuid) RETURNING id::text`,
		sessionID, amount, userID).Scan(&movementID); err != nil {
		t.Fatalf("till movement: %v", err)
	}
	lodgementJournal := gl.ExternalJournal{
		Source: "ADJUSTMENT", SourceRefID: movementID, IdempotencyKey: lodgementJournalKey + movementID,
		Memo: "till lodgement (slipless cash)", BranchID: branch, DocumentDate: may24,
		Lines: []gl.ExternalLine{
			{AccountCode: AcctBank, DebitCents: amount, Memo: "bank deposit (lodgement)"},
			{AccountCode: AcctUndeposited, CreditCents: amount, Memo: "clear undeposited funds"},
		},
	}
	if _, err := glSvc.PostExternal(ctx, lodgementJournal); err != nil {
		t.Fatalf("slipless cash lodgement journal: %v", err)
	}

	lineID := fw104InsertLineAt(t, pool, acct, may24, "CASH DEPOSIT 05B TILL", "DEP-05B-CASH", amount)

	_, err := svc.ManualMatch(ctx, MatchInput{
		Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: payID,
		AmountCents: amount, CanAccessBranch: AllBranches,
	}, "")
	if err == nil {
		t.Fatalf("till cash hand match accepted as a PAYMENT target: 1000 now holds the cash twice (lodgement plus match)")
	}
	if !strings.Contains(err.Error(), "till session") {
		t.Fatalf("refusal does not name the till session rule: %v", err)
	}
	if got := paymentStatus(t, pool, payID); got != "RECEIVED" {
		t.Fatalf("payment moved to %s, want RECEIVED (nothing written)", got)
	}
	var lineStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id::text = $1`, lineID).Scan(&lineStatus); err != nil {
		t.Fatalf("line status: %v", err)
	}
	if lineStatus != "UNMATCHED" {
		t.Fatalf("line is %s, want UNMATCHED (nothing written)", lineStatus)
	}
	var matches, journals int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM bank_matches WHERE statement_line_id::text = $1`, lineID).Scan(&matches); err != nil {
		t.Fatalf("match rows: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE idempotency_key LIKE 'bankrec:%'`).Scan(&journals); err != nil {
		t.Fatalf("bankrec journals: %v", err)
	}
	if matches != 0 || journals != 0 {
		t.Fatalf("the refused match wrote %d match rows and %d bankrec journals, want none", matches, journals)
	}
}

// TestIntegration_FW1_05b_OfficeCashAndChequeStillMatch pins the other half of the rule: cash and
// cheques taken OUTSIDE a till (office receipts, no till session) stay matchable as PAYMENT, booking
// DR 1000 / CR 1020 through the tender's own clearing account.
func TestIntegration_FW1_05b_OfficeCashAndChequeStillMatch(t *testing.T) {
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
		method  string
		payID   string
		lineRef string
	}{
		{method: "CASH", payID: "87057702-0000-4000-8000-000000055552", lineRef: "DEP-05B-OFFICE-CASH"},
		{method: "CHEQUE", payID: "87057703-0000-4000-8000-000000055553", lineRef: "DEP-05B-OFFICE-CHQ"},
	} {
		const amount = int64(4500)
		insertPayment(t, pool, tt.payID, branch, tt.method, "", amount)
		lineID := fw104InsertLineAt(t, pool, acct, may24.AddDate(0, 0, i), "OFFICE RECEIPT 05B "+tt.method, tt.lineRef, amount)

		m, err := svc.ManualMatch(ctx, MatchInput{
			Mode: "manual", StatementLineID: lineID, TargetType: TargetPayment, TargetRefID: tt.payID,
			AmountCents: amount, CanAccessBranch: AllBranches,
		}, "")
		if err != nil {
			t.Fatalf("%s office receipt hand match: %v", tt.method, err)
		}
		assertJournalLegs(t, pool, m.JournalID, map[string][2]int64{
			AcctBank:        {amount, 0},
			AcctUndeposited: {0, amount},
		})
		if got := paymentStatus(t, pool, tt.payID); got != "DEPOSITED" {
			t.Fatalf("%s office receipt payment is %s, want DEPOSITED", tt.method, got)
		}
	}
}
