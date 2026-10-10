//go:build integration

// received_apply_integration_test.go, THE RECEIVED PAYMENT, PROVED AGAINST THE REAL SCHEMA.
//
// The register's per invoice route makes the CALLER split one cheque across invoices, one request
// per slice, and nothing about that is atomic. This file proves the replacement: ONE received
// payment (amount, method, reference) named against a list of the account's open invoices, the
// SERVER allocating oldest first inside ONE transaction, with the invoice row locks taken in the
// sorted ascending id order the till's returns use (PR 277), so it cannot deadlock against a return
// or the counter's payments.
//
// Every leg looks at the LEDGER, not the return value: row counts, balances, the subledger, the
// outbox. Every fact is planted inside test owned customers and torn down, so a shared integration
// database cannot move a number here.
//
// Run: go test -tags integration ./internal/payments/ -run ApplyReceived
package payments

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
	"github.com/jackc/pgx/v5/pgxpool"
)

const applyKingston = "5d1b3f6e-0000-4000-9000-000000000002"

// applyWorld is one test owned account with invoices the legs choose, dated against the pinned now
// so the oldest first walk is deterministic.
type applyWorld struct {
	pool       *pgxpool.Pool
	repo       *Repository
	customerID string
	otherID    string // a second account, for the not your invoice refusal
	tag        string
}

func newApplyWorld(t *testing.T) *applyWorld {
	t.Helper()
	pool := dib510Pool(t)
	w := &applyWorld{pool: pool, repo: NewRepository(pool), tag: "APLY-" + d510RandomTag()}
	mk := func(label string) string {
		var id string
		if err := pool.QueryRow(context.Background(),
			`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
			 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
			w.tag+"-"+label, "Received apply "+w.tag+" "+label, dib510Trenton).Scan(&id); err != nil {
			t.Fatalf("seed customer %s: %v", label, err)
		}
		return id
	}
	w.customerID = mk("main")
	w.otherID = mk("other")
	t.Cleanup(func() {
		c := context.Background()
		for _, cid := range []string{w.customerID, w.otherID} {
			_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`, cid)
			_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`, cid)
			_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, cid)
		}
	})
	return w
}

// invoice plants one open invoice dated RELATIVE to the pinned now: issueDays/dueDays are ages in
// days (90 due 60 means issued 90 days ago, due 60 days ago).
func (w *applyWorld) invoice(t *testing.T, customerID, label string, issueDays, dueDays int, balance int64) string {
	t.Helper()
	issue := applyTestNow.AddDate(0, 0, issueDays*-1)
	due := applyTestNow.AddDate(0, 0, dueDays*-1)
	var id string
	if err := w.pool.QueryRow(context.Background(),
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, $6, 0, $6, $6, $4, 'INVOICE')
		 RETURNING id::text`,
		w.tag+"-"+label, customerID, dib510Trenton,
		issue.Format("2006-01-02"), due.Format("2006-01-02"), balance).Scan(&id); err != nil {
		t.Fatalf("seed invoice %s: %v", label, err)
	}
	return id
}

// invoiceState reads one invoice's balance and status.
func (w *applyWorld) invoiceState(t *testing.T, invoiceID string) (balance int64, status string) {
	t.Helper()
	if err := w.pool.QueryRow(context.Background(),
		`SELECT balance_cents, status::text FROM invoices WHERE id = $1::uuid`, invoiceID).
		Scan(&balance, &status); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	return balance, status
}

// moneyRowCounts is the ledger witness: the four tables a received payment may touch.
func (w *applyWorld) moneyRowCounts(t *testing.T, customerID string) (payments, allocations, subledger, outbox int) {
	t.Helper()
	ctx := context.Background()
	queries := []struct {
		sql string
		dst *int
	}{
		{`SELECT COUNT(*) FROM payments WHERE customer_id = $1::uuid`, &payments},
		{`SELECT COUNT(*) FROM payment_allocations a JOIN payments p ON p.id = a.payment_id
		   WHERE p.customer_id = $1::uuid`, &allocations},
		{`SELECT COUNT(*) FROM customer_transactions WHERE customer_id = $1::uuid`, &subledger},
		{`SELECT COUNT(*) FROM events_outbox WHERE payload->>'customer_id' = $1`, &outbox},
	}
	for _, q := range queries {
		if err := w.pool.QueryRow(ctx, q.sql, customerID).Scan(q.dst); err != nil {
			t.Fatalf("count rows: %v", err)
		}
	}
	return payments, allocations, subledger, outbox
}

