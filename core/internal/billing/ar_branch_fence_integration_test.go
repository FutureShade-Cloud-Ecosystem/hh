//go:build integration

// ar_branch_fence_integration_test.go, the receivables reads answer the caller's branch reach only.
//
// A branch scoped caller (a branch manager, a selling role with Money Desk read) holds grants to one
// branch. Every AR read (the register and its detail, the selection total, the aging, the holds, the
// landing and its paper path) must answer for that reach and no other, whether the caller names
// another branch or names none. Payables and Receivables is global and sees the whole book.
//
// Every fact is created by the test inside its own customers and torn down; the reads are asserted
// against those customers, so nothing the seed or another test wrote can move a number here.
package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/jackc/pgx/v5/pgxpool"
)

type arFenceWorld struct {
	pool *pgxpool.Pool
	// dual has an open invoice in each branch; kingOnly has one in Kingston alone, and a hold.
	dual, kingOnly string
	dualTrenton    string // invoice ids
	dualKingston   string
	kingOnlyInv    string
}

func newARFenceWorld(t *testing.T) *arFenceWorld {
	t.Helper()
	pool := intPool(t)
	ctx := context.Background()
	tag := "ARF-" + randomTag()
	w := &arFenceWorld{pool: pool}

	mkCustomer := func(label string) string {
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents, payment_terms_days)
			 VALUES ($1, $2, 'CONTRACTOR', $3::uuid, 900000, 30) RETURNING id::text`,
			tag+"-"+label, "AR fence "+tag+" "+label, trentonBranch).Scan(&id); err != nil {
			t.Fatalf("seed customer %s: %v", label, err)
		}
		return id
	}
	mkInvoice := func(customerID, branch, label string, balance int64) string {
		due := d510Now.AddDate(0, 0, -100)
		issue := due.AddDate(0, 0, -30)
		var id string
		if err := pool.QueryRow(ctx,
			`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date,
			                       subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, doc_type)
			 VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', $4::date, $5::date, $6, 0, $6, $6, $7, 'INVOICE')
			 RETURNING id::text`,
			tag+"-"+label, customerID, branch, issue.Format("2006-01-02"), due.Format("2006-01-02"),
			balance, issue).Scan(&id); err != nil {
			t.Fatalf("seed invoice %s: %v", label, err)
		}
		return id
	}
	w.dual = mkCustomer("dual")
	w.kingOnly = mkCustomer("king")
	w.dualTrenton = mkInvoice(w.dual, trentonBranch, "T", 10000)
	w.dualKingston = mkInvoice(w.dual, kingstonBranch, "K", 70000)
	w.kingOnlyInv = mkInvoice(w.kingOnly, kingstonBranch, "KO", 50000)
	if _, _, err := NewRepository(pool).PlaceHold(ctx, w.kingOnly, HoldCreditLimit, "fence test", ""); err != nil {
		t.Fatalf("place hold: %v", err)
	}

	t.Cleanup(func() {
		c := context.Background()
		for _, cid := range []string{w.dual, w.kingOnly} {
			_, _ = pool.Exec(c, `DELETE FROM ar_promises_to_pay WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM credit_holds WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1::uuid`, cid)
			_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1::uuid`, cid)
		}
	})
	return w
}

func arTrentonOnly() context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "", RoleCode: "BRANCH_MANAGER", Tier: identity.TierManager, Scope: identity.ScopeBranch,
		Status: "ACTIVE", Branches: map[string]bool{trentonBranch: true},
	})
}

func arGlobalPayRec() context.Context {
	return identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "", RoleCode: "PAYABLES_RECEIVABLES", Tier: identity.TierManager, Scope: identity.ScopeGlobal,
		Status: "ACTIVE",
	})
}

func (w *arFenceWorld) registerService(ctx context.Context) *RegisterService {
	return NewRegisterService(NewRepository(w.pool)).
		WithClock(func() time.Time { return d510Now }).
		withCaller(identity.StaffContextFromContext)
}

func registerIDs(p RegisterPage) map[string]bool {
	out := map[string]bool{}
	for _, r := range p.Items {
		out[r.ID] = true
	}
	return out
}

