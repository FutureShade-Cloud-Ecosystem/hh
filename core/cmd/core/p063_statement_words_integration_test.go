//go:build integration

// p063_statement_words_integration_test.go, 0.63: the plain statement a customer reads (the printed, emailed
// and portal documents all read billing.StatementLines) carries fixed words for every undo and credit write,
// never a clerk's free text and never an internal id. Each leg drives the REAL door with a planted reason
// carrying a marker, a figure, another invoice's number and a uuid, then reads the plain statement. A staff
// read (the customer AR read) keeps the full memo. The billing package cannot import payments or till, so the
// legs live here.
package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	staffcontext "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/order"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/payments"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pos"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/till"
	"github.com/jackc/pgx/v5/pgxpool"
)

const p063Reason = "P063LEAK keyed 77777 against INVSECRET-9 aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee by mistake"

var p063UUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

type p063World struct {
	pool       *pgxpool.Pool
	cust       string
	invA, invB string
	pay        *payments.Repository
	bill       *billing.Repository
}

func p063Exec(t *testing.T, pool *pgxpool.Pool, what, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func p063Pool(t *testing.T, suffix string) *pgxpool.Pool {
	t.Helper()
	pool := freshMigratedDB(t, suffix)
	t.Cleanup(func() {
		pool.Close()
		admin, err := pgxpool.New(context.Background(), swapDatabase(os.Getenv("DATABASE_URL"), "postgres"))
		if err != nil {
			t.Errorf("p063: connect to drop the leg database: %v", err)
			return
		}
		defer admin.Close()
		if _, err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS demoseed_it_"+suffix+" WITH (FORCE)"); err != nil {
			t.Errorf("p063: drop the leg database: %v", err)
		}
	})
	body, err := seedFS.ReadFile("dibbits_seed.sql")
	if err != nil {
		t.Fatalf("reading the embedded seed: %v", err)
	}
	p063Exec(t, pool, "apply the seed", string(body))
	p063Exec(t, pool, "plant the org", `INSERT INTO org (id, slug, name) VALUES ($1, 'p063-'||$2::text, 'P063 Org')`, cmRoundNorthOrg, suffix)
	p063Exec(t, pool, "stamp the branches", `UPDATE locations SET org_id = $1 WHERE loc_type = 'BRANCH'`, cmRoundNorthOrg)
	return pool
}

// p063Plant seeds one customer with two invoices (30000 and 20000) issued 45 days ago, as ledger rows the
// statement lists, on a database that already holds the seed.
func p063Plant(t *testing.T, pool *pgxpool.Pool) *p063World {
	t.Helper()
	w := &p063World{pool: pool, pay: payments.NewRepository(pool), bill: billing.NewRepository(pool)}
	ctx := context.Background()
	tag := fmt.Sprintf("P063-%d", time.Now().UnixNano())
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 5000000) RETURNING id::text`, tag, "P063 "+tag, trentonBranchID).Scan(&w.cust); err != nil {
		t.Fatalf("seed the customer: %v", err)
	}
	t.Cleanup(func() { w.cleanup(t) })
	var running int64
	for _, inv := range []struct {
		dst    *string
		suffix string
		total  int64
	}{{&w.invA, "-A", 30000}, {&w.invB, "-B", 20000}} {
		running += inv.total
		if err := pool.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, branch_id, org_id, status, issue_date, due_date, subtotal_cents, tax_cents,
                      total_cents, balance_cents, finalized_at, doc_type)
VALUES ($1, $2::uuid, $3::uuid, (SELECT org_id FROM locations WHERE id = $3::uuid), 'FINALIZED', current_date - 45, current_date - 15, $4, 0, $4, $4,
        now() - interval '45 days', 'INVOICE')
RETURNING id::text`, tag+inv.suffix, w.cust, trentonBranchID, inv.total).Scan(inv.dst); err != nil {
			t.Fatalf("seed invoice %s: %v", inv.suffix, err)
		}
		p063Exec(t, pool, "seed the INVOICE ledger row", `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'INVOICE', $2, $6, 'invoice', $3::uuid, $4::uuid, $5,
        (current_date - 45)::timestamp AT TIME ZONE 'America/Toronto')`,
			w.cust, inv.total, *inv.dst, trentonBranchID, tag+inv.suffix, running)
	}
	return w
}