// One cheque, three invoices, the server's split not the caller's: the oldest closes, the middle
// takes the rest in part, the youngest is untouched, and the ledger carries ONE payment row with its
// two allocations, not three payments.
func TestApplyReceived_OnePaymentSplitsAcrossThreeInvoices(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	oldest := w.invoice(t, w.customerID, "OLD", 90, 60, 30000)
	middle := w.invoice(t, w.customerID, "MID", 60, 30, 20000)
	youngest := w.invoice(t, w.customerID, "NEW", 30, 10, 10000)

	// The list arrives YOUNGEST FIRST on purpose: the server allocates by age, not by the list.
	ans, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ 771",
		IdempotencyKey: w.tag, InvoiceIDs: []string{youngest, middle, oldest}, AmountCents: 40000,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ans.Preview || ans.Replayed || ans.PaymentID == "" {
		t.Fatalf("a fresh apply answers a real payment: %+v", ans)
	}
	if len(ans.Lines) != 3 || ans.Lines[0].InvoiceID != oldest || ans.Lines[1].InvoiceID != middle || ans.Lines[2].InvoiceID != youngest {
		t.Fatalf("the split answers oldest first for every named invoice, got %v", ans.Lines)
	}
	if ans.Lines[0].Outcome != ApplyOutcomeCloses || ans.Lines[0].AppliedCents != 30000 || ans.Lines[0].BalanceAfterCents != 0 {
		t.Fatalf("the oldest closes in full: %+v", ans.Lines[0])
	}
	if ans.Lines[1].Outcome != ApplyOutcomePart || ans.Lines[1].AppliedCents != 10000 || ans.Lines[1].BalanceAfterCents != 10000 {
		t.Fatalf("the middle takes the remainder in part: %+v", ans.Lines[1])
	}
	if ans.Lines[2].Outcome != ApplyOutcomeNone || ans.Lines[2].AppliedCents != 0 || ans.Lines[2].BalanceAfterCents != 10000 {
		t.Fatalf("the youngest is untouched and says so: %+v", ans.Lines[2])
	}
	if ans.AppliedCents != 40000 || ans.UnappliedCents != 0 {
		t.Fatalf("the whole payment is applied: applied %d unapplied %d", ans.AppliedCents, ans.UnappliedCents)
	}

	// The ledger, not the answer, is the witness.
	var payRows, allocRows int
	var payAmount int64
	if err := w.pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(SUM(amount_cents),0) FROM payments WHERE customer_id = $1::uuid`, w.customerID).
		Scan(&payRows, &payAmount); err != nil {
		t.Fatalf("read payments: %v", err)
	}
	if payRows != 1 || payAmount != 40000 {
		t.Fatalf("ONE payment row of the whole amount, got %d rows summing %d", payRows, payAmount)
	}
	if err := w.pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(SUM(a.amount_cents),0) FROM payment_allocations a
		  JOIN payments p ON p.id = a.payment_id WHERE p.customer_id = $1::uuid`,
		w.customerID).Scan(&allocRows, &payAmount); err != nil {
		t.Fatalf("read allocations: %v", err)
	}
	if allocRows != 2 || payAmount != 40000 {
		t.Fatalf("two allocations (the untouched invoice gets none), got %d rows summing %d", allocRows, payAmount)
	}
	if b, s := w.invoiceState(t, oldest); b != 0 || s != "PAID" {
		t.Fatalf("the oldest is PAID at zero, got %d %s", b, s)
	}
	if b, s := w.invoiceState(t, middle); b != 10000 || s != "PARTIALLY_PAID" {
		t.Fatalf("the middle is PARTIALLY_PAID at 10000, got %d %s", b, s)
	}
	if b, s := w.invoiceState(t, youngest); b != 10000 || s != "FINALIZED" {
		t.Fatalf("the youngest is untouched, got %d %s", b, s)
	}
	// The subledger rows: one PAYMENT row per slice, the amounts the split applied, and the account's
	// open balance exactly 20000 after them. The slices are WRITTEN in the lock order (ascending
	// invoice id), so each row's running balance_after follows that order, not the age order the
	// answer carries; what the account owes after both is order independent and is the figure pinned.
	var amts []int64
	rows, err := w.pool.Query(ctx, `
		SELECT amount_cents FROM customer_transactions
		 WHERE customer_id = $1::uuid AND txn_type = 'PAYMENT' ORDER BY amount_cents`, w.customerID)
	if err != nil {
		t.Fatalf("read subledger: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var amt int64
		if err := rows.Scan(&amt); err != nil {
			t.Fatalf("scan subledger: %v", err)
		}
		amts = append(amts, amt)
	}
	if len(amts) != 2 || amts[0] != -30000 || amts[1] != -10000 {
		t.Fatalf("the subledger carries one row per applied slice, got %v", amts)
	}
	var openAR int64
	if err := w.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1::uuid AND status IN ('FINALIZED','PARTIALLY_PAID')`,
		w.customerID).Scan(&openAR); err != nil {
		t.Fatalf("read open AR: %v", err)
	}
	if openAR != 20000 {
		t.Fatalf("the account owes exactly 20000 after the payment, got %d", openAR)
	}
}

// A retry with the same key posts once: the second call answers the stored payment as a replay, and
// not one table the payment touches grows a row.
func TestApplyReceived_ARetryWithTheSameKeyPostsOnce(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	inv := w.invoice(t, w.customerID, "ONE", 90, 60, 25000)
	in := ApplyRequest{
		CustomerID: w.customerID, Method: "EFT", Reference: "TRACE-88",
		IdempotencyKey: w.tag, InvoiceIDs: []string{inv}, AmountCents: 25000,
	}
	first, err := w.repo.ApplyReceived(ctx, in)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	second, err := w.repo.ApplyReceived(ctx, in)
	if err != nil {
		t.Fatalf("the double click must succeed as a replay, not error: %v", err)
	}
	if !second.Replayed || second.PaymentID != first.PaymentID {
		t.Fatalf("the retry answers the first payment: replayed %v ids %s vs %s", second.Replayed, second.PaymentID, first.PaymentID)
	}
	payments, allocations, subledger, outbox := w.moneyRowCounts(t, w.customerID)
	if payments != 1 || allocations != 1 || subledger != 1 || outbox != 1 {
		t.Fatalf("a retry posts nothing: payments %d allocations %d subledger %d outbox %d", payments, allocations, subledger, outbox)
	}
	// The journal rail hears the payment once: exactly one payment.received row, the row the GL
	// consumer keys its one journal on.
	var events int
	if err := w.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM events_outbox WHERE topic = 'payment.received' AND payload->>'payment_id' = $1`,
		first.PaymentID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if events != 1 {
		t.Fatalf("one payment.received event for the one payment, got %d", events)
	}
}

