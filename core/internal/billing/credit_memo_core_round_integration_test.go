//go:build integration

package billing

// credit_memo_core_round_integration_test.go: THE CORE ROUND'S LEGS (S3's read of PR 617).
//
// Six findings, one leg each, every one red on the branch before its fix:
//
//   - THE PARK PLANS UNDER THE LOCK: a parked credit and a posted credit of one invoice cannot
//     together exceed it. The leg parks one writer mid-transaction and races the posted writer
//     through it (testkit.NewInsertPark, a BEFORE INSERT park, never an AFTER trigger).
//   - THE FENCE: an invoice at a branch the caller does not reach, and one at a branch of another
//     org, answer exactly as a missing invoice answers; a body branch out of reach, and one of
//     another org, are refused.
//   - THE KEY IS BOUND TO ITS REQUEST: the same idempotency key with a different body is a conflict,
//     never somebody else's credit read back as issued.
//   - THE APPROVAL RE-CHECKS THE RIGHT RULE: a credit of a PAID invoice is approvable (the old
//     balance re-check refused it for good), and a re-check against the locked SF-2e remainder still
//     refuses a credit whose room is gone.

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// The fence fixtures' fixed ids. They are planted beside the seed and removed at each test's end;
// the seed itself carries no org rows (0421 backfills only where an org exists to take).
const (
	cmNorthOrgID    = "ce110000-0000-4000-8000-0000000000c1"
	cmSouthOrgID    = "ce110000-0000-4000-8000-0000000000c2"
	cmSouthBranchID = "ce110000-0000-4000-8000-0000000000c3"
)

// plantCreditMemoOrgs gives the two seeded branches the north org and plants one branch of a second
// org, so the fence's org lines have something true to read. Restored at test end.
func plantCreditMemoOrgs(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO org (id, slug, name) VALUES
		  ($1::uuid, 'cm-round-north', 'Credit Round North'),
		  ($2::uuid, 'cm-round-south', 'Credit Round South')
		ON CONFLICT (id) DO NOTHING`, cmNorthOrgID, cmSouthOrgID); err != nil {
		t.Fatalf("plant the round's orgs: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO locations (id, branch_id, parent_id, loc_type, code, name, address, org_id)
		VALUES ($1::uuid, $1::uuid, NULL, 'BRANCH', 'CRCORE', 'Credit Round South', '{}', $2::uuid)
		ON CONFLICT (id) DO NOTHING`, cmSouthBranchID, cmSouthOrgID); err != nil {
		t.Fatalf("plant the south branch: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE locations SET org_id = $1::uuid WHERE id IN ($2::uuid, $3::uuid) AND org_id IS NULL`,
		cmNorthOrgID, trentonBranch, kingstonBranch); err != nil {
		t.Fatalf("give the seeded branches their org: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `UPDATE locations SET org_id = NULL WHERE id IN ($1::uuid, $2::uuid)`, trentonBranch, kingstonBranch)
		_, _ = pool.Exec(c, `DELETE FROM locations WHERE id = $1::uuid`, cmSouthBranchID)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id IN ($1::uuid, $2::uuid)`, cmNorthOrgID, cmSouthOrgID)
	})
}

// seedCustomerAt is seedCustomer with the customer's home branch named, so an org's customers are of
// that org's own branch.
func seedCustomerAt(t *testing.T, pool *pgxpool.Pool, tag, branchID string) string {
	t.Helper()
	ctx := context.Background()
	var id string
	if err := pool.QueryRow(ctx,
		`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 0) RETURNING id::text`,
		"CMCR-"+tag, "Credit Round "+tag, branchID).Scan(&id); err != nil {
		t.Fatalf("seed the round's customer: %v", err)
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

// cmStampInvoiceOrg gives a seeded invoice its branch's org, as the writer stamps on every real
// document (SF-2p); the seed helper leaves the column null, and the fence reads it.
func cmStampInvoiceOrg(t *testing.T, pool *pgxpool.Pool, invoiceID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		UPDATE invoices SET org_id = (SELECT l.org_id FROM locations l WHERE l.id = invoices.branch_id)
		 WHERE id = $1::uuid`, invoiceID); err != nil {
		t.Fatalf("stamp the invoice's org: %v", err)
	}
}

func cmTrentonOnlyCaller() CreditMemoCaller {
	return CreditMemoCaller{OrgID: cmNorthOrgID, Scope: identity.ScopeBranch, Branches: map[string]bool{trentonBranch: true}}
}

