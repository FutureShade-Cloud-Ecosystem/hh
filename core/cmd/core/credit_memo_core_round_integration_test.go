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
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/branches"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/sync"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	cmRoundNorthOrg = "ce110000-0000-4000-8000-0000000000d1"
	// cmRoundLeftOrg is the org a leg of another package can leave a branch on when it does not
	// restore what it stamped (FLAKE-4's leak, the leaver never named).
	cmRoundLeftOrg = "ce110000-0000-4000-8000-0000000000d2"
	// The round's own staff rows, planted and deleted again. Taking the first user by id and
	// overwriting its platform_user_id (FLAKE-5) made the round's requester the fxstaff fixture's
	// inside sales clerk whenever that row sat lowest by id, and the cleanup NULLed the very id
	// every later fxstaff inside sales leg resolves its clerk by.
	cmRoundRequesterID = "ce110000-0000-4000-8000-0000000000e1"
	cmRoundDeciderID   = "ce110000-0000-4000-8000-0000000000e2"
	// The platform subjects the two planted rows carry for the round.
	cmRoundRequesterPlatform = "cm-round-actor"
	cmRoundDeciderPlatform   = "cm-round-decider"
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

// cmRoundWorld plants what one rail leg needs: the org on the branches, a customer, one invoice
// with room to credit (its total sits over the seeded threshold, so the credit parks), and two
// staff users the fixture plants itself (the requester and the decider; the four eyes needs both).
//
// The fixture touches only rows it owns. The users are its own, deleted again with the leg: an
// earlier plant took the first user by id, overwrote that user's platform_user_id and NULLed it on
// cleanup, which is the fixture's own field to set, never a borrowed user's (FLAKE-5). The
// branches are all stamped with the round's org and each one's prior org_id is restored exactly at
// the end, NULL or not: stamping only the orgless branches left a branch some earlier leg had left
// on another org off the stamp, and the fence refused the round's own park (FLAKE-4).
func cmRoundWorldPlant(t *testing.T, pool *pgxpool.Pool) cmRoundWorld {
	t.Helper()
	ctx := context.Background()
	world := cmRoundWorld{org: cmRoundNorthOrg}
	if _, err := pool.Exec(ctx, `
		INSERT INTO org (id, slug, name) VALUES ($1::uuid, 'cm-round-rail', 'Credit Round Rail')
		ON CONFLICT (id) DO NOTHING`, cmRoundNorthOrg); err != nil {
		t.Fatalf("plant the rail org: %v", err)
	}
	type cmRoundBranchOrg struct {
		id    string
		prior *string
	}
	var branchOrgs []cmRoundBranchOrg
	rows, err := pool.Query(ctx, `SELECT id::text, org_id::text FROM locations`)
	if err != nil {
		t.Fatalf("read the branches' orgs: %v", err)
	}
	for rows.Next() {
		var b cmRoundBranchOrg
		if err := rows.Scan(&b.id, &b.prior); err != nil {
			rows.Close()
			t.Fatalf("read a branch's org: %v", err)
		}
		branchOrgs = append(branchOrgs, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("read the branches' orgs: %v", err)
	}
	// The cleanups register in the order the rows exist, and each one only
	// after its own row or stamp is in place: the branch restore goes before
	// the first update, so a failure between the snapshot and any later step
	// still gives every branch its prior org back, and a row's delete never
	// registers ahead of that row's successful insert (an insert conflict
	// would then make the cleanup delete a row this plant did not own).
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM org WHERE id = $1::uuid`, cmRoundNorthOrg)
	})
	t.Cleanup(func() {
		c := context.Background()
		// Each branch returns to the exact org it carried before the stamp,
		// NULL or not: a prior NULL stays NULL and a non NULL prior is never
		// cleared.
		for _, b := range branchOrgs {
			_, _ = pool.Exec(c, `UPDATE locations SET org_id = $1 WHERE id = $2::uuid`, b.prior, b.id)
		}
	})
	for _, b := range branchOrgs {
		if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = $1::uuid WHERE id = $2::uuid`, cmRoundNorthOrg, b.id); err != nil {
			t.Fatalf("give the branches their org: %v", err)
		}
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		VALUES ('CMRAIL-' || floor(random() * 1e9)::text, 'Credit Round Rail', 'CONTRACTOR',
		        $1::uuid, 0)
		RETURNING id::text`, trentonBranchID).Scan(&world.cust); err != nil {
		t.Fatalf("seed the rail customer: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		_, _ = pool.Exec(c, `DELETE FROM customer_transactions WHERE customer_id = $1::uuid`, world.cust)
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, world.cust)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, world.cust)
	})
	if err := pool.QueryRow(ctx, `
		INSERT INTO invoices (invoice_no, customer_id, branch_id, org_id, status, issue_date, due_date,
		                      subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at)
		VALUES ('CMRAIL-INV-' || floor(random() * 1e9)::text, $1::uuid, $2::uuid,
		        (SELECT org_id FROM locations WHERE id = $2::uuid), 'FINALIZED',
		        '1990-01-15', '1990-01-15', 600_000, 78_000, 678_000, 678_000, now())
		RETURNING id::text`, world.cust, trentonBranchID).Scan(&world.inv); err != nil {
		t.Fatalf("seed the rail invoice: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM invoice_lines WHERE invoice_id = $1::uuid`, world.inv)
	})
	// The round's own requester and decider, planted here and deleted again at the end. The
	// withdrawal leg's store resolves the actor through the platform user id, so the requester
	// carries the round's own; no seeded row and no other fixture's row is ever taken or touched.
	if _, err := pool.Exec(ctx, `
		INSERT INTO users (id, email, display_name, status, password_hash, platform_user_id)
		VALUES ($1::uuid, 'cm-round-requester@test.invalid', 'Credit Round Requester', 'ACTIVE', NULL, $2),
		       ($3::uuid, 'cm-round-decider@test.invalid', 'Credit Round Decider', 'ACTIVE', NULL, $4)`,
		cmRoundRequesterID, cmRoundRequesterPlatform, cmRoundDeciderID, cmRoundDeciderPlatform); err != nil {
		t.Fatalf("plant the round's requester and decider: %v", err)
	}
	t.Cleanup(func() {
		// The round's own rows only: the two planted users go (their approval
		// requests cascade, the decided_by and journal actor stamps fall NULL
		// on their own).
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id IN ($1::uuid, $2::uuid)`,
			cmRoundRequesterID, cmRoundDeciderID)
	})
	world.requester = cmRoundRequesterID
	world.requesterPlatform = cmRoundRequesterPlatform
	world.decider = cmRoundDeciderID
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
		WithJEDecider(jeDeciderAdapter{svc: glSvc, journals: rules, memos: w})
	svc := billing.NewCreditMemoService(w, slog.Default()).
		WithPostingRules(creditMemoRulesAdapter{rules: rules}).
		WithThreshold(creditMemoThresholdAdapter{rules: rules}).
		WithParker(creditMemoParkerAdapter{pool: pool, w: w, gl: glSvc, rules: rules, approvals: approvals}).
		// The fence's org reads, wired as newCreditMemoHandler wires them: without them the fence
		// fails closed and every park here is refused.
		WithBranchOrg(branches.NewRepository(pool)).
		WithCustomerHome(customer.NewRepository(pool))
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
	// The reversing journal is the round's own row: it is posted under the memo's key and it
	// is read again by the legs below, so it is taken back only when the leg ends. The lines
	// cascade with the journal.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE idempotency_key = $1`,
			billing.CreditMemoJournalKeyPrefix+res.CreditMemoID)
	})
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
	// The refused memo's journal is the round's own row too: it is voided, not deleted, and a
	// voided journal on the shared gate is still a line no later package can decide.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE idempotency_key = $1`,
			billing.CreditMemoJournalKeyPrefix+res.CreditMemoID)
	})
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
	// The ledger's own parked journal is the round's own row as well: a decline never bridges
	// it, so the maker's own act to void it is never coming, and a DRAFT journal left on the
	// shared gate is the line gl's deactivation leg reads later in the suite's go list order.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE id = $1::uuid`, j.ID)
	})
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

