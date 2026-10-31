//go:build integration

package customer

// terms_job_integration_test.go, the job half of the terms seam (wave 3 JA-2).
//
// TermsForJob answers for a customer and a job: the job's own code when it has one, else the
// customer's. The read is pinned against the real master, and it rides the caller's transaction
// when one is carried (the QD5 shape, PR 722 car 18's lesson).

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// -- wave 3 JA-2: the job half of the terms seam ---------------------------------------------

// ja2Trenton is the yard's own branch (migration 0003 plants it on every migrated database).
const ja2Trenton = "5d1b3f6e-0000-4000-9000-000000000001"

// TestIntegration_TermsForJob_TheJobsOwnCodeElseTheCustomers: TermsForJob answers for a customer
// and a job. The job's own days name a standard row of the master when they match one (15 is
// NET15) and answer the days with NO code when they do not (7), the same ruling the customer half
// carries; a job with no own terms answers exactly what TermsFor answers for its customer.
func TestIntegration_TermsForJob_TheJobsOwnCodeElseTheCustomers(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo).WithJobs(repo)

	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: tm1AccountNo("JA2-TERMS"), Name: "Job Terms Yard", PaymentTermsDays: termsDays(30),
	})
	if err != nil {
		t.Fatalf("create the customer: %v", err)
	}
	tm1Cleanup(t, pool, acct.CustomerID)
	if _, err := svc.SetTerms(ctx, acct.CustomerID, Terms2PctNet30); err != nil {
		t.Fatalf("put the customer on 2/10 net 30: %v", err)
	}
	own := 15
	odd := 7
	plant := func(jobNo string, days *int) Job {
		t.Helper()
		j, err := repo.InsertJob(ctx, JobWrite{
			CustomerID: acct.CustomerID, BranchID: ja2Trenton, JobNo: jobNo,
			Fields: JobFields{Name: "Driveway", TermsDays: days},
		})
		if err != nil {
			t.Fatalf("plant the job %s: %v", jobNo, err)
		}
		return j
	}

	if got, err := svc.TermsForJob(ctx, acct.CustomerID, plant("J-TJ-15", &own).JobID); err != nil {
		t.Fatalf("read the job's own terms: %v", err)
	} else if got.Code != TermsNet15 || got.NetDays != 15 {
		t.Fatalf("the job's own 15 days read as NET15/15: got %q/%d", got.Code, got.NetDays)
	}
	if got, err := svc.TermsForJob(ctx, acct.CustomerID, plant("J-TJ-7", &odd).JobID); err != nil {
		t.Fatalf("read the job's odd own terms: %v", err)
	} else if got.Code != "" || got.NetDays != 7 {
		t.Fatalf("the job's own 7 days answer the days with no code: got %q/%d", got.Code, got.NetDays)
	}
	customerTerms, err := svc.TermsFor(ctx, acct.CustomerID)
	if err != nil {
		t.Fatalf("read the customer's terms: %v", err)
	}
	if got, err := svc.TermsForJob(ctx, acct.CustomerID, plant("J-TJ-NONE", nil).JobID); err != nil {
		t.Fatalf("read the job with no own terms: %v", err)
	} else if got != customerTerms {
		t.Fatalf("a job with no own terms answers the customer's own terms: got %+v, want %+v", got, customerTerms)
	}

	// A job of another customer is not the customer's job, and the read says so: not found.
	other, err := svc.CreateCustomer(ctx, CreateInput{AccountNo: tm1AccountNo("JA2-OTHER"), Name: "Other Yard"})
	if err != nil {
		t.Fatalf("create the other customer: %v", err)
	}
	tm1Cleanup(t, pool, other.CustomerID)
	if _, err := svc.TermsForJob(ctx, other.CustomerID, plant("J-TJ-X", nil).JobID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a job of another customer answered %v, want not found", err)
	}
}

// TestIntegration_TermsForJob_RidesTheCallersTransaction: the job terms read rides the caller's
// transaction when one is carried (QD5's lesson, PR 722 car 18). Inside an open transaction that
// has JUST changed the job's terms and not committed, the read answers the uncommitted days, on
// that transaction, over a pool of exactly one connection: a read that went to the pool here would
// wait for a second connection the pool cannot give.
func TestIntegration_TermsForJob_RidesTheCallersTransaction(t *testing.T) {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		url = "postgres://hardscape:hardscape@localhost:5435/hardscapeos?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse the pool url: %v", err)
	}
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open the pool of one: %v", err)
	}
	defer pool.Close()
	ctx := context.Background()
	repo := NewRepository(pool)
	svc := NewService(repo).WithWriter(repo).WithJobs(repo)

	acct, err := svc.CreateCustomer(ctx, CreateInput{
		AccountNo: tm1AccountNo("JA2-TX"), Name: "One Transaction Yard", PaymentTermsDays: termsDays(30),
	})
	if err != nil {
		t.Fatalf("create the customer: %v", err)
	}
	tm1Cleanup(t, pool, acct.CustomerID)
	own := 15
	j, err := repo.InsertJob(ctx, JobWrite{
		CustomerID: acct.CustomerID, BranchID: ja2Trenton, JobNo: "J-TX",
		Fields: JobFields{Name: "Driveway", TermsDays: &own},
	})
	if err != nil {
		t.Fatalf("plant the job: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the caller's transaction: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The caller's own uncommitted change: 15 becomes 7 inside the transaction.
	if _, err := tx.Exec(ctx, `UPDATE customer_jobs SET terms_days = 7 WHERE id = $1`, j.JobID); err != nil {
		t.Fatalf("change the job's terms on the transaction: %v", err)
	}
	got, err := svc.TermsForJob(WithCallerTx(ctx, tx), acct.CustomerID, j.JobID)
	if err != nil {
		t.Fatalf("read the terms on the caller's transaction: %v", err)
	}
	if got.NetDays != 7 || got.Code != "" {
		t.Fatalf("the read answered the caller's own uncommitted days: got %q/%d, want \"\"/7", got.Code, got.NetDays)
	}
}
