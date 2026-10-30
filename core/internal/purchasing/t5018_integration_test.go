//go:build integration

// t5018_integration_test.go, the Financial Controller's subcontractor mark on a vendor, against a
// real database: the flag the year's T5018 report filters on, and the contractor's CRA business
// number the form asks for. The role wall itself is proved at the gate, on the generated census
// entry, in core/internal/platform/authz/rolegate_t5018_test.go.
package purchasing

import (
	"context"
	"testing"
)

// TestIntegration_T5018Flag_TheControllerSetsTheMarkAndTheNumber: the write sets the flag and the
// business number in one statement, the row holds both, the answer reads both, and the write is
// audited: the flag is the fact a CRA form is built on, so who changed it and when is part of that
// record.
func TestIntegration_T5018Flag_TheControllerSetsTheMarkAndTheNumber(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)
	audits := &countingAudit{}
	h.svc.WithAudit(audits)

	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{
		Name: "T5018 Subcontractor (demo)", OrderEmail: "accounts@t5018demo.example",
	})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	if v.IsT5018Subcontractor || v.BusinessNumber != "" {
		t.Fatalf("a new vendor reads %+v, want the flag off and no number", v)
	}

	yes, number := true, "123456789"
	rec, err := h.svc.SetVendorT5018(h.ctx, org, v.ID, SetVendorT5018Request{
		IsSubcontractor: &yes, BusinessNumber: &number,
	})
	if err != nil {
		t.Fatalf("set the T5018 mark: %v", err)
	}
	if !rec.IsT5018Subcontractor || rec.BusinessNumber != "123456789" {
		t.Fatalf("the record reads %+v, want the flag on and the number written", rec)
	}

	// The row itself, not just the answer: the report reads the row.
	var flagged bool
	var bn string
	if err := h.pool.QueryRow(context.Background(),
		`SELECT is_t5018_subcontractor, COALESCE(business_number,'') FROM vendors WHERE id = $1`, v.ID).
		Scan(&flagged, &bn); err != nil {
		t.Fatalf("read the vendor row: %v", err)
	}
	if !flagged || bn != "123456789" {
		t.Fatalf("the row reads flagged %v number %q, want true and 123456789", flagged, bn)
	}
	if n := audits.count("purchasing.vendor.t5018.set"); n != 1 {
		t.Fatalf("%d purchasing.vendor.t5018.set entries for one set, want 1", n)
	}
}

// TestIntegration_T5018Flag_ClearingTheMarkKeepsTheNumber: the two facts are separate. A
// contractor can keep its number while the mark is off, and the mark can come back with the number
// it already had.
func TestIntegration_T5018Flag_ClearingTheMarkKeepsTheNumber(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)

	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "T5018 Cleared (demo)"})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	yes, number := true, "987654321"
	if _, err := h.svc.SetVendorT5018(h.ctx, org, v.ID, SetVendorT5018Request{
		IsSubcontractor: &yes, BusinessNumber: &number,
	}); err != nil {
		t.Fatalf("set the T5018 mark: %v", err)
	}

	no := false
	rec, err := h.svc.SetVendorT5018(h.ctx, org, v.ID, SetVendorT5018Request{IsSubcontractor: &no})
	if err != nil {
		t.Fatalf("clear the T5018 mark: %v", err)
	}
	if rec.IsT5018Subcontractor {
		t.Fatalf("the record reads %+v, want the flag off", rec)
	}
	if rec.BusinessNumber != "987654321" {
		t.Fatalf("the record reads number %q, want the number kept", rec.BusinessNumber)
	}

	// An empty number clears it, and the row keeps the distinction: cleared is NULL, never given
	// is never written.
	empty := ""
	rec, err = h.svc.SetVendorT5018(h.ctx, org, v.ID, SetVendorT5018Request{
		IsSubcontractor: &no, BusinessNumber: &empty,
	})
	if err != nil {
		t.Fatalf("clear the business number: %v", err)
	}
	if rec.BusinessNumber != "" {
		t.Fatalf("the record reads number %q, want it cleared", rec.BusinessNumber)
	}
	var isNull bool
	if err := h.pool.QueryRow(context.Background(),
		`SELECT business_number IS NULL FROM vendors WHERE id = $1`, v.ID).Scan(&isNull); err != nil {
		t.Fatalf("read the vendor row: %v", err)
	}
	if !isNull {
		t.Fatalf("a cleared business number is written as a value, want NULL")
	}
}

// TestIntegration_T5018Flag_TheMarkMustBeSaid: the body that does not say what the flag is names
// nothing to set, and says so rather than silently leaving the record as it was.
func TestIntegration_T5018Flag_TheMarkMustBeSaid(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)

	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "T5018 Silent (demo)"})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	if _, err := h.svc.SetVendorT5018(h.ctx, org, v.ID, SetVendorT5018Request{}); err == nil {
		t.Fatalf("a body naming no flag was accepted")
	}
}

// TestIntegration_T5018Flag_AnUnknownVendorIsRefused: a vendor that does not exist and a vendor
// another org owns answer the same 404, so the status cannot be used to probe which ids exist.
func TestIntegration_T5018Flag_AnUnknownVendorIsRefused(t *testing.T) {
	h := newHarness(t)
	org := h.orgID(t)
	yes := true
	if _, err := h.svc.SetVendorT5018(h.ctx, org, "6f1f0000-0000-4000-9000-000000000000", SetVendorT5018Request{
		IsSubcontractor: &yes,
	}); err == nil {
		t.Fatalf("an unknown vendor was marked")
	}
}

// TestIntegration_T5018Flag_ABadBusinessNumberIsRefused: the business number is the CRA's own, so
// a number that is not one is refused at the door rather than written into the record a CRA form
// is built from.
func TestIntegration_T5018Flag_ABadBusinessNumberIsRefused(t *testing.T) {
	h := newHarness(t)
	vendorMasterCleanup(t, h)
	org := h.orgID(t)

	v, err := h.svc.CreateVendor(h.ctx, org, CreateVendorRequest{Name: "T5018 Bad Number (demo)"})
	if err != nil {
		t.Fatalf("create vendor: %v", err)
	}
	yes := true
	bad := "12345"
	if _, err := h.svc.SetVendorT5018(h.ctx, org, v.ID, SetVendorT5018Request{
		IsSubcontractor: &yes, BusinessNumber: &bad,
	}); err == nil {
		t.Fatalf("a business number that is not one was written")
	}
	// The program account form is legal: the nine digits with RC0001 after them.
	good := "123456789RC0001"
	rec, err := h.svc.SetVendorT5018(h.ctx, org, v.ID, SetVendorT5018Request{
		IsSubcontractor: &yes, BusinessNumber: &good,
	})
	if err != nil {
		t.Fatalf("a program account business number was refused: %v", err)
	}
	if rec.BusinessNumber != good {
		t.Fatalf("the record reads number %q, want %q", rec.BusinessNumber, good)
	}
}
