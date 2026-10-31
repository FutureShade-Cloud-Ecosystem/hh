//go:build integration

// nsf_change_integration_test.go, TM-2 fix round 2, P1-1: a bounced cheque counters the change on
// account its receipt left, as the void and the reversal do. The legs run the real apply, the real
// bounce and the real GL consumer, with the cheque at the invoice's face (so the discount rule
// turns it into the payoff plus change).
//
// Run: go test -tags integration ./internal/till/ -run NSFChange
package till

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/notify"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
)

func TestNSFChange_FaceChequeBounceCountersTheChange(t *testing.T) {
	r := runNSFChangeLeg(t, 0)
	if r.change != 0 {
		t.Errorf("the change group nets %d after the bounce, want 0", r.change)
	}
	if r.bal != 100000 || r.st != "FINALIZED" {
		t.Errorf("after the bounce the invoice is %d %s, want 100000 FINALIZED", r.bal, r.st)
	}
	if r.n5450 != 0 {
		t.Errorf("5450 nets %d after the bounce, want 0", r.n5450)
	}
	if want := 100000 + r.n1100; r.aged != want {
		t.Errorf("the aging reads %d, want the invoice plus 1100's net (the NSF fee only) = %d", r.aged, want)
	}
	if r.n1100 != r.fee {
		t.Errorf("1100 nets %d over the payment journals, want the NSF fee only (%d)", r.n1100, r.fee)
	}
	// P3-2: the register's discount figure was 2000 with the cheque and nets to 0 once it bounced.
	if r.registerBefore != 2000 || r.registerAfter != 0 {
		t.Errorf("the register's discount figure reads %d after the apply and %d after the bounce, want 2000 then 0", r.registerBefore, r.registerAfter)
	}
}

// Part of the change was applied to another invoice before the bounce: the bounce takes the
// void's answer. It counters the receipt's WHOLE recorded change and refuses nothing, so the group
// nets +applied (the part that already paid the other invoice becomes a receivable owed back, the
// other invoice stays paid to that extent) and the aging still ties to 1100.
func TestNSFChange_PartlyAppliedChangeIsOwedBack(t *testing.T) {
	r := runNSFChangeLeg(t, 1500)
	if r.change != 1500 {
		t.Errorf("the change group nets %d after the bounce, want +1500 (the applied part owed back)", r.change)
	}
	if r.bal != 100000 || r.st != "FINALIZED" {
		t.Errorf("after the bounce invoice one is %d %s, want 100000 FINALIZED", r.bal, r.st)
	}
	if r.otherBal != 48500 || r.otherSt != "PARTIALLY_PAID" {
		t.Errorf("invoice two reads %d %s, want 48500 PARTIALLY_PAID", r.otherBal, r.otherSt)
	}
	if r.n5450 != 0 {
		t.Errorf("5450 nets %d after the bounce, want 0", r.n5450)
	}
	// Invoice one 100000, invoice two 48500, the owed back 1500, then whatever 1100 holds beyond
	// the invoices' own debits (the fee).
	if want := int64(100000+48500+1500) + r.n1100; r.aged != want {
		t.Errorf("the aging reads %d, want %d", r.aged, want)
	}
}

type nsfChangeOut struct {
	change, bal, otherBal, n5450, n1100, aged, fee int64
	// registerBefore and registerAfter are the register's early payment discount figure for the
	// discounted invoice after the apply and after the bounce (P3-2: it must net back to 0).
	registerBefore, registerAfter int64
	st, otherSt                   string
}