// The refusal that lands midway through the list refuses the WHOLE payment: an invoice that is not
// the account's (here another customer's) leaves nothing applied anywhere, not even on the invoices
// that were fine.
func TestApplyReceived_ARefusalMidwayLeavesNothingApplied(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	first := w.invoice(t, w.customerID, "F1", 90, 60, 10000)
	second := w.invoice(t, w.customerID, "F2", 60, 30, 10000)
	foreign := w.invoice(t, w.otherID, "X1", 30, 10, 10000)

	_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", IdempotencyKey: w.tag,
		InvoiceIDs: []string{first, second, foreign}, AmountCents: 20000,
	})
	if err == nil {
		t.Fatal("an invoice of another account must refuse the whole payment")
	}
	var nse NoSuchInvoiceError
	if !errors.As(err, &nse) || nse.InvoiceID != foreign {
		t.Fatalf("the refusal names the invoice that is not the account's, got %v", err)
	}
	payments, allocations, subledger, outbox := w.moneyRowCounts(t, w.customerID)
	if payments != 0 || allocations != 0 || subledger != 0 || outbox != 0 {
		t.Fatalf("a refused payment writes nothing: payments %d allocations %d subledger %d outbox %d", payments, allocations, subledger, outbox)
	}
	if b, _ := w.invoiceState(t, first); b != 10000 {
		t.Fatalf("the first invoice must be untouched, balance %d", b)
	}
	if b, _ := w.invoiceState(t, second); b != 10000 {
		t.Fatalf("the second invoice must be untouched, balance %d", b)
	}
	var keyRows int
	if err := w.pool.QueryRow(ctx, `SELECT COUNT(*) FROM payments WHERE idempotency_key = $1`, w.tag).Scan(&keyRows); err != nil {
		t.Fatalf("count by key: %v", err)
	}
	if keyRows != 0 {
		t.Fatalf("a refused payment must not burn its key, found %d rows", keyRows)
	}
}

