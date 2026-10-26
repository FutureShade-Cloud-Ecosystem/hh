//go:build integration

// payout_integration_test.go: held money leaves 2310 only by the act that moves it (SF-2m, SF-2n; ruling 52.3).
//
// A reversal posts Dr AR / Cr 2310 and the money is held for the customer. SF-2l applies it to an invoice. These legs
// are the other acts: cash handed back from the drawer (Dr 2310 / Cr 1020 with a PAID_OUT movement), a cheque or
// transfer (Dr 2310 / Cr 1000), and the write off after the count has posted the shortage (Dr 2310 / Cr 6350). Each runs
// through the real settle, reversal, till, pay out service and GL consumer, and ends with the accounts at their true
// balances and the held figure fallen by the amount.
package payments_test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
)

var payoutSeq int

// payoutRepo is a payments repository with the till's seam bound, on the world's own pool.
func (w *reversalGLWorld) payoutRepo() *payments.Repository {
	return payments.NewRepository(w.r.pool).WithTill(till.NewService(till.NewRepository(w.r.pool)))
}

func (w *reversalGLWorld) payOut(repo *payments.Repository, orig, kind string, cents int64, extra func(*payments.PayoutInput)) (payments.Payout, error) {
	w.r.t.Helper()
	payoutSeq++
	in := payments.PayoutInput{
		PaymentID: orig, Kind: kind, AmountCents: cents, Reason: "paid back to the customer", ActorID: w.r.userID,
		IdempotencyKey: fmt.Sprintf("payout-%s-%d", w.r.suffix, payoutSeq), IsManager: true,
	}
	if extra != nil {
		extra(&in)
	}
	return payments.NewReverseService(repo).PayOut(w.r.staffCtx(), in)
}

type payoutState struct {
	rows, movements, outbox, subledger int
}