func TestARFence_RegisterAnswersTheCallersBranchesOnly(t *testing.T) {
	w := newARFenceWorld(t)
	svc := w.registerService(arTrentonOnly())

	// The caller names no branch: the answer is its reach, not the book.
	page, err := svc.Register(arTrentonOnly(), RegisterFilter{CustomerID: w.dual, Limit: 50})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	ids := registerIDs(page)
	if !ids[w.dualTrenton] || ids[w.dualKingston] {
		t.Fatalf("a Trenton only caller must see the Trenton invoice and not the Kingston one, got %v", ids)
	}
	if page.Total != 1 || page.Counts.Open != 1 || page.Counts.Overdue != 1 {
		t.Fatalf("the total and the chips must count the reach only, got total %d open %d overdue %d",
			page.Total, page.Counts.Open, page.Counts.Overdue)
	}

	// An account that lives wholly in another branch is not on the page at all.
	page, err = svc.Register(arTrentonOnly(), RegisterFilter{CustomerID: w.kingOnly, Limit: 50})
	if err != nil {
		t.Fatalf("register king only: %v", err)
	}
	if len(page.Items) != 0 || page.Total != 0 {
		t.Fatalf("another branch's account must not read, got %d rows", len(page.Items))
	}

	// Naming another branch is a forbidden question, not an empty answer.
	if _, err := svc.Register(arTrentonOnly(), RegisterFilter{BranchID: kingstonBranch, Limit: 50}); !errors.Is(err, ErrBranchForbidden) {
		t.Fatalf("naming a branch outside the reach must be refused, got %v", err)
	}

	// Payables and Receivables is global: the whole book, either branch.
	page, err = svc.Register(arGlobalPayRec(), RegisterFilter{CustomerID: w.dual, Limit: 50})
	if err != nil {
		t.Fatalf("register global: %v", err)
	}
	if ids := registerIDs(page); !ids[w.dualTrenton] || !ids[w.dualKingston] {
		t.Fatalf("a global caller must see both branches, got %v", ids)
	}
}

