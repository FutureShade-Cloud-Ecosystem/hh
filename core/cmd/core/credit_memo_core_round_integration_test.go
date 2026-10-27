//go:build integration

package main

// credit_memo_core_round_integration_test.go: THE REAL RAIL'S LEGS for a parked credit memo (S3's
// read of PR 617). The route's own tests park through a spy; these walk the real parker (the
// composition root's adapter over billing, gl and the approvals queue) and the real rail, so the
// approve half and the refuse half are exercised as the Controller's decision drives them.
//
//   - APPROVE: a parked credit approved on the rail finalizes the memo, writes the subledger row and
//     posts the reversing journal, all on the decision's one transaction. (Red on the branch: the
//     finalize's SQL could not run at all.)
//   - REFUSE: a refused parked credit voids its DRAFT memo and its DRAFT journal, so the invoice's
//     room is freed for the credit that follows.

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/billing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	cmRoundNorthOrg = "ce110000-0000-4000-8000-0000000000d1"
)

// cmRoundWorld is one leg's fixture: the org, the customer, the invoice, the two staff users.
type cmRoundWorld struct {
	cust, inv, requester, decider, requesterPlatform, org string
}

// cmRoundPool opens the leg's pool on the gate database the suite points DATABASE_URL at.
func cmRoundPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set, integration tests skipped")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("round pool: %v", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("round pool not reachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// cmRoundWorld plants what one rail leg needs: the org on the seeded branches, a customer, one
// invoice with room to credit (its total sits over the seeded threshold, so the credit parks), and
// two staff users (the requester and the decider; the four eyes needs both).
func cmRoundWorldPlant(t *testing.T, pool *pgxpool.Pool) cmRoundWorld {
	t.Helper()
	ctx := context.Background()
	world := cmRoundWorld{org: cmRoundNorthOrg}
	if _, err := pool.Exec(ctx, `
		INSERT INTO org (id, slug, name) VALUES ($1::uuid, 'cm-round-rail', 'Credit Round Rail')
		ON CONFLICT (id) DO NOTHING`, cmRoundNorthOrg); err != nil {
		t.Fatalf("plant the rail org: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = $1::uuid WHERE org_id IS NULL`, cmRoundNorthOrg); err != nil {
		t.Fatalf("give the branches their org: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		VALUES ('CMRAIL-' || floor(random() * 1e9)::text, 'Credit Round Rail', 'CONTRACTOR',
		        $1::uuid, 0)
		RETURNING id::text`, trentonBranchID).Scan(&world.cust); err != nil {
		t.Fatalf("seed the rail customer: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, org_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		VALUES ('CMRAIL-INV-' || floor(random() * 1e9)::text, $1::uuid, $2::uuid,
		        (SELECT org_id FROM locations WHERE id = $2::uuid), 'FINALIZED',
		        '1990-01-15', '1990-01-15', 600_000, 78_000, 678_000, 678_000, now())
		RETURNING id::text`, world.cust, trentonBranchID).Scan(&world.inv); err != nil {
		t.Fatalf("seed the rail invoice: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM users WHERE deleted_at IS NULL ORDER BY id LIMIT 1`).Scan(&world.requester); err != nil {
		t.Fatalf("seed users missing: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM users WHERE deleted_at IS NULL AND id <> $1 ORDER BY id LIMIT 1`,
		world.requester).Scan(&world.decider); err != nil {
		t.Fatalf("seed users missing: %v", err)
	}
	// The withdrawal leg's store resolves the actor through the platform user id, so the requester
	// carries one for the round and it is restored at the end.
	world.requesterPlatform = "cm-round-actor"
	if _, err := pool.Exec(ctx, `UPDATE users SET platform_user_id = $1 WHERE id = $2::uuid`,
		world.requesterPlatform, world.requester); err != nil {
		t.Fatalf("give the requester a platform id: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `UPDATE users SET platform_user_id = NULL WHERE id = $1::uuid`, world.requester)
		_, _ = pool.Exec(c, `DELETE FROM invoice_lines WHERE invoice_id = $1::uuid`, world.inv)
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, world.cust)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, world.cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, world.cust)
		_, _ = pool.Exec(c, `UPDATE locations SET org_id = NULL WHERE org_id = $1::uuid`, cmRoundNorthOrg)
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1::uuid`, cmRoundNorthOrg)
	})
	return world
}

// cmRoundService builds the route's service over the REAL parker, the REAL ledger and the REAL rail,
// exactly as the composition root wires them.
func cmRoundService(t *testing.T, pool *pgxpool.Pool) (*billing.CreditMemoService, *identity.Approvals) {
	t.Helper()
	w := billing.NewInvoiceWriter(pool)
	rules := gl.NewRepository(pool)
	glSvc := gl.NewService(gl.NewRepository(pool))
	approvals := identity.NewApprovals(identity.NewApprovalRepository(pool)).
		WithJEDecider(jeDeciderAdapter{svc: glSvc, memos: w})
	svc := billing.NewCreditMemoService(w, slog.Default()).
		WithPostingRules(creditMemoRulesAdapter{rules: rules}).
		WithThreshold(creditMemoThresholdAdapter{rules: rules}).
		WithParker(creditMemoParkerAdapter{pool: pool, w: w, gl: glSvc, rules: rules, approvals: approvals})
	return svc, approvals
}

func cmRoundParkCredit(t *testing.T, pool *pgxpool.Pool, svc *billing.CreditMemoService, requester, cust, inv, key string) billing.CreditMemoRouteResult {
	t.Helper()
	ctx := identity.ContextWithStaffUserID(context.Background(), requester)
	res, err := svc.Issue(ctx, billing.CreditMemoRouteRequest{
		CustomerID: cust, BranchID: trentonBranchID, InvoiceID: inv,
		AmountCents: 0, Reason: "Goods came back", IdempotencyKey: key,
	}, billing.CreditMemoCaller{OrgID: cmRoundNorthOrg, Scope: identity.ScopeGlobal})
	if err != nil {
		t.Fatalf("park the credit: %v", err)
	}
	if res.Status != billing.CreditMemoAwaitingApproval {
		t.Fatalf("the credit is %q, want AWAITING_APPROVAL", res.Status)
	}
	return res
}

func cmRoundDecider() identity.StaffContext {
	return identity.StaffContext{
		UserID: "cm-round-decider", RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, Branches: map[string]bool{},
	}
}

// TestIntegration_CreditMemoRound_TheRealRailPostsAParkedCredit walks the Controller's decision end
// to end: park through the real parker, approve on the real rail, and the credit is a FINALIZED
// memo, one subledger row and a POSTED reversing journal that credits 1100 by the total.
func TestIntegration_CreditMemoRound_TheRealRailPostsAParkedCredit(t *testing.T) {
	pool := cmRoundPool(t)
	world := cmRoundWorldPlant(t, pool)
	svc, approvals := cmRoundService(t, pool)

	res := cmRoundParkCredit(t, pool, svc, world.requester, world.cust, world.inv, "cm-rail-approve")
	decider := cmRoundDecider()
	decider.UserID = world.decider
	if err := approvals.DecideApproval(context.Background(), res.ApprovalID, decider, true, "agreed for the round"); err != nil {
		t.Fatalf("fw1-12a core red: approving a parked credit on the real rail failed: %v", err)
	}

	ctx := context.Background()
	var status string
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT status, balance_cents FROM invoices WHERE id = $1::uuid`, res.CreditMemoID).
		Scan(&status, &balance); err != nil {
		t.Fatalf("read the approved memo: %v", err)
	}
	if status != "FINALIZED" || balance != -678_000 {
		t.Fatalf("the approved credit reads %s/%d, want FINALIZED/-678000", status, balance)
	}
	var rows int
	var amount int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(SUM(amount_cents),0) FROM customer_transactions
		 WHERE customer_id = $1::uuid AND source_ref_id = $2::uuid`,
		world.cust, res.CreditMemoID).Scan(&rows, &amount); err != nil {
		t.Fatalf("read the subledger row: %v", err)
	}
	if rows != 1 || amount != -678_000 {
		t.Fatalf("the approved credit wrote %d subledger rows summing %d, want one row of -678000", rows, amount)
	}
	var journalStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM gl_journals WHERE idempotency_key = $1`,
		billing.CreditMemoJournalKeyPrefix+res.CreditMemoID).Scan(&journalStatus); err != nil {
		t.Fatalf("read the reversing journal: %v", err)
	}
	if journalStatus != "POSTED" {
		t.Fatalf("the reversing journal reads %s, want POSTED", journalStatus)
	}
	var arNet int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(COALESCE(l.debit_cents, 0) - COALESCE(l.credit_cents, 0)), 0) FROM gl_journal_lines l
		 JOIN gl_accounts a ON a.id = l.account_id
		 JOIN gl_journals j ON j.id = l.journal_id
		 WHERE j.idempotency_key = $1 AND a.code = '1100'`,
		billing.CreditMemoJournalKeyPrefix+res.CreditMemoID).Scan(&arNet); err != nil {
		t.Fatalf("read the 1100 legs: %v", err)
	}
	if arNet != -678_000 {
		t.Fatalf("the reversing entry moves 1100 by %d, want -678000 (the credit's total)", arNet)
	}
	store := billing.NewAgingStore(pool, nil)
	entities, _, err := store.Pull(ctx, sync.ScopeFilter{Tenant: true, TenantID: world.cust}, 0, 500)
	if err != nil {
		t.Fatalf("pull the aging feed: %v", err)
	}
	found := false
	for _, e := range entities {
		found = found || e.ID == res.CreditMemoID
	}
	if !found {
		t.Fatal("the approved credit did not reach the staff sync feed")
	}
}

// TestIntegration_CreditMemoRound_ARefusedParkVoidsTheMemoAndFreesTheRoom is the refuse half: the
// decision voids the DRAFT memo and its DRAFT journal, and a later credit of the invoice is no
// longer blocked by the refused one.
func TestIntegration_CreditMemoRound_ARefusedParkVoidsTheMemoAndFreesTheRoom(t *testing.T) {
	pool := cmRoundPool(t)
	world := cmRoundWorldPlant(t, pool)
	svc, approvals := cmRoundService(t, pool)

	res := cmRoundParkCredit(t, pool, svc, world.requester, world.cust, world.inv, "cm-rail-refuse")
	decider := cmRoundDecider()
	decider.UserID = world.decider
	if err := approvals.DecideApproval(context.Background(), res.ApprovalID, decider, false, "not warranted"); err != nil {
		t.Fatalf("refuse the parked credit: %v", err)
	}

	ctx := context.Background()
	var memoStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM invoices WHERE id = $1::uuid`, res.CreditMemoID).
		Scan(&memoStatus); err != nil {
		t.Fatalf("read the refused memo: %v", err)
	}
	if memoStatus != "VOID" {
		t.Fatalf("fw1-12a core red: the refused credit's memo reads %s, want VOID (the room must free)", memoStatus)
	}
	var journalStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM gl_journals WHERE idempotency_key = $1`,
		billing.CreditMemoJournalKeyPrefix+res.CreditMemoID).Scan(&journalStatus); err != nil {
		t.Fatalf("read the refused journal: %v", err)
	}
	if journalStatus != "VOID" {
		t.Fatalf("fw1-12a core red: the refused credit's journal reads %s, want VOID", journalStatus)
	}
	// The room is freed: a small posted credit of the invoice goes through.
	issueCtx := identity.ContextWithStaffUserID(ctx, world.requester)
	follow, err := svc.Issue(issueCtx, billing.CreditMemoRouteRequest{
		CustomerID: world.cust, BranchID: trentonBranchID, InvoiceID: world.inv,
		AmountCents: 100_000, Reason: "The next credit", IdempotencyKey: "cm-rail-after-refuse",
	}, billing.CreditMemoCaller{OrgID: cmRoundNorthOrg, Scope: identity.ScopeGlobal})
	if err != nil {
		t.Fatalf("fw1-12a core red: the refused park held the invoice's room; the next credit answered %v", err)
	}
	if follow.Status != billing.CreditMemoIssued {
		t.Fatalf("the next credit is %q, want ISSUED", follow.Status)
	}
}

// TestIntegration_CreditMemoRound_ADeclineBridgesNothingForTheLedgersOwnJournal pins the shipped rule
// where the core round moved it: the adapter's reject arm no-ops for a journal that is not a credit
// memo's, so the ledger's own parked journal stays DRAFT on a decline, the maker's own act to void.
func TestIntegration_CreditMemoRound_ADeclineBridgesNothingForTheLedgersOwnJournal(t *testing.T) {
	pool := cmRoundPool(t)
	world := cmRoundWorldPlant(t, pool)
	_, approvals := cmRoundService(t, pool)

	glSvc := gl.NewService(gl.NewRepository(pool)).WithApprovalFiler(jeApprovalFilerAdapter{svc: approvals})
	maker := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: world.requester, RoleCode: "FINANCIAL_CONTROLLER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: identity.StatusActive, Branches: map[string]bool{},
	})
	j, err := glSvc.CreateJournal(maker, gl.CreateJournalInput{
		Memo: "a parked manual journal", BranchID: trentonBranchID,
		Lines: []gl.JournalLineInput{
			{AccountCode: "1000", DebitCents: 600_000},
			{AccountCode: "4000", CreditCents: 600_000},
		},
	})
	if err != nil {
		t.Fatalf("park the manual journal: %v", err)
	}
	if j.Status != "DRAFT" {
		t.Fatalf("the manual journal reads %s, want DRAFT (over the threshold)", j.Status)
	}
	var reqID string
	if err := pool.QueryRow(context.Background(),
		`SELECT id::text FROM permission_approval_requests WHERE subject_ref = $1 AND status = 'PENDING'`,
		j.ID).Scan(&reqID); err != nil {
		t.Fatalf("read the journal's request: %v", err)
	}
	decider := cmRoundDecider()
	decider.UserID = world.decider
	if err := approvals.DecideApproval(context.Background(), reqID, decider, false, "not warranted"); err != nil {
		t.Fatalf("decline the manual journal: %v", err)
	}
	var status string
	if err := pool.QueryRow(context.Background(), `SELECT status FROM gl_journals WHERE id = $1`, j.ID).
		Scan(&status); err != nil {
		t.Fatalf("read the declined journal: %v", err)
	}
	if status != "DRAFT" {
		t.Fatalf("the ledger's own parked journal reads %s after a decline, want DRAFT (the maker's own act to void)", status)
	}
}
