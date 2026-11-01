//go:build integration

package billing

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestIntegration_JA4_StatementByJob_SectionsAndJobStatement pins JA-4 item 3: the customer's
// statement has a section per job and one for account level, each with its subtotal, and the closing
// balance is the plain statement's; a job statement shows only that job's lines, addressed to the
// job's contact, or to the customer's statement contact when the job has none, and says so.
func TestIntegration_JA4_StatementByJob_SectionsAndJobStatement(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	svc := NewStatementService(repo)
	ctx := context.Background()
	f := jaSeed(t, pool)
	asOf := time.Now().Add(time.Hour)

	if _, err := pool.Exec(ctx,
		`INSERT INTO customer_contacts (customer_id, name, email, phone, is_primary) VALUES ($1::uuid, 'Office Manager', 'office@example.test', '555-0100', TRUE)`,
		f.customer); err != nil {
		t.Fatalf("seed the customer's contact: %v", err)
	}

	plain, err := repo.StatementLines(ctx, f.customer, asOf, AllBranches)
	if err != nil {
		t.Fatalf("StatementLines: %v", err)
	}
	closing := plain[len(plain)-1].BalanceRun

	st, err := svc.StatementByJob(ctx, f.customer, asOf, "")
	if err != nil {
		t.Fatalf("StatementByJob: %v", err)
	}
	if st.ClosingCents != closing {
		t.Fatalf("closing %d != the plain statement's %d", st.ClosingCents, closing)
	}
	var sum int64
	got := map[string]int64{}
	for _, sec := range st.Sections {
		sum += sec.SubtotalCents
		got[sec.JobID] = sec.SubtotalCents
		if sec.AccountLevel != (sec.JobID == "") {
			t.Fatalf("section %q AccountLevel = %v", sec.JobID, sec.AccountLevel)
		}
	}
	if sum != closing || got[f.jobA] != 30000 || got[f.jobB] != 20000 || got[""] != 14500 {
		t.Fatalf("sections = %+v (sum %d, closing %d)", got, sum, closing)
	}
	if len(st.Sections) == 0 || !st.Sections[0].AccountLevel {
		t.Fatalf("the account level section comes first: %+v", st.Sections)
	}

	// A job statement shows only that job, addressed to the job's contact.
	if _, err := pool.Exec(ctx,
		`UPDATE customer_jobs SET contact_name = 'Site Super', contact_email = 'super@example.test' WHERE id = $1::uuid`, f.jobA); err != nil {
		t.Fatalf("set the job's contact: %v", err)
	}
	a, err := svc.StatementByJob(ctx, f.customer, asOf, f.jobA)
	if err != nil {
		t.Fatalf("job statement A: %v", err)
	}
	if len(a.Sections) != 1 || a.Sections[0].JobID != f.jobA || a.ClosingCents != 30000 {
		t.Fatalf("job A statement = %+v", a)
	}
	for _, ln := range a.Sections[0].Lines {
		if ln.JobID != f.jobA {
			t.Fatalf("a job statement shows another job's line: %+v", ln)
		}
	}
	if a.Contact.Source != "job" || a.Contact.Name != "Site Super" || a.Contact.Email != "super@example.test" {
		t.Fatalf("job A contact = %+v", a.Contact)
	}

	if _, err := pool.Exec(ctx, `UPDATE customer_jobs SET contact_name = '', contact_email = NULL, contact_phone = '' WHERE id = $1::uuid`, f.jobB); err != nil {
		t.Fatalf("clear job B's contact: %v", err)
	}
	// A job with no contact falls back to the customer's, and the statement says so.
	b, err := svc.StatementByJob(ctx, f.customer, asOf, f.jobB)
	if err != nil {
		t.Fatalf("job statement B: %v", err)
	}
	if b.Contact.Source != "customer" || b.Contact.Email != "office@example.test" || b.Contact.Note == "" {
		t.Fatalf("job B contact = %+v, want the customer's with a note", b.Contact)
	}

	// Another customer's job is no job of this one.
	other := jaSeed(t, pool, "-o")
	if _, err := svc.StatementByJob(ctx, f.customer, asOf, other.jobA); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another customer's job: err = %v, want ErrNotFound", err)
	}
}
