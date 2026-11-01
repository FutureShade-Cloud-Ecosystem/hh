//go:build integration

// finance_charge_audit_tx_integration_test.go, the FC-1b fix round: every finance charge audit write
// is inside its act's transaction.
//
// Run: go test -tags integration ./internal/billing/ -run W3FC1BFix
package billing

import (
	"testing"
)

// P3-2: a record that fails inside the press rolls the whole run back: no charge row, no document.
func TestIntegration_W3FC1BFix_FailedPressRecordRollsTheRunBack(t *testing.T) {
	r := fcRigNew(t)
	rec := r.realAudit()
	cust := r.customer(r.branch, "AUDITROLL")
	r.tradeInvoice(cust, r.branch, fc1Due31, 100_000, "AUDITROLL")
	rec.failTx = true
	w := r.writer(fcAfterMonth()).WithAudit(rec)
	_, err := w.RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month, ActorID: fc1Controller})
	if err == nil {
		t.Fatalf("W3-FC1b-fix red: a press whose audit record failed answered no error")
	}
	if rows := r.chargeRows(r.branch); len(rows) != 0 {
		t.Fatalf("W3-FC1b-fix red: a press whose audit record failed left charge rows %v", rows)
	}
	if n := r.chargeInvoiceCount(r.branch); n != 0 {
		t.Fatalf("W3-FC1b-fix red: a press whose audit record failed left %d charge documents", n)
	}
	// The same press with the audit healthy then charges, so the rollback left nothing in the way.
	rec.failTx = false
	res, err := w.RunFinanceCharges(r.ctx, FinanceChargeRequest{BranchID: r.branch, Month: fc1Month, ActorID: fc1Controller})
	if err != nil || res.Charged != 1 {
		t.Fatalf("W3-FC1b-fix red: the press after the rollback = %+v, %v, want 1 charge", res, err)
	}
}

// P3-2: a failed record leaves the waiver unchanged (set, and clear), and a waiver set twice leaves
// two audit rows, the history the overwritten row no longer holds.
func TestIntegration_W3FC1BFix_WaiverAuditIsInsideItsTransaction(t *testing.T) {
	r := fcRigNew(t)
	rec := r.realAudit()
	cust := r.customer(r.branch, "WAIVEAUDIT")
	svc := NewFinanceChargeWaiverService(NewRepository(r.pool)).WithAudit(rec)
	in := FinanceChargeWaiverInput{CustomerID: cust, Reason: "first", ActorID: fc1Controller, IsController: true}

	rec.failTx = true
	if _, err := svc.SetWaiver(r.ctx, in); err == nil {
		t.Fatalf("W3-FC1b-fix red: a waiver whose audit record failed answered no error")
	}
	if _, ok, err := NewRepository(r.pool).FinanceChargeWaiver(r.ctx, cust); err != nil || ok {
		t.Fatalf("W3-FC1b-fix red: a waiver whose audit record failed left a row (%v, %v)", ok, err)
	}

	rec.failTx = false
	if _, err := svc.SetWaiver(r.ctx, in); err != nil {
		t.Fatalf("W3-FC1b-fix red: the first waiver: %v", err)
	}
	in.Reason = "second"
	if _, err := svc.SetWaiver(r.ctx, in); err != nil {
		t.Fatalf("W3-FC1b-fix red: the second waiver: %v", err)
	}
	if rows := r.auditRows(ActFinanceChargeWaive, cust); len(rows) != 2 || rows[0]["reason"] != "first" || rows[1]["reason"] != "second" {
		t.Fatalf("W3-FC1b-fix red: waiver set twice left audit rows %v, want two, first then second", rows)
	}

	rec.failTx = true
	if _, err := svc.ClearWaiver(r.ctx, in); err == nil {
		t.Fatalf("W3-FC1b-fix red: a clear whose audit record failed answered no error")
	}
	if v, ok, err := NewRepository(r.pool).FinanceChargeWaiver(r.ctx, cust); err != nil || !ok || !v.Active {
		t.Fatalf("W3-FC1b-fix red: a clear whose audit record failed changed the waiver (%+v, %v, %v)", v, ok, err)
	}
	rec.failTx = false
	if _, err := svc.ClearWaiver(r.ctx, in); err != nil {
		t.Fatalf("W3-FC1b-fix red: the clear: %v", err)
	}
	if rows := r.auditRows(ActFinanceChargeWaiveCleared, cust); len(rows) != 1 {
		t.Fatalf("W3-FC1b-fix red: the clear left %d audit rows, want 1", len(rows))
	}
}
