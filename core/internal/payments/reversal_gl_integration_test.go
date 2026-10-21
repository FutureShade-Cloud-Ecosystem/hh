//go:build integration

// reversal_gl_integration_test.go: a payment reversal posts to the GL (SF-2d).
//
// reversal.go writes the reversing payments row, re-opens the invoices, writes the contra subledger rows and emits
// payment.reversed so the GL books the reversing journal. Before SF-2d the topic was not in gl.PostedTopics, so no
// handler took it and the row ended DEAD ("no handler registered for topic"): the GL kept the original receipt's
// legs while the subledger netted to zero.
//
// Each case takes the receipt, then the reversal, drains the outbox through the real GL consumer and reads the GL
// net per account over the journals that belong to the case (the receipt's, the reversal's, the bank clearing's)
// against the AR subledger. The RIGHT answer: the cash leg of the receipt is reversed to zero on the same account,
// AR nets to the subledger (zero), and a deposited receipt's reversal comes out of the bank (1000).
package payments_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/bankrec"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

// glPosterOver is bankrec's JournalPoster over the GL's external posting (the composition root's adapter).
type glPosterOver struct{ svc *gl.Service }

func (a glPosterOver) PostJournal(ctx context.Context, in gl.ExternalJournal) (string, error) {
	return a.svc.PostExternal(ctx, in)
}

type reversalGLWorld struct {
	r    *loRig
	gl   *gl.Service
	disp *notify.Dispatcher
}

func newReversalGLWorld(t *testing.T) *reversalGLWorld {
	t.Helper()
	r := newLORig(t, 4)
	glSvc := gl.NewService(gl.NewRepository(r.pool)).
		WithPaymentReader(till.NewRepository(r.pool)).
		WithDeadRequeuer(notify.NewPgOutboxStore(r.pool))
	disp := notify.NewDispatcher(notify.NewPgOutboxStore(r.pool), nil)
	glSvc.RegisterPostingHandlers(disp)
	return &reversalGLWorld{r: r, gl: glSvc, disp: disp}
}

// drain handles outbox batches until one comes back empty (the suite shares one outbox).
func (w *reversalGLWorld) drain() {
	for i := 0; i < 200; i++ {
		if w.disp.DrainOnce(context.Background()) == 0 {
			return
		}
	}
}

// receipt takes a payment of cents by method on a fresh invoice and returns its id.
func (w *reversalGLWorld) receipt(method string, cents int64) (paymentID string) {
	w.r.t.Helper()
	inv := w.r.invoice("RG"+method, 1_000_000)
	if method == "CARD" {
		return w.r.payOnce(inv, cents).ID
	}
	if err := w.r.settle("register", inv, cents); err != nil {
		w.r.t.Fatalf("cheque receipt: %v", err)
	}
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT p.id::text FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
		  WHERE a.invoice_id = $1::uuid AND p.reverses_payment_id IS NULL`, inv).Scan(&paymentID); err != nil {
		w.r.t.Fatalf("find the receipt: %v", err)
	}
	return paymentID
}

// nets is the GL net (debit minus credit) per account code over the journals that belong to the case.
func (w *reversalGLWorld) nets(keys []string, refs []string) map[string]int64 {
	w.r.t.Helper()
	rows, err := w.r.obs.Query(context.Background(), `
		SELECT a.code, COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0)
		  FROM gl_journal_lines l
		  JOIN gl_journals j ON j.id = l.journal_id
		  JOIN gl_accounts a ON a.id = l.account_id
		 WHERE j.idempotency_key = ANY($1) OR j.source_ref_id::text = ANY($2)
		 GROUP BY a.code`, keys, refs)
	if err != nil {
		w.r.t.Fatalf("read the GL: %v", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var code string
		var n int64
		if err := rows.Scan(&code, &n); err != nil {
			w.r.t.Fatalf("scan: %v", err)
		}
		out[code] = n
	}
	return out
}

// subledger is the AR subledger net for the case's customer: the receipt's row and the reversal's contra row.
func (w *reversalGLWorld) subledger() int64 {
	w.r.t.Helper()
	var n int64
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(amount_cents), 0) FROM customer_transactions WHERE customer_id = $1::uuid AND txn_type = 'PAYMENT'`,
		w.r.custID).Scan(&n); err != nil {
		w.r.t.Fatalf("read the subledger: %v", err)
	}
	return n
}

