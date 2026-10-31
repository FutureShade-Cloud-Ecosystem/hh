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
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
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
		_, _ = pool.Exec(c, `DELETE FROM invoices WHERE customer_id = $1`, e.customer)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, e.customer)
		_, _ = pool.Exec(c, `DELETE FROM customers WHERE id = $1`, other.CustomerID)
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