func (w *reversalGLWorld) payoutState(orig string) payoutState {
	w.r.t.Helper()
	var s payoutState
	c := context.Background()
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM payment_payouts WHERE payment_id = $1::uuid`, orig).Scan(&s.rows)
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM till_movements m WHERE m.reason LIKE 'HELD PAYOUT%' AND m.created_by = $1::uuid`, w.r.userID).Scan(&s.movements)
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM events_outbox WHERE topic = 'payment.paid_out' AND payload->>'payment_id' = $1`, orig).Scan(&s.outbox)
	_ = w.r.obs.QueryRow(c, `SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid`, w.r.custID).Scan(&s.subledger)
	return s
}

func (w *reversalGLWorld) heldOf(orig string) payments.HeldFigure {
	w.r.t.Helper()
	h, err := w.r.repo.HeldFor(context.Background(), orig, nil)
	if err != nil {
		w.r.t.Fatalf("held: %v", err)
	}
	return h
}

func (w *reversalGLWorld) payoutNets(orig, rev string, extra ...string) map[string]int64 {
	w.r.t.Helper()
	keys := append(caseKeys(orig, rev), extra...)
	w.cleanupJournals(keys, nil)
	return w.walkNets(keys, nil)
}

func (w *reversalGLWorld) cleanupPayouts() {
	w.r.t.Cleanup(func() {
		c := context.Background()
		_, _ = w.r.obs.Exec(c, `DELETE FROM payment_payouts WHERE customer_id = $1::uuid`, w.r.custID)
		_, _ = w.r.obs.Exec(c, `DELETE FROM till_movements WHERE reason LIKE 'HELD PAYOUT%' AND created_by = $1::uuid`, w.r.userID)
	})
}

// (1) Cash handed back from the drawer: Dr 2310 / Cr 1020, a PAID_OUT movement on the open session, the held figure and
// the session's expected cash both fall by it.
func TestIntegration_Payout_DrawerCashLandsAPaidOutMovement(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	session := w.openSession()
	orig := w.registerReceipt("CASH", 10_000)
	w.inSession(session, orig)
	w.drain()
	rev := w.reverse(orig)
	repo := w.payoutRepo()
	res, err := w.payOut(repo, orig, payments.PayoutDrawer, 4_000, func(in *payments.PayoutInput) { in.TillSessionID = session })
	if err != nil {
		t.Fatalf("the drawer pay out was refused: %v", err)
	}
	if res.HeldBeforeCents != 10_000 || res.HeldAfterCents != 6_000 {
		t.Errorf("held %d to %d, want 10000 to 6000", res.HeldBeforeCents, res.HeldAfterCents)
	}
	if h := w.heldOf(orig); h.HeldCents != 6_000 || h.PaidOutCents != 4_000 {
		t.Errorf("the held read says %+v, want 6000 held and 4000 paid out", h)
	}
	mv, err := till.NewRepository(w.r.pool).MovementTotalsByMethod(context.Background(), session)
	if err != nil {
		t.Fatalf("movements: %v", err)
	}
	if mv["CASH"].PaidOut != 4_000 {
		t.Errorf("the session's cash paid out reads %d, want 4000 (expected cash falls with it)", mv["CASH"].PaidOut)
	}
	got := w.payoutNets(orig, rev, "payment.paid_out:"+res.PayoutID)
	wantNets(t, "drawer pay out", got, map[string]int64{"1020": 6_000, "1100": 0, "2310": -6_000})
}

// (2) A cheque or transfer written to the customer: Dr 2310 / Cr 1000, the reference recorded and required.
func TestIntegration_Payout_BankChequeCreditsTheBank(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	orig := w.receipt("CHEQUE", 10_000)
	w.drain()
	rev := w.reverse(orig)
	repo := w.payoutRepo()
	if _, err := w.payOut(repo, orig, payments.PayoutBank, 10_000, nil); err == nil {
		t.Errorf("a bank pay out was accepted with no cheque or transfer reference")
	}
	res, err := w.payOut(repo, orig, payments.PayoutBank, 10_000, func(in *payments.PayoutInput) { in.Reference = "CHQ 100245" })
	if err != nil {
		t.Fatalf("the bank pay out was refused: %v", err)
	}
	var ref string
	if err := w.r.obs.QueryRow(context.Background(), `SELECT reference FROM payment_payouts WHERE id = $1::uuid`, res.PayoutID).Scan(&ref); err != nil || ref != "CHQ 100245" {
		t.Errorf("the reference reads %q (%v), want it recorded", ref, err)
	}
	if h := w.heldOf(orig); h.HeldCents != 0 {
		t.Errorf("the held figure reads %d, want 0", h.HeldCents)
	}
	got := w.payoutNets(orig, rev, "payment.paid_out:"+res.PayoutID)
	wantNets(t, "bank pay out", got, map[string]int64{"1000": -10_000, "1020": 10_000, "1100": 0, "2310": 0})
}

// (3) Every arm is refused above the held figure, for a non manager, and for an out of reach payment (answered as
// missing), with nothing written; a replay answers the first result.
func TestIntegration_Payout_RefusalsWriteNothing(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	session := w.openSession()
	orig := w.registerReceipt("CASH", 10_000)
	w.inSession(session, orig)
	w.drain()
	w.reverse(orig)
	repo := w.payoutRepo()
	// A payment never reversed holds nothing.
	fresh := w.r.invoice("RPO", 50_000)
	if err := w.r.settle("register", fresh, 3_000); err != nil {
		t.Fatalf("a second cheque: %v", err)
	}
	var plain string
	if err := w.r.obs.QueryRow(context.Background(), `SELECT payment_id::text FROM payment_allocations WHERE invoice_id = $1::uuid`, fresh).Scan(&plain); err != nil {
		t.Fatalf("find it: %v", err)
	}
	before := w.payoutState(orig)
	drawer := func(in *payments.PayoutInput) { in.TillSessionID = session }
	bank := func(in *payments.PayoutInput) { in.Reference = "EFT 9" }
	cases := []struct {
		name string
		kind string
		cut  int64
		set  func(*payments.PayoutInput)
		want string
	}{
		{"drawer above held", payments.PayoutDrawer, 10_001, drawer, "holds"},
		{"bank above held", payments.PayoutBank, 10_001, bank, "holds"},
	}
	for _, c := range cases {
		if _, err := w.payOut(repo, orig, c.kind, c.cut, c.set); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s answered %v, want it refused in words (%q)", c.name, err, c.want)
		}
	}
	for _, kind := range []string{payments.PayoutDrawer, payments.PayoutBank} {
		set := drawer
		if kind == payments.PayoutBank {
			set = bank
		}
		if _, err := w.payOut(repo, orig, kind, 1_000, func(in *payments.PayoutInput) { set(in); in.IsManager = false }); err == nil {
			t.Errorf("a %s pay out was accepted from a caller who is not a manager", kind)
		}
		if _, err := w.payOut(repo, orig, kind, 1_000, func(in *payments.PayoutInput) {
			set(in)
			in.CanAccessBranch = func(string) bool { return false }
		}); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Errorf("an out of reach %s pay out answered %v, want it missing", kind, err)
		}
		if _, err := w.payOut(repo, orig, kind, 1_000, func(in *payments.PayoutInput) { set(in); in.Reason = " " }); err == nil {
			t.Errorf("a %s pay out was accepted with no reason", kind)
		}
	}
	if _, err := w.payOut(repo, plain, payments.PayoutBank, 1_000, bank); err == nil {
		t.Errorf("held money was paid out of a payment that was never reversed")
	}
	if after := w.payoutState(orig); after != before {
		t.Errorf("a refused pay out wrote: before %+v after %+v", before, after)
	}
	// The control, and the replay: the whole held amount goes through once.
	first, err := w.payOut(repo, orig, payments.PayoutBank, 10_000, func(in *payments.PayoutInput) { bank(in); in.IdempotencyKey = "po-replay-" + w.r.suffix })
	if err != nil {
		t.Fatalf("the whole held amount was refused after the refusals: %v", err)
	}
	again, err := w.payOut(repo, orig, payments.PayoutBank, 10_000, func(in *payments.PayoutInput) { bank(in); in.IdempotencyKey = "po-replay-" + w.r.suffix })
	if err != nil || !again.Replayed || again.PayoutID != first.PayoutID {
		t.Errorf("a replay answered %+v (%v), want the first result flagged as a replay", again, err)
	}
	if _, err := w.payOut(repo, orig, payments.PayoutBank, 9_000, func(in *payments.PayoutInput) { bank(in); in.IdempotencyKey = "po-replay-" + w.r.suffix }); err == nil {
		t.Errorf("a spent key was accepted with another amount")
	}
	if st := w.payoutState(orig); st.rows != 1 || st.outbox != 1 {
		t.Errorf("after the replays the case reads %+v, want one payout and one event", st)
	}
}

// (4) A pay out and a re-application racing on one payment cannot together exceed the held figure: the writer that holds
// the payment row first lands, the other is refused. The park holds the re-application between its payment row and the
// invoices counter draw.
func TestIntegration_Payout_BesideAReapplicationNeverExceedsTheHeldFigure(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	orig := w.receipt("CHEQUE", 10_000)
	inv := w.invoiceOf(orig)
	w.drain()
	w.reverse(orig)
	park := testkit.NewDrawPark(t, w.r.pool, "invoices", 55020901)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := w.reapplyKeyed(payments.NewRepository(park.Pool), orig, "po-race-a-"+w.r.suffix, payments.Allocation{InvoiceID: inv, AmountCents: 7_000})
		first <- err
	}()
	park.WaitParked("the re-application holds the payment row at the invoices counter draw")
	go func() {
		_, err := w.payOut(w.payoutRepo(), orig, payments.PayoutBank, 7_000, func(in *payments.PayoutInput) { in.Reference = "CHQ race" })
		second <- err
	}()
	park.WaitBlocked("the pay out queues on the payment row")
	park.AnswersAllowing(map[string]chan error{"first": first, "second": second}, map[string]string{"second": "holds"})
	h := w.heldOf(orig)
	t.Logf("after the race: %+v", h)
	if h.HeldCents != 3_000 || h.AppliedCents != 7_000 || h.PaidOutCents != 0 {
		t.Errorf("the race left %+v, want the re-application to hold 7000 and the pay out refused", h)
	}
	if st := w.payoutState(orig); st.rows != 0 {
		t.Errorf("the refused pay out wrote %+v", st)
	}
}

// (6) The write off (SF-2n): refused before the count has posted the shortage, accepted after, and 6350 nets the
// shortage back. The cash left the drawer unrecorded, so the close posts Dr 6350 / Cr 1020 and the write off clears 2310.
func TestIntegration_Payout_WriteOffWaitsForTheCountThenClearsTheShortage(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	session := w.openSession()
	orig := w.registerReceipt("CASH", 10_000)
	w.inSession(session, orig)
	w.drain()
	rev := w.reverse(orig)
	repo := w.payoutRepo()
	before := w.payoutState(orig)
	if _, err := w.payOut(repo, orig, payments.PayoutWriteOff, 10_000, nil); err == nil || !strings.Contains(err.Error(), "count for this payment's session is not complete yet") {
		t.Errorf("a write off before the count answered %v, want it refused in words about the count", err)
	}
	w.closeSession(session, 0) // the cash is gone: counted 0 against 10000 expected
	if after := w.payoutState(orig); after != before {
		t.Errorf("the refused write off wrote: before %+v after %+v", before, after)
	}
	if _, err := w.payOut(repo, orig, payments.PayoutWriteOff, 10_001, nil); err == nil {
		t.Errorf("a write off above the held figure was accepted")
	}
	res, err := w.payOut(repo, orig, payments.PayoutWriteOff, 10_000, nil)
	if err != nil {
		t.Fatalf("the write off after the count was refused: %v", err)
	}
	if h := w.heldOf(orig); h.HeldCents != 0 {
		t.Errorf("the held figure reads %d, want 0", h.HeldCents)
	}
	if _, err := w.payOut(repo, orig, payments.PayoutWriteOff, 1, nil); err == nil {
		t.Errorf("a second write off was accepted with nothing held")
	}
	got := w.payoutNets(orig, rev, "payment.paid_out:"+res.PayoutID, "till.session.completed:"+session)
	wantNets(t, "write off", got, map[string]int64{"1020": 0, "1100": 0, "2310": 0, "6350": 0})
}

// A write off needs a payment rung through a till session, and a session that was SHORT.
func TestIntegration_Payout_WriteOffNeedsASessionWithAShortage(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	orig := w.receipt("CHEQUE", 10_000)
	w.drain()
	w.reverse(orig)
	repo := w.payoutRepo()
	if _, err := w.payOut(repo, orig, payments.PayoutWriteOff, 1_000, nil); err == nil || !strings.Contains(err.Error(), "till session") {
		t.Errorf("a write off of a payment with no till session answered %v, want it refused in words", err)
	}
	session := w.openSession()
	cash := w.registerReceipt("CASH", 5_000)
	w.inSession(session, cash)
	w.drain()
	w.reverse(cash)
	w.closeSession(session, 5_000) // the drawer is exactly as expected: no shortage
	if _, err := w.payOut(repo, cash, payments.PayoutWriteOff, 1_000, nil); err == nil || !strings.Contains(err.Error(), "no shortage") {
		t.Errorf("a write off against a session with no shortage answered %v, want it refused in words", err)
	}
}

// A drawer pay out that names no session takes the payment's branch's one OPEN drawer, and is refused in words when the
// branch has none or more than one (the Money Desk cannot name a drawer). A fresh branch keeps the count deterministic.
func TestIntegration_Payout_AnUnnamedDrawerIsTheBranchsOneOpenDrawer(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	ctx := context.Background()
	branch := uuid.NewString()
	if _, err := w.r.obs.Exec(ctx, `INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name)
		VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', 'DR'||$2, 'drawer leg branch')`, branch, w.r.suffix); err != nil {
		t.Fatalf("seed a branch: %v", err)
	}
	t.Cleanup(func() {
		var left int
		if err := w.r.obs.QueryRow(context.Background(), `SELECT count(*) FROM locations WHERE id = $1::uuid`, branch).Scan(&left); err != nil || left != 0 {
			t.Errorf("the drawer leg left %d locations row(s) behind for branch %s (err=%v), want 0", left, branch, err)
		}
	})
	t.Cleanup(func() {
		c := context.Background()
		_, _ = w.r.obs.Exec(c, `UPDATE payments SET till_session_id = NULL WHERE branch_id = $1::uuid`, branch)
		_, _ = w.r.obs.Exec(c, `DELETE FROM till_movements WHERE till_session_id IN (SELECT id FROM till_sessions WHERE branch_id = $1::uuid)`, branch)
		_, _ = w.r.obs.Exec(c, `DELETE FROM till_sessions WHERE branch_id = $1::uuid`, branch)
		_, _ = w.r.obs.Exec(c, `DELETE FROM payment_payouts WHERE branch_id = $1::uuid`, branch)
		_, _ = w.r.obs.Exec(c, `DELETE FROM invoices WHERE branch_id = $1::uuid`, branch)
		_, _ = w.r.obs.Exec(c, `DELETE FROM locations WHERE id = $1::uuid`, branch)
	})
	var inv string
	if err := w.r.obs.QueryRow(ctx, `INSERT INTO invoices (invoice_no, customer_id, branch_id, status, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		VALUES ('DR-'||$1, $2::uuid, $3::uuid, 'FINALIZED', CURRENT_DATE + 30, 90000, 0, 90000, 90000, now()) RETURNING id::text`, w.r.suffix, w.r.custID, branch).Scan(&inv); err != nil {
		t.Fatalf("seed the invoice: %v", err)
	}
	p, err := w.r.repo.Pay(ctx, w.r.custID, "CARD", "gw-dr-"+w.r.suffix, []payments.Allocation{{InvoiceID: inv, AmountCents: 6_000}})
	if err != nil {
		t.Fatalf("take the payment: %v", err)
	}
	w.drain()
	w.reverse(p.ID)
	repo := w.payoutRepo()
	if _, err := w.payOut(repo, p.ID, payments.PayoutDrawer, 1_000, nil); err == nil || !strings.Contains(err.Error(), "no drawer is open") {
		t.Errorf("a branch with no open drawer answered %v, want it refused in words", err)
	}
	tillRepo := till.NewRepository(w.r.pool)
	open := func(station string) string {
		id, err := tillRepo.OpenSession(ctx, till.TillSession{BranchID: branch, Station: station, OpenedBy: w.r.userID, BlindClose: true}, nil)
		if err != nil {
			t.Fatalf("open %s: %v", station, err)
		}
		return id
	}
	first := open("D1")
	res, err := w.payOut(repo, p.ID, payments.PayoutDrawer, 2_000, nil)
	if err != nil {
		t.Fatalf("the unnamed pay out was refused with one open drawer: %v", err)
	}
	var landed string
	if err := w.r.obs.QueryRow(ctx, `SELECT till_session_id::text FROM payment_payouts WHERE id = $1::uuid`, res.PayoutID).Scan(&landed); err != nil || landed != first {
		t.Errorf("the pay out landed on %q (%v), want the branch's one open drawer %q", landed, err, first)
	}
	var moves int
	_ = w.r.obs.QueryRow(ctx, `SELECT count(*) FROM till_movements WHERE till_session_id = $1::uuid AND movement_type = 'PAID_OUT' AND amount_cents = 2000`, first).Scan(&moves)
	if moves != 1 {
		t.Errorf("the open drawer carries %d paid out movements of 2000, want 1", moves)
	}
	open("D2")
	before := w.payoutState(p.ID)
	if _, err := w.payOut(repo, p.ID, payments.PayoutDrawer, 1_000, nil); err == nil || !strings.Contains(err.Error(), "more than one drawer") {
		t.Errorf("a branch with two open drawers answered %v, want it refused in words", err)
	}
	if after := w.payoutState(p.ID); after != before {
		t.Errorf("the refused pay out wrote: before %+v after %+v", before, after)
	}
	// Naming one of the open drawers settles it.
	if _, err := w.payOut(repo, p.ID, payments.PayoutDrawer, 1_000, func(in *payments.PayoutInput) { in.TillSessionID = first }); err != nil {
		t.Errorf("a named drawer was refused: %v", err)
	}
}