// runNSFChangeLeg seeds a customer with a 2/10 invoice of 100000 (inside the window) and, when
// creditToOther is above zero, a second plain invoice of 50000. It applies a face cheque of 100000
// through the real ApplyReceived, applies creditToOther of the change to the second invoice
// through the credit door, bounces the cheque and drains the GL consumer.
func runNSFChangeLeg(t *testing.T, creditToOther int64) nsfChangeOut {
	t.Helper()
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	cctx := globalCtx(userID)
	tag := "NSFC-" + time.Now().Format("150405.000000")

	var customerID, invoiceID, otherID, orgID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 1000000) RETURNING id::text`,
		tag, tag, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO org (id, slug, name) VALUES (gen_random_uuid(), $1::text, $1::text) RETURNING id::text`, "nsfc-"+tag).Scan(&orgID); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []string{
			`DELETE FROM gl_journal_lines WHERE journal_id IN (
			   SELECT j.id FROM gl_journals j JOIN payments p ON p.id = j.source_ref_id WHERE p.customer_id = $1::uuid)`,
			`DELETE FROM gl_journals WHERE source_ref_id IN (SELECT p.id FROM payments p WHERE p.customer_id = $1::uuid)`,
			`DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`,
			`DELETE FROM credit_holds WHERE customer_id = $1::uuid`,
			`DELETE FROM payment_nsf WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`,
			`DELETE FROM customer_transactions WHERE customer_id = $1::uuid`,
			`DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`,
			`DELETE FROM credit_application_keys WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`,
			`DELETE FROM payments WHERE customer_id = $1::uuid`,
			`DELETE FROM invoices WHERE customer_id = $1::uuid`,
			`DELETE FROM customers WHERE id = $1::uuid`,
		} {
			if _, err := pool.Exec(c, q, customerID); err != nil {
				t.Errorf("cleanup failed on %q: %v", q, err)
			}
		}
		if _, err := pool.Exec(c, `DELETE FROM org WHERE id = $1::uuid`, orgID); err != nil {
			t.Errorf("cleanup failed on org: %v", err)
		}
	})
	seedInvoice := func(no string, total int64, terms string, disc string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, discount_date, terms_code,
			                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
			 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED',
			         (now() AT TIME ZONE 'America/Toronto')::date - 2, (now() AT TIME ZONE 'America/Toronto')::date + 28,
			         `+disc+`, NULLIF($4, ''),
			         $5, 0, $5, $5, now(), 'INVOICE') RETURNING id::text`,
			no, customerID, branchID, terms, total).Scan(&id); err != nil {
			t.Fatalf("seed invoice %s: %v", no, err)
		}
		if _, err := pool.Exec(ctx,
			`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
			 VALUES ($1::uuid, 'INVOICE', $2, $2, 'invoice', $3::uuid, $4::uuid, 'nsf change leg invoice')`,
			customerID, total, id, branchID); err != nil {
			t.Fatalf("seed the invoice debit: %v", err)
		}
		return id
	}
	invoiceID = seedInvoice(tag+"-I", 100000, "2-10-NET30", "(now() AT TIME ZONE 'America/Toronto')::date + 8")
	if creditToOther > 0 {
		otherID = seedInvoice(tag+"-J", 50000, "", "NULL")
	}

	repo := payments.NewRepository(pool).WithDiscountTerms(nsfDiscountTerms{})
	ans, err := repo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ NSFC", IdempotencyKey: tag,
		InvoiceIDs: []string{invoiceID}, AmountCents: 100000, ReceivedBy: userID,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ans.Lines[0].DiscountCents != 2000 {
		t.Fatalf("the apply took %d of discount, want 2000", ans.Lines[0].DiscountCents)
	}
	if creditToOther > 0 {
		var changeRowID string
		if err := pool.QueryRow(ctx, `
			SELECT id::text FROM customer_transactions
			 WHERE source_type = 'receipt_change' AND source_ref_id = $1::uuid`, ans.PaymentID).Scan(&changeRowID); err != nil {
			t.Fatalf("find the change row: %v", err)
		}
		if _, err := billing.NewCreditApplyService(billing.NewRepository(pool)).Apply(ctx, billing.ApplyCreditInput{
			CustomerID: customerID, CreditKind: billing.CreditKindAccount, CreditID: changeRowID,
			InvoiceID: otherID, AmountCents: creditToOther, Reason: "apply part of the change",
			ActorID: userID, IdempotencyKey: tag + "-APP", OrgID: orgID,
		}, true); err != nil {
			t.Fatalf("apply part of the change to the other invoice: %v", err)
		}
	}
	var out nsfChangeOut
	registerFigure := func() int64 {
		n, err := billing.NewRepository(pool).DiscountCents(ctx, invoiceID)
		if err != nil {
			t.Fatalf("the register's discount figure: %v", err)
		}
		return n
	}
	out.registerBefore = registerFigure()
	if _, err := svc.MarkNSF(cctx, NSFInput{PaymentID: ans.PaymentID, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	out.registerAfter = registerFigure()

	glSvc := gl.NewService(gl.NewRepository(pool)).WithPaymentReader(NewRepository(pool))
	d := notify.NewDispatcher(notify.NewPgOutboxStore(pool), slog.Default())
	glSvc.RegisterPostingHandlers(d)
	for i := 0; i < 200; i++ {
		d.DrainOnce(ctx)
		var n int
		if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM gl_journals WHERE source_ref_id = $1::uuid`, ans.PaymentID).Scan(&n); err != nil {
			t.Fatalf("watch the journals: %v", err)
		}
		if n >= 3 {
			break
		}
	}

	read := func(id string) (int64, string) {
		var b int64
		var s string
		if err := pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, id).Scan(&b, &s); err != nil {
			t.Fatalf("read invoice: %v", err)
		}
		return b, s
	}
	out.bal, out.st = read(invoiceID)
	if otherID != "" {
		out.otherBal, out.otherSt = read(otherID)
	}
	acct := func(code string) int64 {
		var net int64
		if err := pool.QueryRow(ctx, `
			SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0)
			  FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id JOIN gl_accounts a ON a.id = l.account_id
			 WHERE j.source_ref_id = $1::uuid AND a.code = $2`, ans.PaymentID, code).Scan(&net); err != nil {
			t.Fatalf("read %s: %v", code, err)
		}
		return net
	}
	out.n5450, out.n1100, out.fee = acct("5450"), acct("1100"), nsfFee(t, pool, ans.PaymentID)
	rows, err := billing.NewCreditService(billing.NewRepository(pool)).Aging(ctx, time.Now().Add(time.Hour), "")
	if err != nil {
		t.Fatalf("aging: %v", err)
	}
	out.aged = -1
	for _, r := range rows {
		if r.CustomerID == customerID {
			out.aged = r.TotalCents
		}
	}
	if err := pool.QueryRow(ctx, `SELECT COALESCE(SUM(amount_cents),0) FROM customer_transactions
		WHERE customer_id = $1::uuid AND source_type = 'receipt_change' AND source_ref_id = $2::uuid`,
		customerID, ans.PaymentID).Scan(&out.change); err != nil {
		t.Fatalf("read change: %v", err)
	}
	return out
}