// TestIntegration_CreditMemoRound_ABranchLeftOnAnotherOrgStillParks pins the fence against the
// leftover: a leg of an earlier package can leave Trenton on its own org and never give it back
// (FLAKE-4), and the round that runs after must still park its credit, then restore that branch to
// exactly the org it found, never a NULL and never the round's own.
func TestIntegration_CreditMemoRound_ABranchLeftOnAnotherOrgStillParks(t *testing.T) {
	pool := cmRoundPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		INSERT INTO org (id, slug, name) VALUES ($1::uuid, 'cm-round-left', 'Credit Round Left')
		ON CONFLICT (id) DO NOTHING`, cmRoundLeftOrg); err != nil {
		t.Fatalf("plant the leftover org: %v", err)
	}
	var priorOrg *string
	if err := pool.QueryRow(ctx, `SELECT org_id::text FROM locations WHERE id = $1::uuid`, trentonBranchID).
		Scan(&priorOrg); err != nil {
		t.Fatalf("read Trenton's prior org: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE locations SET org_id = $1::uuid WHERE id = $2::uuid`,
		cmRoundLeftOrg, trentonBranchID); err != nil {
		t.Fatalf("leave Trenton on the leftover org: %v", err)
	}
	// Registered before the plant's own cleanup, so it runs after the plant has restored: this
	// asserts the restore was exact, then gives Trenton its own prior org back and removes the
	// leftover org row.
	t.Cleanup(func() {
		c := context.Background()
		var after *string
		if err := pool.QueryRow(c, `SELECT org_id::text FROM locations WHERE id = $1::uuid`, trentonBranchID).
			Scan(&after); err != nil {
			t.Errorf("read Trenton's org after the round: %v", err)
		} else if after == nil || *after != cmRoundLeftOrg {
			t.Errorf("the round left Trenton on %v, want the leftover org restored exactly", after)
		}
		if _, err := pool.Exec(c, `UPDATE locations SET org_id = $1 WHERE id = $2::uuid`, priorOrg, trentonBranchID); err != nil {
			t.Errorf("give Trenton its prior org back: %v", err)
		}
		_, _ = pool.Exec(c, `DELETE FROM org WHERE id = $1::uuid`, cmRoundLeftOrg)
	})
	world := cmRoundWorldPlant(t, pool)
	svc, _ := cmRoundService(t, pool)

	// On the unfixed plant the stamp skips Trenton (its org is not NULL), so the caller's org and
	// the branch's disagree and the fence refuses the park here.
	res := cmRoundParkCredit(t, pool, svc, world.requester, world.cust, world.inv, "cm-rail-left-org")
	// The park is never decided, so its journal leaves as it stands: a parked DRAFT with a 1100
	// line. The round takes that journal back (the lines cascade with it), because a DRAFT journal
	// line on 1100 left on the shared gate database is exactly what gl's FW1-09a deactivation leg
	// reads later in the suite's go list order, and with the round's planted users gone the
	// journal's approval request is gone with them, so nothing could ever decide it.
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM gl_journals WHERE idempotency_key = $1`,
			billing.CreditMemoJournalKeyPrefix+res.CreditMemoID)
	})
}