func (w *p063World) cleanup(t *testing.T) {
	c := context.Background()
	for _, q := range []struct{ table, sql string }{
		{"events_outbox", `DELETE FROM events_outbox WHERE payload->>'payment_id' IN (SELECT id::text FROM payments WHERE customer_id = $1::uuid)`},
		{"payment_reapplications", `DELETE FROM payment_reapplications WHERE customer_id = $1::uuid`},
		{"payment_allocations", `DELETE FROM payment_allocations WHERE kind = 'REVERSAL' AND payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
		{"payment_allocations", `DELETE FROM payment_allocations WHERE payment_id IN (SELECT id FROM payments WHERE customer_id = $1::uuid)`},
		{"payments", `UPDATE payments SET reversed_by_payment_id = NULL WHERE customer_id = $1::uuid AND reversed_by_payment_id IS NOT NULL`},
		{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid AND reverses_payment_id IS NOT NULL`},
		{"payments", `DELETE FROM payments WHERE customer_id = $1::uuid`},
		{"customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`},
		{"credit_application_keys", `DELETE FROM credit_application_keys WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`},
		{"invoice_lines", `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`},
		{"invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`},
		{"customers", `DELETE FROM customers WHERE id = $1::uuid`},
	} {
		if _, err := w.pool.Exec(c, q.sql, w.cust); err != nil {
			t.Errorf("cleanup %s: %v", q.table, err)
		}
	}
}

func (w *p063World) receipt(t *testing.T) payments.Payment {
	t.Helper()
	p, err := w.pay.Pay(context.Background(), w.cust, "CHEQUE", "",
		[]payments.Allocation{{InvoiceID: w.invA, AmountCents: 15000}, {InvoiceID: w.invB, AmountCents: 5000}})
	if err != nil {
		t.Fatalf("the receipt door: %v", err)
	}
	return p
}

// lines reads the plain statement, fails if any line leaks, and returns the lines. It also ties the closing
// figure to the customer's open invoice balances, so a change of words never moves a figure.
func (w *p063World) lines(t *testing.T, label string, fees ...int64) []billing.StatementLine {
	t.Helper()
	ctx := context.Background()
	ls, err := w.bill.StatementLines(ctx, w.cust, time.Now().Add(time.Hour), billing.BranchReach{Global: true})
	if err != nil {
		t.Fatalf("%s: statement lines: %v", label, err)
	}
	for _, l := range ls {
		for _, bad := range []string{"P063LEAK", "77777", "INVSECRET", "keyed", "mistake", "NSF reversal", "voided receipt", "reversal:", "write off", "re-applied", "credit applied to", "credit memo applied", "credit application reversed", "credit memo restored"} {
			if strings.Contains(l.Description, bad) || strings.Contains(l.DocNo, "INVSECRET") {
				t.Errorf("%s: a plain statement line carries %q: %q (doc %q)", label, bad, l.Description, l.DocNo)
			}
		}
		if p063UUID.MatchString(l.Description) {
			t.Errorf("%s: a plain statement line carries a uuid: %q", label, l.Description)
		}
		if strings.HasPrefix(l.Description, "Payment received") && l.ChargeCents > 0 {
			t.Errorf("%s: a charge line fell back to the receipt words: %q", label, l.Description)
		}
	}
	var open int64
	if err := w.pool.QueryRow(ctx, `SELECT COALESCE(SUM(balance_cents),0) FROM invoices WHERE customer_id = $1::uuid`, w.cust).Scan(&open); err != nil {
		t.Fatalf("%s: open balances: %v", label, err)
	}
	for _, f := range fees { // an NSF fee is a ledger charge that is not an invoice's balance
		open += f
	}
	if len(ls) == 0 || ls[len(ls)-1].BalanceRun != open {
		t.Errorf("%s: closing %v does not tie to the open invoice balances %d", label, ls, open)
	}
	return ls
}

func p063Has(t *testing.T, label string, ls []billing.StatementLine, want string) {
	t.Helper()
	for _, l := range ls {
		if l.Description == want {
			return
		}
	}
	t.Errorf("%s: no line reads %q in %+v", label, want, ls)
}

func p063Count(t *testing.T, label string, ls []billing.StatementLine, want string, n int) {
	t.Helper()
	got := 0
	for _, l := range ls {
		if l.Description == want {
			got++
		}
	}
	if got != n {
		t.Errorf("%s: %d line(s) read %q, want %d in %+v", label, got, want, n, ls)
	}
}

// p063NoChargeReads fails if a charge line (money owed to us) reads the given words.
func p063NoChargeReads(t *testing.T, label string, ls []billing.StatementLine, words string) {
	t.Helper()
	for _, l := range ls {
		if l.ChargeCents > 0 && l.Description == words {
			t.Errorf("%s: a charge line reads %q: %+v", label, words, l)
		}
	}
}

// staffMemo is what the customer AR read (staff) shows for the customer: the full memo survives.
func (w *p063World) staffKeepsReason(t *testing.T, label string) {
	t.Helper()
	var n int
	if err := w.pool.QueryRow(context.Background(),
		`SELECT (SELECT count(*) FROM customer_transactions WHERE customer_id = $1::uuid AND memo LIKE '%P063LEAK%')
		      + (SELECT count(*) FROM payment_nsf WHERE customer_id = $1::uuid AND reason LIKE '%P063LEAK%')`, w.cust).Scan(&n); err != nil {
		t.Fatalf("%s: read the ledger: %v", label, err)
	}
	if n == 0 {
		t.Errorf("%s: the ledger (or the NSF record) no longer keeps the clerk's reason", label)
	}
}

func (w *p063World) bounce(t *testing.T, payID string) {
	t.Helper()
	var userID string
	if err := w.pool.QueryRow(context.Background(), `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("pick a seeded user: %v", err)
	}
	ctx := staffcontext.ContextWithStaff(context.Background(), staffcontext.StaffContext{
		UserID: userID, Tier: staffcontext.TierManager, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE",
	})
	if _, err := till.NewService(till.NewRepository(w.pool)).MarkNSF(ctx, till.NSFInput{PaymentID: payID, Reason: p063Reason}); err != nil {
		t.Fatalf("the NSF door: %v", err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM credit_holds WHERE source_payment_id = $1::uuid`,
			`DELETE FROM payment_nsf WHERE payment_id = $1::uuid`,
		} {
			if _, err := w.pool.Exec(context.Background(), q, payID); err != nil {
				t.Errorf("cleanup of the bounce: %v", err)
			}
		}
	})
}

