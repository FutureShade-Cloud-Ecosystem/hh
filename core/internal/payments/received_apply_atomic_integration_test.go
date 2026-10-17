//go:build integration

// received_apply_atomic_integration_test.go, the legs that prove the received payment is ONE
// transaction and that its replay is not a bearer of the answer.
//
//   - a failure AFTER the payment row and some allocations were written (a trigger planted on one
//     invoice, dropped with the test) leaves no payment, no allocation, no subledger row and no event;
//   - the refusal lands wherever the foreign invoice sits in the sorted lock walk (first, middle,
//     last) and writes nothing each time;
//   - the invoice rows are locked in ascending id order whatever order the caller listed them in
//     (PR 277's order), observed from a second connection while the apply waits on the first row;
//   - a replay of a key is judged like a first attempt: another account, or an invoice outside the
//     caller's branch reach, does not get the first payment's answer.
//
// Run: go test -tags integration ./internal/payments/ -run ApplyReceived
package payments

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// sortedIDs answers n fresh random uuids in ascending text order, the order the apply locks in, so
// a leg can place an invoice at a known position in the walk without a fixed id that a shared
// database could already hold.
func sortedIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = uuid.NewString()
	}
	sort.Strings(ids)
	return ids
}

// invoiceWithID plants one open invoice under an id the leg chooses, so the position of the invoice
// in the sorted lock walk is the leg's to set.
func (w *applyWorld) invoiceWithID(t *testing.T, id, customerID, label string, issueDays, dueDays int, balance int64) string {
	t.Helper()
	issue := applyTestNow.AddDate(0, 0, issueDays*-1)
	due := applyTestNow.AddDate(0, 0, dueDays*-1)
	if _, err := w.pool.Exec(context.Background(),
		`INSERT INTO invoices (id, invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1::uuid, $2, $3::uuid, $4::uuid, 'FINALIZED', $5::date, $6::date, $7, 0, $7, $7, $5, 'INVOICE')`,
		id, w.tag+"-"+label, customerID, dib510Trenton,
		issue.Format("2006-01-02"), due.Format("2006-01-02"), balance); err != nil {
		t.Fatalf("seed invoice %s: %v", label, err)
	}
	return id
}

// A failure after the money began to move rolls the whole payment back. A trigger raises on the
// allocation insert of the LAST invoice the settle reaches, so by then the payment row, the earlier
// allocations, their balance moves, the subledger rows and the event are all written inside the
// transaction; none may survive.
func TestApplyReceived_AFailureAfterWritesBeganRollsEverythingBack(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	ids := sortedIDs(3)
	a := w.invoiceWithID(t, ids[0], w.customerID, "T1", 90, 60, 10000)
	b := w.invoiceWithID(t, ids[1], w.customerID, "T2", 60, 30, 10000)
	c := w.invoiceWithID(t, ids[2], w.customerID, "T3", 30, 10, 10000)

	fn := "apply_atomic_" + strings.ToLower(strings.ReplaceAll(w.tag, "-", "_"))
	if _, err := w.pool.Exec(ctx, fmt.Sprintf(
		`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN RAISE EXCEPTION 'planted failure on the last allocation'; END $$`, fn)); err != nil {
		t.Fatalf("plant the failing function: %v", err)
	}
	if _, err := w.pool.Exec(ctx, fmt.Sprintf(
		`CREATE TRIGGER %s BEFORE INSERT ON payment_allocations FOR EACH ROW
		 WHEN (NEW.invoice_id = '%s'::uuid) EXECUTE FUNCTION %s()`, fn, c, fn)); err != nil {
		t.Fatalf("plant the failing trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON payment_allocations`, fn))
		_, _ = w.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
	})

	_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", IdempotencyKey: w.tag,
		InvoiceIDs: []string{a, b, c}, AmountCents: 30000,
	})
	if err == nil || !strings.Contains(err.Error(), "planted failure") {
		t.Fatalf("the planted failure must surface, got %v", err)
	}
	payments, allocations, subledger, outbox := w.moneyRowCounts(t, w.customerID)
	if payments != 0 || allocations != 0 || subledger != 0 || outbox != 0 {
		t.Fatalf("a failure after writes began leaves nothing: payments %d allocations %d subledger %d outbox %d",
			payments, allocations, subledger, outbox)
	}
	for _, id := range []string{a, b, c} {
		if bal, st := w.invoiceState(t, id); bal != 10000 || st != "FINALIZED" {
			t.Fatalf("invoice %s must be untouched, got %d %s", id, bal, st)
		}
	}
}

