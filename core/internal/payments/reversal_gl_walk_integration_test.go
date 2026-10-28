//go:build integration

// reversal_gl_walk_integration_test.go: every payment.reversed credits 2310 refund_payable, walked to the end (SF-2d
// round 4, brief r2, ruling 52).
//
// A reversal leaves the payment RECEIVED, and the till's expected tenders count every RECEIVED payment of the
// session. So the money is either still in the drawer (lodged, and the lodgement's own journal clears 1020 while
// the bank match only confirms the line, FROM-PRINCIPAL 117 and 120) or gone unrecorded
// (the count is short and the close posts Dr 6350 / Cr 1020). Crediting 1020 at the reversal credits it twice in
// either case. The reversal credits 2310, the money held for the customer, whatever the tender, session, register
// or deposit status; the act that moves the money clears it later (re-application, pay out, write off).
//
// Each walk runs through the real settle, the real reversal, the real till repository, the real bank match and the
// real GL consumer, and ends with the accounts at their true balances (net = debit minus credit).
package payments_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// registerReceipt takes a payment through the AR register on a fresh invoice and returns its id.
func (w *reversalGLWorld) registerReceipt(method string, cents int64) string {
	w.r.t.Helper()
	inv := w.r.invoice("RW"+method, 1_000_000)
	if _, err := w.r.register().RecordPayment(context.Background(), billing.ReceiptInput{
		InvoiceID: inv, Method: method, AmountCents: cents, Reference: "walk", IdempotencyKey: fmt.Sprintf("walk-%d", time.Now().UnixNano()),
	}); err != nil {
		w.r.t.Fatalf("%s register receipt: %v", method, err)
	}
	var id string
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT p.id::text FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
		  WHERE a.invoice_id = $1::uuid AND p.reverses_payment_id IS NULL`, inv).Scan(&id); err != nil {
		w.r.t.Fatalf("find the receipt: %v", err)
	}
	return id
}

func (w *reversalGLWorld) openSession() string {
	w.r.t.Helper()
	repo := till.NewRepository(w.r.pool)
	id, err := repo.OpenSession(context.Background(), till.TillSession{
		BranchID: w.r.branchID, Station: "WALK" + w.r.suffix, OpenedBy: w.r.userID, FloatCents: 0, BlindClose: true,
	}, nil)
	if err != nil {
		w.r.t.Fatalf("open the till: %v", err)
	}
	w.r.t.Cleanup(func() {
		c := context.Background()
		_, _ = w.r.obs.Exec(c, `UPDATE payments SET till_session_id = NULL WHERE till_session_id = $1::uuid`, id)
		_, _ = w.r.obs.Exec(c, `DELETE FROM till_movements WHERE till_session_id = $1::uuid`, id)
		_, _ = w.r.obs.Exec(c, `DELETE FROM till_sessions WHERE id = $1::uuid`, id)
	})
	return id
}

func (w *reversalGLWorld) inSession(session, paymentID string) {
	w.r.t.Helper()
	if _, err := w.r.obs.Exec(context.Background(), `UPDATE payments SET till_session_id = $1::uuid WHERE id = $2::uuid`, session, paymentID); err != nil {
		w.r.t.Fatalf("put the payment in the session: %v", err)
	}
}

// lodgeAndMatch records a LODGEMENT of the method on the session and matches it to a bank line. The lodgement posts
// its own journal (Dr 1000 / Cr 1020, FROM-PRINCIPAL 117) and the bank match only confirms the line (FROM-PRINCIPAL
// 117 and 120), so the walk must name the lodgement's journal to see the money reach the bank.
func (w *reversalGLWorld) lodgeAndMatch(session, method string, cents int64) (matchID, movement, line string) {
	w.r.t.Helper()
	ctx := context.Background()
	repo := till.NewRepository(w.r.pool)
	// CreatedAt: time.Now() is the column default this seed used to ride; the walk reads the row
	// back by created_at DESC.
	if err := repo.InsertMovement(ctx, till.MovementRow{TillSessionID: session, MovementType: "LODGEMENT", Method: method, AmountCents: cents, Reason: "walk", Reference: "WALK-SLIP", CreatedBy: w.r.userID, CreatedAt: time.Now()}); err != nil {
		w.r.t.Fatalf("lodgement: %v", err)
	}
	if err := w.r.obs.QueryRow(ctx, `SELECT id::text FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = 'LODGEMENT' ORDER BY created_at DESC LIMIT 1`, session).Scan(&movement); err != nil {
		w.r.t.Fatalf("find the lodgement: %v", err)
	}
	line = w.r.statementLine(cents)
	bank := bankrec.NewService(bankrec.NewRepository(w.r.pool)).WithJournalPoster(glPosterOver{w.gl}).WithLodgements(repo)
	m, err := bank.ManualMatch(ctx, bankrec.MatchInput{Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetLodgement, TargetRefID: movement, AmountCents: cents, CanAccessBranch: bankrec.AllBranches}, w.r.userID)
	if err != nil {
		w.r.t.Fatalf("the lodgement match: %v", err)
	}
	return m.ID, movement, line
}

// closeSession counts the drawer and closes the session the way the till service does: expected cash is the float
// plus the RECEIVED cash of the session plus paid in minus paid out minus lodgements; over/short is counted minus
// expected, posted by the close through till.session.completed.
func (w *reversalGLWorld) closeSession(session string, countedCash int64) {
	w.r.t.Helper()
	ctx := context.Background()
	repo := till.NewRepository(w.r.pool)
	expected, err := repo.ExpectedTenders(ctx, session)
	if err != nil {
		w.r.t.Fatalf("expected tenders: %v", err)
	}
	mv, err := repo.MovementTotalsByMethod(ctx, session)
	if err != nil {
		w.r.t.Fatalf("movements: %v", err)
	}
	cash := mv["CASH"]
	expectedCash := expected["CASH"] + cash.PaidIn - cash.PaidOut - cash.Lodgement
	if err := repo.SetCounted(ctx, session, w.r.userID, time.Now(), 0); err != nil {
		w.r.t.Fatalf("count: %v", err)
	}
	if err := repo.SetCompleted(ctx, session, w.r.branchID, w.r.userID, time.Now(), countedCash-expectedCash, countedCash, ""); err != nil {
		w.r.t.Fatalf("close: %v", err)
	}
}

func (w *reversalGLWorld) walkNets(keys, refs []string) map[string]int64 {
	w.r.t.Helper()
	w.drain()
	return w.nets(keys, refs)
}

func wantNets(t *testing.T, name string, got, want map[string]int64) {
	t.Helper()
	t.Logf("%s: GL net by account %v", name, got)
	for code, n := range want {
		if got[code] != n {
			t.Errorf("%s: account %s nets %d, want %d", name, code, got[code], n)
		}
	}
	for code, n := range got {
		if _, ok := want[code]; !ok && n != 0 {
			t.Errorf("%s: account %s nets %d, want it untouched", name, code, n)
		}
	}
}

// (a) till cash on an OPEN session, reversed; then the session lodges its cash, the lodgement is matched, and the
// session is counted and closed. 1020 ends at zero, the bank holds the money, 2310 holds the reversed amount.
func TestIntegration_ReversalGL_Walk_TillCashReversedThenLodgedAndClosed(t *testing.T) {
	w := newReversalGLWorld(t)
	session := w.openSession()
	orig := w.registerReceipt("CASH", 10_000)
	w.inSession(session, orig)
	w.drain()
	rev := w.reverse(orig)
	matchID, movement, line := w.lodgeAndMatch(session, "CASH", 10_000)
	w.closeSession(session, 0)
	keys := append(caseKeys(orig, rev), "till.movement.posted:"+movement, "bankrec:match:"+matchID, "till.session.completed:"+session)
	w.cleanupJournals(keys, []string{line})
	wantNets(t, "(a) till cash", w.walkNets(keys, []string{line}), map[string]int64{"1000": 10_000, "1020": 0, "1100": 0, "2310": -10_000})
}

// (b) an AR register cheque, reversed, then banked by a cheque lodgement.
func TestIntegration_ReversalGL_Walk_RegisterChequeReversedThenBanked(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.registerReceipt("CHEQUE", 10_000)
	w.drain()
	rev := w.reverse(orig)
	session := w.openSession()
	matchID, movement, line := w.lodgeAndMatch(session, "CHEQUE", 10_000)
	keys := append(caseKeys(orig, rev), "till.movement.posted:"+movement, "bankrec:match:"+matchID)
	w.cleanupJournals(keys, []string{line})
	wantNets(t, "(b) register cheque", w.walkNets(keys, []string{line}), map[string]int64{"1000": 10_000, "1020": 0, "1100": 0, "2310": -10_000})
}

// (c) a card payment deposited by a bank PAYMENT match, then reversed.
func TestIntegration_ReversalGL_Walk_BankDepositedThenReversed(t *testing.T) {
	w := newReversalGLWorld(t)
	res := w.sequenceFor(t, "CARD")
	wantNets(t, "(c) bank deposited", res.nets, map[string]int64{"1000": 10_000, "1010": 0, "1100": 0, "2310": -10_000})
}

// (d) the shortage case: a cash payment reversed and the cash gone; the count is short and the close posts
// Dr 6350 / Cr 1020. 1020 ends at zero and 2310 holds the amount until a write off clears it.
func TestIntegration_ReversalGL_Walk_ReversedCashGoneThenTheCountIsShort(t *testing.T) {
	w := newReversalGLWorld(t)
	session := w.openSession()
	orig := w.registerReceipt("CASH", 10_000)
	w.inSession(session, orig)
	w.drain()
	rev := w.reverse(orig)
	w.closeSession(session, 0)
	keys := append(caseKeys(orig, rev), "till.session.completed:"+session)
	w.cleanupJournals(keys, nil)
	wantNets(t, "(d) short count", w.walkNets(keys, nil), map[string]int64{"1020": 0, "1100": 0, "2310": -10_000, "6350": 10_000})
}

// The card case (ruling 52.2): a card captured, reversed, then the processor's settlement line arrives. The reversal
// does not touch the gateway, so the capture settles anyway; the bank claim must accept that settlement so the line
// reconciles. 1010 clears, the bank holds the money, 2310 holds the amount for the customer.
func TestIntegration_ReversalGL_Walk_ACardReversedThenTheProcessorSettles(t *testing.T) {
	w := newReversalGLWorld(t)
	ctx := context.Background()
	orig := w.receipt("CARD", 10_000)
	w.drain()
	rev := w.reverse(orig)
	line := w.r.statementLine(10_000)
	bank := bankrec.NewService(bankrec.NewRepository(w.r.pool)).WithJournalPoster(glPosterOver{w.gl})
	m, err := bank.ManualMatch(ctx, bankrec.MatchInput{Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetPayment, TargetRefID: orig, AmountCents: 10_000, CanAccessBranch: bankrec.AllBranches}, w.r.userID)
	keys := append(caseKeys(orig, rev), "bankrec:match:"+m.ID)
	w.cleanupJournals(keys, []string{line})
	if err != nil {
		t.Fatalf("the settlement of a reversed card payment was refused: %v", err)
	}
	wantNets(t, "card settles after the reversal", w.walkNets(keys, []string{line}), map[string]int64{"1000": 10_000, "1010": 0, "1100": 0, "2310": -10_000})
}

// SF-2b's rule stays for every other tender: a reversed cheque is never matched.
func TestIntegration_ReversalGL_Walk_AReversedChequeIsStillNeverMatched(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.registerReceipt("CHEQUE", 10_000)
	w.drain()
	rev := w.reverse(orig)
	line := w.r.statementLine(10_000)
	bank := bankrec.NewService(bankrec.NewRepository(w.r.pool)).WithJournalPoster(glPosterOver{w.gl})
	_, err := bank.ManualMatch(context.Background(), bankrec.MatchInput{Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetPayment, TargetRefID: orig, AmountCents: 10_000, CanAccessBranch: bankrec.AllBranches}, w.r.userID)
	w.cleanupJournals(caseKeys(orig, rev), []string{line})
	if err == nil {
		t.Fatalf("a reversed cheque was matched")
	}
}
