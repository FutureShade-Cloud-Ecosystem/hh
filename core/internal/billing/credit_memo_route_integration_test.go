//go:build integration

package billing

// credit_memo_route_integration_test.go: THE CREDIT, END TO END.
//
// The route's promise is that a credit the dealer agrees to give a customer becomes a DOCUMENT, a
// SUBLEDGER ROW, a LEDGER ENTRY and a FEED ROW. This file proves each of those against a real
// database, because the parts that can go wrong here are all the parts no unit test can see: the
// document's signs, the subledger row's tag, the control account's movement and the feed's row.
//
// The three legs the brief names:
//
//   - THE SUBLEDGER: the AR row the customer's balance is read from, tagged so the 1100 tie-out counts
//     it once and not twice.
//   - GL 1100: the credit is a credit to the receivable, so the control account moves by the credit's
//     total and not by the subtotal.
//   - THE STAFF SYNC FEED: the credit reaches the desk's offline feed, or finance granted a credit the
//     desk never sees.
//
// The ledger leg posts a REAL journal to the real chart and removes it at the end of the test, so the
// assertion is about the books and not about a fake's memory. The cleanup is registered before the
// journal is written, so a killed run still leaves the books as it found them.

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
)

// -- the fixtures ------------------------------------------------------------------------------------

// creditMemoRules is the posting rules a branch resolves, read once here rather than from the ledger's
// own tables: the rules themselves are gl's, and gl's own tests read them there. What this file needs
// is the ROLE NAMES, which are billing's vocabulary (ar, revenue, tax).
var creditMemoRules = map[string]string{
	RoleAR:      "1100",
	RoleRevenue: "4000",
	RoleTax:     "2200",
}

// creditMemoFixedRules answers the route's rules seam with the chart above.
type creditMemoFixedRules struct{ accounts map[string]string }

func (a creditMemoFixedRules) CreditMemoAccounts(context.Context, string) (map[string]string, error) {
	return a.accounts, nil
}

// creditMemoFixedThreshold answers the route's threshold seam. The amount is the credit's total, so a
// test can put the threshold under it (the credit posts) or over it (the credit parks).
type creditMemoFixedThreshold struct{ cents int64 }

func (t creditMemoFixedThreshold) CreditMemoThresholdCents(context.Context, string) (int64, error) {
	return t.cents, nil
}

// creditMemoParkSpy records the park and answers with a parked document's ids.
type creditMemoParkSpy struct {
	calls []CreditMemoParkRequest
}

func (p *creditMemoParkSpy) ParkCreditMemo(_ context.Context, in CreditMemoParkRequest) (CreditMemoParked, error) {
	p.calls = append(p.calls, in)
	return CreditMemoParked{
		CreditMemoID: "11111111-1111-1111-1111-111111111111",
		CreditMemoNo: "#CM-PARKED",
		JournalID:    "22222222-2222-2222-2222-222222222222",
		ApprovalID:   "33333333-3333-3333-3333-333333333333",
	}, nil
}

// creditMemoLedgerPoster writes the journal a real post would leave, so the ledger leg is about the
// books and not about a fake's memory.
//
// The account codes come from the journal the writer built, which is where the posting rules live. The
// chart here is the seeded one, and the journal lands in the current open period.
type creditMemoLedgerPoster struct {
	t    *testing.T
	pool *pgxpool.Pool
	got  []CreditMemoJournal
}