// (7) Two write offs of one session cannot together exceed its shortage (PR 606 round 2, S3's P1). The first write off
// is parked right after its sum and before its insert; the second runs beside it on another payment of the same
// session. The second must queue on the session row and re-read what the first already took, so the pair can never
// write one shortage off twice and book a gain that never happened.
func TestIntegration_Payout_TwoWriteOffsOfOneSessionNeverExceedTheShortage(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	session := w.openSession()
	// Two cash receipts of the session, each on its own invoice tag (the rig keys invoices by tag).
	cashIn := func(tag string, cents int64) string {
		inv := w.r.invoice("W2"+tag, 1_000_000)
		if _, err := w.r.register().RecordPayment(context.Background(), billing.ReceiptInput{
			InvoiceID: inv, Method: "CASH", AmountCents: cents, Reference: "walk",
			IdempotencyKey: fmt.Sprintf("w2-%s-%d", tag, time.Now().UnixNano()),
		}); err != nil {
			t.Fatalf("%s register receipt: %v", tag, err)
		}
		var id string
		if err := w.r.obs.QueryRow(context.Background(),
			`SELECT p.id::text FROM payments p JOIN payment_allocations a ON a.payment_id = p.id
			  WHERE a.invoice_id = $1::uuid AND p.reverses_payment_id IS NULL`, inv).Scan(&id); err != nil {
			t.Fatalf("find the %s receipt: %v", tag, err)
		}
		return id
	}
	first := cashIn("A", 6_000)
	second := cashIn("B", 6_000)
	w.inSession(session, first)
	w.inSession(session, second)
	w.drain()
	w.reverse(first)
	w.reverse(second)
	w.closeSession(session, 2_000) // expected 12,000, counted 2,000: the shortage is 10,000

	park := testkit.NewInsertPark(t, w.r.pool, "payment_payouts", 55020004)
	writeOff := func(pool *pgxpool.Pool, payment, key string) error {
		repo := payments.NewRepository(pool).WithTill(till.NewService(till.NewRepository(pool)))
		_, err := payments.NewReverseService(repo).PayOut(w.r.staffCtx(), payments.PayoutInput{
			PaymentID: payment, Kind: payments.PayoutWriteOff, AmountCents: 6_000, Reason: "the cash left the drawer",
			ActorID: w.r.userID, IdempotencyKey: key + w.r.suffix, IsManager: true,
		})
		return err
	}
	parked := make(chan error, 1)
	go func() { parked <- writeOff(park.Pool, first, "wo-race-parked-") }()
	park.WaitParked("the first write off after its sum, before its insert")

	var besideDone atomic.Bool
	var besideErr error
	beside := make(chan error, 1)
	go func() {
		defer besideDone.Store(true)
		besideErr = writeOff(w.r.pool, second, "wo-race-beside-")
		beside <- besideErr
	}()
	// With the fix the second queues on the session row the first holds; without it, it commits beside the parked first.
	park.WaitBlockedOr("the second write off queues on the session, or has already committed", besideDone.Load)

	park.AnswersAllowing(map[string]chan error{"the parked write off": parked, "the second write off": beside},
		map[string]string{"the second write off": "the count's shortage still to be covered"})
	var total int64
	_ = w.r.obs.QueryRow(context.Background(),
		`SELECT COALESCE(SUM(po.amount_cents), 0) FROM payment_payouts po JOIN payments p ON p.id = po.payment_id
		  WHERE po.kind = 'WRITE_OFF' AND p.till_session_id = $1::uuid`, session).Scan(&total)
	t.Logf("the session's write offs total %d against its 10,000 shortage (the second answered %v)", total, besideErr)
	if total > 10_000 {
		t.Errorf("two write offs of one session took %d cents against its 10,000 shortage: the shortage was written off twice", total)
	}
	if besideErr == nil || !strings.Contains(besideErr.Error(), "the count's shortage still to be covered") {
		t.Errorf("the second write off answered %v, want it refused on the shortage the first already took", besideErr)
	}
}