// An amount over what the selected invoices can take is refused in words with the ceiling named,
// and writes nothing. The ceiling is the payable (balance less holdback), the same figure the per
// invoice route offers.
func TestApplyReceived_OverTheSelectedInvoicesIsRefused(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	a := w.invoice(t, w.customerID, "O1", 90, 60, 10000)
	b := w.invoice(t, w.customerID, "O2", 60, 30, 8000)
	if _, err := w.pool.Exec(ctx, `UPDATE invoices SET holdback_cents = 3000 WHERE id = $1::uuid`, b); err != nil {
		t.Fatalf("plant holdback: %v", err)
	}
	_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CASH", IdempotencyKey: w.tag,
		InvoiceIDs: []string{a, b}, AmountCents: 17001,
	})
	if err == nil {
		t.Fatal("an amount over the selected invoices' payable must be refused")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the over refusal is a bad request in words, got %T %v", err, err)
	}
	if !strings.Contains(bad.Msg, "the selected invoices can take at most 15000 cents right now") {
		t.Fatalf("the refusal names the ceiling: %q", bad.Msg)
	}
	payments, allocations, _, _ := w.moneyRowCounts(t, w.customerID)
	if payments != 0 || allocations != 0 {
		t.Fatalf("the over refusal writes nothing: %d payments %d allocations", payments, allocations)
	}
}

// The preview flag answers the same split without writing anything: the locks are taken (the split
// is computed under them, against live balances) and rolled back with the transaction.
func TestApplyReceived_PreviewAnswersTheSplitAndWritesNothing(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	old := w.invoice(t, w.customerID, "P1", 90, 60, 20000)
	newer := w.invoice(t, w.customerID, "P2", 30, 10, 5000)
	beforeP, beforeA, beforeS, beforeO := w.moneyRowCounts(t, w.customerID)

	ans, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ PREVIEW",
		IdempotencyKey: w.tag, InvoiceIDs: []string{old, newer}, AmountCents: 22000, Preview: true,
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !ans.Preview || ans.PaymentID != "" || ans.PaymentNo != "" || ans.Replayed {
		t.Fatalf("a preview answers no payment: %+v", ans)
	}
	if len(ans.Lines) != 2 || ans.Lines[0].InvoiceID != old || ans.Lines[0].AppliedCents != 20000 ||
		ans.Lines[0].Outcome != ApplyOutcomeCloses || ans.Lines[1].AppliedCents != 2000 || ans.Lines[1].Outcome != ApplyOutcomePart {
		t.Fatalf("the preview split is the apply's split: %v", ans.Lines)
	}
	afterP, afterA, afterS, afterO := w.moneyRowCounts(t, w.customerID)
	if beforeP != afterP || beforeA != afterA || beforeS != afterS || beforeO != afterO {
		t.Fatalf("preview writes nothing: payments %d->%d allocations %d->%d subledger %d->%d outbox %d->%d",
			beforeP, afterP, beforeA, afterA, beforeS, afterS, beforeO, afterO)
	}
	if b, _ := w.invoiceState(t, old); b != 20000 {
		t.Fatalf("the previewed invoice is untouched, balance %d", b)
	}
	// And the same key still applies for real afterwards: a preview burns nothing, key included.
	real, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CHEQUE", Reference: "CHQ PREVIEW",
		IdempotencyKey: w.tag, InvoiceIDs: []string{old, newer}, AmountCents: 22000,
	})
	if err != nil || real.Replayed || real.PaymentID == "" {
		t.Fatalf("the real apply after the preview must post: %v %+v", err, real)
	}
}