// TM-2 fix round 2, P3-1: the bounce reopens by the one FINALIZED rule every undo door shares
// (>= total_cents). A balance that lands between total less holdback and total reads PARTIALLY_PAID.
func TestNSFStatusRule_BounceReadsPartiallyPaidBelowTotal(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	svc := realSvc(pool)
	tag := "NSFS-" + time.Now().Format("150405.000000")
	var customerID, invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 1000000) RETURNING id::text`,
		tag, tag, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []string{
			`DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`,
			`DELETE FROM credit_holds WHERE customer_id = $1::uuid`,
			`DELETE FROM payment_nsf WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`,
			`DELETE FROM customer_transactions WHERE customer_id = $1::uuid`,
			`DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`,
			`DELETE FROM payments WHERE customer_id = $1::uuid`,
			`DELETE FROM invoices WHERE customer_id = $1::uuid`,
			`DELETE FROM customers WHERE id = $1::uuid`,
		} {
			if _, err := pool.Exec(c, q, customerID); err != nil {
				t.Errorf("cleanup failed on %q: %v", q, err)
			}
		}
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
		                       subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'PARTIALLY_PAID', (now() AT TIME ZONE 'America/Toronto')::date - 2,
		         (now() AT TIME ZONE 'America/Toronto')::date + 28, 100000, 0, 10000, 100000, 95000, now(), 'INVOICE') RETURNING id::text`,
		tag+"-I", customerID, branchID).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	ans, err := payments.NewRepository(pool).ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ NSFS", IdempotencyKey: tag,
		InvoiceIDs: []string{invoiceID}, AmountCents: 2000, ReceivedBy: userID,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := svc.MarkNSF(globalCtx(userID), NSFInput{PaymentID: ans.PaymentID, Reason: "cheque returned NSF"}); err != nil {
		t.Fatalf("MarkNSF: %v", err)
	}
	var bal int64
	var st string
	if err := pool.QueryRow(ctx, `SELECT balance_cents, status FROM invoices WHERE id = $1::uuid`, invoiceID).Scan(&bal, &st); err != nil {
		t.Fatalf("read invoice: %v", err)
	}
	if bal != 95000 || st != "PARTIALLY_PAID" {
		t.Errorf("after the bounce the invoice reads %d %s, want 95000 PARTIALLY_PAID", bal, st)
	}
}

