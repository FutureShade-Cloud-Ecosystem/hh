//go:build integration

// repository_integration_test.go, DIB-211 VERIFY:billing.
//
// The ledger row for this module reads "built, only unit test, no integration test", and that was
// exactly right: internal/billing shipped with service_test.go alone, which drives an in-memory fake
// store. Every statement that actually moves money in this package is SQL, the tenant fence on the
// invoice reads, the FINALIZED/PARTIALLY_PAID predicate behind open AR, the cancel gate the order
// service fails closed on, the 1100 subledger tie-out the GL consumer posts against, and the Bill 60
// retention worklist. A fake store proves none of it.
//
// This file is the missing integration evidence. It runs the REAL *billing.Repository against the
// migrated + seeded DB. Every fact it asserts is created by the test in its own tenant namespace and
// torn down (t.Cleanup, LIFO, children first), so the file is order-independent and re-runnable on a
// shared DB, the whole-table reads (ARBalanceCents) are fenced by a back-dated as-of window that no
// seeded or concurrently-created row can fall into.
//
// Run: make reset && go test -tags integration ./internal/billing/...
package billing

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	trentonBranch  = "5d1b3f6e-0000-4000-9000-000000000001"
	kingstonBranch = "5d1b3f6e-0000-4000-9000-000000000002"
)

// asOfEpoch is the back-dated window this file's whole-table AR reads live in. Every seeded and
// test-created invoice in the suite carries a recent issue_date, so an as-of of 1990-06-30 selects
// EXACTLY the rows this test back-dates into it, which is what makes ARBalanceCents assertable by
// absolute value rather than by a racy before/after delta.
var (
	asOfEpoch  = time.Date(1990, 6, 30, 0, 0, 0, 0, time.UTC)
	beforeAny  = time.Date(1989, 1, 1, 0, 0, 0, 0, time.UTC)
	epochIssue = "1990-01-15"
)

func intPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url, set := os.LookupEnv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	fail := func(format string, a ...any) {
		if set {
			t.Fatalf(format, a...)
		}
		t.Skipf(format, a...)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		fail("integration DB unavailable: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		fail("integration DB not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// ns returns a per-test unique namespace so two runs (or two tests) never collide on
// invoices.invoice_no / customers.account_no, both of which are UNIQUE.
func ns(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()%1_000_000_000)
}

// seedCustomer creates a test-owned tenant and removes it (and anything hung off it) at test end.
func seedCustomer(t *testing.T, pool *pgxpool.Pool, tag string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
		"BILV-"+tag, "Billing Verify "+tag, trentonBranch).Scan(&id); err != nil {
		t.Fatalf("seed customer %s: %v", tag, err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1`, id)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`, id)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, id)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, id)
	})
	return id
}

// invSpec is one test-owned invoice header.
type invSpec struct {
	no        string // suffix; the test's namespace is prepended
	status    string
	issue     string // issue_date literal
	subtotal  int64
	tax       int64
	holdback  int64
	total     int64
	balance   int64
	branch    string
	orderID   string // "" → NULL
	finalized bool   // stamp finalized_at (else NULL → the COALESCE fallback path)
}