func TestARFence_AnInvoiceInAnotherBranchIsNotFoundNotForbidden(t *testing.T) {
	w := newARFenceWorld(t)
	svc := w.registerService(arTrentonOnly())

	if _, err := svc.Detail(arTrentonOnly(), w.dualKingston, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a Kingston invoice read by a Trenton caller must be not found, got %v", err)
	}
	if _, err := svc.Detail(arTrentonOnly(), w.dualTrenton, true); err != nil {
		t.Fatalf("the caller's own branch invoice must read: %v", err)
	}

	// The per invoice acts follow the same door.
	_, _, err := svc.AddPromise(arTrentonOnly(), PromiseInput{
		InvoiceID: w.dualKingston, PromisedCents: 1000, PromisedDate: "2026-09-30", PromisedBy: "x",
		IdempotencyKey: "arf-" + randomTag(),
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("a promise on another branch's invoice must be not found, got %v", err)
	}

	if _, err := svc.Detail(arGlobalPayRec(), w.dualKingston, true); err != nil {
		t.Fatalf("a global caller reads any branch's invoice: %v", err)
	}
}

func TestARFence_SelectionTotalsCountTheReachOnly(t *testing.T) {
	w := newARFenceWorld(t)
	svc := w.registerService(arTrentonOnly())

	sum, n, err := svc.Selection(arTrentonOnly(), []string{w.dualTrenton, w.dualKingston, w.kingOnlyInv})
	if err != nil {
		t.Fatalf("selection: %v", err)
	}
	if n != 1 || sum != 10000 {
		t.Fatalf("a selection must total the reach only (1, 10000), got (%d, %d)", n, sum)
	}
	sum, n, err = svc.Selection(arGlobalPayRec(), []string{w.dualTrenton, w.dualKingston, w.kingOnlyInv})
	if err != nil || n != 3 || sum != 130000 {
		t.Fatalf("a global selection totals all three (3, 130000), got (%d, %d, %v)", n, sum, err)
	}
}

func TestARFence_AgingAnswersTheReachOnly(t *testing.T) {
	w := newARFenceWorld(t)
	svc := NewCreditService(NewRepository(w.pool)).WithClock(func() time.Time { return d510Now })

	rows, err := svc.Aging(arTrentonOnly(), d510Now, "")
	if err != nil {
		t.Fatalf("aging: %v", err)
	}
	var dualTotal int64
	for _, r := range rows {
		if r.CustomerID == w.kingOnly {
			t.Fatalf("an account wholly in another branch must not appear in a Trenton caller's aging")
		}
		if r.CustomerID == w.dual {
			dualTotal = r.TotalCents
		}
	}
	if dualTotal != 10000 {
		t.Fatalf("the shared account's aging must total the reach only (10000), got %d", dualTotal)
	}
	if _, err := svc.Aging(arTrentonOnly(), d510Now, kingstonBranch); !errors.Is(err, ErrBranchForbidden) {
		t.Fatalf("naming another branch must be refused, got %v", err)
	}

	rows, err = svc.Aging(arGlobalPayRec(), d510Now, "")
	if err != nil {
		t.Fatalf("aging global: %v", err)
	}
	var sawKing bool
	for _, r := range rows {
		if r.CustomerID == w.kingOnly {
			sawKing = true
		}
		if r.CustomerID == w.dual && r.TotalCents != 80000 {
			t.Fatalf("a global caller sees the whole account (80000), got %d", r.TotalCents)
		}
	}
	if !sawKing {
		t.Fatalf("a global caller must see every account with a balance")
	}
}

func TestARFence_HoldsListTheReachOnly(t *testing.T) {
	w := newARFenceWorld(t)
	svc := NewCreditService(NewRepository(w.pool))

	holds, err := svc.ListHolds(arTrentonOnly(), "ACTIVE")
	if err != nil {
		t.Fatalf("holds: %v", err)
	}
	for _, h := range holds {
		if h.CustomerID == w.kingOnly {
			t.Fatalf("a hold on an account wholly in another branch must not list for a Trenton caller")
		}
	}
	holds, err = svc.ListHolds(arGlobalPayRec(), "ACTIVE")
	if err != nil {
		t.Fatalf("holds global: %v", err)
	}
	var saw bool
	for _, h := range holds {
		if h.CustomerID == w.kingOnly {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a global caller must see the hold")
	}
}

func TestARFence_LandingAnswersTheReachOnly(t *testing.T) {
	w := newARFenceWorld(t)
	svc := NewARLandingService(NewARLandingRepository(w.pool)).WithClock(func() time.Time { return d510Now })

	view, err := svc.Landing(arTrentonOnly(), "", d510Now)
	if err != nil {
		t.Fatalf("landing: %v", err)
	}
	for _, e := range view.Escalation {
		if e.CustomerID == w.kingOnly {
			t.Fatalf("the landing's queue must not carry an account wholly in another branch")
		}
	}
	if _, err := svc.Landing(arTrentonOnly(), kingstonBranch, d510Now); !errors.Is(err, ErrBranchForbidden) {
		t.Fatalf("naming another branch must be refused, got %v", err)
	}
	view, err = svc.Landing(arGlobalPayRec(), "", d510Now)
	if err != nil {
		t.Fatalf("landing global: %v", err)
	}
	var saw bool
	for _, e := range view.Escalation {
		if e.CustomerID == w.kingOnly {
			saw = true
		}
	}
	if !saw {
		t.Fatalf("a global caller's landing must carry the account")
	}
}

func TestARFence_StatementsFollowTheReach(t *testing.T) {
	w := newARFenceWorld(t)
	t.Cleanup(func() {
		c := context.Background()
		for _, cid := range []string{w.dual, w.kingOnly} {
			_, _ = w.pool.Exec(c, `DELETE FROM print_queue WHERE ref_id IN (SELECT id FROM ar_statements WHERE customer_id = $1::uuid)`, cid)
			_, _ = w.pool.Exec(c, `DELETE FROM ar_statement_runs WHERE id IN (SELECT run_id FROM ar_statements WHERE customer_id = $1::uuid)`, cid)
		}
	})
	svc := NewStatementService(NewRepository(w.pool))

	// An account wholly in another branch has no statement to pull for a Trenton caller.
	_, sts, err := svc.GenerateRun(arTrentonOnly(), RunModeInterim, "", d510Now, "", w.kingOnly)
	if err != nil {
		t.Fatalf("interim for another branch's account: %v", err)
	}
	if len(sts) != 0 {
		t.Fatalf("a Trenton caller must not generate a statement for an account wholly in Kingston, got %d", len(sts))
	}

	// A shared account's statement carries the reach's balance only, and the run is filed under the
	// caller's branch.
	run, sts, err := svc.GenerateRun(arTrentonOnly(), RunModeInterim, "", d510Now, "", w.dual)
	if err != nil || len(sts) != 1 {
		t.Fatalf("interim for the shared account: %d statements, %v", len(sts), err)
	}
	if sts[0].ClosingCents != 10000 {
		t.Fatalf("the statement must total the reach (10000), got %d", sts[0].ClosingCents)
	}
	if run.BranchID != trentonBranch {
		t.Fatalf("a single branch caller's run must be filed under its branch, got %q", run.BranchID)
	}
	if _, _, err := svc.GetRun(arTrentonOnly(), run.ID); err != nil {
		t.Fatalf("the caller reads its own run: %v", err)
	}

	// Naming another branch is refused.
	if _, _, err := svc.GenerateRun(arTrentonOnly(), RunModeMass, kingstonBranch, d510Now, ""); !errors.Is(err, ErrBranchForbidden) {
		t.Fatalf("a run for another branch must be refused, got %v", err)
	}

	// A global caller's run of the whole book is not readable by the branch caller; the global caller
	// reads it, and the other branch's latest statement stays out of the scoped caller's reach.
	gRun, gSts, err := svc.GenerateRun(arGlobalPayRec(), RunModeInterim, "", d510Now, "", w.kingOnly)
	if err != nil || len(gSts) != 1 {
		t.Fatalf("global interim: %d statements, %v", len(gSts), err)
	}
	if _, _, err := svc.GetRun(arTrentonOnly(), gRun.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a run outside the reach must be not found, got %v", err)
	}
	if _, _, err := svc.GetRun(arGlobalPayRec(), gRun.ID); err != nil {
		t.Fatalf("a global caller reads any run: %v", err)
	}
	if _, ok, err := svc.LatestStatement(arTrentonOnly(), w.kingOnly); err != nil || ok {
		t.Fatalf("another branch's account has no statement for the caller (ok %v, err %v)", ok, err)
	}
	if _, ok, err := svc.LatestStatement(arGlobalPayRec(), w.kingOnly); err != nil || !ok {
		t.Fatalf("a global caller reads any account's statement (ok %v, err %v)", ok, err)
	}
}
