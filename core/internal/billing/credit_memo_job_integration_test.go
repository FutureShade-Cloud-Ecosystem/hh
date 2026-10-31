//go:build integration

package billing

// credit_memo_job_integration_test.go, the money desk's direct document takes a job (wave 3 JA-2).
//
// A credit memo is an invoices row raised directly, with no order behind it, so it is the door the
// brief's "an invoice made directly takes a job" names. The job is fenced through the customer
// module's own read before anything moves, and the three refusals arrive in words.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/billing -run TestIntegration_CreditMemoJob -count=1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/guards"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// memoJobFence is the customer module's job read as the memo door's fence: the composition root's
// shape, written inline because the adapter lives in cmd/core.
type memoJobFence struct{ svc *customer.Service }

func (f memoJobFence) JobForNewDocument(ctx context.Context, customerID, jobID string) error {
	_, err := f.svc.JobForNewDocument(ctx, customerID, jobID)
	return err
}

// memoJobEnv is one customer with three jobs: one open at the caller's branch, one CLOSED, and one
// open at a branch the caller does not reach, plus a job under ANOTHER customer.
type memoJobEnv struct {
	customer string
	open     string
	closed   string
	far      string
	other    string
}

func memoJobPlant(t *testing.T, pool *pgxpool.Pool) memoJobEnv {
	t.Helper()
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	var e memoJobEnv
	if err := pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name, customer_type, home_branch_id, credit_limit_cents)
		VALUES ($1, 'Memo Job Yard', 'CONTRACTOR', $2::uuid, 0) RETURNING id::text`,
		"MJ-"+ns(t), trentonBranch).Scan(&e.customer); err != nil {
		t.Fatalf("plant the customer: %v", err)
	}
	other, err := customer.NewService(repo).WithWriter(repo).CreateCustomer(ctx, customer.CreateInput{
		AccountNo: "MJO-" + ns(t), Name: "Memo Other Yard"})
	if err != nil {
		t.Fatalf("plant the other customer: %v", err)
	}
	plant := func(customerID, branchID, jobNo string) customer.Job {
		j, err := repo.InsertJob(ctx, customer.JobWrite{
			CustomerID: customerID, BranchID: branchID, JobNo: jobNo,
			Fields: customer.JobFields{Name: "Driveway"},
		})
		if err != nil {
			t.Fatalf("plant the job %s: %v", jobNo, err)
		}
		return j
	}
	e.open = plant(e.customer, trentonBranch, "J-MJ-OPEN").JobID
	e.closed = plant(e.customer, trentonBranch, "J-MJ-CLOSED").JobID
	e.far = plant(e.customer, kingstonBranch, "J-MJ-FAR").JobID
	if _, err := pool.Exec(ctx, `UPDATE customer_jobs SET status = 'CLOSED' WHERE id = $1`, e.closed); err != nil {
		t.Fatalf("close the closed job: %v", err)
	}
	e.other = plant(other.CustomerID, trentonBranch, "J-MJ-OTHER").JobID
	t.Cleanup(func() {
		c := context.Background()
		// The leg leaves nothing behind (the review of PR 747, P3): the memos and their lines, then the
		// jobs, then the customers, and a delete that fails is an error, not a leftover for the next package.
		for _, id := range []string{e.customer, other.CustomerID} {
			for _, stmt := range []string{
				`DELETE FROM customer_transactions WHERE customer_id = $1`,
				`DELETE FROM credit_application_keys WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`,
				`DELETE FROM invoice_lines WHERE invoice_id IN (SELECT id FROM invoices WHERE customer_id = $1)`,
				// A memo's reversal points at its invoice, so the memos go before the invoices.
				`DELETE FROM invoices WHERE customer_id = $1 AND reverses_invoice_id IS NOT NULL`,
				`DELETE FROM invoices WHERE customer_id = $1`,
				`DELETE FROM customer_jobs WHERE customer_id = $1`,
				`DELETE FROM customers WHERE id = $1`,
			} {
				if _, err := pool.Exec(c, stmt, id); err != nil {
					t.Errorf("clean up %q: %v", stmt, err)
				}
			}
		}
	})
	return e
}

// TestIntegration_CreditMemoJob_TheMemoTakesAJobAndTheFenceRefusesInWords: the memo carries the
// job it was fenced against, and the three refusals are the customer module's own words: another
// customer's job, a closed job, and a job outside the caller's reach.
func TestIntegration_CreditMemoJob_TheMemoTakesAJobAndTheFenceRefusesInWords(t *testing.T) {
	pool := intPool(t)
	e := memoJobPlant(t, pool)
	plantCreditMemoOrgs(t, pool)
	repo := customer.NewRepository(pool)
	fence := memoJobFence{svc: customer.NewService(repo).WithJobs(repo)}
	svc := NewCreditMemoService(NewInvoiceWriter(pool), slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithBranchOrg(cmFixedBranchOrgs{trentonBranch: cmNorthOrgID, kingstonBranch: cmNorthOrgID}).
		WithCustomerHome(cmFixedHomes{e.customer: trentonBranch}).
		WithJobFence(fence)
	// The caller is stamped the way the route's own bridge stamps it, so the job fence sees a
	// staff caller whose reach is Trenton alone.
	ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "00000000-0000-4000-9000-000000000001", RoleCode: "FINANCIAL_CONTROLLER",
		Tier: identity.TierLeadership, Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{trentonBranch: true},
	})
	req := CreditMemoRouteRequest{
		CustomerID: e.customer, BranchID: trentonBranch,
		AmountCents: 5_000, Reason: "Pallet came back", JobID: e.open,
	}
	res, err := svc.Issue(ctx, req, cmTrentonOnlyCaller())
	if err != nil {
		t.Fatalf("issue the credit on the job: %v", err)
	}
	var jobID string
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(job_id::text, '') FROM invoices WHERE id = $1::uuid`, res.CreditMemoID).Scan(&jobID); err != nil {
		t.Fatalf("read the raised memo: %v", err)
	}
	if jobID != e.open {
		t.Fatalf("the memo carries the job it was fenced against: got %q, want %q", jobID, e.open)
	}

	for _, tc := range []struct {
		name  string
		jobID string
		words string
	}{
		{"another customer's job", e.other, "another customer's job"},
		{"a closed job", e.closed, "the job is closed"},
		{"a job outside the reach", e.far, "outside your reach"},
	} {
		_, err := svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch,
			AmountCents: 5_000, Reason: "Pallet came back", JobID: tc.jobID,
			IdempotencyKey: "mj-" + ns(t),
		}, cmTrentonOnlyCaller())
		if err == nil || !strings.Contains(err.Error(), tc.words) {
			t.Fatalf("%s refused with %v, want words naming %q", tc.name, err, tc.words)
		}
	}
}