func (w *reversalGLWorld) outboxStatus(origID string) (status string, n int) {
	w.r.t.Helper()
	rows, err := w.r.obs.Query(context.Background(),
		`SELECT status FROM events_outbox WHERE topic = 'payment.reversed' AND payload->>'reverses_payment_id' = $1`, origID)
	if err != nil {
		w.r.t.Fatalf("read the outbox: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		n++
		if err := rows.Scan(&status); err != nil {
			w.r.t.Fatalf("scan: %v", err)
		}
	}
	return status, n
}

// verdict asserts the right balances: every account the case touched nets to zero (the cash leg reversed on the
// account the receipt hit, the bank clearing reversed out of 1000, AR equal to the subledger), and the reversal's
// outbox row was taken, not dead.
func (w *reversalGLWorld) verdict(t *testing.T, name, origID string, keys, refs []string) {
	t.Helper()
	got := w.nets(keys, refs)
	if lines, err := w.r.obs.Query(context.Background(), `SELECT j.idempotency_key, COALESCE(a.code, 'NOACCT'), COALESCE(l.debit_cents, 0), COALESCE(l.credit_cents, 0)
		FROM gl_journals j JOIN gl_journal_lines l ON l.journal_id = j.id LEFT JOIN gl_accounts a ON a.id = l.account_id
		WHERE j.idempotency_key = ANY($1) OR j.source_ref_id::text = ANY($2) ORDER BY j.created_at, l.line_no`, keys, refs); err != nil {
		t.Logf("  journal read: %v", err)
	} else {
		for lines.Next() {
			var k, code string
			var d, c int64
			if err := lines.Scan(&k, &code, &d, &c); err != nil {
				t.Logf("  journal scan: %v", err)
				break
			}
			t.Logf("  journal %s: %s Dr %d Cr %d", k, code, d, c)
		}
		lines.Close()
	}
	sub := w.subledger()
	t.Logf("%s: GL net by account %v | AR subledger net %d | payment.reversed outbox row: %v", name, got, sub, func() string { s, n := w.outboxStatus(origID); return fmt.Sprintf("%s x%d", s, n) }())
	if st, n := w.outboxStatus(origID); n != 1 || st == "DEAD" || st == "PENDING" {
		t.Errorf("%s: the payment.reversed row is %q (rows %d), want it taken (PUBLISHED)", name, st, n)
	}
	for code, net := range got {
		if net != 0 {
			t.Errorf("%s: account %s nets %d after receipt and reversal, want 0", name, code, net)
		}
	}
	if got["1100"] != sub {
		t.Errorf("%s: GL AR nets %d against the subledger's %d", name, got["1100"], sub)
	}
}

// keys returns the idempotency keys of the case's journals once the reversal id is known.
func caseKeys(origID, revID string) []string {
	return []string{"payment.received:" + origID, "payment.reversed:" + revID}
}

func (w *reversalGLWorld) reverse(origID string) (revID string) {
	w.r.t.Helper()
	rev, err := w.r.repo.ReversePayment(context.Background(), payments.ReverseInput{
		PaymentID: origID, Reason: "reversal gl leg", ActorID: w.r.userID, IsManager: true}, time.Now())
	if err != nil {
		w.r.t.Fatalf("reverse: %v", err)
	}
	return rev.ReversalPaymentID
}

func (w *reversalGLWorld) cleanupJournals(keys, refs []string) {
	w.r.t.Cleanup(func() {
		c := context.Background()
		_, _ = w.r.obs.Exec(c, `DELETE FROM gl_journal_lines WHERE journal_id IN
			(SELECT id FROM gl_journals WHERE idempotency_key = ANY($1) OR source_ref_id::text = ANY($2))`, keys, refs)
		_, _ = w.r.obs.Exec(c, `DELETE FROM gl_journals WHERE idempotency_key = ANY($1) OR source_ref_id::text = ANY($2)`, keys, refs)
	})
}

// (a) cash or cheque, undeposited: Dr 1020 / Cr AR at the receipt; the reversal must credit 1020.
func TestIntegration_ReversalGL_UndepositedChequeReversesTheUndepositedLeg(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	w.drain()
	rev := w.reverse(orig)
	keys := caseKeys(orig, rev)
	w.cleanupJournals(keys, nil)
	w.drain()
	w.verdict(t, "cheque undeposited", orig, keys, nil)
}

// (b) card: Dr 1010 / Cr AR at the receipt; the reversal must credit 1010.
func TestIntegration_ReversalGL_CardReversesTheClearingLeg(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CARD", 10_000)
	w.drain()
	rev := w.reverse(orig)
	keys := caseKeys(orig, rev)
	w.cleanupJournals(keys, nil)
	w.drain()
	w.verdict(t, "card", orig, keys, nil)
}

// (c) deposited first (a bank match posts Dr 1000 / Cr 1010), then reversed: the reversal comes out of the bank.
func TestIntegration_ReversalGL_DepositedThenReversedComesOutOfTheBank(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CARD", 10_000)
	w.drain()
	line := w.r.statementLine(10_000)
	bank := bankrec.NewService(bankrec.NewRepository(w.r.pool)).WithJournalPoster(glPosterOver{w.gl})
	if _, err := bank.ManualMatch(context.Background(), bankrec.MatchInput{
		Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetPayment, TargetRefID: orig, AmountCents: 10_000,
	}, w.r.userID); err != nil {
		t.Fatalf("the deposit (bank match): %v", err)
	}
	rev := w.reverse(orig)
	keys := caseKeys(orig, rev)
	w.cleanupJournals(keys, []string{line})
	w.drain()
	w.verdict(t, "deposited then reversed", orig, keys, []string{line})
}

// (d) reversal first, then the deposit is refused (SF-2b): only the receipt and the reversal remain.
func TestIntegration_ReversalGL_ReversedThenTheDepositIsRefused(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CARD", 10_000)
	w.drain()
	rev := w.reverse(orig)
	line := w.r.statementLine(10_000)
	bank := bankrec.NewService(bankrec.NewRepository(w.r.pool)).WithJournalPoster(glPosterOver{w.gl})
	if _, err := bank.ManualMatch(context.Background(), bankrec.MatchInput{
		Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetPayment, TargetRefID: orig, AmountCents: 10_000,
	}, w.r.userID); err == nil {
		t.Fatalf("the deposit of a reversed payment was accepted")
	}
	keys := caseKeys(orig, rev)
	w.cleanupJournals(keys, []string{line})
	w.drain()
	w.verdict(t, "reversed then deposit refused", orig, keys, []string{line})
}

// The old behaviour is a DEAD row. A row written dead before the topic was wired (what the dispatcher did to it)
// posts exactly once after the boot requeue, and exactly once again when requeued twice: gl_journals.idempotency_key
// holds the line.
func TestIntegration_ReversalGL_ADeadRowPostsOnceAfterBootAndOnceWhenRequeuedTwice(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	w.drain()
	rev := w.reverse(orig)
	keys := caseKeys(orig, rev)
	w.cleanupJournals(keys, nil)
	// Put the row where the old dispatcher left it: DEAD, never handled.
	killRow := func() {
		if _, err := w.r.obs.Exec(context.Background(),
			`UPDATE events_outbox SET status = 'DEAD', attempts = 5, last_error = 'no handler registered for topic'
			  WHERE topic = 'payment.reversed' AND payload->>'reverses_payment_id' = $1`, orig); err != nil {
			t.Fatalf("kill the row: %v", err)
		}
	}
	journals := func() int {
		var n int
		if err := w.r.obs.QueryRow(context.Background(),
			`SELECT count(*) FROM gl_journals WHERE idempotency_key = 'payment.reversed:'||$1`, rev).Scan(&n); err != nil {
			t.Fatalf("count journals: %v", err)
		}
		return n
	}
	// Drain what is pending first so the row is not simply taken, then kill it.
	w.drain()
	killRow()
	// The journal the first drain may already have posted is removed so the proof starts from "never posted".
	_, _ = w.r.obs.Exec(context.Background(), `DELETE FROM gl_journal_lines WHERE journal_id IN (SELECT id FROM gl_journals WHERE idempotency_key = 'payment.reversed:'||$1)`, rev)
	_, _ = w.r.obs.Exec(context.Background(), `DELETE FROM gl_journals WHERE idempotency_key = 'payment.reversed:'||$1`, rev)
	if n := journals(); n != 0 {
		t.Fatalf("expected no journal before the boot requeue, found %d", n)
	}
	boot := func() {
		// A restart: a fresh dispatcher, the handlers registered, the boot requeue of the dead rows.
		w.disp = notify.NewDispatcher(notify.NewPgOutboxStore(w.r.pool), nil)
		w.gl.RegisterPostingHandlers(w.disp)
	}
	boot()
	w.drain()
	if n := journals(); n != 1 {
		t.Fatalf("after the boot requeue the dead row posted %d journals, want 1", n)
	}
	killRow()
	boot() // requeued a second time
	boot() // and a third, for good measure
	w.drain()
	if n := journals(); n != 1 {
		t.Fatalf("after requeueing again the reversal has %d journals, want still 1", n)
	}
}

// A multi invoice original reverses its full amount against AR in one journal, as the subledger's contra rows
// (one per allocation) sum to the same figure.
func TestIntegration_ReversalGL_AMultiInvoicePaymentReversesItsFullAmount(t *testing.T) {
	w := newReversalGLWorld(t)
	a, b := w.r.invoice("RGMA", 1_000_000), w.r.invoice("RGMB", 1_000_000)
	p, err := w.r.repo.Pay(context.Background(), w.r.custID, "CARD", fmt.Sprintf("gw-rg-%d", time.Now().UnixNano()),
		[]payments.Allocation{{InvoiceID: a, AmountCents: 6_000}, {InvoiceID: b, AmountCents: 4_000}})
	if err != nil {
		t.Fatalf("pay: %v", err)
	}
	w.drain()
	rev := w.reverse(p.ID)
	keys := caseKeys(p.ID, rev)
	w.cleanupJournals(keys, nil)
	w.drain()
	w.verdict(t, "multi invoice card", p.ID, keys, nil)
	var amt int64
	if err := w.r.obs.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(l.debit_cents), 0) FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		  WHERE j.idempotency_key = 'payment.reversed:'||$1`, rev).Scan(&amt); err != nil {
		t.Fatalf("read the reversal: %v", err)
	}
	if amt != 10_000 {
		t.Errorf("the reversal journal debits AR %d, want the full 10000", amt)
	}
}

// - Round 2: the reversal's account is fixed when the reversal happens -

// sequence runs deposited by a bank match, reversed, then (optionally) unmatched, with the GL consumer drained
// either BEFORE the unmatch (the reversal is posted while the payment still reads DEPOSITED) or AFTER it (posted
// when the payment, if the unmatch went through, reads RECEIVED). It returns the GL net per account over the
// case's journals, the unmatch's error, and the line and payment states.
type sequenceResult struct {
	nets          map[string]int64
	unmatchErr    error
	lineStatus    string
	payStatus     string
	reversalCount int
}

func (w *reversalGLWorld) sequence(t *testing.T, unmatch, drainBeforeUnmatch bool) sequenceResult {
	t.Helper()
	ctx := context.Background()
	orig := w.receipt("CARD", 10_000)
	w.drain()
	line := w.r.statementLine(10_000)
	bank := bankrec.NewService(bankrec.NewRepository(w.r.pool)).WithJournalPoster(glPosterOver{w.gl})
	m, err := bank.ManualMatch(ctx, bankrec.MatchInput{
		Mode: "manual", StatementLineID: line, TargetType: bankrec.TargetPayment, TargetRefID: orig, AmountCents: 10_000,
	}, w.r.userID)
	if err != nil {
		t.Fatalf("the deposit (bank match): %v", err)
	}
	rev := w.reverse(orig)
	keys := append(caseKeys(orig, rev), "bankrec:match:"+m.ID, "bankrec:unmatch:"+m.ID)
	w.cleanupJournals(keys, []string{line})
	if drainBeforeUnmatch {
		w.drain()
	}
	var res sequenceResult
	if unmatch {
		res.unmatchErr = bank.Unmatch(ctx, m.ID, w.r.userID)
	}
	w.drain()
	res.nets = w.nets(keys, []string{line})
	if err := w.r.obs.QueryRow(ctx, `SELECT status FROM bank_statement_lines WHERE id = $1::uuid`, line).Scan(&res.lineStatus); err != nil {
		t.Fatalf("read the line: %v", err)
	}
	if err := w.r.obs.QueryRow(ctx, `SELECT status FROM payments WHERE id = $1::uuid`, orig).Scan(&res.payStatus); err != nil {
		t.Fatalf("read the payment: %v", err)
	}
	return res
}

// The GL must not depend on when the consumer runs, and the bank must never go negative. With the unmatch of a
// reversed payment refused, "identical" means the refused unmatch changes nothing, in either drain order.
func TestIntegration_ReversalGL_DepositedReversedUnmatchedIsTheSameWheneverTheConsumerRuns(t *testing.T) {
	before := newReversalGLWorld(t).sequence(t, true, true)
	after := newReversalGLWorld(t).sequence(t, true, false)
	t.Logf("consumer drained BEFORE the unmatch: GL net %v | unmatch: %v | line %s payment %s", before.nets, before.unmatchErr, before.lineStatus, before.payStatus)
	t.Logf("consumer drained AFTER the unmatch:  GL net %v | unmatch: %v | line %s payment %s", after.nets, after.unmatchErr, after.lineStatus, after.payStatus)
	for name, r := range map[string]sequenceResult{"before": before, "after": after} {
		if r.nets["1000"] < 0 {
			t.Errorf("%s: the bank account nets %d, it went negative", name, r.nets["1000"])
		}
	}
	for code := range before.nets {
		if before.nets[code] != after.nets[code] {
			t.Errorf("the GL depends on timing: account %s nets %d (consumer first) against %d (consumer after)", code, before.nets[code], after.nets[code])
		}
	}
	for name, r := range map[string]sequenceResult{"before": before, "after": after} {
		if r.unmatchErr == nil {
			t.Errorf("%s: the unmatch of a reversed payment was accepted", name)
		}
		if r.lineStatus != "MATCHED" || r.payStatus != "DEPOSITED" {
			t.Errorf("%s: the refused unmatch left the line %s and the payment %s, want MATCHED and DEPOSITED", name, r.lineStatus, r.payStatus)
		}
		for code, n := range r.nets {
			if n != 0 {
				t.Errorf("%s: account %s nets %d, want 0 (the money went back through the reversal)", name, code, n)
			}
		}
	}
}

// The plain case, deposited then reversed with no unmatch, is the same in both drain orders.
func TestIntegration_ReversalGL_DepositedThenReversedIsTheSameInBothDrainOrders(t *testing.T) {
	before := newReversalGLWorld(t).sequence(t, false, true)
	after := newReversalGLWorld(t).sequence(t, false, false)
	for code := range before.nets {
		if before.nets[code] != after.nets[code] {
			t.Errorf("account %s nets %d (drained between) against %d (drained at the end)", code, before.nets[code], after.nets[code])
		}
		if before.nets[code] != 0 {
			t.Errorf("account %s nets %d, want 0", code, before.nets[code])
		}
	}
}
