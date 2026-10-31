//go:build integration

package payments

// discount_base_integration_test.go, RT-2 fix round 2, P3: the early payment discount's base is the invoice
// total less the holdback as the invoice first withheld it, so a credit memo that lowers the invoice's
// holdback never grows the base.
//
// What was chosen, and why: the base is fixed at what the invoice was written with. A credit lowers the
// amount owed; the discount is a percent of what the customer was billed to pay now, and the customer's
// terms do not get richer because a credit moved the holdback column. Reading the credits too (a smaller
// base after a credit) is a question of the dealer's terms and is not made here.

import (
	"context"
	"testing"
	"time"
)

func TestLoadDiscountRoom_TheBaseIgnoresACreditThatLowersTheHoldback(t *testing.T) {
	pool := dib510Pool(t)
	ctx := context.Background()
	_, invoiceID, tag := dib510Fixture(t, pool, 100_000)

	var jobID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO lien_jobs (job_no, branch_id) VALUES ($1, $2::uuid) RETURNING id::text`,
		"DB-"+tag, dib510Trenton).Scan(&jobID); err != nil {
		t.Fatalf("plant the lien job: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM holdback_ledger WHERE job_id = $1::uuid`, jobID); err != nil {
			t.Errorf("clean holdback_ledger: %v", err)
		}
		if _, err := pool.Exec(context.Background(), `DELETE FROM lien_jobs WHERE id = $1::uuid`, jobID); err != nil {
			t.Errorf("clean lien_jobs: %v", err)
		}
	})
	// Written with 10,000 held; a credit then took 5,000 of it back (column and ledger both move).
	for _, amt := range []int64{10_000, -5_000} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO holdback_ledger (job_id, invoice_id, branch_id, entry_type, amount_cents, reason)
			VALUES ($1::uuid, $2::uuid, $3::uuid, 'RETENTION', $4, $5)`,
			jobID, invoiceID, dib510Trenton, amt, "base leg "+tag+time.Now().Format("150405.000000")); err != nil {
			t.Fatalf("plant the ledger row: %v", err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE invoices SET holdback_cents = 5000 WHERE id = $1::uuid`, invoiceID); err != nil {
		t.Fatalf("lower the holdback: %v", err)
	}
	day := time.Now()
	row := applyInvoiceRow{InvoiceID: invoiceID, DiscountDate: &day, TermsCode: "2/10"}
	if err := loadDiscountRoom(ctx, pool, &row); err != nil {
		t.Fatal(err)
	}
	if row.BaseCents != 90_000 {
		t.Errorf("the discount base after a credit lowered the holdback: got %d, want 90000 (total less the holdback as first withheld)", row.BaseCents)
	}
}