// TestIntegration_CreditMemoJob_TheDoorAnswersAJobRefusalInWords: the refusals through the REAL
// route handler (the review of PR 747, P3-1): 400 and the customer module's words for the three
// mistakes, and 404 in words for a job id that is a uuid but no job, never a 500.
func TestIntegration_CreditMemoJob_TheDoorAnswersAJobRefusalInWords(t *testing.T) {
	pool := intPool(t)
	e := memoJobPlant(t, pool)
	plantCreditMemoOrgs(t, pool)
	repo := customer.NewRepository(pool)
	svc := NewCreditMemoService(NewInvoiceWriter(pool), slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithBranchOrg(cmFixedBranchOrgs{trentonBranch: cmNorthOrgID, kingstonBranch: cmNorthOrgID}).
		WithCustomerHome(cmFixedHomes{e.customer: trentonBranch}).
		WithJobFence(memoJobFence{svc: customer.NewService(repo).WithJobs(repo)})
	h := NewCreditMemoRouteHandler(svc)
	ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "00000000-0000-4000-9000-000000000001", RoleCode: "FINANCIAL_CONTROLLER",
		Tier: identity.TierLeadership, Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{trentonBranch: true},
	})
	for _, tc := range []struct {
		name   string
		jobID  string
		status int
		words  string
	}{
		{"another customer's job", e.other, http.StatusBadRequest, "that job is another customer's job"},
		{"a closed job", e.closed, http.StatusBadRequest, "the job is closed, so it takes no new document"},
		{"a job outside the reach", e.far, http.StatusBadRequest, "that job sits at a branch outside your reach"},
		{"a uuid that is no job", uuid.NewString(), http.StatusNotFound, "that job was not found"},
	} {
		raw, _ := json.Marshal(CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, AmountCents: 5_000,
			Reason: "Pallet came back", JobID: tc.jobID, IdempotencyKey: "mjw-" + ns(t),
		})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/ar/credit-memos", bytes.NewReader(raw)).WithContext(ctx)
		rec := httptest.NewRecorder()
		caller := guards.StaffCaller{}
		caller.OrgID = cmNorthOrgID
		caller.Scope = "branch"
		caller.Branches = []string{trentonBranch}
		h.issue(rec, req, caller)
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != tc.status || body.Error != tc.words {
			t.Fatalf("%s answered %d %q, want %d %q", tc.name, rec.Code, body.Error, tc.status, tc.words)
		}
	}
}