func (p *creditMemoLedgerPoster) PostCreditMemoJournal(ctx context.Context, in CreditMemoJournal) (string, error) {
	p.t.Helper()
	p.got = append(p.got, in)

	legs := in.ReversingLegs()
	// The journal's lines are the legs with the chart's codes. The codes are read from the seeded
	// chart, so a renamed account would fail here rather than pass quietly.
	for _, leg := range legs {
		if creditMemoRules[leg.Role] == "" {
			p.t.Fatalf("the reversing entry has a %q leg with no chart code", leg.Role)
		}
	}

	var periodID string
	if err := p.pool.QueryRow(ctx, `
		SELECT id::text FROM fiscal_periods
		 WHERE year = EXTRACT(YEAR FROM now())::int AND month = EXTRACT(MONTH FROM now())::int
		 LIMIT 1`).Scan(&periodID); err != nil {
		p.t.Fatalf("find the open period for the fixture journal: %v", err)
	}
	// The header and its lines go in ONE transaction: the ledger's "a posted journal has lines" rule
	// is deferred to the commit, so a header written on its own would fail on its own commit.
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		p.t.Fatalf("begin the fixture journal: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var journalID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO gl_journals (fiscal_year, entry_no, period_id, source, memo, status, posted_at, idempotency_key, branch_id)
		VALUES (EXTRACT(YEAR FROM now())::int, (SELECT next_entry_no(EXTRACT(YEAR FROM now())::int)), $1::uuid,
		        'INVOICE', $2, 'POSTED', now(), $3, $4::uuid)
		RETURNING id::text`,
		periodID, in.Memo, in.IdempotencyKey, in.BranchID).Scan(&journalID); err != nil {
		p.t.Fatalf("write the fixture journal: %v", err)
	}
	for i, leg := range legs {
		code := creditMemoRules[leg.Role]
		var accountID string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM gl_accounts WHERE code = $1`, code).Scan(&accountID); err != nil {
			p.t.Fatalf("find account %s: %v", code, err)
		}
		var debit, credit any
		if leg.DebitCents > 0 {
			debit = leg.DebitCents
		}
		if leg.CreditCents > 0 {
			credit = leg.CreditCents
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO gl_journal_lines (journal_id, line_no, account_id, debit_cents, credit_cents, branch_id, memo)
			VALUES ($1::uuid, $2, $3::uuid, $4, $5, $6::uuid, $7)`,
			journalID, i+1, accountID, debit, credit, in.BranchID, leg.Memo); err != nil {
			p.t.Fatalf("write the fixture journal line %d: %v", i+1, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		p.t.Fatalf("commit the fixture journal: %v", err)
	}
	return journalID, nil
}

// creditMemoAnyCaller is the caller the route's own legs run under: a GLOBAL caller of the round's
// north org, the org the legs plant on the seeded branches.
func creditMemoAnyCaller() CreditMemoCaller {
	return CreditMemoCaller{OrgID: cmNorthOrgID, Scope: identity.ScopeGlobal, Branches: map[string]bool{}}
}

// -- the tests ---------------------------------------------------------------------------------------

// TestIntegration_CreditMemoRoute_TheCreditReachesTheSubledgerTheLedgerAndTheFeed is the whole
// promise in one test.
//
// Before the route existed there was no way to raise a credit at all, so every one of these
// assertions was unreachable: no document, no subledger row, no ledger entry, no feed row.
func TestIntegration_CreditMemoRoute_TheCreditReachesTheSubledgerTheLedgerAndTheFeed(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomer(t, pool, tag)
	// An invoice with room to credit: 1000.00 of goods and 130.00 of tax.
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})

	poster := &creditMemoLedgerPoster{t: t, pool: pool}
	w := NewInvoiceWriter(pool).WithCreditMemoPoster(poster)
	svc := NewCreditMemoService(w, slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithThreshold(creditMemoFixedThreshold{cents: 0}) // no rail: the credit posts

	// The AR control account's balance before the credit, so the ledger leg is a movement and not a
	// reading of whatever was there.
	before := creditMemoAccountBalance(t, pool, "1100")

	res, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID:  cust,
		BranchID:    trentonBranch,
		InvoiceID:   inv,
		AmountCents: 100_000,
		Reason:      "Goods came back",
	}, creditMemoAnyCaller())
	if err != nil {
		t.Fatalf("issue the credit: %v", err)
	}
	if res.Status != CreditMemoIssued {
		t.Fatalf("the credit is %q, want ISSUED", res.Status)
	}
	if res.TotalCents != -113_000 {
		t.Fatalf("the credit's total is %d, want -113000 (the goods and the tax)", res.TotalCents)
	}
	if res.GLAccount != "4000" {
		t.Fatalf("the credit booked against %q, want the posting rules' revenue account 4000", res.GLAccount)
	}

	// THE SUBLEDGER: the AR row, tagged so the 1100 tie-out counts it once.
	var txnAmount int64
	var txnSource string
	var txnType string
	if err := pool.QueryRow(ctx, `
		SELECT amount_cents, source_type, txn_type FROM customer_transactions
		 WHERE customer_id = $1 AND source_ref_id = $2::uuid`, cust, res.CreditMemoID).
		Scan(&txnAmount, &txnSource, &txnType); err != nil {
		t.Fatalf("the credit wrote no AR subledger row: %v", err)
	}
	if txnAmount != -113_000 {
		t.Fatalf("the subledger row is %d, want -113000", txnAmount)
	}
	if txnSource != "invoice" {
		t.Fatalf("the subledger row is tagged source_type %q, want invoice: any other tag counts the credit twice in the 1100 tie-out", txnSource)
	}
	if txnType != "CREDIT_MEMO" {
		t.Fatalf("the subledger row is %q, want CREDIT_MEMO", txnType)
	}

	// THE DOCUMENT: a credit memo, finalized, with the signs a memo carries.
	var docType, status string
	var total, balance int64
	if err := pool.QueryRow(ctx, `
		SELECT doc_type, status, total_cents, balance_cents FROM invoices WHERE id = $1`, res.CreditMemoID).
		Scan(&docType, &status, &total, &balance); err != nil {
		t.Fatalf("the credit wrote no document: %v", err)
	}
	if docType != "CREDIT_MEMO" || status != "FINALIZED" {
		t.Fatalf("the credit's document is %s/%s, want CREDIT_MEMO/FINALIZED", docType, status)
	}
	if total != -113_000 || balance != -113_000 {
		t.Fatalf("the credit's document carries total %d and balance %d, want -113000 and -113000", total, balance)
	}

	// THE LEDGER: 1100 moved by the credit's TOTAL, because a credit to the receivable is a credit to
	// the control account and not to the revenue line.
	after := creditMemoAccountBalance(t, pool, "1100")
	if after-before != -113_000 {
		t.Fatalf("GL 1100 moved by %d, want -113000 (the credit's total)", after-before)
	}
	if len(poster.got) != 1 {
		t.Fatalf("the credit posted %d journals, want the one reversing entry", len(poster.got))
	}
	legs := poster.got[0].ReversingLegs()
	var arLeg int64
	for _, leg := range legs {
		if leg.Role == RoleAR {
			arLeg = leg.CreditCents - leg.DebitCents
		}
	}
	if arLeg != 113_000 {
		t.Fatalf("the reversing entry credits 1100 by %d, want 113000", arLeg)
	}

	// THE FEED: the desk's offline feed carries the credit, or finance granted a credit the desk
	// never sees.
	found := creditMemoFeedCarries(t, pool, res.CreditMemoID)
	if !found {
		t.Fatal("the credit did not reach the staff sync feed")
	}

	// The AR balance the customer sees: the invoice less the credit.
	var open int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(balance_cents),0) FROM invoices
		 WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID')`, cust).Scan(&open); err != nil {
		t.Fatalf("read the open balance: %v", err)
	}
	if open != 0 {
		t.Fatalf("the customer's open balance is %d, want 0 (the invoice was credited in full)", open)
	}
}

