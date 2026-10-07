// demoseed_island.go, the lines behind the migration-planted CP-03 island's money.
//
// WHY THIS SECTION EXISTS. Migration 0055 plants a self-contained demo island for the portal
// projects rollup: customer CUST-DEMO-CP03, quote Q-DEMO-CP03 (ACCEPTED, total 20000 cents), the
// two orders behind the rollup's spend figures (SO-DEMO-CP03-Q at 20000, SO-DEMO-CP03-X at 50000)
// and their invoices. The island's headers carry money and its documents carry NO LINES, which is
// a state the product's own writers cannot produce: both recompute paths fold the header up from
// the lines (quote/edit_repository.go recomputeTotals, order/edit_repository.go recomputeTotals),
// so a header with figures and no lines is a document whose money answers to nothing. On the
// Quote Desk it read exactly as that: Convert to Order on the accepted quote offered an empty item
// table under a $200.00 total, a confident number with no lines behind it.
//
// WHY THE FIX IS HERE AND NOT IN THE MIGRATION. Migrations are numbered, never edited after
// commit, and 0055 is long since committed. The demoseed is the one writer whose audience is
// exactly the demo world (the demo image's entrypoint is the only thing that runs it), the same
// ruling that puts the scrub-name renames here rather than in a migration, and every statement
// this section adds is an INSERT the applier already knows how to yield: the island's OWN rows
// (headers, customer, invoices) stay exactly as the migration planted them, including their
// figures, so the portal rollup's quoted-versus-spend story is untouched. Only the missing lines
// arrive, and they are struck to the cent against the headers the migration already wrote: the
// quote's one line and the quoted order's one line are the same like-for-like work the migration's
// own commentary names, and the ad-hoc order's two lines are the add-on spend its variance fix is
// demonstrated with.
//
// NOT DAY-KEYED, NOT PERSONA-KEYED. The island exists from the migrations alone, on every
// deployment, so this section builds unconditionally: no demo login resolves anywhere in it. The
// lines reference the demo catalogue (§3), which the same apply plants earlier in the same
// transaction, so the foreign keys resolve inside the one transaction the applier holds.
package main

import "fmt"

// The CP-03 island's fixed row ids, as migration 0055 writes them (the df… "Dibbits Fixture"
// block). Repeated here as data, the same way the scrub-name pair repeats the migration-planted
// customer ids: the id is the join key and the plan may not derive it.
const (
	islandQuoteID  = "df000000-0000-4000-9000-0000000000c1"
	islandOrderQID = "df000000-0000-4000-9000-0000000000d1"
	islandOrderXID = "df000000-0000-4000-9000-0000000000d2"
)

// islandLines writes the quote_lines and order_lines the island's headers promised.
//
// THE ARITHMETIC IS THE MIGRATION'S OWN, to the cent, because the applier cannot UPDATE the
// headers to match new lines: the lines must sum onto the figures 0055 already planted. Every
// unit price is the demo catalogue's list price in the product's base uom, so a clerk who
// re-enters the quantity at the counter resolves the same money.
//
//	Q-DEMO-CP03     20000 = P04 wall block 16 PC at 1250
//	SO-DEMO-CP03-Q  20000 = P04 wall block 16 PC at 1250, the quoted order's like-for-like line
//	SO-DEMO-CP03-X  50000 = P05 step slab 4 PC at 9800 + P09 mulch 2 CYD at 5400, the add-on spend
func (b *builder) islandLines() {
	at := b.agoDays(9)

	// The quote's line. pre_discount_total and disc_pct ride at their no-discount values, the
	// shape every undisturbed quote line carries (the guarded discount write stamps the pair; a
	// line nobody discounted has its gross equal to its net).
	q := b.prod["P04"]
	b.add(insert("quote_lines", "(id)",
		"id", demoID("islandline:quote:1"),
		"quote_id", islandQuoteID,
		"line_no", 1,
		"product_id", demoID("product:P04"),
		"description", q.Name,
		"qty_entered", "16.0000", "uom_entered", q.UOM, "qty_base", "16.0000",
		"unit_price_cents", int64(1250), "price_uom", q.UOM,
		"line_total_cents", int64(20000), "pre_discount_total_cents", int64(20000), "disc_pct", 0,
		"metadata", demoMetadata,
		"created_at", at, "updated_at", at,
	))

	// The orders' lines. fulfilled_base carries the full quantity: both island orders are INVOICED
	// history, and the order book gives an INVOICED order's lines their whole quantity fulfilled.
	orderLine := func(key, orderID string, no int, prod string, qty, unit int64) {
		p := b.prod[prod]
		qtyLit := fmt.Sprintf("%d.0000", qty)
		b.add(insert("order_lines", "(id)",
			"id", demoID(key),
			"order_id", orderID,
			"line_no", no,
			"product_id", demoID("product:"+prod),
			"description", p.Name,
			"qty_entered", qtyLit, "uom_entered", p.UOM, "qty_base", qtyLit,
			"fulfilled_base", qtyLit,
			"unit_price_cents", unit, "price_uom", p.UOM,
			"line_total_cents", qty * unit,
			"metadata", demoMetadata,
			"created_at", at, "updated_at", at,
		))
	}
	orderLine("islandline:orderq:1", islandOrderQID, 1, "P04", 16, 1250)
	orderLine("islandline:orderx:1", islandOrderXID, 1, "P05", 4, 9800)
	orderLine("islandline:orderx:2", islandOrderXID, 2, "P09", 2, 5400)
}