func cmNorthGlobalCaller() CreditMemoCaller {
	return CreditMemoCaller{OrgID: cmNorthOrgID, Scope: identity.ScopeGlobal, Branches: map[string]bool{}}
}

// cmFixedBranchOrgs answers the branches module's read from a fixed branch to org map.
type cmFixedBranchOrgs map[string]string

func (m cmFixedBranchOrgs) BranchInOrg(_ context.Context, orgID, branchID string) (bool, error) {
	return orgID != "" && m[branchID] == orgID, nil
}

// cmFixedHomes answers the customer module's read from a fixed customer to home branch map.
type cmFixedHomes map[string]string

func (m cmFixedHomes) CustomerHomeBranch(_ context.Context, customerID string) (string, bool, error) {
	home, ok := m[customerID]
	return home, ok, nil
}

// cmRoundRoute builds the route over the fixed rules and threshold, with the fence's two reads
// answered from the fixture maps. The leg registers each customer's home in the returned map.
func cmRoundRoute(t *testing.T, pool *pgxpool.Pool, threshold int64) (*CreditMemoService, cmFixedHomes) {
	t.Helper()
	homes := cmFixedHomes{}
	svc := NewCreditMemoService(NewInvoiceWriter(pool), slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithThreshold(creditMemoFixedThreshold{cents: threshold}).
		WithBranchOrg(cmFixedBranchOrgs{trentonBranch: cmNorthOrgID, kingstonBranch: cmNorthOrgID, cmSouthBranchID: cmSouthOrgID}).
		WithCustomerHome(homes)
	return svc, homes
}

// TestIntegration_CreditMemoRound_TheParkPlansUnderTheReversalLock is S3's probe 1 as a leg.
//
// The parked writer is stood between its plan and its insert (a BEFORE INSERT park on invoices). The
// posted credit of the same remainder must wait behind it and then find nothing left; the two credits
// together must never exceed the invoice.
func TestIntegration_CreditMemoRound_TheParkPlansUnderTheReversalLock(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust := seedCustomer(t, pool, tag)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})
	w := NewInvoiceWriter(pool)

	park := testkit.NewInsertPark(t, pool, "invoices", 1)
	parked := make(chan error, 1)
	go func() {
		tx, err := park.Pool.Begin(ctx)
		if err != nil {
			parked <- err
			return
		}
		if _, _, err := w.ParkCreditMemoTx(ctx, tx, CreditMemoInput{
			CustomerID: cust, BranchID: trentonBranch, ReversesInvoiceID: inv,
			AmountCents: 0, Memo: "parked whole", IdempotencyKey: tag + "-parked",
		}); err != nil {
			parked <- err
			_ = tx.Rollback(ctx)
			return
		}
		parked <- tx.Commit(ctx)
	}()
	park.WaitParked("the parked credit")

	posted := make(chan error, 1)
	go func() {
		_, err := w.IssueCreditMemo(ctx, CreditMemoInput{
			CustomerID: cust, BranchID: trentonBranch, ReversesInvoiceID: inv,
			AmountCents: 0, Memo: "posted whole", IdempotencyKey: tag + "-posted",
		})
		posted <- err
	}()
	// With the lock taken at plan time the posted credit stands on the invoice's row lock before it
	// can insert; without it this wait times out, which is the red.
	park.WaitBlocked("the posted credit")

	park.AnswersAllowing(map[string]chan error{"parked": parked, "posted": posted},
		map[string]string{"posted": "nothing left to credit"})

	var postedMemos int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND idempotency_key = $1`, tag+"-posted").
		Scan(&postedMemos); err != nil {
		t.Fatalf("count the posted writer's memos: %v", err)
	}
	if postedMemos != 0 {
		t.Fatalf("fw1-12a core red: the posted credit landed beside the uncommitted park (%d memos); the park must plan under the reversal lock", postedMemos)
	}
	var status string
	var total int64
	if err := pool.QueryRow(ctx,
		`SELECT status, total_cents FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND idempotency_key = $1`, tag+"-parked").
		Scan(&status, &total); err != nil {
		t.Fatalf("read the parked memo: %v", err)
	}
	if status != "DRAFT" || total != -113_000 {
		t.Fatalf("the parked memo reads %s/%d, want DRAFT/-113000", status, total)
	}
}

// TestIntegration_CreditMemoRound_AnInvoiceAtAnotherBranchAnswersNotFound is the fence's reach line.
//
// The invoice sits at a branch the caller does not reach, and the answer is the one a missing invoice
// gets, so a caller cannot read a ledger's shape out of the refusals.
func TestIntegration_CreditMemoRound_AnInvoiceAtAnotherBranchAnswersNotFound(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, trentonBranch)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true, branch: kingstonBranch,
	})
	cmStampInvoiceOrg(t, pool, inv)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = trentonBranch

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch, InvoiceID: inv,
		AmountCents: 0, Reason: "Goods came back",
	}, cmTrentonOnlyCaller())
	if err == nil || !strings.Contains(err.Error(), "no such invoice to reverse") {
		t.Fatalf("fw1-12a core red: an invoice at another branch answered %v, want the not found refusal", err)
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 0 {
		t.Fatalf("the out of reach credit wrote %d documents, want none", memos)
	}
}

// TestIntegration_CreditMemoRound_AnInvoiceAtAnotherOrgAnswersNotFound is the fence's org line: a
// GLOBAL caller reaches every branch of its own org and nothing of another's.
func TestIntegration_CreditMemoRound_AnInvoiceAtAnotherOrgAnswersNotFound(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, cmSouthBranchID)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true, branch: cmSouthBranchID,
	})
	cmStampInvoiceOrg(t, pool, inv)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = cmSouthBranchID

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: cmSouthBranchID, InvoiceID: inv,
		AmountCents: 0, Reason: "Goods came back",
	}, cmNorthGlobalCaller())
	if err == nil || !strings.Contains(err.Error(), "no such invoice to reverse") {
		t.Fatalf("fw1-12a core red: an invoice of another org answered %v, want the not found refusal", err)
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 0 {
		t.Fatalf("the other org's credit wrote %d documents, want none", memos)
	}
}

// TestIntegration_CreditMemoRound_ABranchOutOfReachIsRefused is the fence on a standalone credit.
func TestIntegration_CreditMemoRound_ABranchOutOfReachIsRefused(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, kingstonBranch)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = kingstonBranch

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: kingstonBranch,
		AmountCents: 10_000, Reason: "Goodwill",
	}, cmTrentonOnlyCaller())
	if err == nil {
		t.Fatal("fw1-12a core red: a standalone credit at a branch the caller does not reach was issued")
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 0 {
		t.Fatalf("the out of reach credit wrote %d documents, want none", memos)
	}
}

// TestIntegration_CreditMemoRound_ABranchOfAnotherOrgIsRefused is the fence's org line on a
// standalone credit: GLOBAL is bounded to its org.
func TestIntegration_CreditMemoRound_ABranchOfAnotherOrgIsRefused(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, cmSouthBranchID)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = cmSouthBranchID

	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: cmSouthBranchID,
		AmountCents: 10_000, Reason: "Goodwill",
	}, cmNorthGlobalCaller())
	if err == nil {
		t.Fatal("fw1-12a core red: a standalone credit at another org's branch was issued")
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 0 {
		t.Fatalf("the other org's credit wrote %d documents, want none", memos)
	}
}

// TestIntegration_CreditMemoRound_TheSameKeyOnADifferentBodyConflicts binds the idempotency key to
// its request: a key reused with a different amount is a conflict, never the first credit read back
// as if the second had been issued.
func TestIntegration_CreditMemoRound_TheSameKeyOnADifferentBodyConflicts(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	plantCreditMemoOrgs(t, pool)
	cust := seedCustomerAt(t, pool, tag, trentonBranch)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})
	cmStampInvoiceOrg(t, pool, inv)
	svc, homes := cmRoundRoute(t, pool, 0)
	homes[cust] = trentonBranch

	if _, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch, InvoiceID: inv,
		AmountCents: 100_000, Reason: "Goods came back", IdempotencyKey: tag + "-key",
	}, cmNorthGlobalCaller()); err != nil {
		t.Fatalf("issue the first credit: %v", err)
	}
	_, err := svc.Issue(ctx, CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranch, InvoiceID: inv,
		AmountCents: 50_000, Reason: "A different credit", IdempotencyKey: tag + "-key",
	}, cmNorthGlobalCaller())
	if err == nil {
		t.Fatal("fw1-12a core red: the same key with a different body answered the first credit as issued")
	}
	var memos int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE doc_type = 'CREDIT_MEMO' AND customer_id = $1`, cust).Scan(&memos); err != nil {
		t.Fatalf("count the credit memos: %v", err)
	}
	if memos != 1 {
		t.Fatalf("the customer has %d credit memos, want the one", memos)
	}
}