func (w *p063World) memo(t *testing.T, invoiceID string, cents int64, key string) string {
	t.Helper()
	svc, _ := cmRoundService(t, w.pool)
	var userID string
	if err := w.pool.QueryRow(context.Background(), `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
		t.Fatalf("pick a seeded user: %v", err)
	}
	res, err := svc.Issue(staffcontext.ContextWithStaffUserID(context.Background(), userID), billing.CreditMemoRouteRequest{
		CustomerID: w.cust, BranchID: trentonBranchID, InvoiceID: invoiceID, AmountCents: cents,
		Reason: p063Reason, IdempotencyKey: key,
	}, billing.CreditMemoCaller{OrgID: cmRoundNorthOrg, Scope: staffcontext.ScopeGlobal})
	if err != nil {
		t.Fatalf("the credit memo door: %v", err)
	}
	t.Cleanup(func() {
		p063Exec(t, w.pool, "memo journals", `DELETE FROM gl_journals WHERE idempotency_key = $1`, billing.CreditMemoJournalKeyPrefix+res.CreditMemoID)
	})
	return res.CreditMemoID
}

// p064Order plants one CONFIRMED order for the world's customer on the Trenton branch, its number
// minted by the house's own branch numbering (next_order_no, migration 0087, the form the writer's
// order memos carry), with n lines of two units at 2000, fully fulfilled. It answers the order's id
// and number; p064DropOrder deletes what this planted.
func (w *p063World) p064OrderN(t *testing.T, n int) (id, no string) {
	t.Helper()
	if err := w.pool.QueryRow(context.Background(), `
INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
VALUES (next_order_no($2::uuid), $1::uuid, $2::uuid, 'CONFIRMED', 'PICKUP', $3, $3)
RETURNING id::text, order_no::text`, w.cust, trentonBranchID, 2000*n).Scan(&id, &no); err != nil {
		t.Fatalf("seed the order: %v", err)
	}
	if _, err := w.pool.Exec(context.Background(), `
INSERT INTO order_lines (order_id, line_no, description, qty_entered, uom_entered, qty_base,
                         unit_price_cents, price_uom, line_total_cents, fulfilled_base)
SELECT $1::uuid, k, 'P064 order line', 2, 'EA', 2, 2000, 'EA', 2000, 2
  FROM generate_series(1, $2::int) k`, id, n); err != nil {
		t.Fatalf("seed the order lines: %v", err)
	}
	return id, no
}

func (w *p063World) p064Order(t *testing.T) (id, no string) { return w.p064OrderN(t, 1) }

// p064Groups plants one COMPLETE shipment group per order line, each taking its line's whole
// quantity, in ONE statement so the group partition trigger (0126) holds at its commit; it answers
// the first group's id.
func (w *p063World) p064Groups(t *testing.T, ord string) string {
	t.Helper()
	var g1 string
	if err := w.pool.QueryRow(context.Background(), `
WITH grp AS (
  INSERT INTO shipment_groups (order_id, group_no, status, completed_at)
  SELECT $1::uuid, k, 'COMPLETE', now()
    FROM generate_series(1, (SELECT count(*) FROM order_lines WHERE order_id = $1::uuid)::int) k
  RETURNING id, group_no),
ins AS (
  INSERT INTO shipment_group_lines (shipment_group_id, order_line_id, qty_base)
  SELECT grp.id, ol.id, ol.qty_base FROM grp
    JOIN order_lines ol ON ol.order_id = $1::uuid AND ol.line_no = grp.group_no
  RETURNING 1)
SELECT (SELECT id::text FROM grp WHERE group_no = 1), (SELECT count(*) FROM ins)`, ord).Scan(&g1, new(int)); err != nil {
		t.Fatalf("seed the shipment groups: %v", err)
	}
	return g1
}

// p064DropOrder deletes one planted order and its children, and the outbox events the order's cancel
// wrote. The groups go first, their cascade emptying the group lines while the order still shows no
// groups: the partition trigger (0126) short-circuits on an order with no groups, so deleting the
// lines one group at a time would refuse. Every delete fails loudly, naming the table.
func (w *p063World) p064DropOrder(t *testing.T, ord string) {
	t.Helper()
	c := context.Background()
	for _, q := range []struct{ table, sql string }{
		{"events_outbox", `DELETE FROM events_outbox WHERE payload->>'order_id' = $1`},
		{"events_outbox", `DELETE FROM events_outbox WHERE payload->>'invoice_id' IN (SELECT id::text FROM invoices WHERE customer_id = $1::uuid)`},
		{"shipment_groups", `DELETE FROM shipment_groups WHERE order_id = $1::uuid`},
		{"order_lines", `DELETE FROM order_lines WHERE order_id = $1::uuid`},
		{"orders", `DELETE FROM orders WHERE id = $1::uuid`},
	} {
		if _, err := w.pool.Exec(c, q.sql, ord); err != nil {
			t.Errorf("cleanup %s: %v", q.table, err)
		}
	}
}

// p064LineFor answers the one statement line whose document number is docNo.
func p064LineFor(t *testing.T, label, docNo string, ls []billing.StatementLine) billing.StatementLine {
	t.Helper()
	for _, l := range ls {
		if l.DocNo == docNo {
			return l
		}
	}
	t.Fatalf("%s: no statement line for document %q in %+v", label, docNo, ls)
	return billing.StatementLine{}
}

func TestIntegration_P063_PlainStatementWords(t *testing.T) {
	pool := p063Pool(t, "p063words")
	ctx := context.Background()

	t.Run("Void", func(t *testing.T) {
		w := p063Plant(t, pool)
		p := w.receipt(t)
		if _, err := w.pay.VoidPayment(ctx, payments.VoidInput{PaymentID: p.ID, Reason: p063Reason, IsManager: true}, time.Now(), houseday.DefaultZone()); err != nil {
			t.Fatalf("the void door: %v", err)
		}
		ls := w.lines(t, "void")
		p063Has(t, "void", ls, "Payment voided")
		w.staffKeepsReason(t, "void")
	})

	t.Run("ReverseThenReapply", func(t *testing.T) {
		w := p063Plant(t, pool)
		p := w.receipt(t)
		if _, err := w.pay.ReversePayment(ctx, payments.ReverseInput{PaymentID: p.ID, Reason: p063Reason, IsManager: true}, time.Now()); err != nil {
			t.Fatalf("the reversal door: %v", err)
		}
		ls := w.lines(t, "reversal")
		p063Has(t, "reversal", ls, "Payment reversed")
		if _, err := w.pay.ReapplyHeld(ctx, payments.ReapplyInput{
			PaymentID: p.ID, Reason: p063Reason, IdempotencyKey: "p063-reapply-" + w.cust, IsManager: true,
			Allocations: []payments.Allocation{{InvoiceID: w.invA, AmountCents: 15000}},
		}, time.Now()); err != nil {
			t.Fatalf("the reapply door: %v", err)
		}
		ls = w.lines(t, "reapply")
		p063Has(t, "reapply", ls, "Payment applied")
		w.staffKeepsReason(t, "reversal")
	})

	t.Run("NSF", func(t *testing.T) {
		w := p063Plant(t, pool)
		p := w.receipt(t)
		w.bounce(t, p.ID)
		ls := w.lines(t, "nsf", 4500)
		p063Has(t, "nsf", ls, "Returned payment")
		p063Has(t, "nsf", ls, "NSF fee")
		w.staffKeepsReason(t, "nsf")
	})

	t.Run("CreditApplyAndReverse", func(t *testing.T) {
		w := p063Plant(t, pool)
		memoID := w.memo(t, w.invA, 4000, "p063-memo-"+w.cust)
		ls := w.lines(t, "credit memo")
		// The memo's line is its own number and the reversed invoice's number, both read by id.
		var cmNo, invANo string
		if err := pool.QueryRow(ctx, `SELECT invoice_no FROM invoices WHERE id = $1::uuid`, memoID).Scan(&cmNo); err != nil {
			t.Fatalf("read the credit memo's number: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT invoice_no FROM invoices WHERE id = $1::uuid`, w.invA).Scan(&invANo); err != nil {
			t.Fatalf("read the reversed invoice's number: %v", err)
		}
		p063Has(t, "credit memo", ls, cmNo+", reverses "+invANo)
		app, err := w.bill.ApplyCredit(ctx, billing.ApplyCreditInput{
			CustomerID: w.cust, CreditKind: billing.CreditKindMemo, CreditID: memoID, InvoiceID: w.invB,
			AmountCents: 4000, Reason: p063Reason, IdempotencyKey: "p063-apply-" + w.cust, OrgID: cmRoundNorthOrg,
		}, time.Now())
		if err != nil {
			t.Fatalf("the credit apply door: %v", err)
		}
		ls = w.lines(t, "credit applied")
		p063Has(t, "credit applied", ls, "Credit applied")
		p063Has(t, "credit applied", ls, "Credit memo applied")
		if _, err := w.bill.ReverseCreditApplication(ctx, billing.ReverseCreditApplicationInput{
			InvoiceID: w.invB, ApplicationID: app.ApplicationID, Reason: p063Reason, OrgID: cmRoundNorthOrg,
		}, time.Now()); err != nil {
			t.Fatalf("the credit reverse door: %v", err)
		}
		ls = w.lines(t, "credit reversed")
		p063Has(t, "credit reversed", ls, "Credit application reversed")
		p063Has(t, "credit reversed", ls, "Credit memo restored")
		w.staffKeepsReason(t, "credit")
	})

	// A credit the account holds (a counter return, a receipt's change, a cancelled deposit) is applied and
	// reversed through the real doors: its offsetting row copies the credit's own type, so a counter return
	// credit (a CREDIT_MEMO row) must still read in the application's words, a charge line among them.
	for _, fam := range []struct{ source, txn, memo string }{
		{"pos_return", "CREDIT_MEMO", ""},
		{"receipt_change", "ADJUSTMENT", "overpaid receipt left on account (4242)"},
		{"deposit_cancel", "ADJUSTMENT", "Supplier deposit #INV-4242 credited back on the order's cancel"},
	} {
		fam := fam
		t.Run("AccountCreditApplyAndReverse_"+fam.source, func(t *testing.T) {
			w := p063Plant(t, pool)
			var creditID string
			if err := pool.QueryRow(ctx, `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
VALUES ($1::uuid, $2, -4000, 46000, $3, gen_random_uuid(), $4::uuid, $5)
RETURNING id::text`, w.cust, fam.txn, fam.source, trentonBranchID, fam.memo).Scan(&creditID); err != nil {
				t.Fatalf("seed the account credit: %v", err)
			}
			app, err := w.bill.ApplyCredit(ctx, billing.ApplyCreditInput{
				CustomerID: w.cust, CreditKind: billing.CreditKindAccount, CreditID: creditID, InvoiceID: w.invB,
				AmountCents: 4000, Reason: p063Reason, IdempotencyKey: "p063-acct-" + fam.source + "-" + w.cust, OrgID: cmRoundNorthOrg,
			}, time.Now())
			if err != nil {
				t.Fatalf("the credit apply door: %v", err)
			}
			ls := w.lines(t, fam.source+" applied")
			p063Count(t, fam.source+" applied", ls, "Credit applied", 2)
			p063NoChargeReads(t, fam.source+" applied", ls, "Credit memo")
			if _, err := w.bill.ReverseCreditApplication(ctx, billing.ReverseCreditApplicationInput{
				InvoiceID: w.invB, ApplicationID: app.ApplicationID, Reason: p063Reason, OrgID: cmRoundNorthOrg,
			}, time.Now()); err != nil {
				t.Fatalf("the credit reverse door: %v", err)
			}
			ls = w.lines(t, fam.source+" reversed", -4000) // the credit is open again
			p063Count(t, fam.source+" reversed", ls, "Credit application reversed", 2)
			p063NoChargeReads(t, fam.source+" reversed", ls, "Credit memo")
			w.staffKeepsReason(t, fam.source)
		})
	}

	t.Run("WriteOffAndReverse", func(t *testing.T) {
		w := p063Plant(t, pool)
		res, err := w.bill.WriteOffInvoice(ctx, billing.WriteOffInput{
			InvoiceID: w.invA, Reason: p063Reason, IdempotencyKey: "p063-wo-" + w.cust, OrgID: cmRoundNorthOrg, IsController: true,
		}, time.Now())
		if err != nil {
			t.Fatalf("the write off door: %v", err)
		}
		ls := w.lines(t, "write off")
		p063Has(t, "write off", ls, "Write-off")
		if _, err := w.bill.ReverseWriteOff(ctx, billing.ReverseWriteOffInput{
			InvoiceID: w.invA, WriteOffID: res.WriteOffID, Reason: p063Reason, OrgID: cmRoundNorthOrg,
		}, time.Now()); err != nil {
			t.Fatalf("the write off reversal door: %v", err)
		}
		ls = w.lines(t, "write off reversed")
		p063Has(t, "write off reversed", ls, "Write-off reversed")
		w.staffKeepsReason(t, "write off")
	})

	// A writer the statement has never heard of reads as fixed words, never as its memo.
	t.Run("AnUnknownMemoReadsAsFixedWords", func(t *testing.T) {
		w := p063Plant(t, pool)
		for _, txn := range []string{"ADJUSTMENT", "PAYMENT", "WRITE_OFF", "NSF", "CREDIT_MEMO", "INVOICE"} {
			p063Exec(t, pool, "plant a ledger row with an unknown memo", `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type, source_ref_id, branch_id, memo)
VALUES ($1::uuid, $2, 0, 0, 'brand_new_writer', $3::uuid, $4::uuid, $5)`,
				w.cust, txn, w.invA, trentonBranchID, "a future writer: "+p063Reason)
		}
		ls := w.lines(t, "unknown memo")
		for _, want := range []string{"Adjustment", "Payment received, thank you", "Write-off", "Returned payment", "Credit memo", "Invoice"} {
			p063Has(t, "unknown memo", ls, want)
		}
		w.staffKeepsReason(t, "unknown memo")
	})

	// 0.64 N1: the invoice writer's own order memos (`#INV-n, order #SO-x-n` and the progress shape)
	// pass only when the order number in them is the invoice's OWN order's number, read by id through
	// the order module's seam; a clerk's crafted memo in the same shape, or any other token in the
	// slot, reads "Invoice". The writer's whole order invoice, raised with no memo, still reads its
	// own words.
	t.Run("OrderInvoiceOwnOrder", func(t *testing.T) {
		w := p063Plant(t, pool)
		ctx := context.Background()
		ord, ordNo := w.p064Order(t)
		t.Cleanup(func() { w.p064DropOrder(t, ord) })
		tail := newInvoiceWriter(pool).WithOrderSource(w6cOrderInvoiceSource{svc: order.NewService(order.NewRepository(pool))})
		doc, err := tail.InvoiceOrder(ctx, ord)
		if err != nil {
			t.Fatalf("the invoice writer: %v", err)
		}
		// The statement reads through the order module's own number seam, wired as the composition
		// root wires it.
		bill := billing.NewRepository(pool).WithOrderNumbers(order.NewRepository(pool))
		ls, err := bill.StatementLines(ctx, w.cust, time.Now().Add(time.Hour), billing.BranchReach{Global: true})
		if err != nil {
			t.Fatalf("order invoice: statement lines: %v", err)
		}
		for _, l := range ls {
			for _, bad := range []string{"P063LEAK", "INVSECRET"} {
				if strings.Contains(l.Description, bad) {
					t.Errorf("order invoice: a plain statement line carries %q: %q", bad, l.Description)
				}
			}
		}
		if l := p064LineFor(t, "order invoice", doc.InvoiceNo, ls); l.Description != doc.InvoiceNo+", order "+ordNo {
			t.Errorf("the writer's own order line reads %q, want %q", l.Description, doc.InvoiceNo+", order "+ordNo)
		}
		// The clerk's crafted memo in the writer's shape, on an invoice of the same account that has
		// no order, must read fixed words: nothing in the slot the writer did not mint.
		var invANo string
		if err := pool.QueryRow(ctx, `SELECT invoice_no::text FROM invoices WHERE id = $1::uuid`, w.invA).Scan(&invANo); err != nil {
			t.Fatalf("read invoice A's number: %v", err)
		}
		p063Exec(t, pool, "craft the clerk's order memo", `
UPDATE customer_transactions SET memo = '#INV-1, order #SO-P063LEAK-1'
 WHERE customer_id = $1::uuid AND txn_type = 'INVOICE' AND source_ref_id = $2::uuid`, w.cust, w.invA)
		ls, err = bill.StatementLines(ctx, w.cust, time.Now().Add(time.Hour), billing.BranchReach{Global: true})
		if err != nil {
			t.Fatalf("crafted memo: statement lines: %v", err)
		}
		for _, l := range ls {
			if strings.Contains(l.Description, "P063LEAK") {
				t.Errorf("crafted memo: a plain statement line carries P063LEAK: %q", l.Description)
			}
		}
		if l := p064LineFor(t, "crafted memo", invANo, ls); l.Description != "Invoice" {
			t.Errorf("the crafted order memo reads %q, want Invoice", l.Description)
		}
	})

	// 0.64 N2: the credit memo line's "reverses" half is the join's read of the reversed invoice, and
	// that join is filtered to the account's own customer. A planted memo whose reverses_invoice_id
	// names ANOTHER customer's invoice (the only writer refuses, credit_memo.go, so this row is the
	// only way the filter can be reached) reads the memo's own number alone, never a number of that
	// other invoice.
	t.Run("CreditMemoReversesSameCustomerOnly", func(t *testing.T) {
		w := p063Plant(t, pool)
		ctx := context.Background()
		tag := fmt.Sprintf("P064N2-%d", time.Now().UnixNano())
		var otherCust, otherInv, otherNo string
		if err := pool.QueryRow(ctx,
			`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
			 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 5000000) RETURNING id::text`, tag, "P064 "+tag, trentonBranchID).Scan(&otherCust); err != nil {
			t.Fatalf("seed the other customer: %v", err)
		}
		t.Cleanup(func() {
			for _, q := range []struct{ table, sql string }{
				{"customer_transactions", `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`},
				{"invoice_lines", `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1::uuid)`},
				{"invoices", `DELETE FROM invoices WHERE customer_id = $1::uuid`},
				{"customers", `DELETE FROM customers WHERE id = $1::uuid`},
			} {
				if _, err := pool.Exec(context.Background(), q.sql, otherCust); err != nil {
					t.Errorf("cleanup %s: %v", q.table, err)
				}
			}
		})
		if err := pool.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, branch_id, org_id, status, issue_date, due_date, subtotal_cents,
                      tax_cents, total_cents, balance_cents, finalized_at, doc_type)
VALUES ($1, $2::uuid, $3::uuid, (SELECT org_id FROM locations WHERE id = $3::uuid), 'FINALIZED',
        current_date - 45, current_date - 15, 12000, 0, 12000, 12000, now() - interval '45 days', 'INVOICE')
RETURNING id::text, invoice_no::text`, tag+"-OTHER", otherCust, trentonBranchID).Scan(&otherInv, &otherNo); err != nil {
			t.Fatalf("seed the other customer's invoice: %v", err)
		}
		var cmID, cmNo string
		if err := pool.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, branch_id, org_id, status, issue_date, due_date, subtotal_cents,
                      tax_cents, total_cents, balance_cents, finalized_at, doc_type, reverses_invoice_id)
VALUES ('#CM-' || nextval('invoice_no_seq'), $1::uuid, $2::uuid, (SELECT org_id FROM locations WHERE id = $2::uuid),
        'FINALIZED', current_date, current_date, -4000, 0, -4000, -4000, now(), 'CREDIT_MEMO', $3::uuid)
RETURNING id::text, invoice_no::text`, w.cust, trentonBranchID, otherInv).Scan(&cmID, &cmNo); err != nil {
			t.Fatalf("plant the credit memo: %v", err)
		}
		p063Exec(t, pool, "plant the credit memo's ledger row", `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'CREDIT_MEMO', -4000, 46000, 'invoice', $2::uuid, $3::uuid, '',
        (current_date - 40)::timestamp AT TIME ZONE 'America/Toronto')`, w.cust, cmID, trentonBranchID)
		ls := w.lines(t, "cross customer memo")
		if l := p064LineFor(t, "cross customer memo", cmNo, ls); l.Description != cmNo {
			t.Errorf("the cross customer memo line reads %q, want %q alone", l.Description, cmNo)
		}
		for _, l := range ls {
			if strings.Contains(l.Description, otherNo) {
				t.Errorf("the statement carries the other customer's invoice number %q: %q", otherNo, l.Description)
			}
		}
	})

	// 0.64 N3: the restored words' door legs. Each leg drives the real write door (never a planted
	// row) and reads the plain statement: the line reads the door's own words.
	//
	// The order cancel door with a paid supplier deposit (CREDIT_BACK): the closed part and the
	// credited back part are two lines, each in the settlement's own words.
	t.Run("RestoredWords_DepositCancelDoor", func(t *testing.T) {
		w := p063Plant(t, pool)
		ctx := context.Background()
		ord, _ := w.p064Order(t)
		t.Cleanup(func() { w.p064DropOrder(t, ord) })
		var dep, depNo string
		if err := pool.QueryRow(ctx, `
INSERT INTO invoices (invoice_no, customer_id, order_id, branch_id, org_id, status, issue_date, due_date,
                      subtotal_cents, tax_cents, total_cents, balance_cents, source_kind, finalized_at, doc_type)
VALUES ('#INV-' || nextval('invoice_no_seq'), $1::uuid, $2::uuid, $3::uuid,
        (SELECT org_id FROM locations WHERE id = $3::uuid), 'FINALIZED', current_date, current_date,
        4000, 0, 4000, 4000, 'SUPPLIER_DEPOSIT', now(), 'INVOICE')
RETURNING id::text, invoice_no::text`, w.cust, ord, trentonBranchID).Scan(&dep, &depNo); err != nil {
			t.Fatalf("plant the deposit document: %v", err)
		}
		// The INVOICE row the deposit's finalize writes, the charge that puts the deposit on the
		// account (the same plant internal/order's deposit cancel legs use).
		p063Exec(t, pool, "plant the deposit's finalize ledger row", `
INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after, source_type,
                                   source_ref_id, branch_id, memo, occurred_at)
VALUES ($1::uuid, 'INVOICE', 4000, 54000, 'invoice', $2::uuid, $3::uuid, 'Supplier deposit', clock_timestamp())`,
			w.cust, dep, trentonBranchID)
		if _, err := payments.NewRepository(pool).ApplyReceived(ctx, payments.ApplyRequest{
			CustomerID: w.cust, Method: "CHEQUE", Reference: "p064-dep",
			IdempotencyKey: "p064-dep-" + w.cust, InvoiceIDs: []string{dep}, AmountCents: 2500,
		}); err != nil {
			t.Fatalf("the received payment door: %v", err)
		}
		var userID string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM users ORDER BY id LIMIT 1`).Scan(&userID); err != nil {
			t.Fatalf("pick a seeded user: %v", err)
		}
		mctx := staffcontext.ContextWithStaff(ctx, staffcontext.StaffContext{
			UserID: userID, Tier: staffcontext.TierManager, Scope: staffcontext.ScopeGlobal, Status: "ACTIVE",
		})
		orderSvc := order.NewService(order.NewRepository(pool)).
			WithCancelGuards(billing.NewRepository(pool), pos.NewRepository(pool)).
			WithDepositCanceller(billing.NewInvoiceWriter(pool))
		if _, err := orderSvc.CancelOrderManual(mctx, ord, "customer walked away", false); err != nil {
			t.Fatalf("the order cancel door: %v", err)
		}
		// The credit back leaves the paid part on the account: an open credit the statement's closing
		// nets (the aging's own figure), not an invoice balance.
		ls := w.lines(t, "deposit cancel", -2500)
		p063Has(t, "deposit cancel", ls, "Supplier deposit "+depNo+" closed on the order's cancel")
		p063Has(t, "deposit cancel", ls, "Supplier deposit "+depNo+" credited back on the order's cancel")
	})

	// The payments settle door with an NSF fee allocation: after the bounce, the fee the policy
	// charges is settled by a received payment naming it, and the row that settle writes reads its
	// own words.
	t.Run("RestoredWords_NSFFeePaymentDoor", func(t *testing.T) {
		w := p063Plant(t, pool)
		p := w.receipt(t)
		w.bounce(t, p.ID)
		var nsfID string
		if err := pool.QueryRow(context.Background(),
			`SELECT id::text FROM payment_nsf WHERE payment_id = $1::uuid ORDER BY created_at DESC LIMIT 1`, p.ID).Scan(&nsfID); err != nil {
			t.Fatalf("read the bounce's fee: %v", err)
		}
		if _, err := w.pay.ApplyReceived(context.Background(), payments.ApplyRequest{
			CustomerID: w.cust, Method: "CHEQUE", Reference: "p064-fee",
			IdempotencyKey: "p064-fee-" + w.cust, InvoiceIDs: []string{nsfID}, AmountCents: 4500,
		}); err != nil {
			t.Fatalf("the payments settle door: %v", err)
		}
		// The fee payment settles the fee the bounce charged, so the account's net non invoice
		// charge is zero and the closing ties to the invoice balances alone.
		ls := w.lines(t, "nsf fee payment")
		p063Has(t, "nsf fee payment", ls, "Returned payment")
		p063Has(t, "nsf fee payment", ls, "NSF fee payment")
	})

	// The invoice writer with an order's number and no memo: the whole order's document and a
	// progress invoice each read their own shape, on a branch coded order number.
	t.Run("RestoredWords_OrderInvoiceDoor", func(t *testing.T) {
		w := p063Plant(t, pool)
		ctx := context.Background()
		bill := billing.NewRepository(pool).WithOrderNumbers(order.NewRepository(pool))
		tail := newInvoiceWriter(pool).WithOrderSource(w6cOrderInvoiceSource{svc: order.NewService(order.NewRepository(pool))})
		ord1, ord1No := w.p064Order(t)
		t.Cleanup(func() { w.p064DropOrder(t, ord1) })
		doc1, err := tail.InvoiceOrder(ctx, ord1)
		if err != nil {
			t.Fatalf("the whole order invoice door: %v", err)
		}
		ord2, ord2No := w.p064OrderN(t, 2)
		t.Cleanup(func() { w.p064DropOrder(t, ord2) })
		g1 := w.p064Groups(t, ord2)
		doc2, err := tail.InvoiceOrderGroups(ctx, ord2, []string{g1}, "ON_DEMAND_SUBSET")
		if err != nil {
			t.Fatalf("the progress invoice door: %v", err)
		}
		ls, err := bill.StatementLines(ctx, w.cust, time.Now().Add(time.Hour), billing.BranchReach{Global: true})
		if err != nil {
			t.Fatalf("order invoice words: statement lines: %v", err)
		}
		if l := p064LineFor(t, "whole order", doc1.InvoiceNo, ls); l.Description != doc1.InvoiceNo+", order "+ord1No {
			t.Errorf("the whole order line reads %q, want %q", l.Description, doc1.InvoiceNo+", order "+ord1No)
		}
		progress := doc2.InvoiceNo + ", progress invoice for order " + ord2No + " (1 shipment group(s))"
		if l := p064LineFor(t, "progress", doc2.InvoiceNo, ls); l.Description != progress {
			t.Errorf("the progress line reads %q, want %q", l.Description, progress)
		}
	})
}
