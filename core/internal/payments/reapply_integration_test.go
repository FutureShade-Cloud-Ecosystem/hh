//go:build integration

// reapply_integration_test.go: re-applying the money a reversal holds in 2310 to an invoice (SF-2l, ruled 49.3 and 52).
//
// A reversal posts Dr AR / Cr 2310 and leaves the original RECEIVED, so the money is the dealer's, held for the
// customer. Re-application clears it by applying it to invoices of the same customer: Dr 2310 / Cr AR, the invoice
// balance and the AR subledger moving through the settle's own slice. Each leg runs through the real settle, the real
// reversal, the real re-application and the real GL consumer, and ends with the accounts at their true balances
// (net = debit minus credit), GL AR equal to the AR subledger, and 2310 back to what the payment still holds.
package payments_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func (w *reversalGLWorld) reapplySvc(repo *payments.Repository) *payments.ReverseService {
	return payments.NewReverseService(repo)
}

var reapplySeq int

// reapplyOn re-applies cents of orig's held money to each named invoice, as a manager, under a fresh key.
func (w *reversalGLWorld) reapplyOn(orig string, allocs ...payments.Allocation) (payments.Reapplication, error) {
	w.r.t.Helper()
	reapplySeq++
	return w.reapplyKeyed(w.r.repo, orig, fmt.Sprintf("reapply-%s-%d", w.r.suffix, reapplySeq), allocs...)
}

func (w *reversalGLWorld) reapplyKeyed(repo *payments.Repository, orig, key string, allocs ...payments.Allocation) (payments.Reapplication, error) {
	return w.reapplySvc(repo).Reapply(context.Background(), payments.ReapplyInput{
		PaymentID: orig, Reason: "rung on the wrong account", ActorID: w.r.userID, IdempotencyKey: key,
		Allocations: allocs, IsManager: true,
	})
}

// reapplyState is everything a re-application writes around one payment: its rows, the invoices' balances, the AR
// subledger's rows and the outbox rows that name the payment.
type reapplyState struct {
	rows, subledgerRows, outbox int
	applied                     int64
	balances                    string
}

func (w *reversalGLWorld) reapplyState(orig string, invoices ...string) reapplyState {
	w.r.t.Helper()
	var s reapplyState
	c := context.Background()
	_ = w.r.obs.QueryRow(c, `SELECT count(*), COALESCE(SUM(amount_cents), 0) FROM payment_reapplications WHERE payment_id = $1::uuid`, orig).Scan(&s.rows, &s.applied)
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid`, w.r.custID).Scan(&s.subledgerRows)
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM events_outbox WHERE topic = 'payment.reapplied' AND payload->>'payment_id' = $1`, orig).Scan(&s.outbox)
	var parts []string
	for _, inv := range invoices {
		var bal int64
		var status string
		_ = w.r.obs.QueryRow(c, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, inv).Scan(&bal, &status)
		parts = append(parts, fmt.Sprintf("%d/%s", bal, status))
	}
	s.balances = strings.Join(parts, ",")
	return s
}