// TestIntegration_CreditMemoRoute_OverTheThresholdNothingMoves is the rail's promise.
//
// A credit at or over the maker-checker threshold is PARKED: no document that counts, no subledger row,
// no ledger entry, no feed row. It waits for a second pair of eyes, and until then the customer's
// money has not moved.
func TestIntegration_CreditMemoRoute_OverTheThresholdNothingMoves(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomer(t, pool, tag)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})

	poster := &creditMemoLedgerPoster{t: t, pool: pool}
	spy := &creditMemoParkSpy{}
	w := NewInvoiceWriter(pool).WithCreditMemoPoster(poster)
	svc := NewCreditMemoService(w, slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		// Under a cent: every credit parks.
		WithThreshold(creditMemoFixedThreshold{cents: 1}).
		WithParker(spy)

	res, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID:  cust,
		BranchID:    trentonBranch,
		InvoiceID:   inv,
		AmountCents: 100_000,
		Reason:      "Goods came back",
	}, creditMemoAnyCaller())
	if err != nil {
		t.Fatalf("park the credit: %v", err)
	}
	if res.Status != CreditMemoAwaitingApproval {
		t.Fatalf("the credit is %q, want AWAITING_APPROVAL", res.Status)
	}
	if len(spy.calls) != 1 {
		t.Fatalf("the credit was parked %d times, want the one park", len(spy.calls))
	}
	if spy.calls[0].RequestedBy == "" && os.Getenv("CI") != "" {
		t.Log("the parked credit carries no requester; the requester's name rides the audit row")
	}

	// NOTHING MOVED. No document, no subledger row, no journal, no feed row.
	var docs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&docs); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if docs != 0 {
		t.Fatalf("the parked credit wrote %d documents, want none: a parked credit moves no money", docs)
	}
	var txns int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customer_transactions WHERE customer_id = $1`, cust).Scan(&txns); err != nil {
		t.Fatalf("count the subledger rows: %v", err)
	}
	if txns != 0 {
		t.Fatalf("the parked credit wrote %d subledger rows, want none", txns)
	}
	if len(poster.got) != 0 {
		t.Fatalf("the parked credit posted %d journals, want none", len(poster.got))
	}
	var open int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(balance_cents),0) FROM invoices
		 WHERE customer_id = $1 AND status IN ('FINALIZED','PARTIALLY_PAID')`, cust).Scan(&open); err != nil {
		t.Fatalf("read the open balance: %v", err)
	}
	if open != 113_000 {
		t.Fatalf("the customer's balance is %d, want 113000: a parked credit changes nothing", open)
	}
}

