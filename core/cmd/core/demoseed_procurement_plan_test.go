package main

// demoseed_procurement_plan_test.go, the buying world's promises asserted on the PLAN. The
// database half is demoseed_integration_test.go's (the whole plan applies against a real schema,
// twice, creating nothing the second time); what only the plan can prove is the shape that keeps
// the demo honest: the vendor set is the buyer's own, both POs stay open, and no seeded line can
// close the K08 gap the buyer's whole story turns on.

import (
	"fmt"
	"testing"
)

// procurementRows pulls the buying world's rows out of a plan, by table, keyed by the row id list
// the summary names.
func procurementRows(rows []row, sum planSummary) map[string][]map[string]any {
	ids := map[string]bool{}
	for _, id := range sum.ProcurementRowIDs {
		ids[id] = true
	}
	out := map[string][]map[string]any{}
	for _, r := range rows {
		if ids[fmt.Sprint(r.vals[0])] {
			out[r.table] = append(out[r.table], fieldMap(r))
		}
	}
	return out
}

func TestProcurementPlan_AVendorSetOfItsOwn(t *testing.T) {
	rows, _ := buildForTest(t)
	p := procurementRows(rows, planSummaryOf(t))
	vs := p["vendors"]
	if len(vs) != 3 {
		t.Fatalf("the buying world holds %d vendors, want its own set of three", len(vs))
	}
	for _, v := range vs {
		no := fmt.Sprint(v["vendor_no"])
		if no == "" || no[:5] != demoPrefix[:5] {
			t.Errorf("vendor %v carries number %q, want the seed's own DEMO numbering", v["name"], no)
		}
		if v["is_active"] != true {
			t.Errorf("vendor %v is not active, the buyer must be able to order from the set", v["name"])
		}
	}
	// Two of the three carry the remit fallback address the outbound send reads; one carries none,
	// the honest untagged state.
	withEmail := 0
	for _, v := range vs {
		remit := fmt.Sprint(v["remit_address"])
		if remit != "{}" {
			withEmail++
		}
	}
	if withEmail != 2 {
		t.Errorf("%d vendors carry a remit address, want two of the three", withEmail)
	}
	// None of the seeded vendors shadows a real supplier brand row (the conflict arbiter is the
	// number, and the set must never ride on 0057's or the seed's).
	for _, v := range vs {
		for _, real := range []string{"V-UNILOCK", "V-TECHO", "V-OAKS"} {
			if fmt.Sprint(v["vendor_no"]) == real {
				t.Errorf("the demo vendor set claims the real supplier number %s", real)
			}
		}
	}
}

func TestProcurementPlan_TwoOpenPOsThatLeaveTheGap(t *testing.T) {
	rows, sum := buildForTest(t)
	p := procurementRows(rows, sum)
	pos := p["purchasing_pos"]
	if len(pos) != 2 {
		t.Fatalf("the buying world holds %d purchase orders, want two", len(pos))
	}
	lines := p["purchasing_po_lines"]
	if len(lines) != 4 {
		t.Fatalf("the buying world holds %d PO lines, want four", len(lines))
	}
	byPO := map[string][]map[string]any{}
	for _, l := range lines {
		byPO[fmt.Sprint(l["po_id"])] = append(byPO[fmt.Sprint(l["po_id"])], l)
	}
	poByID := map[string]map[string]any{}
	for _, po := range pos {
		poByID[fmt.Sprint(po["id"])] = po
		if po["status"] != "ORDERED" {
			t.Errorf("PO %v reads status %v, want ORDERED (open supply the incoming read grades)", po["po_no"], po["status"])
		}
		if fmt.Sprint(po["ordered_at"]) == "" {
			t.Errorf("PO %v carries no ordered_at, a placed order is a placed order on the register", po["po_no"])
		}
	}
	// One PO per branch: the Trenton buyer's own order, and the Kingston order the fence leg must
	// hold out of their register.
	branches := map[string]bool{}
	for _, po := range pos {
		branches[fmt.Sprint(po["branch_id"])] = true
	}
	if len(branches) != 2 {
		t.Errorf("the two POs sit on %d branches, want one each", len(branches))
	}
	// THE GAP STAYS OPEN: no seeded line names a product that carries a reorder point, so the
	// shortfall worklist's on order leg cannot move, in either branch. P07 at Trenton is the K08
	// gap itself; P05 at Trenton and P09 at Kingston are the seed's other two gaps and stay open
	// for the same reason.
	for _, l := range lines {
		prod := productKeyOf(t, rows, fmt.Sprint(l["product_id"]))
		if _, hasPoint := reorderPoint[prod]; hasPoint {
			t.Errorf("PO line for %s (%v) carries a reorder point, a seeded supply line would close a gap the buyer is meant to find", prod, l["description"])
		}
	}
	// The summary the operator reads names both orders.
	if len(sum.ProcurementLines) != 2 {
		t.Errorf("the summary carries %d procurement lines, want one per order", len(sum.ProcurementLines))
	}
}

// planSummaryOf is the summary half of buildForTest, for tests that read both.
func planSummaryOf(t *testing.T) planSummary {
	t.Helper()
	_, sum := buildForTest(t)
	return sum
}

// productKeyOf walks the plan's product rows back to their key, so a test can speak in the spec
// table's own terms (P07, not a uuid).
func productKeyOf(t *testing.T, rows []row, productID string) string {
	t.Helper()
	for _, p := range demoProducts {
		if demoID("product:"+p.Key) == productID {
			return p.Key
		}
	}
	// Not a catalogue product (a test fixture's own row): answer the id itself, which then matches
	// no reorder point and fails no gap assertion above.
	return productID
}