// (8) A write off is cash only (PR 606 round 2, S3's P2). The shortage the close posts is counted cash less expected
// cash, so a reversed CHEQUE or CARD payment rung through a short session cannot be written off against that shortage:
// its money was never in the drawer. A cash payment of the same session still can.
func TestIntegration_Payout_AWriteOffIsCashOnly(t *testing.T) {
	w := newReversalGLWorld(t)
	w.cleanupPayouts()
	session := w.openSession()
	cheque := w.registerReceipt("CHEQUE", 8_000)
	cash := w.registerReceipt("CASH", 2_000)
	w.inSession(session, cheque)
	w.inSession(session, cash)
	w.drain()
	w.reverse(cheque)
	w.reverse(cash)
	w.closeSession(session, 0) // expected cash 2,000, counted 0: the shortage is 2,000
	repo := w.payoutRepo()
	before := w.payoutState(cheque)
	if _, err := w.payOut(repo, cheque, payments.PayoutWriteOff, 1_500, nil); err == nil || !strings.Contains(err.Error(), "cash") {
		t.Errorf("a write off of a cheque payment answered %v, want it refused in words about cash", err)
	}
	if after := w.payoutState(cheque); after != before {
		t.Errorf("the refused write off wrote: before %+v after %+v", before, after)
	}
	if _, err := w.payOut(repo, cash, payments.PayoutWriteOff, 1_500, nil); err != nil {
		t.Errorf("the cash write off of the same short session was refused: %v", err)
	}
}
