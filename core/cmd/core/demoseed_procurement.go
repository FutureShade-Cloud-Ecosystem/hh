// demoseed_procurement.go, the buying world (the roles roadmap's first procurement increment):
// a demo vendor set of its own and two open purchase orders, one per branch, planted for the buyer
// the populate's procurement account signs in.
//
// WHY A VENDOR SET OF ITS OWN. The vendors table otherwise holds only the warranty migration's and
// the dibbits seed's real supplier brands (0057, the seed's 30), which are facts about a real
// dealer's supply chain, not a demo world a tester may edit, rename or deactivate through the new
// vendor master routes. Three DEMO numbered suppliers with role label names give the vendor
// register, the PO create validation and the outbound send's address book a set nobody minds a
// tester breaking, the same separation every other section of this seed keeps.
//
// WHY TWO OPEN POs THAT DO NOT COVER THE K08 GAP. The procurement gap is the point of the demo:
// Granular A sits below its reorder point at Trenton (§3 seeds the point above the on hand, the
// shortage SH2 names it "nothing on order to cover it") and the buyer's whole story is turning that
// gap into a placed order. A seeded PO covering P07 at Trenton would close the gap before the
// tester opens the app. Both POs therefore buy products with NO reorder point at all (P02, P03,
// P08, P10): the shortfall worklist's on order leg grades only rows the reorder point table holds,
// so no line here can move any gap, in either branch. One PO sits at Kingston on purpose: it is the
// other branch's order the Trenton buyer must NOT read (the HH-181 fence's demo leg, planted where
// the Buying and Stock register will render it).
//
// NUMBERED IN THE SEED'S OWN STYLE, DEMO-TRT-P01 and DEMO-KGN-P02, the way the order book's O04
// reads DEMO-TRT-O04. The po_no never comes from next_po_no: the live mint draws per branch
// counters, and a demo seed that moved a branch's next real PO number would be a mutation of
// shared state with no row edited (the parked ticket ruling, ids.go's own words).
//
// STABLE-KEYED, NOT DAY-KEYED: a purchase order is a document, written once, like the deposit
// world's orders. A second apply recognises both and writes nothing.
package main

import "fmt"

// demoVendors is the buyer's own supplier set. The email is the remit fallback the outbound send's
// address book reads (wire_operations.go item5VendorAddressBook); the third supplier carries none,
// the honest state a buyer meets on a vendor they have not chased for an address yet.
type procVendorSpec struct {
	Key    string
	No     string
	Name   string
	Terms  int
	Email  string
	Active bool
}

var demoProcurementVendors = []procVendorSpec{
	{"V1", demoPrefix + "VND-01", "Granular supplier (demo)", 30, "orders@demo-granular.example", true},
	{"V2", demoPrefix + "VND-02", "Paver supplier (demo)", 30, "orders@demo-pavers.example", true},
	{"V3", demoPrefix + "VND-03", "Edging and tools supplier (demo)", 15, "", true},
}

// procLineSpec is one line on a seeded PO. Cost is the branch weighted average the §3 seed plants
// (branchCost, in the product's base unit), so the order's value reads as what the branch pays.
type procLineSpec struct {
	Prod string
	Qty  string
}

// procPOSpec is one open purchase order. Status stays ORDERED: the receiving worklist and the
// incoming read both grade it as outstanding supply, and no receipt rows are planted (receiving is
// the yard's cross role leg, which this increment leaves pending on purpose).
type procPOSpec struct {
	Key       string
	Branch    int
	Vendor    string
	Status    string
	Expected  int // days from the plan clock
	PlacedAgo int // days before the plan clock the order was placed
	Notes     string
	Lines     []procLineSpec
}

var demoProcurementPOs = []procPOSpec{
	{"P01", 1, "V2", "ORDERED", 3, 2, "Restock for the display yard",
		[]procLineSpec{{"P02", "600.0000"}, {"P03", "240.0000"}}},
	{"P02", 0, "V1", "ORDERED", 5, 4, "Bulk sand and edging restock",
		[]procLineSpec{{"P08", "40.0000"}, {"P10", "100.0000"}}},
}

func (b *builder) procurementWorld() {
	for _, v := range demoProcurementVendors {
		remit := "{}"
		if v.Email != "" {
			remit = fmt.Sprintf(`{"email":%q}`, v.Email)
		}
		b.addProcurement(insert("vendors", "(vendor_no)",
			"id", demoID("procurement-vendor:"+v.Key),
			"vendor_no", v.No,
			"name", v.Name,
			"payment_terms_days", v.Terms,
			"remit_address", remit,
			"is_active", v.Active,
			"metadata", demoMetadata,
			"created_at", b.agoDays(90), "updated_at", b.agoDays(90),
		))
	}
	for _, po := range demoProcurementPOs {
		poKey := "procurement-po:" + po.Key
		at := b.agoDays(po.PlacedAgo)
		no := fmt.Sprintf("%s%s-%s", demoPrefix, b.branchCode(po.Branch), po.Key)
		b.addProcurement(insert("purchasing_pos", "(po_no)",
			"id", demoID(poKey),
			"po_no", no,
			"vendor_id", demoID("procurement-vendor:"+po.Vendor),
			"branch_id", b.branchID(po.Branch),
			"status", po.Status,
			"expected_at", b.day(po.Expected),
			"ordered_at", at,
			"notes", po.Notes,
			"created_at", at, "updated_at", at,
		))
		for i, ln := range po.Lines {
			p := b.prod[ln.Prod]
			b.addProcurement(insert("purchasing_po_lines", "(po_id, line_no)",
				"id", demoID(poKey+fmt.Sprintf(":%d", i+1)),
				"po_id", demoID(poKey),
				"line_no", i+1,
				"product_id", demoID("product:"+ln.Prod),
				"description", p.Name,
				"qty_ordered_base", ln.Qty,
				"uom", p.UOM,
				"unit_cost_cents", branchCost[ln.Prod][po.Branch],
				"created_at", at, "updated_at", at,
			))
		}
		b.sum.ProcurementLines = append(b.sum.ProcurementLines,
			fmt.Sprintf("%s  %s  %s  %d line(s), expected %s", no, b.branchCode(po.Branch),
				po.Status, len(po.Lines), b.day(po.Expected).Format("2006-01-02")))
	}
}

// addProcurement adds a row and records its id, the rows this world's proofs find by.
func (b *builder) addProcurement(r row) {
	b.add(r)
	b.sum.ProcurementRowIDs = append(b.sum.ProcurementRowIDs, fmt.Sprint(r.vals[0]))
}