// TestIntegration_CreditMemoRound_AnOverThresholdCreditOfAPaidInvoiceIsApprovable is S3's probe 2.
//
// The old approval re-check read the original invoice's balance, which a credit never changes, so an
// over-threshold credit of a paid invoice could be parked but never approved. The parked credit
// finalizes, writes the subledger row, and the customer is credited.
func TestIntegration_CreditMemoRound_AnOverThresholdCreditOfAPaidInvoiceIsApprovable(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust := seedCustomer(t, pool, tag)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "PAID", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 0, finalized: true,
	})
	w := NewInvoiceWriter(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the park: %v", err)
	}
	doc, _, err := w.ParkCreditMemoTx(ctx, tx, CreditMemoInput{
		CustomerID: cust, BranchID: trentonBranch, ReversesInvoiceID: inv,
		AmountCents: 0, Memo: "paid invoice credit", IdempotencyKey: tag + "-parked",
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("park the credit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the park: %v", err)
	}

	ftx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the finalize: %v", err)
	}
	if err := w.FinalizeParkedCreditMemoTx(ctx, ftx, doc.InvoiceID); err != nil {
		_ = ftx.Rollback(ctx)
		t.Fatalf("fw1-12a core red: the approval of a parked credit of a paid invoice answered %v, want it approved", err)
	}
	if err := ftx.Commit(ctx); err != nil {
		t.Fatalf("commit the finalize: %v", err)
	}
	var status string
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT status, balance_cents FROM invoices WHERE id = $1`, doc.InvoiceID).
		Scan(&status, &balance); err != nil {
		t.Fatalf("read the finalized memo: %v", err)
	}
	if status != "FINALIZED" || balance != -113_000 {
		t.Fatalf("the approved credit reads %s/%d, want FINALIZED/-113000", status, balance)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM customer_transactions WHERE source_ref_id = $1::uuid`, doc.InvoiceID).
		Scan(&rows); err != nil {
		t.Fatalf("count the subledger rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the approved credit wrote %d subledger rows, want one", rows)
	}
}