// A failure AFTER the event was written leaves neither the money nor the event (item 102,
// condition a). The outbox insert is the walk's last write, so the only write after it is the
// commit itself: the planted failure is a DEFERRED constraint trigger on the payment row, which
// fires at COMMIT time, after the payment, the allocations, the balance moves, the subledger rows
// and the payment.received event are all written inside the transaction. If the event's fate were
// not tied to the transaction's, this is the leg that would say so: the rollback must take the
// event with the money.
func TestApplyReceived_AFailureAfterTheEventWriteLeavesNeitherTheMoneyNorTheEvent(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	ids := sortedIDs(3)
	a := w.invoiceWithID(t, ids[0], w.customerID, "E1", 90, 60, 10000)
	b := w.invoiceWithID(t, ids[1], w.customerID, "E2", 60, 30, 10000)
	c := w.invoiceWithID(t, ids[2], w.customerID, "E3", 30, 10, 10000)

	fn := "apply_event_tx_" + strings.ToLower(strings.ReplaceAll(w.tag, "-", "_"))
	if _, err := w.pool.Exec(ctx, fmt.Sprintf(
		`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		 BEGIN RAISE EXCEPTION 'planted failure at commit, after the event write'; END $$`, fn)); err != nil {
		t.Fatalf("plant the failing function: %v", err)
	}
	if _, err := w.pool.Exec(ctx, fmt.Sprintf(
		`CREATE CONSTRAINT TRIGGER %s AFTER INSERT ON payments DEFERRABLE INITIALLY DEFERRED
		 FOR EACH ROW WHEN (NEW.idempotency_key = '%s') EXECUTE FUNCTION %s()`,
		fn, w.tag, fn)); err != nil {
		t.Fatalf("plant the deferred failing trigger: %v", err)
	}
	t.Cleanup(func() {
		_, _ = w.pool.Exec(context.Background(), fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON payments`, fn))
		_, _ = w.pool.Exec(context.Background(), fmt.Sprintf(`DROP FUNCTION IF EXISTS %s()`, fn))
	})

	_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", IdempotencyKey: w.tag,
		InvoiceIDs: []string{a, b, c}, AmountCents: 30000,
	})
	if err == nil || !strings.Contains(err.Error(), "planted failure at commit") {
		t.Fatalf("the planted failure after the event write must surface, got %v", err)
	}
	// Neither the money nor the event: the event WAS written before the commit failed, and the
	// rollback took it with everything else.
	payments, allocations, subledger, outbox := w.moneyRowCounts(t, w.customerID)
	if payments != 0 || allocations != 0 || subledger != 0 || outbox != 0 {
		t.Fatalf("a failure after the event write leaves neither the money nor the event: payments %d allocations %d subledger %d outbox %d",
			payments, allocations, subledger, outbox)
	}
	for _, id := range []string{a, b, c} {
		if bal, st := w.invoiceState(t, id); bal != 10000 || st != "FINALIZED" {
			t.Fatalf("invoice %s must be untouched, got %d %s", id, bal, st)
		}
	}
	var keyRows int
	if err := w.pool.QueryRow(ctx, `SELECT COUNT(*) FROM payments WHERE idempotency_key = $1`, w.tag).Scan(&keyRows); err != nil {
		t.Fatalf("count by key: %v", err)
	}
	if keyRows != 0 {
		t.Fatalf("the failed payment burned no key, found %d rows", keyRows)
	}
}

// The refusal for another account's invoice does not depend on where that invoice falls in the
// sorted lock walk: first, middle or last, nothing is applied and the key is not burned.
func TestApplyReceived_TheForeignInvoiceRefusesAtAnyPlaceInTheWalk(t *testing.T) {
	for _, pos := range []struct {
		name  string
		index int // the foreign invoice's place among the three sorted ids
	}{
		{"first in the walk", 0},
		{"middle of the walk", 1},
		{"last in the walk", 2},
	} {
		t.Run(pos.name, func(t *testing.T) {
			w := newApplyWorld(t)
			ctx := context.Background()
			ids := sortedIDs(3)
			ours := append(append([]string{}, ids[:pos.index]...), ids[pos.index+1:]...)
			lo := w.invoiceWithID(t, ours[0], w.customerID, "L", 90, 60, 10000)
			hi := w.invoiceWithID(t, ours[1], w.customerID, "H", 60, 30, 10000)
			foreign := w.invoiceWithID(t, ids[pos.index], w.otherID, "X", 30, 10, 10000)
			_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
				CustomerID: w.customerID, Method: "CASH", IdempotencyKey: w.tag,
				InvoiceIDs: []string{lo, hi, foreign}, AmountCents: 20000,
			})
			var nse NoSuchInvoiceError
			if !errors.As(err, &nse) || nse.InvoiceID != foreign {
				t.Fatalf("the foreign invoice refuses the whole payment, got %v", err)
			}
			payments, allocations, subledger, outbox := w.moneyRowCounts(t, w.customerID)
			if payments != 0 || allocations != 0 || subledger != 0 || outbox != 0 {
				t.Fatalf("nothing applied: %d %d %d %d", payments, allocations, subledger, outbox)
			}
			for _, id := range []string{lo, hi} {
				if bal, _ := w.invoiceState(t, id); bal != 10000 {
					t.Fatalf("invoice %s untouched, balance %d", id, bal)
				}
			}
		})
	}
}

// The apply takes the invoice rows in ASCENDING id order whatever order the caller listed them in,
// the order the till's returns lock theirs in (PR 277), so it cannot meet a return head on. The leg
// holds the LOWER invoice from a second connection, lists the invoices HIGH first, and while the
// apply waits on the lower row asks, with NOWAIT, for the higher one: if the apply had walked the
// list it would already hold the higher row and the probe would be refused.
func TestApplyReceived_InvoiceLocksAreTakenAscendingWhateverTheListOrder(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	ids := sortedIDs(2)
	low := w.invoiceWithID(t, ids[0], w.customerID, "LOW", 90, 60, 10000)
	high := w.invoiceWithID(t, ids[1], w.customerID, "HIGH", 60, 30, 10000)

	holder, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin holder: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, low); err != nil {
		t.Fatalf("hold the lower invoice: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
			CustomerID: w.customerID, Method: "EFT", IdempotencyKey: w.tag,
			InvoiceIDs: []string{high, low}, AmountCents: 20000,
		})
		done <- err
	}()

	// Wait until the apply is parked on a row lock (it queues behind the holder).
	deadline := time.Now().Add(15 * time.Second)
	for {
		var waiting int
		if err := w.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM pg_stat_activity
			 WHERE wait_event_type = 'Lock' AND query LIKE '%customer_id = $2::uuid%'
			   AND query LIKE '%FOR NO KEY UPDATE%'
			   AND pid <> pg_backend_pid()`).Scan(&waiting); err != nil {
			t.Fatalf("watch the waiter: %v", err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the apply never queued behind the held invoice")
		}
		time.Sleep(50 * time.Millisecond)
	}

	probe, err := w.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin probe: %v", err)
	}
	_, probeErr := probe.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE NOWAIT`, high)
	_ = probe.Rollback(ctx)
	if probeErr != nil {
		t.Fatalf("while parked on the lower invoice the apply must not yet hold the higher one: %v", probeErr)
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release the holder: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("the apply lands once the lower row frees: %v", err)
	}
	if payments, allocations, _, _ := w.moneyRowCounts(t, w.customerID); payments != 1 || allocations != 2 {
		t.Fatalf("one payment, two allocations, got %d and %d", payments, allocations)
	}
}

// A replay is judged like a first attempt, not handed to whoever holds the key. The same caller
// replays freely; another account naming the key is refused; a caller whose branch reach does not
// include the payment's invoice is told no such invoice, exactly as a first attempt would be.
func TestApplyReceived_AReplayIsNotABearerOfTheAnswer(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	inv := w.invoice(t, w.customerID, "RP", 60, 30, 9000)
	if _, err := w.pool.Exec(ctx, `UPDATE invoices SET branch_id = $2::uuid WHERE id = $1::uuid`, inv, applyKingston); err != nil {
		t.Fatalf("move the invoice to Kingston: %v", err)
	}
	other := w.invoice(t, w.otherID, "RPO", 60, 30, 9000)
	in := ApplyRequest{CustomerID: w.customerID, Method: "CHEQUE", IdempotencyKey: w.tag,
		BranchIDs: []string{applyKingston}, InvoiceIDs: []string{inv}, AmountCents: 9000}
	first, err := w.repo.ApplyReceived(ctx, in)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}

	if again, err := w.repo.ApplyReceived(ctx, in); err != nil || !again.Replayed || again.PaymentID != first.PaymentID {
		t.Fatalf("the same caller replays the same answer: %v %+v", err, again)
	}

	trenton := in
	trenton.BranchIDs = []string{dib510Trenton}
	var nse NoSuchInvoiceError
	if _, err := w.repo.ApplyReceived(ctx, trenton); !errors.As(err, &nse) || nse.InvoiceID != inv {
		t.Fatalf("a replay from outside the invoice's branch reach is not found, got %v", err)
	}

	foreignAccount := in
	foreignAccount.CustomerID = w.otherID
	foreignAccount.InvoiceIDs = []string{other}
	if got, err := w.repo.ApplyReceived(ctx, foreignAccount); err == nil {
		t.Fatalf("another account naming the key must not be handed the first payment: %+v", got)
	}
	if payments, _, _, _ := w.moneyRowCounts(t, w.otherID); payments != 0 {
		t.Fatalf("the other account holds no payment, got %d", payments)
	}
}

// A fenced caller does not LOCK, and so does not WAIT ON, a row it may not see (the Opus review's
// P3-2): the account and the branch reach are folded into the locking statement itself, so a held
// foreign row cannot stall a request that was never going to be allowed to read it. The answer is
// unchanged, the same not found words for a missing row, another account's and an out of reach one.
func TestApplyReceived_AFencedCallerDoesNotWaitOnARowItMayNotSee(t *testing.T) {
	for _, tc := range []struct {
		name    string
		foreign bool // the row is another account's, else this account's but out of reach
	}{
		{"another account's invoice", true},
		{"this account's invoice outside the reach", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newApplyWorld(t)
			ctx := context.Background()
			king := w.invoice(t, w.customerID, "FW", 30, 10, 9000)
			if !tc.foreign {
				if _, err := w.pool.Exec(ctx,
					`UPDATE invoices SET branch_id = $2::uuid WHERE id = $1::uuid`, king, applyKingston); err != nil {
					t.Fatalf("move the invoice to Kingston: %v", err)
				}
			} else if _, err := w.pool.Exec(ctx,
				`UPDATE invoices SET customer_id = $2::uuid WHERE id = $1::uuid`, king, w.otherID); err != nil {
				t.Fatalf("move the invoice to the other account: %v", err)
			}

			// A second connection holds the row this request may not see.
			holder, err := w.pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin holder: %v", err)
			}
			if _, err := holder.Exec(ctx, `SELECT 1 FROM invoices WHERE id = $1::uuid FOR NO KEY UPDATE`, king); err != nil {
				t.Fatalf("hold the row: %v", err)
			}
			release := make(chan struct{})
			go func() {
				time.Sleep(3 * time.Second)
				_ = holder.Rollback(ctx)
				close(release)
			}()
			t.Cleanup(func() { _ = holder.Rollback(ctx) })

			// The scoped apply must answer its not found WHILE the holder still holds the row.
			done := make(chan error, 1)
			go func() {
				_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
					CustomerID: w.customerID, Method: "CASH", IdempotencyKey: w.tag,
					BranchIDs: []string{dib510Trenton}, InvoiceIDs: []string{king}, AmountCents: 9000,
				})
				done <- err
			}()
			select {
			case err := <-done:
				var nse NoSuchInvoiceError
				if !errors.As(err, &nse) || nse.InvoiceID != king {
					t.Fatalf("the refusal is the not found refusal, got %v", err)
				}
			case <-time.After(1500 * time.Millisecond):
				t.Fatal("the request waited on a row it may not see: the lock must fold the account and the reach")
			}
			<-release
		})
	}
}