// Two applies against the same invoice serialise: the payments counter row is taken before any
// invoice row (the one money lock order), so the two never interleave their rows. The leg's amounts
// fit only once: whichever apply lands second must refuse on the ceiling, never a deadlock, and the
// invoice's balance moves by exactly what one apply could take.
func TestApplyReceived_TwoConcurrentAppliesAgainstOneInvoiceSerialise(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	inv := w.invoice(t, w.customerID, "S1", 90, 60, 10000)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
				CustomerID: w.customerID, Method: "EFT",
				IdempotencyKey: fmt.Sprintf("%s-%d", w.tag, i),
				InvoiceIDs:     []string{inv}, AmountCents: 6000,
			})
			errs[i] = err
		}(i)
	}
	wg.Wait()
	landed, refused := 0, 0
	refusalWords := ""
	for _, err := range errs {
		switch {
		case err == nil:
			landed++
		case errors.As(err, new(NoSuchInvoiceError)):
			t.Fatalf("the invoice exists and is the account's: %v", err)
		default:
			// A 40P01 or a connection level failure here is the deadlock this leg exists to catch:
			// the refusal this leg expects is the ceiling's own words.
			var bad httpx.BadRequestError
			if !errors.As(err, &bad) {
				t.Fatalf("a concurrent apply must serialise or refuse on the ceiling, got %v", err)
			}
			refused++
			refusalWords = bad.Msg
		}
	}
	if landed != 1 || refused != 1 {
		t.Fatalf("exactly one apply of 6000 lands against a 10000 invoice and one refuses: landed %d refused %d (%q)", landed, refused, refusalWords)
	}
	if !strings.Contains(refusalWords, "can take at most 4000 cents") {
		t.Fatalf("the second apply names the ceiling the first apply left: %q", refusalWords)
	}
	if b, s := w.invoiceState(t, inv); b != 4000 || s != "PARTIALLY_PAID" {
		t.Fatalf("the invoice moved once, to 4000 PARTIALLY_PAID, got %d %s", b, s)
	}
	payments, allocations, _, _ := w.moneyRowCounts(t, w.customerID)
	if payments != 1 || allocations != 1 {
		t.Fatalf("one payment row and one allocation survive the race, got %d and %d", payments, allocations)
	}
}

// A branch scoped caller (the reach list) does not see another branch's invoice at all: the refusal
// is the same no such invoice as a missing one, no existence leak across the branch wall, exactly as
// the till's pay balance door answers an order out of reach.
func TestApplyReceived_AnotherBranchsInvoiceIsNotFound(t *testing.T) {
	w := newApplyWorld(t)
	ctx := context.Background()
	king := w.invoice(t, w.customerID, "KGN", 30, 10, 7000)
	if _, err := w.pool.Exec(ctx,
		`UPDATE invoices SET branch_id = $2::uuid WHERE id = $1::uuid`, king, applyKingston); err != nil {
		t.Fatalf("move invoice to Kingston: %v", err)
	}

	_, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CASH", IdempotencyKey: w.tag,
		BranchIDs: []string{dib510Trenton}, InvoiceIDs: []string{king}, AmountCents: 7000,
	})
	if err == nil {
		t.Fatal("an invoice outside the caller's branch reach must refuse")
	}
	var nse NoSuchInvoiceError
	if !errors.As(err, &nse) || nse.InvoiceID != king {
		t.Fatalf("the out of reach refusal is the not found refusal, got %v", err)
	}
	// The same invoice under a global reach (nil) is payable: reach, not the invoice, was the fact.
	if _, err := w.repo.ApplyReceived(ctx, ApplyRequest{
		CustomerID: w.customerID, Method: "CASH", IdempotencyKey: w.tag + "-g",
		InvoiceIDs: []string{king}, AmountCents: 7000,
	}); err != nil {
		t.Fatalf("a global reach applies the same invoice: %v", err)
	}
}
