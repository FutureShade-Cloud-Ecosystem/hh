//go:build integration

// vendor_credit_backstop_integration_test.go, wave 3 item 0.12 (S6 on 704's follow ups): the
// applied total backstop on a vendor credit, and the bill side cap's own leg.
//
//  1. the credit's applied_cents column (0467) is moved by every application under the row lock,
//     equals the applications' sum, and its CHECK is the database's own guard: a write that would
//     pass the credit's total is refused by the database itself;
//  2. a credit LARGER than the bill's open, applied above the bill's open, reaches the bill side
//     cap (P3-1: leg 3's over press tripped the credit guard first, so the bill guard had no leg).
//
// Run: go test -tags integration ./internal/purchasing/ -run W3P0A
package purchasing

import (
	"strings"
	"testing"
	"time"
)

// TestIntegration_W3P0A_AppliedCentsColumnIsTheBackstop: two applications move the column under
// the lock to exactly the applications' sum, and the database itself refuses an applied total
// above the credit's total, naming the CHECK that refused it.
func TestIntegration_W3P0A_AppliedCentsColumnIsTheBackstop(t *testing.T) {
	rig := newVCRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	_, bill := rig.createReceivedPOAndPostBill(t, "p0a-backstop", 30000, todayStr, todayStr)
	credit := rig.createPostedCredit(t, "p0a-backstop", branchTRT, 12000)

	if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 5000, IdempotencyKey: "w3p0a-backstop-1",
	}); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if _, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 3000, IdempotencyKey: "w3p0a-backstop-2",
	}); err != nil {
		t.Fatalf("second apply: %v", err)
	}

	// The column equals the applications' sum: each press moved it under the row lock it held.
	var column, sum int64
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT c.applied_cents,
		        (SELECT COALESCE(SUM(a.amount_cents), 0) FROM purchasing_vendor_credit_applications a WHERE a.credit_id = c.id)
		   FROM purchasing_vendor_credits c WHERE c.id = $1`, credit.ID).Scan(&column, &sum); err != nil {
		t.Fatalf("W3-P0A red: read the credit's applied total: %v", err)
	}
	if column != 8000 || sum != 8000 {
		t.Fatalf("W3-P0A red: the credit's applied_cents is %d beside the applications' sum %d, want 8000 and 8000",
			column, sum)
	}

	// The database's own guard. A service press above the credit's open is refused in words before
	// any write, so the only way to stand at the cap from below and press once more is a direct
	// write; the CHECK must refuse exactly that, the floor under the service's words.
	_, err := rig.pool.Exec(rig.ctx,
		`UPDATE purchasing_vendor_credits SET applied_cents = applied_cents + 5000 WHERE id = $1`, credit.ID)
	if err == nil || !strings.Contains(err.Error(), "purchasing_vendor_credits_applied_within_total") {
		t.Fatalf("W3-P0A red: a write above the credit's total must fail the applied_within_total CHECK, got %v", err)
	}
	// And the refused write moved nothing.
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT applied_cents FROM purchasing_vendor_credits WHERE id = $1`, credit.ID).Scan(&column); err != nil {
		t.Fatal(err)
	}
	if column != 8000 {
		t.Fatalf("W3-P0A red: the refused write moved the applied total to %d, want 8000", column)
	}
}

// TestIntegration_W3P0A_ApplicationAboveTheBillsOpenIsRefusedByTheBillGuard: the credit is bigger
// than the bill, so a press above the BILL's open passes the credit guard and must be refused by
// the bill guard, naming the bill's open. A press at the open itself lands and covers the bill.
func TestIntegration_W3P0A_ApplicationAboveTheBillsOpenIsRefusedByTheBillGuard(t *testing.T) {
	rig := newVCRig(t)
	todayStr := time.Now().In(rig.zone).Format("2006-01-02")

	_, bill := rig.createReceivedPOAndPostBill(t, "p0a-billcap", 20000, todayStr, todayStr)
	credit := rig.createPostedCredit(t, "p0a-billcap", branchTRT, 45000)

	_, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 25000, IdempotencyKey: "w3p0a-billcap-over",
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds the bill's open 20000 cents") {
		t.Fatalf("W3-P0A red: a press above the bill's open must be refused by the BILL guard naming the bill's open, got %v", err)
	}
	var n int
	if err := rig.pool.QueryRow(rig.ctx,
		`SELECT count(*) FROM purchasing_vendor_credit_applications WHERE credit_id = $1`, credit.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("W3-P0A red: the refused press wrote %d application rows", n)
	}

	detail, err := rig.svc.ApplyVendorCredit(rig.ctx, credit.ID, ApplyVendorCreditRequest{
		BillID: bill.ID, AmountCents: 20000, IdempotencyKey: "w3p0a-billcap-exact",
	})
	if err != nil {
		t.Fatalf("the press at the bill's open: %v", err)
	}
	if detail.AppliedCents != 20000 || detail.OpenCents != 25000 {
		t.Fatalf("the exact press applied %d and left %d of the credit open, want 20000 and 25000",
			detail.AppliedCents, detail.OpenCents)
	}
}
