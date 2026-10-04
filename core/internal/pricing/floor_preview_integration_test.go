//go:build integration

package pricing

// The editors' floor warning against the real Repository and the seeded catalogue
// (CP0-6): the product source reads the products and their branch cost, the preview
// grades them through the real resolver and gate, a preview writes nothing, and the
// confirmation lands in the real pricing log where ListAudit reads it back.
//
// Run with: DATABASE_URL=... go test -tags integration -count=1 -run FloorWarning ./internal/pricing/

import (
	"context"
	"strings"
	"testing"
)

func auditCount(t *testing.T, svc *Service) int {
	t.Helper()
	rows, err := svc.ListAudit(context.Background(), AuditFilter{Limit: 500})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	return len(rows)
}

func TestIntegration_FloorWarning_ADeepDiscountNamesSeededProductsAndWritesNothing(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newAuditRefFixture(t, pool)
	svc := realSvc(pool)
	before := auditCount(t, svc)

	out, err := svc.PreviewFloor(ctx, FloorChange{Kind: "rule", Rule: &Rule{
		Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: f.categoryID,
		CalcType: CalcPctOffList, DiscountBps: refPtrI64(9500), PriceUOM: f.baseUOM, Priority: 90}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if out.Evaluated == 0 || len(out.Items) == 0 {
		t.Fatalf("95 percent off the seeded paver category must put seeded products under the floor: %+v", out)
	}
	for _, it := range out.Items {
		if it.MarginBps >= it.FloorBps || it.SKU == "" || it.ProductID == "" || it.CategoryID != f.categoryID {
			t.Errorf("an item must be under its floor and named: %+v", it)
		}
	}
	if after := auditCount(t, svc); after != before {
		t.Errorf("a preview must write no pricing log row: %d before, %d after", before, after)
	}

	gentle, err := svc.PreviewFloor(ctx, FloorChange{Kind: "rule", Rule: &Rule{
		Scope: ScopeTierCategory, CustomerType: "CONTRACTOR", CategoryID: f.categoryID,
		CalcType: CalcPctOffList, DiscountBps: refPtrI64(100), PriceUOM: f.baseUOM, Priority: 90}})
	if err != nil {
		t.Fatalf("PreviewFloor: %v", err)
	}
	if len(gentle.Items) >= len(out.Items) {
		t.Errorf("1 percent off must warn of fewer products than 95 percent: %d vs %d", len(gentle.Items), len(out.Items))
	}
}

func TestIntegration_FloorWarning_ProductSourceFencesByProductAndCategory(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newAuditRefFixture(t, pool)
	repo := NewRepository(pool)
	one, err := repo.CandidateProducts(ctx, "", f.productID, "", 10)
	if err != nil || len(one) != 1 || one[0].ProductID != f.productID || one[0].BaseUOM == "" {
		t.Fatalf("by product: %+v, %v", one, err)
	}
	cat, err := repo.CandidateProducts(ctx, f.categoryID, "", "", 1000)
	if err != nil || len(cat) == 0 {
		t.Fatalf("by category: %+v, %v", cat, err)
	}
	for _, c := range cat {
		if c.CategoryID != f.categoryID {
			t.Errorf("a category query returned another category's product: %+v", c)
		}
	}
	capped, err := repo.CandidateProducts(ctx, "", "", "", 3)
	if err != nil || len(capped) != 3 {
		t.Fatalf("the limit must hold: %d, %v", len(capped), err)
	}
}

func TestIntegration_FloorWarning_TheConfirmationIsInThePricingLog(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	f := newAuditRefFixture(t, pool)
	svc := realSvc(pool)

	rule, err := svc.CreateRule(ctx, Rule{
		Scope: ScopeTierCategory, CustomerType: "GOVERNMENT", CategoryID: f.categoryID,
		CalcType: CalcPctOffList, DiscountBps: refPtrI64(9400), PriceUOM: f.baseUOM, Priority: 71,
		DetailText: "IT QD-4: floor confirmation"}, "")
	if err != nil {
		t.Fatalf("CreateRule: %v", err)
	}
	cleanupAudit(t, pool, "pricing_rule", rule.ID)
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM pricing_rules WHERE id=$1`, rule.ID) })

	if err := svc.RecordFloorConfirmation(ctx, FloorConfirmation{
		EntityType: "pricing_rule", EntityID: rule.ID, ProductIDs: []string{f.productID},
		RefType: RefCategory, RefID: f.categoryID}, ""); err != nil {
		t.Fatalf("RecordFloorConfirmation: %v", err)
	}
	rows, err := svc.ListAudit(ctx, AuditFilter{EntityType: "pricing_rule", EntityID: rule.ID, Limit: 50})
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	var found bool
	for _, r := range rows {
		if r.FieldName == "floor_warning_confirmed" {
			found = true
			if r.Action != "UPDATE" || !strings.Contains(r.NewValue, f.productID) || r.RefEntityType != RefCategory || r.RefEntityID != f.categoryID {
				t.Errorf("the log row is wrong: %+v", r)
			}
		}
	}
	if !found {
		t.Fatalf("the confirmation is not in the pricing log: %+v", rows)
	}

	if err := svc.RecordFloorConfirmation(ctx, FloorConfirmation{
		EntityType: "pricing_rule", EntityID: "00000000-0000-4000-8000-000000000000", ProductIDs: []string{f.productID}}, ""); err == nil {
		t.Errorf("a confirmation for a record that does not exist must be refused")
	}
}