// TM-2 fix round 2, P3-2: the register's discount figure nets to 0 on a reversed receipt, through
// the real reversal. (The bounced receipt is proved in the face cheque leg above.)
func TestRegisterDiscountFigure_NetsToZeroOnAReversedReceipt(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	branchID, userID := seedBranchUser(t, pool)
	tag := "RGDR-" + time.Now().Format("150405.000000")
	var customerID, invoiceID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 1000000) RETURNING id::text`,
		tag, tag, branchID).Scan(&customerID); err != nil {
		t.Fatalf("seed customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		for _, q := range []string{
			`DELETE FROM events_outbox WHERE payload->>'customer_id' = $1`,
			`DELETE FROM customer_transactions WHERE customer_id = $1::uuid`,
			`DELETE FROM payment_allocations WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`,
			`UPDATE payments SET reversed_by_payment_id = NULL, reverses_payment_id = NULL WHERE customer_id = $1::uuid`,
			`DELETE FROM payments WHERE customer_id = $1::uuid`,
			`DELETE FROM invoices WHERE customer_id = $1::uuid`,
			`DELETE FROM customers WHERE id = $1::uuid`,
		} {
			if _, err := pool.Exec(c, q, customerID); err != nil {
				t.Errorf("cleanup failed on %q: %v", q, err)
			}
		}
	})
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, discount_date, terms_code,
		                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
		 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED',
		         (now() AT TIME ZONE 'America/Toronto')::date - 2, (now() AT TIME ZONE 'America/Toronto')::date + 28,
		         (now() AT TIME ZONE 'America/Toronto')::date + 8, '2-10-NET30',
		         100000, 0, 100000, 100000, now(), 'INVOICE') RETURNING id::text`,
		tag+"-I", customerID, branchID).Scan(&invoiceID); err != nil {
		t.Fatalf("seed invoice: %v", err)
	}
	repo := payments.NewRepository(pool).WithDiscountTerms(nsfDiscountTerms{})
	ans, err := repo.ApplyReceived(ctx, payments.ApplyRequest{
		CustomerID: customerID, Method: "CHEQUE", Reference: "CHQ RGDR", IdempotencyKey: tag,
		InvoiceIDs: []string{invoiceID}, AmountCents: 98000, ReceivedBy: userID,
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	figure := func() int64 {
		n, err := billing.NewRepository(pool).DiscountCents(ctx, invoiceID)
		if err != nil {
			t.Fatalf("the register's discount figure: %v", err)
		}
		return n
	}
	if got := figure(); got != 2000 {
		t.Fatalf("the register's discount figure reads %d after the apply, want 2000", got)
	}
	if _, err := repo.ReversePayment(ctx, payments.ReverseInput{PaymentID: ans.PaymentID, Reason: "reversed", IsManager: true}, time.Now()); err != nil {
		t.Fatalf("reverse: %v", err)
	}
	if got := figure(); got != 0 {
		t.Errorf("the register's discount figure reads %d after the reversal, want 0", got)
	}
}