// memoJobSvc builds the credit memo service with the job fence bound, and the caller that reaches
// Trenton alone.
func memoJobSvc(t *testing.T, pool *pgxpool.Pool, e memoJobEnv) (*CreditMemoService, context.Context) {
	t.Helper()
	plantCreditMemoOrgs(t, pool)
	repo := customer.NewRepository(pool)
	svc := NewCreditMemoService(NewInvoiceWriter(pool), slog.Default()).
		WithPostingRules(creditMemoFixedRules{accounts: creditMemoRules}).
		WithBranchOrg(cmFixedBranchOrgs{trentonBranch: cmNorthOrgID, kingstonBranch: cmNorthOrgID}).
		WithCustomerHome(cmFixedHomes{e.customer: trentonBranch}).
		WithJobFence(memoJobFence{svc: customer.NewService(repo).WithJobs(repo)})
	ctx := identity.ContextWithStaff(context.Background(), identity.StaffContext{
		UserID: "00000000-0000-4000-9000-000000000001", RoleCode: "FINANCIAL_CONTROLLER",
		Tier: identity.TierLeadership, Scope: identity.ScopeBranch, Status: identity.StatusActive,
		Branches: map[string]bool{trentonBranch: true},
	})
	return svc, ctx
}

// memoJobInvoice seeds a finalized invoice of 1,000.00 for the customer on the named job ("" for none).
func memoJobInvoice(t *testing.T, pool *pgxpool.Pool, customerID, jobID string) string {
	t.Helper()
	inv := seedInvoice(t, pool, customerID, ns(t), invSpec{
		no: "INV", status: "FINALIZED", subtotal: 100_000, tax: 0,
		total: 100_000, balance: 100_000, finalized: true,
	})
	cmStampInvoiceOrg(t, pool, inv)
	if jobID != "" {
		if _, err := pool.Exec(context.Background(),
			`UPDATE invoices SET job_id = $2::uuid WHERE id = $1::uuid`, inv, jobID); err != nil {
			t.Fatalf("put the invoice on its job: %v", err)
		}
	}
	return inv
}

func memoJobOf(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()
	var jobID string
	if err := pool.QueryRow(context.Background(),
		`SELECT COALESCE(job_id::text, '') FROM invoices WHERE id = $1::uuid`, id).Scan(&jobID); err != nil {
		t.Fatalf("read the memo's job: %v", err)
	}
	return jobID
}