// TestIntegration_CreditMemoRound_TheApprovalReChecksTheCapUnderTheLock keeps a re-check, the right
// one: the locked SF-2e remainder. The original's room was taken while this credit waited (stood in
// here by an edit to the original), so the approval refuses and the parked credit stays a draft.
func TestIntegration_CreditMemoRound_TheApprovalReChecksTheCapUnderTheLock(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	tag := ns(t)
	cust := seedCustomer(t, pool, tag)
	inv := seedInvoice(t, pool, cust, tag, invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 13_000,
		total: 113_000, balance: 113_000, finalized: true,
	})
	w := NewInvoiceWriter(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the park: %v", err)
	}
	doc, _, err := w.ParkCreditMemoTx(ctx, tx, CreditMemoInput{
		CustomerID: cust, BranchID: trentonBranch, ReversesInvoiceID: inv,
		AmountCents: 0, Memo: "waited too long", IdempotencyKey: tag + "-parked",
	})
	if err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("park the credit: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the park: %v", err)
	}
	// The room is gone while the credit waited (another document took the original down; stood in by
	// an edit, the only door there is today).
	if _, err := pool.Exec(ctx,
		`UPDATE invoices SET subtotal_cents = 40_000, tax_cents = 5_200, total_cents = 45_200 WHERE id = $1`, inv); err != nil {
		t.Fatalf("shrink the original: %v", err)
	}

	ftx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the finalize: %v", err)
	}
	err = w.FinalizeParkedCreditMemoTx(ctx, ftx, doc.InvoiceID)
	if err == nil {
		_ = ftx.Rollback(ctx)
		t.Fatal("fw1-12a core red: the approval approved a credit whose room was taken while it waited")
	}
	if strings.Contains(err.Error(), "operator is not unique") || strings.Contains(err.Error(), "42725") {
		_ = ftx.Rollback(ctx)
		t.Fatalf("fw1-12a core red: the re-check answered the approval's broken SQL (%v), want the cap's own words", err)
	}
	_ = ftx.Rollback(ctx)
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1`, doc.InvoiceID).Scan(&status); err != nil {
		t.Fatalf("read the parked memo: %v", err)
	}
	if status != "DRAFT" {
		t.Fatalf("the refused credit's memo reads %s, want DRAFT", status)
	}
}
