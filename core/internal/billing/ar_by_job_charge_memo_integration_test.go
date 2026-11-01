//go:build integration

package billing

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_JA4_FinanceChargeStaysAtAccountLevelAndMemoCreditsItsJob pins JA-4 item 4: a
// finance charge is raised with no job and stays at account level, a credit memo naming a job credits
// that job, and the by job aging and the by job statement both show each in its section and tie.
func TestIntegration_JA4_FinanceChargeStaysAtAccountLevelAndMemoCreditsItsJob(t *testing.T) {
	pool := intPool(t)
	repo := NewRepository(pool)
	credit := NewCreditService(repo)
	stmt := NewStatementService(repo)
	ctx := context.Background()
	f := jaSeed(t, pool)

	// The account level invoice is the finance charge (no job); a credit memo of 5000 names job A.
	if _, err := pool.Exec(ctx, `UPDATE invoices SET source_kind = 'FINANCE_CHARGE' WHERE id = $1::uuid`, f.invAcct); err != nil {
		t.Fatalf("mark the finance charge: %v", err)
	}
	memo := seedInvoice(t, pool, f.customer, f.n, invSpec{no: "JA-CM", status: "FINALIZED", branch: trentonBranch,
		issue: time.Now().AddDate(0, 0, -1).Format("2006-01-02"), subtotal: 5000, total: 5000, balance: 5000, finalized: true})
	if _, err := pool.Exec(ctx, `UPDATE invoices SET doc_type = 'CREDIT_MEMO', subtotal_cents = -5000, total_cents = -5000, balance_cents = -5000, job_id = $2::uuid WHERE id = $1::uuid`, memo, f.jobA); err != nil {
		t.Fatalf("make the credit memo: %v", err)
	}
	asOf := time.Now().Add(time.Hour)

	rows, err := credit.AgingByJob(ctx, asOf, "", "")
	if err != nil {
		t.Fatalf("AgingByJob: %v", err)
	}
	mine, sum := jaByJob(rows, f.customer)
	if mine[f.jobA].TotalCents != 25000 || mine[f.jobB].TotalCents != 20000 || mine[""].TotalCents != f.accountCents {
		t.Fatalf("aging by job = A %d, B %d, account %d; want 25000, 20000, %d (the charge and the NSF fee)",
			mine[f.jobA].TotalCents, mine[f.jobB].TotalCents, mine[""].TotalCents, f.accountCents)
	}
	var grand int64
	for _, r := range rows {
		grand += r.TotalCents
	}
	ar, err := repo.ARBalanceCents(ctx, asOf, "")
	if err != nil {
		t.Fatalf("ARBalanceCents: %v", err)
	}
	if grand != ar || sum != f.totalCents-5000 {
		t.Fatalf("totals: by job grand %d, ARBalanceCents %d, customer by job %d (want %d)", grand, ar, sum, f.totalCents-5000)
	}

	st, err := stmt.StatementByJob(ctx, f.customer, asOf, "")
	if err != nil {
		t.Fatalf("StatementByJob: %v", err)
	}
	got := map[string]int64{}
	for _, sec := range st.Sections {
		got[sec.JobID] = sec.SubtotalCents
	}
	if got[f.jobA] != 25000 || got[""] != f.accountCents || st.ClosingCents != sum {
		t.Fatalf("statement sections = %+v, closing %d; want A 25000, account %d, closing %d", got, st.ClosingCents, f.accountCents, sum)
	}
}