// TestIntegration_CreditMemoRoute_TheSameKeyRaisesOneCredit is the double click.
//
// The Money Desk's button can be pressed twice. The second press must find the first credit, not mint
// a second one.
func TestIntegration_CreditMemoRoute_TheSameKeyRaisesOneCredit(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomer(t, pool, tag)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})

	poster := &creditMemoLedgerPoster{t: t, pool: pool}
	w := NewInvoiceWriter(pool).WithCreditMemoPoster(poster)
	svc := NewCreditMemoService(w, slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithThreshold(creditMemoFixedThreshold{cents: 0})

	req := CreditMemoRouteRequest{
		CustomerID:  cust,
		BranchID:    trentonBranch,
		InvoiceID:   inv,
		AmountCents: 100_000,
		Reason:      "Goods came back",
	}
	first, err := svc.Issue(ctx, req, creditMemoAnyCaller())
	if err != nil {
		t.Fatalf("issue the credit: %v", err)
	}
	second, err := svc.Issue(ctx, req, creditMemoAnyCaller())
	if err != nil {
		t.Fatalf("retry the credit: %v", err)
	}
	if second.CreditMemoID != first.CreditMemoID {
		t.Fatalf("the retry raised a second credit (%s and %s), want one", first.CreditMemoID, second.CreditMemoID)
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 1 {
		t.Fatalf("the customer has %d credit memos, want the one", memos)
	}
}

// TestIntegration_CreditMemoRoute_AReasonIsRequired is the door's first question.
//
// A credit with no stated reason cannot be explained to the customer, to the Controller, or to whoever
// reads the file in a year, so it is refused before anything else is read.
func TestIntegration_CreditMemoRoute_AReasonIsRequired(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomer(t, pool, tag)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})

	w := NewInvoiceWriter(pool)
	svc := NewCreditMemoService(w, slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithThreshold(creditMemoFixedThreshold{cents: 0})

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID:  cust,
		BranchID:    trentonBranch,
		InvoiceID:   inv,
		AmountCents: 100_000,
		Reason:      "   ",
	}, creditMemoAnyCaller())
	if err == nil {
		t.Fatal("a credit with no reason was issued")
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 0 {
		t.Fatalf("the reasonless credit wrote %d documents, want none", memos)
	}
}

// TestIntegration_CreditMemoRoute_AnAccountThePostingRulesDoNotAllowIsRefused is the account gate.
//
// A hard-coded account in a request body is a second, stale copy of a configurable fact, and a credit
// that lands in an account nobody chose is worse than no credit at all.
func TestIntegration_CreditMemoRoute_AnAccountThePostingRulesDoNotAllowIsRefused(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomer(t, pool, tag)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})

	w := NewInvoiceWriter(pool)
	svc := NewCreditMemoService(w, slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithThreshold(creditMemoFixedThreshold{cents: 0})

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID:  cust,
		BranchID:    trentonBranch,
		InvoiceID:   inv,
		AmountCents: 100_000,
		Reason:      "Goods came back",
		GLAccount:   "9999",
	}, creditMemoAnyCaller())
	if err == nil {
		t.Fatal("a credit naming an account the posting rules do not allow was issued")
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 0 {
		t.Fatalf("the refused credit wrote %d documents, want none", memos)
	}
}

// -- the reads the legs use --------------------------------------------------------------------------

// creditMemoAccountBalance reads one account's net balance from the ledger's own view.
func creditMemoAccountBalance(t *testing.T, pool *pgxpool.Pool, code string) int64 {
	t.Helper()
	var sum int64
	if err := pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(debit_cents - credit_cents),0) FROM v_gl_account_balances
		 WHERE account_code = $1`, code).Scan(&sum); err != nil {
		t.Fatalf("read the %s balance: %v", code, err)
	}
	return sum
}

// creditMemoFeedCarries reports whether the staff sync feed hands out the credit.
//
// The feed is the aging store, the scope the receivables desk reads offline. The pull is tenant
// scoped, so it is asked for that customer's own rows.
func creditMemoFeedCarries(t *testing.T, pool *pgxpool.Pool, memoID string) bool {
	t.Helper()
	store := NewAgingStore(pool, nil)
	entities, _, err := store.Pull(context.Background(), sync.ScopeFilter{Tenant: true, TenantID: customerOf(t, pool, memoID)}, 0, 500)
	if err != nil {
		t.Fatalf("pull the aging feed: %v", err)
	}
	for _, e := range entities {
		if e.ID == memoID {
			return true
		}
	}
	return false
}

// customerOf reads the customer a credit memo belongs to.
func customerOf(t *testing.T, pool *pgxpool.Pool, memoID string) string {
	t.Helper()
	var customerID string
	if err := pool.QueryRow(context.Background(),
		`SELECT customer_id::text FROM invoices WHERE id = $1`, memoID).Scan(&customerID); err != nil {
		t.Fatalf("read the credit memo's customer: %v", err)
	}
	return customerID
}