// TestIntegration_CreditMemoJob_AReversalCarriesTheInvoicesJob (the review of PR 747, P2-2): a memo
// that reverses an invoice on a job takes that job when the request names none, the closed job rule
// does not apply to a reversal of a document already on that job, and a request naming a different
// job than the invoice's is refused in words.
func TestIntegration_CreditMemoJob_AReversalCarriesTheInvoicesJob(t *testing.T) {
	pool := intPool(t)
	e := memoJobPlant(t, pool)
	svc, ctx := memoJobSvc(t, pool, e)
	repo := customer.NewRepository(pool)
	second, err := repo.InsertJob(context.Background(), customer.JobWrite{
		CustomerID: e.customer, BranchID: trentonBranch, JobNo: "J-MJ-SECOND",
		Fields: customer.JobFields{Name: "Back patio"},
	})
	if err != nil {
		t.Fatalf("plant the second open job: %v", err)
	}

	t.Run("the inherited job", func(t *testing.T) {
		inv := memoJobInvoice(t, pool, e.customer, e.open)
		res, err := svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, InvoiceID: inv,
			Reason: "Whole order came back", IdempotencyKey: "mji-" + ns(t),
		}, cmTrentonOnlyCaller())
		if err != nil {
			t.Fatalf("reverse the invoice with no job named: %v", err)
		}
		if got := memoJobOf(t, pool, res.CreditMemoID); got != e.open {
			t.Fatalf("the memo carries the reversed invoice's job: got %q, want %q", got, e.open)
		}
	})

	t.Run("a closed job's invoice reversed", func(t *testing.T) {
		inv := memoJobInvoice(t, pool, e.customer, e.closed)
		res, err := svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 40_000,
			Reason: "Part came back", IdempotencyKey: "mjc-" + ns(t),
		}, cmTrentonOnlyCaller())
		if err != nil {
			t.Fatalf("reverse an invoice on a closed job (ordinary, the document is already on it): %v", err)
		}
		if got := memoJobOf(t, pool, res.CreditMemoID); got != e.closed {
			t.Fatalf("the memo carries the closed job: got %q, want %q", got, e.closed)
		}
		// Naming that same closed job is the same reversal.
		res2, err := svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 10_000,
			Reason: "Another part came back", JobID: e.closed, IdempotencyKey: "mjc2-" + ns(t),
		}, cmTrentonOnlyCaller())
		if err != nil {
			t.Fatalf("name the invoice's own closed job: %v", err)
		}
		if got := memoJobOf(t, pool, res2.CreditMemoID); got != e.closed {
			t.Fatalf("the named memo carries the closed job: got %q, want %q", got, e.closed)
		}
	})

	t.Run("a different job refused", func(t *testing.T) {
		inv := memoJobInvoice(t, pool, e.customer, e.open)
		_, err := svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 10_000,
			Reason: "Moved to the wrong job", JobID: second.JobID, IdempotencyKey: "mjd-" + ns(t),
		}, cmTrentonOnlyCaller())
		if err == nil || !strings.Contains(err.Error(), "reverses an invoice on another job") {
			t.Fatalf("a different job than the invoice's: got %v, want words naming the invoice's job", err)
		}
	})

	t.Run("an invoice on no job takes a newly named job through the open rule", func(t *testing.T) {
		inv := memoJobInvoice(t, pool, e.customer, "")
		res, err := svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 10_000,
			Reason: "Name the job", JobID: e.open, IdempotencyKey: "mjn-" + ns(t),
		}, cmTrentonOnlyCaller())
		if err != nil {
			t.Fatalf("name a job on an invoice that has none: %v", err)
		}
		if got := memoJobOf(t, pool, res.CreditMemoID); got != e.open {
			t.Fatalf("got %q, want %q", got, e.open)
		}
		_, err = svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, InvoiceID: inv, AmountCents: 10_000,
			Reason: "Name a closed job", JobID: e.closed, IdempotencyKey: "mjn2-" + ns(t),
		}, cmTrentonOnlyCaller())
		if err == nil || !strings.Contains(err.Error(), "the job is closed") {
			t.Fatalf("a newly named closed job: got %v, want the closed words", err)
		}
	})
}

// TestIntegration_CreditMemoJob_TheKeyBelongsToTheJobToo (the review of PR 747, P3): the derived
// idempotency key takes the job into account, so the same amount and reason credited to two jobs of
// one customer on one house day are two credits, not the second replayed as the first; and a key a
// caller supplies, reused with a different job, is refused as a conflict rather than replayed.
//
// WHY the job is part of the key and of the body check: a credit on job A and a credit on job B are
// different credits (JA-3 and JA-4 read them by job), exactly as a different amount or invoice is.
// The EFFECTIVE job is used (the inherited one on a reversal), so a reversal asked with no job and
// the same reversal asked naming the invoice's job are one credit, as they should be.
func TestIntegration_CreditMemoJob_TheKeyBelongsToTheJobToo(t *testing.T) {
	pool := intPool(t)
	e := memoJobPlant(t, pool)
	svc, ctx := memoJobSvc(t, pool, e)
	repo := customer.NewRepository(pool)
	second, err := repo.InsertJob(context.Background(), customer.JobWrite{
		CustomerID: e.customer, BranchID: trentonBranch, JobNo: "J-MJ-KEY2",
		Fields: customer.JobFields{Name: "Side path"},
	})
	if err != nil {
		t.Fatalf("plant the second open job: %v", err)
	}
	ask := func(jobID, key string) (CreditMemoRouteResult, error) {
		return svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, AmountCents: 5_000,
			Reason: "Same amount, same reason", JobID: jobID, IdempotencyKey: key,
		}, cmTrentonOnlyCaller())
	}

	// No key: the derived key. Two jobs, one house day: two credits.
	a, err := ask(e.open, "")
	if err != nil {
		t.Fatalf("credit job A: %v", err)
	}
	b, err := ask(second.JobID, "")
	if err != nil {
		t.Fatalf("credit job B: %v", err)
	}
	if a.CreditMemoID == b.CreditMemoID {
		t.Fatalf("the second job's credit was answered with the first job's credit %q (a replay)", a.CreditMemoID)
	}
	if got := memoJobOf(t, pool, b.CreditMemoID); got != second.JobID {
		t.Fatalf("the second credit is on job %q, want %q", got, second.JobID)
	}
	// The same job again is still the one credit (the double click the key exists for).
	again, err := ask(e.open, "")
	if err != nil {
		t.Fatalf("ask job A again: %v", err)
	}
	if again.CreditMemoID != a.CreditMemoID {
		t.Fatalf("the same credit twice raised two documents: %q and %q", a.CreditMemoID, again.CreditMemoID)
	}

	// A supplied key, reused with a different job: a conflict, not a replay.
	key := "mjk-" + ns(t)
	if _, err := ask(e.open, key); err != nil {
		t.Fatalf("first use of the key: %v", err)
	}
	_, err = ask(second.JobID, key)
	var conflict *creditMemoKeyConflict
	if !errors.As(err, &conflict) {
		t.Fatalf("the key reused with a different job answered %v, want the key conflict", err)
	}
}