func (w *reversalGLWorld) balanceOf(inv string) (int64, string) {
	w.r.t.Helper()
	var bal int64
	var status string
	if err := w.r.obs.QueryRow(context.Background(), `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, inv).Scan(&bal, &status); err != nil {
		w.r.t.Fatalf("read the invoice: %v", err)
	}
	return bal, status
}

// reapplyNets drains the outbox and reads the GL nets over the payment's own journals and the named batches'.
func (w *reversalGLWorld) reapplyNets(orig, rev string, batches ...string) map[string]int64 {
	w.r.t.Helper()
	keys := caseKeys(orig, rev)
	for _, b := range batches {
		keys = append(keys, "payment.reapplied:"+b)
	}
	w.cleanupJournals(keys, nil)
	return w.walkNets(keys, nil)
}

// reapplyVerdict is the shared ending: every account the case touched nets to want, GL AR equals the AR subledger, and
// the batches' outbox rows were taken (PUBLISHED), not left PENDING or DEAD.
func (w *reversalGLWorld) reapplyVerdict(t *testing.T, name, orig, rev string, want map[string]int64, batches ...string) {
	t.Helper()
	got := w.reapplyNets(orig, rev, batches...)
	sub := w.subledger()
	t.Logf("%s: GL net by account %v | AR subledger net %d", name, got, sub)
	wantNets(t, name, got, want)
	if got["1100"] != sub {
		t.Errorf("%s: GL AR nets %d against the subledger's %d", name, got["1100"], sub)
	}
	var open int
	_ = w.r.obs.QueryRow(context.Background(), `SELECT count(*) FROM events_outbox WHERE topic = 'payment.reapplied' AND payload->>'payment_id' = $1 AND status <> 'PUBLISHED'`, orig).Scan(&open)
	if open != 0 {
		t.Errorf("%s: %d payment.reapplied outbox rows were not taken", name, open)
	}
}

// (1) A reversed payment re-applied to its own invoice: 2310 back to zero, AR and the subledger true, whatever tender.
func TestIntegration_Reapply_AReversedPaymentGoesBackOnItsOwnInvoice(t *testing.T) {
	for _, tc := range []struct{ method, cashSide string }{{"CHEQUE", "1020"}, {"CASH", "1020"}, {"CARD", "1010"}} {
		t.Run(tc.method, func(t *testing.T) {
			w := newReversalGLWorld(t)
			var orig string
			if tc.method == "CASH" {
				orig = w.registerReceipt("CASH", 10_000)
			} else {
				orig = w.receipt(tc.method, 10_000)
			}
			inv := w.invoiceOf(orig)
			w.drain()
			rev := w.reverse(orig)
			if bal, _ := w.balanceOf(inv); bal != 1_000_000 {
				t.Fatalf("the reversal should reopen the invoice to 1000000, it reads %d", bal)
			}
			res, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: inv, AmountCents: 10_000})
			if err != nil {
				t.Fatalf("the re-application was refused: %v", err)
			}
			bal, status := w.balanceOf(inv)
			t.Logf("%s: invoice %d/%s, held before %d after %d, lines %+v", tc.method, bal, status, res.HeldBeforeCents, res.HeldAfterCents, res.Lines)
			if bal != 990_000 || status != "PARTIALLY_PAID" {
				t.Errorf("the invoice reads %d/%s, want 990000/PARTIALLY_PAID", bal, status)
			}
			if res.AppliedCents != 10_000 || res.HeldBeforeCents != 10_000 || res.HeldAfterCents != 0 {
				t.Errorf("the answer reads applied %d held %d to %d, want 10000 and 10000 to 0", res.AppliedCents, res.HeldBeforeCents, res.HeldAfterCents)
			}
			w.reapplyVerdict(t, tc.method+" back on its own invoice", orig, rev,
				map[string]int64{tc.cashSide: 10_000, "1100": -10_000, "2310": 0}, res.BatchID)
		})
	}
}

// (2) Re-applied to a second invoice of the same customer: the first invoice stays reopened, the second takes the money.
func TestIntegration_Reapply_AReversedPaymentGoesOnASecondInvoice(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	first := w.invoiceOf(orig)
	second := w.r.invoice("RSECOND", 50_000)
	w.drain()
	rev := w.reverse(orig)
	res, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: second, AmountCents: 10_000})
	if err != nil {
		t.Fatalf("the re-application was refused: %v", err)
	}
	b1, s1 := w.balanceOf(first)
	b2, s2 := w.balanceOf(second)
	t.Logf("first %d/%s, second %d/%s", b1, s1, b2, s2)
	if b1 != 1_000_000 || s1 != "FINALIZED" {
		t.Errorf("the first invoice reads %d/%s, want it left reopened at 1000000/FINALIZED", b1, s1)
	}
	if b2 != 40_000 || s2 != "PARTIALLY_PAID" {
		t.Errorf("the second invoice reads %d/%s, want 40000/PARTIALLY_PAID", b2, s2)
	}
	w.reapplyVerdict(t, "second invoice", orig, rev, map[string]int64{"1020": 10_000, "1100": -10_000, "2310": 0}, res.BatchID)
}

// A request may split the held money across invoices, and an invoice it pays in full closes.
func TestIntegration_Reapply_OneRequestSplitsAcrossInvoicesAndClosesOne(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	a := w.r.invoice("RSPLITA", 4_000)
	b := w.r.invoice("RSPLITB", 50_000)
	w.drain()
	rev := w.reverse(orig)
	res, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: b, AmountCents: 6_000}, payments.Allocation{InvoiceID: a, AmountCents: 4_000})
	if err != nil {
		t.Fatalf("the re-application was refused: %v", err)
	}
	ba, sa := w.balanceOf(a)
	bb, sb := w.balanceOf(b)
	if ba != 0 || sa != "PAID" || bb != 44_000 || sb != "PARTIALLY_PAID" {
		t.Errorf("invoices read %d/%s and %d/%s, want 0/PAID and 44000/PARTIALLY_PAID", ba, sa, bb, sb)
	}
	w.reapplyVerdict(t, "split", orig, rev, map[string]int64{"1020": 10_000, "1100": -10_000, "2310": 0}, res.BatchID)
}

// (3) An apply above the held amount is refused with nothing written, and the held money can never be applied twice.
func TestIntegration_Reapply_AboveTheHeldAmountIsRefusedWithNothingWritten(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()
	w.reverse(orig)
	before := w.reapplyState(orig, inv)
	_, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: inv, AmountCents: 10_001})
	after := w.reapplyState(orig, inv)
	t.Logf("above the held amount: err %v | before %+v after %+v", err, before, after)
	if err == nil {
		t.Fatalf("10001 was applied from 10000 held")
	}
	if after != before {
		t.Errorf("the refused apply wrote: before %+v after %+v", before, after)
	}
	// The control: the refusal was the cap, not a broken door. The whole held amount goes through.
	if _, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: inv, AmountCents: 10_000}); err != nil {
		t.Errorf("the whole held amount was refused after the over apply: %v", err)
	}
}

func TestIntegration_Reapply_HeldMoneyCanNeverBeAppliedTwice(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	other := w.r.invoice("RTWICE", 100_000)
	w.drain()
	rev := w.reverse(orig)
	first, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: inv, AmountCents: 6_000})
	if err != nil || first.HeldAfterCents != 4_000 {
		t.Fatalf("the first slice: held after %d, err %v", first.HeldAfterCents, err)
	}
	before := w.reapplyState(orig, inv, other)
	if _, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: other, AmountCents: 4_001}); err == nil {
		t.Errorf("4001 was applied from 4000 still held")
	}
	if after := w.reapplyState(orig, inv, other); after != before {
		t.Errorf("the refused second apply wrote: before %+v after %+v", before, after)
	}
	last, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: other, AmountCents: 4_000})
	if err != nil || last.HeldAfterCents != 0 {
		t.Fatalf("the remainder: held after %d, err %v", last.HeldAfterCents, err)
	}
	before = w.reapplyState(orig, inv, other)
	if _, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: other, AmountCents: 1}); err == nil {
		t.Errorf("one more cent was applied after the held money was used up")
	}
	if after := w.reapplyState(orig, inv, other); after != before {
		t.Errorf("the refused apply after exhaustion wrote: before %+v after %+v", before, after)
	}
	w.reapplyVerdict(t, "applied in two slices", orig, rev, map[string]int64{"1020": 10_000, "1100": -10_000, "2310": 0}, first.BatchID, last.BatchID)
}

// (4) Two concurrent applies of the same held money: the writer that holds the payment row first lands, and the other
// is refused. The park holds the first writer between its invoice row and the invoices counter draw.
func TestIntegration_Reapply_TwoConcurrentAppliesOfTheSameHeldMoney(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	other := w.r.invoice("RRACE", 100_000)
	w.drain()
	rev := w.reverse(orig)
	park := testkit.NewDrawPark(t, w.r.pool, "invoices", 55020801)
	first, second := make(chan error, 1), make(chan error, 1)
	var firstBatch string
	go func() {
		res, err := w.reapplyKeyed(payments.NewRepository(park.Pool), orig, "race-first-"+w.r.suffix, payments.Allocation{InvoiceID: inv, AmountCents: 7_000})
		firstBatch = res.BatchID
		first <- err
	}()
	park.WaitParked("the first re-application holds the payment row and its invoice at the invoices counter draw")
	go func() {
		_, err := w.reapplyKeyed(w.r.repo, orig, "race-second-"+w.r.suffix, payments.Allocation{InvoiceID: other, AmountCents: 7_000})
		second <- err
	}()
	park.WaitBlocked("the second re-application queues on the payment row")
	park.AnswersAllowing(map[string]chan error{"first": first, "second": second}, map[string]string{"second": "holds"})
	st := w.reapplyState(orig, inv, other)
	t.Logf("after the race: %+v", st)
	if st.applied != 7_000 || st.rows != 1 {
		t.Errorf("exactly one 7000 slice should have landed, the table reads %d cents in %d rows", st.applied, st.rows)
	}
	w.reapplyVerdict(t, "concurrent applies", orig, rev, map[string]int64{"1020": 10_000, "1100": -10_000 + 3_000, "2310": -3_000}, firstBatch)
}

// (5) A re-applied payment cannot be reversed or bounced again: the original is already reversed, and the re-applied
// slices are not payments, so there is nothing for either to take twice.
func TestIntegration_Reapply_AReappliedPaymentCannotBeReversedOrBouncedAgain(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()
	w.reverse(orig)
	if _, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: inv, AmountCents: 10_000}); err != nil {
		t.Fatalf("the re-application was refused: %v", err)
	}
	before := w.reapplyState(orig, inv)
	var reversing int
	_ = w.r.obs.QueryRow(context.Background(), `SELECT count(*) FROM payments WHERE reverses_payment_id = $1::uuid`, orig).Scan(&reversing)
	if err := w.reverseErr(orig); err == nil {
		t.Errorf("a re-applied payment was reversed a second time")
	}
	if err := w.bounce(orig); err == nil {
		t.Errorf("a re-applied payment was bounced")
	}
	var reversingAfter int
	_ = w.r.obs.QueryRow(context.Background(), `SELECT count(*) FROM payments WHERE reverses_payment_id = $1::uuid`, orig).Scan(&reversingAfter)
	if after := w.reapplyState(orig, inv); after != before || reversingAfter != reversing {
		t.Errorf("a refused reverse or bounce wrote: before %+v after %+v, reversing rows %d to %d", before, after, reversing, reversingAfter)
	}
}

// A payment that was never reversed holds nothing, a reversing row holds nothing, and a bounced payment holds nothing.
func TestIntegration_Reapply_OnlyAReversedPaymentHoldsMoney(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()
	if _, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: inv, AmountCents: 1_000}); err == nil {
		t.Errorf("held money was applied from a payment that was never reversed")
	}
	rev := w.reverse(orig)
	if _, err := w.reapplyOn(rev, payments.Allocation{InvoiceID: inv, AmountCents: 1_000}); err == nil {
		t.Errorf("held money was applied from a reversing row")
	}
	bouncedInv := w.r.invoice("RBNC", 20_000)
	if err := w.r.settle("register", bouncedInv, 5_000); err != nil {
		t.Fatalf("the cheque to bounce: %v", err)
	}
	bounced := ""
	if err := w.r.obs.QueryRow(context.Background(), `SELECT payment_id::text FROM payment_allocations WHERE invoice_id = $1::uuid`, bouncedInv).Scan(&bounced); err != nil {
		t.Fatalf("find the cheque to bounce: %v", err)
	}
	if err := w.bounce(bounced); err != nil {
		t.Fatalf("the bounce: %v", err)
	}
	if _, err := w.reapplyOn(bounced, payments.Allocation{InvoiceID: inv, AmountCents: 1_000}); err == nil {
		t.Errorf("held money was applied from a bounced payment")
	}
	if st := w.reapplyState(orig, inv); st.rows != 0 || st.outbox != 0 {
		t.Errorf("a refused apply wrote: %+v", st)
	}
	// The control: the reversed original does hold its money.
	if _, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: inv, AmountCents: 10_000}); err != nil {
		t.Errorf("the reversed payment's own money was refused: %v", err)
	}
}

// The invoice rules: the payment's own customer, open, within its payable, a manager.
func TestIntegration_Reapply_TheInvoiceRulesRefuseWithNothingWritten(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	small := w.r.invoice("RSMALL", 3_000)
	paid := w.r.invoice("RPAID", 8_000)
	if _, err := w.r.obs.Exec(context.Background(), `UPDATE invoices SET balance_cents = 0, status = 'PAID' WHERE id = $1::uuid`, paid); err != nil {
		t.Fatalf("close an invoice: %v", err)
	}
	var stranger string
	if err := w.r.obs.QueryRow(context.Background(), `INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		VALUES ('RS-'||$1, 'Stranger '||$1, 'CONTRACTOR', $2::uuid, 0) RETURNING id::text`, w.r.suffix, w.r.branchID).Scan(&stranger); err != nil {
		t.Fatalf("seed a second customer: %v", err)
	}
	var theirs string
	if err := w.r.obs.QueryRow(context.Background(), `INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		VALUES ('RS-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30, 50000, 0, 50000, 50000, now()) RETURNING id::text`, w.r.suffix, stranger, w.r.branchID).Scan(&theirs); err != nil {
		t.Fatalf("seed the other customer's invoice: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.r.obs.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, stranger)
		_, _ = w.r.obs.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, stranger)
	})
	w.drain()
	w.reverse(orig)
	before := w.reapplyState(orig, inv, small, paid)
	for name, a := range map[string]payments.Allocation{
		"another customer's invoice":  {InvoiceID: theirs, AmountCents: 1_000},
		"a paid invoice":              {InvoiceID: paid, AmountCents: 1_000},
		"over the invoice's payable":  {InvoiceID: small, AmountCents: 3_001},
		"an invoice that is not real": {InvoiceID: "00000000-0000-4000-8000-000000000001", AmountCents: 1_000},
	} {
		if _, err := w.reapplyOn(orig, a); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A non manager is refused before the store is reached.
	svc := w.reapplySvc(w.r.repo)
	if _, err := svc.Reapply(context.Background(), payments.ReapplyInput{PaymentID: orig, Reason: "x", IdempotencyKey: "nm-" + w.r.suffix,
		Allocations: []payments.Allocation{{InvoiceID: inv, AmountCents: 1_000}}}); err == nil {
		t.Errorf("a caller who is not a manager re-applied held money")
	}
	if _, err := svc.Reapply(context.Background(), payments.ReapplyInput{PaymentID: orig, Reason: "  ", IdempotencyKey: "nr-" + w.r.suffix, IsManager: true,
		Allocations: []payments.Allocation{{InvoiceID: inv, AmountCents: 1_000}}}); err == nil {
		t.Errorf("held money was re-applied without a reason")
	}
	if after := w.reapplyState(orig, inv, small, paid); after != before {
		t.Errorf("a refused apply wrote: before %+v after %+v", before, after)
	}
	// The control: the same payment's money goes onto a good invoice of its own customer.
	if _, err := w.reapplyOn(orig, payments.Allocation{InvoiceID: small, AmountCents: 3_000}); err != nil {
		t.Errorf("a good invoice was refused after the refusals: %v", err)
	}
}

// A replay of a request under its own key answers the first result and writes nothing; a spent key with another body is
// refused.
func TestIntegration_Reapply_AReplayWritesNothingAndAnotherBodyIsRefused(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()
	w.reverse(orig)
	key := "replay-" + w.r.suffix
	first, err := w.reapplyKeyed(w.r.repo, orig, key, payments.Allocation{InvoiceID: inv, AmountCents: 4_000})
	if err != nil {
		t.Fatalf("the first apply: %v", err)
	}
	before := w.reapplyState(orig, inv)
	again, err := w.reapplyKeyed(w.r.repo, orig, key, payments.Allocation{InvoiceID: inv, AmountCents: 4_000})
	if err != nil {
		t.Fatalf("the replay: %v", err)
	}
	if !again.Replayed || again.BatchID != first.BatchID || again.AppliedCents != 4_000 {
		t.Errorf("the replay answered %+v, want the first result flagged as a replay", again)
	}
	if _, err := w.reapplyKeyed(w.r.repo, orig, key, payments.Allocation{InvoiceID: inv, AmountCents: 5_000}); err == nil {
		t.Errorf("a spent key was accepted with another amount")
	}
	if after := w.reapplyState(orig, inv); after != before {
		t.Errorf("the replay or the refused body wrote: before %+v after %+v", before, after)
	}
}

// The table's own guard refuses an insert above the held figure and an insert for a payment that was never reversed,
// whoever writes it.
func TestIntegration_Reapply_TheTableRefusesAnOverApplyWhoeverWritesIt(t *testing.T) {
	w := newReversalGLWorld(t)
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()
	insert := func(cents int64, key string) error {
		_, err := w.r.obs.Exec(context.Background(), `INSERT INTO payment_reapplications
			(batch_id, payment_id, invoice_id, customer_id, branch_id, amount_cents, reason, idempotency_key)
			VALUES (gen_random_uuid(), $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, 'direct', $6)`, orig, inv, w.r.custID, w.r.branchID, cents, key)
		return err
	}
	if err := insert(1, "direct-unreversed-"+w.r.suffix); err == nil {
		t.Errorf("the table took a row for a payment that was never reversed")
	}
	w.reverse(orig)
	if err := insert(10_001, "direct-over-"+w.r.suffix); err == nil {
		t.Errorf("the table took 10001 against 10000 held")
	}
	if err := insert(10_000, "direct-ok-"+w.r.suffix); err != nil {
		t.Errorf("the table refused the whole held amount: %v", err)
	}
	if err := insert(1, "direct-more-"+w.r.suffix); err == nil {
		t.Errorf("the table took a cent beyond the held amount")
	}
}
