//go:build integration

// job_rules_integration_test.go, wave 3 JA-1: the job account's own rules.
//
// The split decided in wave 3 (FIN-WAVE3-PLAN 1.1) is one sentence: a job shares the customer's
// limit and terms unless it carries its own; the customer's exposure includes every job; a job
// limit never raises the customer's; a closed job takes no new document.
//
// This file pins the three rules the service owns, against the real migrated database:
//
//   - a job's own credit limit can never exceed the customer's own, and the refusal is in words;
//   - a job without its own terms reads the customer's terms, and a job with its own reads them;
//   - a CLOSED job is refused as the job of any new document.
//
// The ceiling is a service rule and not a table constraint on purpose: the customer's own limit may
// be edited later, and a constraint written today would then refuse a job that was fair when it was
// made. The table holds the figures, the service holds the rule.
//
// Run: DATABASE_URL=… go test -tags integration ./internal/customer/... -run TestIntegration_JobRules
package customer

import (
	"context"
	"errors"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/load"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/httpx"
)

// TestIntegration_JobRules_LimitAboveTheCustomerIsRefused is red leg 1: a job's own credit limit
// can never exceed the customer's own, because the customer's exposure includes every job it
// holds. The refusal is in words, and it carries no figures: the customer's limit is an account
// position figure, and a refusal that quoted it would hand over what the position policy hides.
func TestIntegration_JobRules_LimitAboveTheCustomerIsRefused(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithJobs(repo)

	f := plantJobWorld(t, pool)
	global := identity.StaffContext{UserID: f.userA, Scope: identity.ScopeGlobal}
	ctx = identity.ContextWithStaff(ctx, global)

	// The customer in the fixture carries a 500000 cent limit.
	above := int64(600000)
	_, err := svc.CreateJob(ctx, f.customerID, JobCreateInput{
		JobNo: "J-CEIL-1", Name: "Mill job", BranchID: f.branchA, CreditLimitCents: &above,
	})
	if err == nil {
		t.Fatal("a job limit above the customer's was accepted")
	}
	var bad httpx.BadRequestError
	if !errors.As(err, &bad) {
		t.Fatalf("the refusal is not a client error: %v", err)
	}
	if !strings.Contains(err.Error(), "credit limit") {
		t.Fatalf("the refusal is not in words: %v", err)
	}
	if strings.Contains(err.Error(), "500000") || strings.Contains(err.Error(), "600000") {
		t.Fatalf("the refusal quotes the figures, and the customer's limit is a position figure: %v", err)
	}

	// Nothing was written.
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM customer_jobs WHERE customer_id = $1 AND job_no = 'J-CEIL-1'`, f.customerID).Scan(&count); err != nil {
		t.Fatalf("count the jobs: %v", err)
	}
	if count != 0 {
		t.Fatalf("the refused job was written anyway: %d rows", count)
	}

	// A job at the customer's own limit is fair, and one below it is fair.
	atLimit := int64(500000)
	job, err := svc.CreateJob(ctx, f.customerID, JobCreateInput{
		JobNo: "J-CEIL-2", Name: "Mill job", BranchID: f.branchA, CreditLimitCents: &atLimit,
	})
	if err != nil {
		t.Fatalf("a job at the customer's own limit was refused: %v", err)
	}
	below := int64(1000)
	if _, err := svc.CreateJob(ctx, f.customerID, JobCreateInput{
		JobNo: "J-CEIL-3", Name: "Mill job", BranchID: f.branchA, CreditLimitCents: &below,
	}); err != nil {
		t.Fatalf("a job below the customer's own limit was refused: %v", err)
	}

	// A job with no own limit shares the customer's, and shares it whatever the customer has.
	if _, err := svc.CreateJob(ctx, f.customerID, JobCreateInput{
		JobNo: "J-CEIL-4", Name: "Mill job", BranchID: f.branchA,
	}); err != nil {
		t.Fatalf("a job that shares the customer's limit was refused: %v", err)
	}

	// The edit is ruled the same way: raising a job's own limit above the customer's is refused.
	raised := int64(700000)
	_, err = svc.EditJob(ctx, f.customerID, job.JobID, JobEditInput{CreditLimitCents: optionalInt64(raised)})
	if err == nil {
		t.Fatal("an edit raising the job's limit above the customer's was accepted")
	}
	var badEdit httpx.BadRequestError
	if !errors.As(err, &badEdit) {
		t.Fatalf("the edit refusal is not a client error: %v", err)
	}
	if !strings.Contains(err.Error(), "credit limit") {
		t.Fatalf("the edit refusal is not in words: %v", err)
	}
	after, err := svc.GetJob(ctx, f.customerID, job.JobID)
	if err != nil {
		t.Fatalf("read the job back: %v", err)
	}
	if after.CreditLimitCents == nil || *after.CreditLimitCents != atLimit {
		t.Fatalf("the refused edit changed the job: %v", after.CreditLimitCents)
	}
}

// optionalInt64 is an edit that sets a figure, the way a body would carry it.
func optionalInt64(v int64) OptionalValue[int64] {
	o := OptionalValue[int64]{Present: true}
	o.Value = &v
	return o
}

// optionalInt is an edit that sets a number of days.
func optionalInt(v int) OptionalValue[int] {
	o := OptionalValue[int]{Present: true}
	o.Value = &v
	return o
}

// TestIntegration_JobRules_EffectiveTermsAreRedLegsTwoAndThree: a job without its own terms reads
// the CUSTOMER's terms, and a job with its own terms reads its own. The read is the one the later
// waves use (JA-2 dates an invoice by the job's terms), so it is one read and it says which of the
// two it answered.
func TestIntegration_JobRules_EffectiveTermsAreRedLegsTwoAndThree(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithJobs(repo)

	f := plantJobWorld(t, pool)
	global := identity.StaffContext{UserID: f.userA, Scope: identity.ScopeGlobal}
	ctx = identity.ContextWithStaff(ctx, global)

	// The customer's own terms: the fixture's customer carries 30.
	acct, err := svc.GetAccount(ctx, f.customerID)
	if err != nil {
		t.Fatalf("read the customer: %v", err)
	}
	if acct.PaymentTermsDays != 30 {
		t.Fatalf("the fixture customer's terms are not 30: %d", acct.PaymentTermsDays)
	}

	// A job with no own terms reads the customer's.
	shared, err := svc.CreateJob(ctx, f.customerID, JobCreateInput{
		JobNo: "J-TERMS-1", Name: "Mill job", BranchID: f.branchA,
	})
	if err != nil {
		t.Fatalf("open the job that shares the customer's terms: %v", err)
	}
	terms, err := svc.EffectiveTerms(ctx, shared.JobID)
	if err != nil {
		t.Fatalf("read the effective terms of the job: %v", err)
	}
	if terms.TermsDays != acct.PaymentTermsDays {
		t.Fatalf("a job with no own terms read %d days, want the customer's %d", terms.TermsDays, acct.PaymentTermsDays)
	}
	if terms.Own {
		t.Fatal("a job with no own terms answered as if they were its own")
	}

	// A job with its own terms reads its own.
	own := 14
	ownJob, err := svc.CreateJob(ctx, f.customerID, JobCreateInput{
		JobNo: "J-TERMS-2", Name: "Mill job", BranchID: f.branchA, TermsDays: &own,
	})
	if err != nil {
		t.Fatalf("open the job with its own terms: %v", err)
	}
	terms, err = svc.EffectiveTerms(ctx, ownJob.JobID)
	if err != nil {
		t.Fatalf("read the effective terms of the job: %v", err)
	}
	if terms.TermsDays != own {
		t.Fatalf("a job with its own terms read %d days, want its own %d", terms.TermsDays, own)
	}
	if !terms.Own {
		t.Fatal("a job with its own terms answered as if they were the customer's")
	}

	// The terms the job is dated by are on the wire too, so a screen reads one number.
	job, err := svc.GetJob(ctx, f.customerID, shared.JobID)
	if err != nil {
		t.Fatalf("read the job: %v", err)
	}
	if job.EffectiveTermsDays != acct.PaymentTermsDays {
		t.Fatalf("the job's effective terms are not the customer's: %d", job.EffectiveTermsDays)
	}
}

// TestIntegration_JobRules_ClosedJobIsRefused is red leg 4: a CLOSED job is refused as the job of
// any new document. The refusal is the job's own status, read in one go, and it is not the same
// answer as a job that does not exist.
func TestIntegration_JobRules_ClosedJobIsRefused(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithJobs(repo)

	f := plantJobWorld(t, pool)
	global := identity.StaffContext{UserID: f.userA, Scope: identity.ScopeGlobal}
	ctx = identity.ContextWithStaff(ctx, global)

	job, err := svc.CreateJob(ctx, f.customerID, JobCreateInput{
		JobNo: "J-CLOSED-1", Name: "Mill job", BranchID: f.branchA,
	})
	if err != nil {
		t.Fatalf("open the job: %v", err)
	}

	// An open job takes a document.
	if err := svc.JobOpenForCustomer(ctx, f.customerID, job.JobID); err != nil {
		t.Fatalf("an open job was refused: %v", err)
	}

	// A closed job is not.
	if _, err := svc.CloseJob(ctx, f.customerID, job.JobID); err != nil {
		t.Fatalf("close the job: %v", err)
	}
	err = svc.JobOpenForCustomer(ctx, f.customerID, job.JobID)
	if !errors.Is(err, ErrJobClosed) {
		t.Fatalf("a closed job answered %v, want the closed refusal", err)
	}

	// A job of another customer is not the customer's job at all.
	other, err := svc.CreateJob(ctx, f.customerID, JobCreateInput{
		JobNo: "J-CLOSED-2", Name: "Mill job", BranchID: f.branchA,
	})
	if err != nil {
		t.Fatalf("open the second job: %v", err)
	}
	otherCustomer := plantJobCustomer(t, pool, "JA1-RULES-OTHER-"+randSuffix())
	if err := svc.JobOpenForCustomer(ctx, otherCustomer, other.JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a job of another customer answered %v, want not found", err)
	}
}

// TestIntegration_JobRules_RehomeMovesOpenAndClosedJobs proves that re-homing a customer moves
// its open and closed jobs to the new home in the same transaction, so the new home's caller
// lists both and count(DISTINCT branch_id) over the customer's jobs is 1.
func TestIntegration_JobRules_RehomeMovesOpenAndClosedJobs(t *testing.T) {
	bg := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithJobs(repo)

	f := plantJobWorld(t, pool)
	global := identity.ContextWithStaff(bg, identity.StaffContext{UserID: f.userA, Scope: identity.ScopeGlobal})
	acct, err := svc.GetAccount(global, f.customerID)
	if err != nil {
		t.Fatalf("read the customer: %v", err)
	}

	// Open a second job at branch A and close it, so the customer holds one OPEN job and one CLOSED job.
	second, err := svc.CreateJob(global, f.customerID, JobCreateInput{
		JobNo: "J-REHOME-2", Name: "Phase two", BranchID: f.branchA,
	})
	if err != nil {
		t.Fatalf("open the second job: %v", err)
	}
	if _, err := svc.CloseJob(global, f.customerID, second.JobID); err != nil {
		t.Fatalf("close the second job: %v", err)
	}

	rehome := func(rowID, branchID string) {
		t.Helper()
		tx, err := pool.Begin(bg)
		if err != nil {
			t.Fatalf("begin tx: %v", err)
		}
		defer func() { _ = tx.Rollback(bg) }()
		_, err = (CustomerLoadWriter{}).Apply(bg, tx, load.Write{
			OrgID:    fxLoadOrgID,
			LegacyID: "CUST-REHOME",
			RowID:    rowID,
			Record:   map[string]any{"account_no": acct.AccountNo, "name": acct.Name},
			Refs:     []load.RefMatch{{Kind: load.KindBranch, LegacyID: "BR", HHID: branchID}},
		})
		if err != nil {
			t.Fatalf("re-home customer (rowID=%q) to %s: %v", rowID, branchID, err)
		}
		if err := tx.Commit(bg); err != nil {
			t.Fatalf("commit re-home: %v", err)
		}
	}

	// Re-home via the attach arm (loadwriter.go:189, RowID == "") to branch B.
	rehome("", f.branchB)

	ctxB := identity.ContextWithStaff(bg, identity.StaffContext{UserID: f.userB, Scope: identity.ScopeBranch, Branches: map[string]bool{f.branchB: true}})
	jobsB, err := svc.ListJobs(ctxB, f.customerID)
	if err != nil {
		t.Fatalf("list jobs as new home B caller: %v", err)
	}
	if len(jobsB) != 2 {
		t.Fatalf("new home B caller listed %d jobs, want 2 (open and closed)", len(jobsB))
	}
	var distinctBranches int
	if err := pool.QueryRow(bg, `SELECT count(DISTINCT branch_id) FROM customer_jobs WHERE customer_id = $1`, f.customerID).Scan(&distinctBranches); err != nil {
		t.Fatalf("count distinct branches: %v", err)
	}
	if distinctBranches != 1 {
		t.Fatalf("distinct branch_id count after attach re-home = %d, want 1", distinctBranches)
	}

	// Re-home via the update arm (loadwriter.go:254, RowID == customerID) back to branch A.
	rehome(f.customerID, f.branchA)

	ctxA := identity.ContextWithStaff(bg, identity.StaffContext{UserID: f.userA, Scope: identity.ScopeBranch, Branches: map[string]bool{f.branchA: true}})
	jobsA, err := svc.ListJobs(ctxA, f.customerID)
	if err != nil {
		t.Fatalf("list jobs as new home A caller: %v", err)
	}
	if len(jobsA) != 2 {
		t.Fatalf("new home A caller listed %d jobs, want 2 (open and closed)", len(jobsA))
	}
	if err := pool.QueryRow(bg, `SELECT count(DISTINCT branch_id) FROM customer_jobs WHERE customer_id = $1`, f.customerID).Scan(&distinctBranches); err != nil {
		t.Fatalf("count distinct branches: %v", err)
	}
	if distinctBranches != 1 {
		t.Fatalf("distinct branch_id count after update re-home = %d, want 1", distinctBranches)
	}
}