// TestIntegration_CreditMemoJob_AJobelessRetryReplays (the delta read of PR 747, P3-1): an account
// credit with no job, asked twice on one house day with no caller key, is one credit.
func TestIntegration_CreditMemoJob_AJobelessRetryReplays(t *testing.T) {
	pool := intPool(t)
	e := memoJobPlant(t, pool)
	svc, ctx := memoJobSvc(t, pool, e)
	ask := func() CreditMemoRouteResult {
		r, err := svc.Issue(ctx, CreditMemoRouteRequest{
			CustomerID: e.customer, BranchID: trentonBranch, AmountCents: 4_100,
			Reason: "Jobless retry",
		}, cmTrentonOnlyCaller())
		if err != nil {
			t.Fatalf("issue a jobless credit: %v", err)
		}
		return r
	}
	first, again := ask(), ask()
	if first.CreditMemoID != again.CreditMemoID {
		t.Fatalf("a jobless retry raised two credits: %q and %q", first.CreditMemoID, again.CreditMemoID)
	}
}

// TestIntegration_CreditMemoJob_ARacingLoserOnOneKeyGetsItsBodyChecked (the delta read of PR 747,
// P3-2): two requests race on one caller key with different jobs. One wins; the other is a key
// conflict, never the winner's memo handed back as its own.
func TestIntegration_CreditMemoJob_ARacingLoserOnOneKeyGetsItsBodyChecked(t *testing.T) {
	pool := intPool(t)
	e := memoJobPlant(t, pool)
	svc, ctx := memoJobSvc(t, pool, e)
	repo := customer.NewRepository(pool)
	second, err := repo.InsertJob(context.Background(), customer.JobWrite{
		CustomerID: e.customer, BranchID: trentonBranch, JobNo: "J-MJ-RACE2",
		Fields: customer.JobFields{Name: "Race path"},
	})
	if err != nil {
		t.Fatalf("plant the second open job: %v", err)
	}
	for round := 0; round < 8; round++ {
		key := "mjr-" + ns(t) + "-" + string(rune('a'+round))
		jobs := []string{e.open, second.JobID}
		type out struct {
			res CreditMemoRouteResult
			err error
		}
		outs := make([]out, 2)
		start := make(chan struct{})
		done := make(chan int, 2)
		for i := range jobs {
			go func(i int) {
				<-start
				r, err := svc.Issue(ctx, CreditMemoRouteRequest{
					CustomerID: e.customer, BranchID: trentonBranch, AmountCents: 5_000,
					Reason: "Race", JobID: jobs[i], IdempotencyKey: key,
				}, cmTrentonOnlyCaller())
				outs[i] = out{r, err}
				done <- i
			}(i)
		}
		close(start)
		<-done
		<-done
		wins, conflicts := 0, 0
		for _, o := range outs {
			var c *creditMemoKeyConflict
			switch {
			case o.err == nil:
				wins++
			case errors.As(o.err, &c):
				conflicts++
			default:
				t.Fatalf("round %d: unexpected error %v", round, o.err)
			}
		}
		if wins != 1 || conflicts != 1 {
			t.Fatalf("round %d: %d answered and %d conflicted, want one winner and one 409 (the loser was handed the winner's memo)", round, wins, conflicts)
		}
	}
}
