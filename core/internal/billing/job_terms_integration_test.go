//go:build integration

package billing

// job_terms_integration_test.go, the invoice dated by the JOB's own terms (wave 3 JA-2).
//
// A document on a job is dated by the job's own terms when the job carries them, and by the
// customer's when it does not. The read is the customer module's own (TermsForJob, the job half of
// the terms seam), bound to the writer exactly as the customer half is, and the legs below run
// against the REAL master behind it, not a stub: the job is planted through the customer module's
// repository and the writer reads it through the bound seam.
//
// The clock is the evening one (21:30 house time, the UTC day already rolled over), so every leg
// pins the house day boundary the evening guard reruns: the issue day is the house day and the due
// day counts the job's terms from that same day.
//
//  · a job with its own 7 days is due 7 house days out and carries NO code (no standard term
//    names 7, and a code reaches a document only when the master holds its row)
//  · a job with its own 15 days is due 15 house days out and carries NET15
//  · a job with no terms of its own reads the customer's own, discount window and all
//
// Run: DATABASE_URL=… go test -tags integration ./internal/billing -run TestIntegrationJobTerms -count=1

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customer"
)

// jobTermsSource is the order-facts seam answering one fixed billable order on a job, so the
// terms read and the writer's dating are the only things under test.
type jobTermsSource struct {
	customer, job, branch string
	terms                 int
}

func (s jobTermsSource) InvoiceableOrder(_ context.Context, _ string, _ []string) (OrderInvoiceFacts, error) {
	return OrderInvoiceFacts{
		CustomerID: s.customer, BranchID: s.branch, JobID: s.job, ClosesOrder: true, TermsDays: s.terms,
		Lines: []OrderInvoiceLine{{
			LineNo: 1, Description: "Pavers", QtyEntered: "1", UOMEntered: "EA", QtyBase: "1",
			UnitPriceCents: 5_000, PriceUOM: "EA", LineTotalCents: 5_000,
		}},
	}, nil
}

func (jobTermsSource) MarkGroupsInvoiced(context.Context, []string, string, time.Time) error {
	return nil
}

// TestIntegrationJobTerms_TheJobsOwnDaysDateTheDocumentAndACodeComesOnlyFromTheMaster: a job that
// carries its own days is dated by them on the house day. Seven days is no standard term, so the
// document carries NO code; fifteen is NET15, so it carries NET15. The caller's resolved terms (30)
// are overridden either way, which is the reason a job's terms may only ever arrive through the seam.
func TestIntegrationJobTerms_TheJobsOwnDaysDateTheDocumentAndACodeComesOnlyFromTheMaster(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	at := eveningClock()
	for _, tc := range []struct {
		name     string
		ownDays  int
		wantDue  string
		wantCode string // "" means the document carries NO code
	}{
		{name: "seven days, no standard term names them", ownDays: 7, wantDue: "2026-01-11", wantCode: ""},
		{name: "fifteen days, NET15 names them", ownDays: 15, wantDue: "2026-01-19", wantCode: "NET15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cust := seedCustomer(t, pool, ns(t))
			days := tc.ownDays
			job, err := repo.InsertJob(ctx, customer.JobWrite{
				CustomerID: cust, BranchID: trentonBranch, JobNo: "J-TERMS-" + ns(t),
				Fields: customer.JobFields{Name: "Driveway", TermsDays: &days},
			})
			if err != nil {
				t.Fatalf("plant the job: %v", err)
			}
			w := NewInvoiceWriter(pool).
				WithOrderSource(jobTermsSource{customer: cust, job: job.JobID, branch: trentonBranch, terms: 30}).
				WithTerms(customer.NewService(repo)).
				WithClock(func() time.Time { return at })
			doc, err := w.InvoiceOrder(ctx, "")
			if err != nil {
				t.Fatalf("raise the order invoice on the job's own terms: %v", err)
			}
			var issue, due string
			var code *string
			if err := pool.QueryRow(ctx,
				`SELECT issue_date::text, due_date::text, terms_code FROM invoices WHERE id = $1::uuid`,
				doc.InvoiceID).Scan(&issue, &due, &code); err != nil {
				t.Fatalf("read the raised invoice: %v", err)
			}
			if issue != "2026-01-04" {
				t.Fatalf("the issue date is the house day: got %s, want 2026-01-04", issue)
			}
			if due != tc.wantDue {
				t.Fatalf("the due date is the job's own days from the house day: got %s, want %s", due, tc.wantDue)
			}
			if tc.wantCode == "" && code != nil {
				t.Fatalf("no standard term names the job's days, so the document carries no code: got %s", *code)
			}
			if tc.wantCode != "" && (code == nil || *code != tc.wantCode) {
				t.Fatalf("the document carries the code the master holds for the job's days: got %v, want %s", code, tc.wantCode)
			}
		})
	}
}

// TestIntegrationJobTerms_AJobWithNoOwnTermsReadsTheCustomersOwn: a job that carries no terms of
// its own is dated by the customer's, and the customer's discount term arrives whole: the net days,
// the discount window and the code are the customer's own answer.
func TestIntegrationJobTerms_AJobWithNoOwnTermsReadsTheCustomersOwn(t *testing.T) {
	pool := intPool(t)
	ctx := context.Background()
	repo := customer.NewRepository(pool)
	cust := seedCustomer(t, pool, ns(t))
	svc := customer.NewService(repo).WithWriter(repo)
	if _, err := svc.SetTerms(ctx, cust, customer.Terms2PctNet30); err != nil {
		t.Fatalf("put the customer on 2/10 net 30: %v", err)
	}
	job, err := repo.InsertJob(ctx, customer.JobWrite{
		CustomerID: cust, BranchID: trentonBranch, JobNo: "J-NOTERMS-" + ns(t),
		Fields: customer.JobFields{Name: "Driveway"},
	})
	if err != nil {
		t.Fatalf("plant the job with no own terms: %v", err)
	}
	at := eveningClock()
	w := NewInvoiceWriter(pool).
		WithOrderSource(jobTermsSource{customer: cust, job: job.JobID, branch: trentonBranch, terms: 30}).
		WithTerms(customer.NewService(repo)).
		WithClock(func() time.Time { return at })
	doc, err := w.InvoiceOrder(ctx, "")
	if err != nil {
		t.Fatalf("raise the order invoice on a job with no own terms: %v", err)
	}
	var issue, due string
	var discount *string
	var code *string
	if err := pool.QueryRow(ctx,
		`SELECT issue_date::text, due_date::text, discount_date::text, terms_code FROM invoices WHERE id = $1::uuid`,
		doc.InvoiceID).Scan(&issue, &due, &discount, &code); err != nil {
		t.Fatalf("read the raised invoice: %v", err)
	}
	if issue != "2026-01-04" {
		t.Fatalf("the issue date is the house day: got %s, want 2026-01-04", issue)
	}
	if due != "2026-02-03" {
		t.Fatalf("the job reads the customer's net days: got %s, want 2026-02-03", due)
	}
	if discount == nil || *discount != "2026-01-14" {
		t.Fatalf("the job reads the customer's discount window: got %v, want 2026-01-14", discount)
	}
	if code == nil || *code != customer.Terms2PctNet30 {
		t.Fatalf("the document carries the customer's code: got %v, want %s", code, customer.Terms2PctNet30)
	}
}
