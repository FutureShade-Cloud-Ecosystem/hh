// demoseed_contractor_cards.go, the contractor's board cards: what HH Pro's orders, scope items and pickups
// feeds hold for the demo account (UX-67, ruling row 2562 option a).
//
// THE PATH A REAL CARD TAKES. The contractor `orders` sync scope is not the staff orders table: it is
// the plan table, the contractor's own editable board card (internal/planflow/orders.go), and
// `scope_items` is plan_line (planflow/items.go). A pushed create writes the card at the birth stage
// `plan` with org and tenant stamped from the token and a fresh seq from the `orders` counter, and a line
// with a seq from the `scope_items` counter. A card never becomes a staff orders row: D7 keeps the one
// dealer order aggregate on `orders`, and the supplier's desk moves a card to quote, order and invoice
// once those artifacts exist (the tenant may only move plan to quote and back). The price legs of a
// line are the pricing flow's to write, so a pushed line cannot carry them.
//
// So the pushed path cannot make the later stages or priced lines, and this section is the INSERT only
// seed like the rest of the contractor world (this command sits outside the module fence). The rows go in
// seq NULL; the once per world repair (demoseed_seqrepair_demo.go, which already lists plan, plan_line and
// will_call_pickups) numbers them from the modules' own counters, so no counter is touched here.
//
// WHAT IT PLANTS. One card per contractor order in the stage its state maps to (a billed order is an
// invoice card, every other order is an order card), a quote card for each of the two live quotes, and
// a plan card for the draft materials list. Order lines carry the order's own prices (source erp), quote
// lines the quoted ones, the draft none. One will call pickup on the pickup order, so the pickups feed
// serves it (the feed reads the pickups of the tenant's own orders). The contractor's board has four
// stages, not the staff order states, so "being picked" and "out for delivery" show as order cards
// whose staff order carries that state.
package main

import "fmt"

// contractorCardRows is the number of rows contractorCards writes: 8 order cards with 15 lines, 2 quote
// cards with 5 lines, 1 plan card with 3 lines, and 1 pickup.
const contractorCardRows = 8 + 15 + 2 + 5 + 1 + 3 + 1

type cCard struct {
	Key   string // the source order, quote or project key
	Stage string
	Job   int
	Name  string
	Ref   string // po_reference
}

// contractorCardList: the board in the order the supplier's columns read.
var contractorCardList = []cCard{
	{"CO1", "invoice", 0, "Terrace pavers, first drop", "PO-1041"},
	{"CO2", "invoice", 1, "Retaining wall block", "PO-1042"},
	{"CO3", "invoice", 2, "Courtyard stone and sand", "PO-1043"},
	{"CO4", "invoice", 0, "Terrace base aggregate", "PO-1044"},
	{"CO5", "order", 1, "Wall block, lower wall", "PO-1045"},
	{"CO6", "order", 0, "Terrace pavers, second drop", "PO-1046"},
	{"CO7", "order", 2, "Driveway stone, back drive", "PO-1047"},
	{"CO8", "order", 1, "Coping and sand, will call", "PO-1048"},
	{"CQ1", "quote", 2, "Courtyard and driveway quote", "PO-1049"},
	{"CQ2", "quote", 0, "Coastal terrace quote", "PO-1050"},
	{"PP1", "plan", 0, "Coastal terrace materials", ""},
}

func (b *builder) contractorCards(add func(row), orderMoney map[string]contractorMoney) {
	cust := contractorCustomerID
	orderByKey := map[string]cOrder{}
	for _, o := range contractorOrders {
		orderByKey[o.Key] = o
	}
	quoteByKey := map[string]cQuote{}
	for _, q := range contractorQuotes {
		quoteByKey[q.Key] = q
	}
	projectByKey := map[string]cProject{}
	for _, p := range contractorProjects {
		projectByKey[p.Key] = p
	}
	for i, c := range contractorCardList {
		var lines []cLine
		var fulfillment any
		var requested any
		var instructions string
		source, age := "erp", 0
		switch {
		case orderByKey[c.Key].Key != "":
			o := orderByKey[c.Key]
			lines, fulfillment, requested, instructions, age = o.Lines, o.Fulfillment, b.day(o.ReqOffset), o.Instruction, o.AgeDays
		case quoteByKey[c.Key].Key != "":
			q := quoteByKey[c.Key]
			lines, source, age = q.Lines, "quoted", q.AgeDays
		default:
			p := projectByKey[c.Key]
			lines, source, age = p.Lines, "unpriced", p.Age
		}
		created := b.agoDays(age)
		cardID := demoID("contractor-card:" + c.Key)
		var ref any
		if c.Ref != "" {
			ref = c.Ref
		}
		add(insert("plan", "(id)",
			"id", cardID,
			"org_id", driverOrgStamp(),
			"tenant_id", cust,
			"project_id", contractorJobIDs[c.Job],
			"name", c.Name,
			"stage", c.Stage,
			"fulfillment", fulfillment,
			"requested_date", requested,
			"delivery_address", fmt.Sprintf(`{"address":%q}`, contractorJobAddress[c.Job]),
			"site_instructions", instructions,
			"po_reference", ref,
			"sort_order", i,
			"created_at", created, "updated_at", created,
		))
		b.sum.ContractorLines = append(b.sum.ContractorLines,
			fmt.Sprintf("card     %-3s  %-7s  %d line(s)", c.Key, c.Stage, len(lines)))
		unit := func(j int) any { return nil }
		total := func(j int) any { return nil }
		if source != "unpriced" {
			m := b.contractorMoney(lines)
			unit = func(j int) any { return m.Unit[j] }
			total = func(j int) any { return m.Total[j] }
		}
		for j, l := range lines {
			p := b.prod[l.Prod]
			qty := fmt.Sprintf("%d.0000", l.Qty)
			var priceUOM any
			if source != "unpriced" {
				priceUOM = p.UOM
			}
			add(insert("plan_line", "(id)",
				"id", demoID(fmt.Sprintf("contractor-cardline:%s:%d", c.Key, j+1)),
				"plan_id", cardID,
				"org_id", driverOrgStamp(),
				"tenant_id", cust,
				"product_id", demoID("product:"+l.Prod),
				"description", p.Name,
				"qty_entered", qty, "uom_entered", p.UOM, "qty_base", qty,
				"unit_price_cents", unit(j), "price_uom", priceUOM, "line_total_cents", total(j),
				"price_source", source,
				"metadata", demoMetadata,
				"created_at", created, "updated_at", created,
			))
		}
	}

	// The pickup order's will call, the one pickup the contractor's own orders carry. ACTIVE like the
	// staff world's open pickups; the token hash is the digest of a raw token nobody is shown.
	add(insert("will_call_pickups", "(id)",
		"id", demoID("willcall:contractor:CO8"),
		"order_id", demoID("contractor-order:CO8"),
		"branch_id", b.in.Contractor.BranchID,
		"token_hash", demoTokenHash(demoRef("dwc_", "willcall:contractor:CO8")),
		"status", "ACTIVE",
		"expires_at", b.agoDays(-5),
		"created_at", b.agoDays(1),
	))
}