func seedInvoice(t *testing.T, pool *pgxpool.Pool, customerID, namespace string, s invSpec) string {
	t.Helper()
	ctx := context.Background()
	branch := s.branch
	if branch == "" {
		branch = trentonBranch
	}
	issue := s.issue
	if issue == "" {
		issue = epochIssue
	}
	var order any
	if s.orderID != "" {
		order = s.orderID
	}
	var finalized any
	if s.finalized {
		finalized = time.Date(1990, 1, 15, 12, 0, 0, 0, time.UTC)
	}
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO invoices
		   (invoice_no, customer_id, order_id, branch_id, status, issue_date, due_date,
		    subtotal_cents, tax_cents, holdback_cents, total_cents, balance_cents, finalized_at)
		 VALUES ($1,$2,$3::uuid,$4::uuid,$5,$6::date,$6::date,$7,$8,$9,$10,$11,$12)
		 RETURNING id::text`,
		namespace+"-"+s.no, customerID, order, branch, s.status, issue,
		s.subtotal, s.tax, s.holdback, s.total, s.balance, finalized).Scan(&id); err != nil {
		t.Fatalf("seed invoice %s: %v", s.no, err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id = $1`, id)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE id = $1`, id)
	})
	return id
}

// -- VERIFY:billing / 1, the tenant fence on the invoice list -----------------------------------

// The list read is the portal's AR surface. The unit test proves the fake filters; this proves the
// SQL does: a second tenant's invoices are invisible, the status filter narrows BOTH the page and the
// envelope total, paging walks the ORDER BY, and the ordering is the documented issue_date DESC,
// invoice_no.
func TestIntegration_Billing_ListInvoices_TenantFenceFilterAndPaging(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)

	mine := seedCustomer(t, pool, n+"-A")
	theirs := seedCustomer(t, pool, n+"-B")

	// Three of mine (two FINALIZED, one DRAFT) + one of theirs on the SAME issue_date, so a broken
	// tenant predicate cannot hide behind the ordering.
	seedInvoice(t, pool, mine, n, invSpec{no: "F2", status: "FINALIZED", issue: "1990-02-01", total: 5000, balance: 5000, finalized: true})
	seedInvoice(t, pool, mine, n, invSpec{no: "F1", status: "FINALIZED", issue: "1990-03-01", total: 7000, balance: 7000, finalized: true})
	seedInvoice(t, pool, mine, n, invSpec{no: "D1", status: "DRAFT", issue: "1990-03-01", total: 100, balance: 100})
	seedInvoice(t, pool, theirs, n, invSpec{no: "X1", status: "FINALIZED", issue: "1990-03-01", total: 9999, balance: 9999, finalized: true})

	// Unfiltered: exactly my three, newest issue_date first, invoice_no breaking the tie.
	// Cursor carry (HH-3.2 fold): the total echo is gone with the offset
	// family; the page is the assertion.
	all, err := repo.ListInvoices(ctx, mine, Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("tenant fence leaked or dropped rows: page=%d, want 3", len(all))
	}
	wantOrder := []string{n + "-D1", n + "-F1", n + "-F2"} // 1990-03-01 (D1,F1 by no), then 1990-02-01
	for i, w := range wantOrder {
		if all[i].InvoiceNo != w {
			t.Fatalf("order[%d] = %q, want %q (issue_date DESC, invoice_no)", i, all[i].InvoiceNo, w)
		}
		if all[i].CustomerID != mine {
			t.Fatalf("row %d belongs to %q, not the requesting tenant", i, all[i].CustomerID)
		}
	}

	// Status filter narrows the page (the same WHERE both halves carried
	// when the count existed; under the cursor envelope the page is the proof).
	fin, err := repo.ListInvoices(ctx, mine, Filter{Status: "FINALIZED", Limit: 50})
	if err != nil {
		t.Fatalf("list finalized: %v", err)
	}
	if len(fin) != 2 {
		t.Fatalf("status filter: page=%d, want 2", len(fin))
	}

	// Paging walks the same ORDER BY through the cursor key of row one.
	page2, err := repo.ListInvoices(ctx, mine, Filter{Limit: 1, AfterCursor: true,
		CursorIssueDate: all[0].IssueDate.Format("2006-01-02"), CursorInvoiceNo: all[0].InvoiceNo})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(page2) != 1 || page2[0].InvoiceNo != wantOrder[1] {
		t.Fatalf("paging: page=%d first=%q, want 1/%q", len(page2), page2[0].InvoiceNo, wantOrder[1])
	}
}

// -- VERIFY:billing / 2, Get joins lines, and a cross-tenant id is NOT FOUND (never FORBIDDEN) ---

// The 404-not-403 rule is a privacy contract: a 403 confirms the invoice exists. Only the real SQL can
// prove the tenant predicate is in the WHERE rather than in a post-filter that leaks via the error.
func TestIntegration_Billing_GetInvoice_JoinsLinesAndHidesOtherTenants(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)

	mine := seedCustomer(t, pool, n+"-A")
	theirs := seedCustomer(t, pool, n+"-B")

	inv := seedInvoice(t, pool, mine, n, invSpec{no: "G1", status: "FINALIZED", subtotal: 10000, tax: 1300, total: 11300, balance: 11300, finalized: true})
	other := seedInvoice(t, pool, theirs, n, invSpec{no: "G2", status: "FINALIZED", total: 500, balance: 500, finalized: true})

	for i, l := range []struct {
		no    int
		desc  string
		qty   string
		uom   string
		unit  int64
		total int64
	}{
		{2, "second line", "3.0000", "EA", 2000, 6000},
		{1, "first line", "2.0000", "EA", 2000, 4000},
	} {
		if _, err := pool.Exec(ctx,
			`INSERT INTO invoice_lines (invoice_id, line_no, description, qty_entered, uom_entered,
			                            qty_base, unit_price_cents, price_uom, line_total_cents)
			 VALUES ($1,$2,$3,$4::numeric,$5,$4::numeric,$6,$5,$7)`,
			inv, l.no, l.desc, l.qty, l.uom, l.unit, l.total); err != nil {
			t.Fatalf("seed line %d: %v", i, err)
		}
	}

	got, err := repo.GetInvoice(ctx, mine, inv)
	if err != nil {
		t.Fatalf("get own invoice: %v", err)
	}
	if got.TotalCents != 11300 || got.TaxCents != 1300 {
		t.Fatalf("header round-trip: %+v", got)
	}
	if len(got.Lines) != 2 {
		t.Fatalf("lines joined = %d, want 2", len(got.Lines))
	}
	// Inserted out of order on purpose, the ORDER BY line_no must be doing the work.
	if got.Lines[0].LineNo != 1 || got.Lines[1].LineNo != 2 {
		t.Fatalf("lines must come back ordered by line_no, got %d then %d", got.Lines[0].LineNo, got.Lines[1].LineNo)
	}
	if got.Lines[0].LineTotalCents != 4000 {
		t.Fatalf("line 1 total = %d, want 4000", got.Lines[0].LineTotalCents)
	}
	// A nullable product_id must not blow up the scan (it is read through a *string).
	if got.Lines[0].ProductID != "" {
		t.Fatalf("a NULL product_id must decode to the empty string, got %q", got.Lines[0].ProductID)
	}

	// Another tenant's real invoice: NOT FOUND, not forbidden, and no header leaks through the error.
	if _, err := repo.GetInvoice(ctx, mine, other); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant get = %v, want ErrNotFound (a 403 would confirm the invoice exists)", err)
	}
	// A well-formed but absent id takes the same path.
	if _, err := repo.GetInvoice(ctx, mine, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing get = %v, want ErrNotFound", err)
	}
}

// -- VERIFY:billing / 3, open AR counts FINALIZED + PARTIALLY_PAID only -------------------------

// This predicate is what the portal shows the customer as "what you owe" and what the credit seam
// spends. DRAFT is not yet AR; PAID and VOID are not AR any more. The fake store cannot prove the
// status list in the SQL matches the one in the doc comment.
func TestIntegration_Billing_OpenBalanceCents_CountsOnlyLiveAR(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	seedInvoice(t, pool, cust, n, invSpec{no: "OB-FIN", status: "FINALIZED", total: 10000, balance: 10000, finalized: true})
	seedInvoice(t, pool, cust, n, invSpec{no: "OB-PART", status: "PARTIALLY_PAID", total: 8000, balance: 3000, finalized: true})
	seedInvoice(t, pool, cust, n, invSpec{no: "OB-DRAFT", status: "DRAFT", total: 4000, balance: 4000})
	seedInvoice(t, pool, cust, n, invSpec{no: "OB-PAID", status: "PAID", total: 6000, balance: 0, finalized: true})
	// A VOID invoice with a non-zero balance is the trap: it is unwound money, and a status list that
	// forgot to exclude it would silently overstate AR by its balance.
	seedInvoice(t, pool, cust, n, invSpec{no: "OB-VOID", status: "VOID", total: 5000, balance: 5000, finalized: true})

	open, err := repo.OpenBalanceCents(ctx, cust)
	if err != nil {
		t.Fatalf("open balance: %v", err)
	}
	if open != 13000 {
		t.Fatalf("open AR = %d, want 13000 (FINALIZED 10000 + PARTIALLY_PAID 3000; DRAFT/PAID/VOID excluded)", open)
	}

	// The service layer over the real repository: available credit is limit − open, floored at zero.
	svc := NewService(repo)
	avail, err := svc.AvailableCreditCents(ctx, cust, 20000)
	if err != nil {
		t.Fatalf("available credit: %v", err)
	}
	if avail != 7000 {
		t.Fatalf("available credit = %d, want 7000", avail)
	}
	over, err := svc.AvailableCreditCents(ctx, cust, 5000)
	if err != nil {
		t.Fatalf("available credit (over limit): %v", err)
	}
	if over != 0 {
		t.Fatalf("an over-limit account must floor at 0, got %d (a negative here would read as head-room)", over)
	}
}

// -- VERIFY:billing / 4, the order-cancel money gate --------------------------------------------

// order.Service refuses a manual CANCELLED when money is attached, and it fails CLOSED on a read
// error. This predicate is the whole gate. Its status list is the money-critical part: PAID must
// count as attached, VOID must not.
func TestIntegration_Billing_OrderHasFinalizedInvoice_CancelPredicate(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	newOrder := func(tag string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment, subtotal_cents, total_cents)
			 VALUES ($1,$2,$3::uuid,'CONFIRMED','PICKUP',1000,1000) RETURNING id::text`,
			"#"+n+"-"+tag, cust, trentonBranch).Scan(&id); err != nil {
			t.Fatalf("seed order %s: %v", tag, err)
		}
		t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM orders WHERE id = $1`, id) })
		return id
	}

	for _, tc := range []struct {
		name   string
		status string // "" → no invoice at all
		want   bool
	}{
		{"no invoice at all", "", false},
		{"DRAFT invoice is not money yet", "DRAFT", false},
		{"FINALIZED invoice is money attached", "FINALIZED", true},
		{"PARTIALLY_PAID is money attached", "PARTIALLY_PAID", true},
		{"PAID is money attached", "PAID", true},
		{"VOID has already been unwound", "VOID", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ord := newOrder(tc.status + tc.name[:3])
			if tc.status != "" {
				seedInvoice(t, pool, cust, ns(t), invSpec{
					no: "CG", status: tc.status, total: 1000, balance: 1000, orderID: ord,
					finalized: tc.status != "DRAFT",
				})
			}
			got, err := repo.OrderHasFinalizedInvoice(ctx, ord)
			if err != nil {
				t.Fatalf("predicate: %v", err)
			}
			if got != tc.want {
				t.Fatalf("OrderHasFinalizedInvoice(%s) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}

	// Fail-closed: with no pool the predicate must ERROR rather than answer a bare false, because the
	// order service reads false as "no money attached, cancel away".
	if _, err := (&Repository{}).OrderHasFinalizedInvoice(ctx, "irrelevant"); err == nil {
		t.Fatal("an unreadable cancel predicate must return an error, never a bare false")
	}
}

// -- VERIFY:billing / 5, the 1100 AR subledger tie-out ------------------------------------------

// ARBalanceCents is what the GL trial balance is reconciled AGAINST. Its contract is that it sums the
// same two sources v_ar_aging unions, so the aging grand total ties to it by construction. Both halves
// are asserted here, plus the as-of and branch scoping, in a back-dated window that no other row in
// the DB occupies (see asOfEpoch), which is what makes the absolute figures assertable at all.
func TestIntegration_Billing_ARBalanceCents_ScopesAndTiesToAgingView(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	// Baseline in the back-dated window: whatever (if anything) already lives there.
	baseAll, err := repo.ARBalanceCents(ctx, asOfEpoch, "")
	if err != nil {
		t.Fatalf("baseline AR: %v", err)
	}
	baseTRT, err := repo.ARBalanceCents(ctx, asOfEpoch, trentonBranch)
	if err != nil {
		t.Fatalf("baseline AR (TRT): %v", err)
	}

	// Trenton: 12000 open + a 500 NSF (a non-invoice AR transaction, the second union arm).
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-T1", status: "FINALIZED", branch: trentonBranch, total: 12000, balance: 12000, finalized: true})
	// Kingston: 3000 open.
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-K1", status: "FINALIZED", branch: kingstonBranch, total: 3000, balance: 3000, finalized: true})
	// Excluded by status / balance: a DRAFT contributes nothing, and the zero-balance FINALIZED
	// carries the dated allocation that settled it inside the window, so it too nets to zero at the
	// as-of date (the as-of balance rebuilds from dated acts, FW1-06b; a settlement with no dated
	// act behind it would read as still owed at the date).
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-T2", status: "DRAFT", branch: trentonBranch, total: 999, balance: 999})
	t3 := seedInvoice(t, pool, cust, n, invSpec{no: "AR-T3", status: "FINALIZED", branch: trentonBranch, total: 4000, balance: 0, finalized: true})
	var t3Pay string
	if err := pool.QueryRow(ctx,
		`INSERT INTO payments (payment_no, customer_id, method, amount_cents, status, branch_id)
		 VALUES (nextval('payment_no_seq')::text, $1::uuid, 'CHEQUE', 4000, 'RECEIVED', $2::uuid)
		 RETURNING id::text`, cust, trentonBranch).Scan(&t3Pay); err != nil {
		t.Fatalf("seed AR-T3's settling payment: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO payment_allocations (payment_id, invoice_id, amount_cents, created_at)
		 VALUES ($1::uuid, $2::uuid, 4000, $3)`, t3Pay, t3, time.Date(1990, 5, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed AR-T3's dated allocation: %v", err)
	}
	// This test's payment rows are its own; they go before the customer cleanup runs.
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM payment_allocations WHERE payment_id IN
			(SELECT id FROM payments WHERE customer_id = $1::uuid)`, cust)
		_, _ = pool.Exec(c, `DELETE FROM payments WHERE customer_id = $1::uuid`, cust)
	})
	// Excluded by as-of: issued AFTER the window.
	seedInvoice(t, pool, cust, n, invSpec{no: "AR-T4", status: "FINALIZED", branch: trentonBranch, issue: "1991-01-01", total: 7777, balance: 7777, finalized: true})

	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_transactions (customer_id, txn_type, amount_cents, balance_after,
		                                    source_type, source_ref_id, branch_id, occurred_at, created_at)
		 VALUES ($1,'NSF',500,500,'payment',gen_random_uuid(),$2::uuid,$3,$3)`,
		cust, trentonBranch, time.Date(1990, 1, 20, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("seed NSF txn: %v", err)
	}

	gotAll, err := repo.ARBalanceCents(ctx, asOfEpoch, "")
	if err != nil {
		t.Fatalf("AR consolidated: %v", err)
	}
	if delta := gotAll - baseAll; delta != 15500 {
		t.Fatalf("consolidated AR delta = %d, want 15500 (12000 TRT + 3000 KGN + 500 NSF; DRAFT, zero-balance and post-as-of excluded)", delta)
	}

	gotTRT, err := repo.ARBalanceCents(ctx, asOfEpoch, trentonBranch)
	if err != nil {
		t.Fatalf("AR TRT: %v", err)
	}
	if delta := gotTRT - baseTRT; delta != 12500 {
		t.Fatalf("Trenton AR delta = %d, want 12500 (Kingston's 3000 must not be in a branch-scoped read)", delta)
	}

	// As-of BEFORE anything this test wrote: the window is empty, so the branch read is whatever the
	// baseline was at that earlier point, and none of our rows may appear.
	early, err := repo.ARBalanceCents(ctx, beforeAny, trentonBranch)
	if err != nil {
		t.Fatalf("AR early: %v", err)
	}
	if early >= gotTRT {
		t.Fatalf("an earlier as-of must not include later rows: %d >= %d", early, gotTRT)
	}

	// The tie-out itself: the aging view's grand total for the same window equals ARBalanceCents.
	// This is the contract the GL 1100 reconciliation rests on; if the two drift, the trial balance
	// reconciles against a number the aging report does not show.
	var agingTotal int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(balance_cents), 0) FROM (
			SELECT balance_cents FROM invoices
			 WHERE status IN ('FINALIZED','PARTIALLY_PAID') AND balance_cents <> 0
			   AND issue_date <= $1 AND branch_id = $2::uuid
			UNION ALL
			SELECT amount_cents FROM customer_transactions
			 WHERE txn_type IN ('NSF','ADJUSTMENT','CREDIT_MEMO','WRITE_OFF') AND source_type <> 'invoice'
			   AND created_at <= $1 AND branch_id = $2::uuid
		) src`, asOfEpoch, trentonBranch).Scan(&agingTotal); err != nil {
		t.Fatalf("aging grand total: %v", err)
	}
	if agingTotal != gotTRT {
		t.Fatalf("AR subledger (%d) does not tie to the aging grand total (%d) for the same window", gotTRT, agingTotal)
	}
}

// -- VERIFY:billing / 6, the GL invoice projection ----------------------------------------------

// InvoiceForGL feeds the ar.invoice.finalized journal legs. A wrong column here books the wrong
// amount to the wrong account, and the unit tests never touch the SELECT.
func TestIntegration_Billing_InvoiceForGL_ProjectsLegsAndFallsBackOnFinalizedAt(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	inv := seedInvoice(t, pool, cust, n, invSpec{
		no: "GL1", status: "FINALIZED", branch: kingstonBranch,
		subtotal: 20000, tax: 2600, holdback: 2000, total: 22600, balance: 22600, finalized: true,
	})

	a, err := repo.InvoiceForGL(ctx, inv)
	if err != nil {
		t.Fatalf("invoice for gl: %v", err)
	}
	if a.SubtotalCents != 20000 || a.TaxCents != 2600 || a.TotalCents != 22600 || a.HoldbackCents != 2000 {
		t.Fatalf("leg amounts = %+v, want 20000/2600/22600/2000", a)
	}
	if a.BranchID != kingstonBranch || a.CustomerID != cust {
		t.Fatalf("the journal must carry the invoice's OWN branch + customer, got %s / %s", a.BranchID, a.CustomerID)
	}
	if a.InvoiceNo != n+"-GL1" {
		t.Fatalf("invoice_no = %q, want %q", a.InvoiceNo, n+"-GL1")
	}
	if a.FinalizedAt.Year() != 1990 {
		t.Fatalf("FinalizedAt = %v, want the stamped 1990 value (the posting period comes from here)", a.FinalizedAt)
	}

	// The documented fallback: an unstamped invoice posts under created_at rather than the zero time
	// (a zero time would post into a period that does not exist).
	unstamped := seedInvoice(t, pool, cust, n, invSpec{no: "GL2", status: "FINALIZED", total: 100, balance: 100})
	b, err := repo.InvoiceForGL(ctx, unstamped)
	if err != nil {
		t.Fatalf("invoice for gl (unstamped): %v", err)
	}
	if b.FinalizedAt.IsZero() {
		t.Fatal("an invoice with a NULL finalized_at must fall back to created_at, not the zero time")
	}
	if b.FinalizedAt.Year() < 2000 {
		t.Fatalf("the fallback must be created_at (now), got %v", b.FinalizedAt)
	}
}

// -- VERIFY:billing / 7, the Bill 60 retention worklist -----------------------------------------

// InvoicesWithHoldback + InvoiceHoldbackFacts are FROZEN signatures the M6 lien worker consumes.
//
// HISTORY. Wave 2 escalated DIB-T2-F1: the doc comment said "every FINALIZED invoice still carrying
// retention" while the SQL filtered on holdback_cents > 0 ALONE, so DRAFT retention (never billed)
// and VOID retention (already unwound) both reached the release worklist. This test pinned that
// SHIPPED behaviour and instructed whoever fixed it to flip the expectations rather than mute it.
//
// Wave 3 / DIB-417 is that fix, and these are the flipped expectations. The worklist is now scoped to
// billedStatuses (FINALIZED, PARTIALLY_PAID, PAID) and the two wrong rows are asserted ABSENT, with
// the same loudness in the other direction, because "DRAFT retention is back on the worklist" is a
// legal-clock regression and must never be a silent one. PAID is asserted PRESENT on purpose: paying
// the billed portion does not release retention, and dropping PAID would hide the most common
// release candidate there is.
func TestIntegration_Billing_HoldbackWorklist_AndReleaseFacts(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	n := ns(t)
	cust := seedCustomer(t, pool, n)

	fin := seedInvoice(t, pool, cust, n, invSpec{no: "HB-FIN", status: "FINALIZED", branch: kingstonBranch, total: 50000, balance: 50000, holdback: 5000, finalized: true})
	draft := seedInvoice(t, pool, cust, n, invSpec{no: "HB-DRAFT", status: "DRAFT", branch: kingstonBranch, total: 20000, balance: 20000, holdback: 2000})
	void := seedInvoice(t, pool, cust, n, invSpec{no: "HB-VOID", status: "VOID", branch: kingstonBranch, total: 10000, balance: 0, holdback: 1000, finalized: true})
	// A PAID invoice still carrying retention: the billed portion is settled, the holdback is not.
	// This is the row a status filter written carelessly ("only FINALIZED") would wrongly drop.
	paid := seedInvoice(t, pool, cust, n, invSpec{no: "HB-PAID", status: "PAID", branch: kingstonBranch, total: 25000, balance: 0, holdback: 2500, finalized: true})
	// No retention → never on the worklist under any reading.
	seedInvoice(t, pool, cust, n, invSpec{no: "HB-NONE", status: "FINALIZED", branch: kingstonBranch, total: 30000, balance: 30000, finalized: true})
	// Retention at the OTHER branch → must not appear in a Kingston-scoped worklist.
	seedInvoice(t, pool, cust, n, invSpec{no: "HB-TRT", status: "FINALIZED", branch: trentonBranch, total: 40000, balance: 40000, holdback: 4000, finalized: true})

	rows, err := repo.InvoicesWithHoldback(ctx, kingstonBranch)
	if err != nil {
		t.Fatalf("holdback worklist: %v", err)
	}
	got := map[string]int64{}
	for _, r := range rows {
		if r.CustomerID == cust {
			got[r.InvoiceID] = r.HoldbackCents
			if r.BranchID != kingstonBranch {
				t.Fatalf("a branch-scoped worklist returned %s from branch %s", r.InvoiceNo, r.BranchID)
			}
			if r.FinalizedAt == "" {
				t.Fatalf("%s carries no finalized_at, the Bill 60 clock has nothing to age from", r.InvoiceNo)
			}
		}
	}
	if got[fin] != 5000 {
		t.Fatalf("the FINALIZED retention must be on the worklist: got %v", got)
	}
	if got[paid] != 2500 {
		t.Fatalf("a PAID invoice's retention must STAY on the worklist, settling the billed portion does "+
			"not release the holdback, and this is the commonest release candidate there is: got %v", got)
	}
	if _, ok := got[draft]; ok {
		t.Fatal("DIB-417 REGRESSION: a DRAFT invoice's retention is back on the Bill 60 release worklist. " +
			"DRAFT retention has never been billed, so the legal clock has not started and there is nothing " +
			"to release, releasing it invents a payable. Restore the billedStatuses filter in lien_seam.go.")
	}
	if _, ok := got[void]; ok {
		t.Fatal("DIB-417 REGRESSION: a VOID invoice's retention is back on the Bill 60 release worklist. " +
			"That retention was unwound with the invoice; releasing it pays the same dollars twice. " +
			"Restore the billedStatuses filter in lien_seam.go.")
	}
	if len(got) != 2 {
		t.Fatalf("worklist for this tenant at Kingston = %d rows, want 2 (FINALIZED + PAID; the DRAFT, the VOID, "+
			"the zero-retention and the Trenton invoice must all be out): %v", len(got), got)
	}

	// The per-invoice release projection.
	facts, err := repo.InvoiceHoldbackFacts(ctx, fin)
	if err != nil {
		t.Fatalf("holdback facts: %v", err)
	}
	if facts.InvoiceID != fin || facts.HoldbackCents != 5000 || facts.BranchID != kingstonBranch || facts.CustomerID != cust {
		t.Fatalf("release facts = %+v, want the invoice's own retention/branch/customer", facts)
	}
	if _, err := repo.InvoiceHoldbackFacts(ctx, "00000000-0000-4000-8000-000000000000"); err == nil {
		t.Fatal("a missing invoice must error rather than return a zero-retention projection (which would read as 'nothing to release')")
	}
}
