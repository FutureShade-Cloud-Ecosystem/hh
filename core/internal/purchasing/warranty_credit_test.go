package purchasing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/settings"
)

type fakeWarrantyCreditSeam struct {
	openCredit *OpenWarrantyCredit
	claimInfo  *OpenWarrantyCredit
}

func (f *fakeWarrantyCreditSeam) OpenCreditForVendor(_ context.Context, vendorID, branchID string) (*OpenWarrantyCredit, error) {
	return f.openCredit, nil
}

func (f *fakeWarrantyCreditSeam) ClaimInfo(_ context.Context, claimID string) (*OpenWarrantyCredit, error) {
	return f.claimInfo, nil
}

func TestWarrantyCredit_CostGateMarshal(t *testing.T) {
	// Positive control: manager caller sees cost fields
	lensMgr := costLens{allow: true}
	poMgr := PO{
		ID:                         "00000000-0000-0000-0000-000000000001",
		SubtotalCents:              100000, // $1,000.00
		WarrantyCreditAppliedCents: 26400,  // $264.00
		NetTotalCents:              73600,  // $736.00
	}
	lensMgr.stampPO(&poMgr)

	bytesMgr, err := json.Marshal(poMgr)
	if err != nil {
		t.Fatalf("marshal manager PO: %v", err)
	}
	strMgr := string(bytesMgr)
	if !strings.Contains(strMgr, `"subtotal_cents":100000`) {
		t.Errorf("manager payload missing subtotal_cents: %s", strMgr)
	}
	if !strings.Contains(strMgr, `"warranty_credit_applied_cents":26400`) {
		t.Errorf("manager payload missing warranty_credit_applied_cents: %s", strMgr)
	}
	if !strings.Contains(strMgr, `"net_total_cents":73600`) {
		t.Errorf("manager payload missing net_total_cents: %s", strMgr)
	}

	// Negative control: frontline clerk does not see any cost bytes
	lensClerk := costLens{allow: false}
	poClerk := PO{
		ID:                         "00000000-0000-0000-0000-000000000001",
		SubtotalCents:              100000,
		WarrantyCreditAppliedCents: 26400,
		NetTotalCents:              73600,
	}
	lensClerk.stampPO(&poClerk)

	bytesClerk, err := json.Marshal(poClerk)
	if err != nil {
		t.Fatalf("marshal clerk PO: %v", err)
	}
	strClerk := string(bytesClerk)
	if strings.Contains(strClerk, "subtotal_cents") {
		t.Errorf("clerk payload leaked subtotal_cents: %s", strClerk)
	}
	if strings.Contains(strClerk, "warranty_credit_applied_cents") {
		t.Errorf("clerk payload leaked warranty_credit_applied_cents: %s", strClerk)
	}
	if strings.Contains(strClerk, "net_total_cents") {
		t.Errorf("clerk payload leaked net_total_cents: %s", strClerk)
	}
	if strings.Contains(strClerk, "null") {
		t.Errorf("clerk payload carried literal null: %s", strClerk)
	}
}

func TestWarrantyCredit_RulingR3CapAtSubtotal(t *testing.T) {
	// Ruling R3: unit prices remain unchanged; credit caps at subtotal; net total reduces to zero if credit >= subtotal
	lens := costLens{allow: true}
	subtotal := int64(20000)     // $200.00
	creditExcess := int64(30000) // $300.00
	po := PO{
		ID:                         "00000000-0000-0000-0000-000000000002",
		SubtotalCents:              subtotal,
		WarrantyCreditAppliedCents: creditExcess,
	}
	lens.stampPO(&po)

	if po.NetTotalCents != 0 {
		t.Errorf("net total should be 0 when credit exceeds subtotal, got %d", po.NetTotalCents)
	}
	if po.POCosts.NetTotal == nil || *po.POCosts.NetTotal != 0 {
		t.Errorf("POCosts.NetTotal should be 0, got %v", po.POCosts.NetTotal)
	}
}

func TestWarrantyCredit_PolicyCostVisibility(t *testing.T) {
	svc := NewService(nil)
	fake := &fakePolicy{p: Policy{CostVisibility: settings.VisibilityHidden}}
	svc.policySeam = fake

	ctx := context.Background()
	lens := svc.costLensFor(ctx, "00000000-0000-0000-0000-000000000001")
	if lens.allow {
		t.Error("VisibilityHidden should not allow cost bytes")
	}
}
