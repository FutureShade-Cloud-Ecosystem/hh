// quotes.go, the quote world: what the Quote Desk holds in the demo world, as data.
//
// The demo world held no quote. This section adds three, for the two proof customers (C01 and C02),
// owned by two different salespeople, in the one branch the demo logins reach, covering the states a
// tester needs: a DRAFT with lines and a floor request waiting on the branch's Sales manager, a SENT
// quote whose share link a tester can open, and one ACCEPTED quote ready to convert whose lines
// carry a discount a manager cleared and a discount that sits above the floor.
//
// THE OWNERS ARE THE TWO DEMO LOGINS, and that is why this section is conditional. The demo populate
// plants the Sales login (Inside Sales) and the Sales manager login (Sales manager) AFTER this command
// has run once, so on the first apply neither exists and this section builds nothing, exactly the
// posture the freight approvals take for a missing dispatcher (a quote with no salesperson is owned
// by nobody, QD-1, and a request with no requester is a card nobody filed). A second apply once the
// populate has run recognises everything already written and adds this section. The one ordering
// consequence for a box is named in the report: the entrypoint runs the apply again after the
// populate. Why these two and not the migrations' planted accounts: the floor request's requester
// must READ as Inside Sales (identity's requester role read goes through the platform link and the
// staff role grant), and only the populate's two accounts carry both, so a planted account's request
// would read as no salesperson and reach leadership alone.
//
// EVERY NUMBER IS WHAT THE QUOTE BUILDER WOULD HAVE WRITTEN, by the builder's own integer rules, so a
// seeded figure the app recomputes differently cannot exist:
//
//   - the line's unit price is the rule price the resolver strikes for the customer (b.unitPrice,
//     the same demo rules the order book prices from, no quantity break crossed on any quote line);
//   - a discount is struck from the stored gross, half up once: net = (gross*(100-d)+50)/100 (the
//     discount write, builder_repository.go UpdateLineDiscountGuarded), with the gross kept in
//     pre_discount_total_cents;
//   - the header is the terms write's money: subtotal from the lines, tax flat HST over the subtotal
//     (plus delivery, none here) half up ONCE at the header, total the three added;
//   - the pricing log rows are the rows the floor seam writes (quote/floor.go floorLogRows and
//     rulePriceLogRows): an UPDATE row for a below rule above floor price and for a line accepted at a
//     rule price that itself sits below the floor (CP0-3 answer A), a BYPASS row naming
//     MANAGER_SCOPE_OVERRIDE for a below floor price the Sales manager cleared, with the clearing
//     stamped on the line's metadata exactly as the write stamps it;
//   - the floor request is the row identity's FileFloorApproval writes: the quote id as subject, the
//     detail sentence the quote module builds from its own figures, and the binding (line number,
//     discount, the line's OWN row id and the product) in data.
//
// The cost and the floor the margin figures are struck over are the world's own: product_branch_costs
// (§16) for the quote's branch, and the category floor the migrations' policy rows carry
// (demoFloorBps), asserted against the database by the world's proof so a changed policy fails loudly
// rather than seeding a stale margin.
//
// NOT DAY-KEYED. A quote is a document, not a day: stable keys, written once. The share links and the
// quotes' own validity run well past the clock the first apply read, far enough that a box looked
// at weekly keeps a link that opens.
package main

import (
	"encoding/json"
	"fmt"
)

// The demo role accounts, by the login emails the demo populate writes (core/internal/demo
// populate.go: salesEmail and managerEmail). The email is the join key and not the id, for the reason
// resolvePersonas gives: the populate's own proof asserts these two literals still name its accounts.
const (
	personaQuoteSales   = "sales@hh-demo.invalid"
	personaQuoteManager = "sales.manager@hh-demo.invalid"
)

// quotePersonaEmails is the pair resolve.go adds to the personas it looks up. Kept apart from
// demoPersonaEmails on purpose: that list is "the three logins 0200 plants" and the command prints
// which of them a database has; these two arrive later, from the demo populate.
var quotePersonaEmails = []string{personaQuoteSales, personaQuoteManager}

// resolvablePersonaEmails is everything resolvePersonas asks the database for.
func resolvablePersonaEmails() []string {
	out := append([]string{}, demoPersonaEmails...)
	return append(out, quotePersonaEmails...)
}

// demoFloorBps is the margin floor the world's two categories carry (the PAVER and BULK policy rows
// the migrations plant, both at 2000). The plan may not read a database, so the figure is held here
// and the world's proof compares it with the table.
const demoFloorBps = 2000

// quoteValidDays is how long past the clock a quote's validity and a share link's life run.
const quoteValidDays = 365

type quoteLineSpec struct {
	Prod    string
	Qty     int // whole base units, every demo quote line prices a whole quantity
	DiscPct int // the line discount, 0 for none
	// Cleared marks a below floor discount the branch's Sales manager cleared on its own authority:
	// the line carries the clearing stamp and the pricing log a BYPASS row naming the manager.
	Cleared bool
}

type quoteSpec struct {
	Key, Cust, Status string
	Owner             string // the owner persona's login email
	Branch            int
	AgeDays           int
	Project, PO       string
	Contact, Notes    string
	Lines             []quoteLineSpec
	// Link gives the quote a share link (a SENT quote is sent through one, an ACCEPTED quote was
	// accepted through one); Accepted adds the acceptance record behind an ACCEPTED quote.
	Link bool
	// Editor is the persona whose hand last edited the quote (the one that cleared a line); empty
	// means the owner.
	Editor string
}

// The three quotes. All at the branch the demo logins reach (index 1, Trenton on the demo database).
//
//	Q1  DRAFT     C02  Sales          Brussels 200 PC at the rule price, and a request filed for a 12 percent
//	                                  discount on it that lands below the floor; edging 30 EA at 4 percent
//	                                  below the rule price and above the floor (an UPDATE log row)
//	Q2  SENT      C01  Sales manager  Holland pavers 240 PC at a rule price that sits below the floor
//	                                  (CP0-3 answer A: accepted, marked, logged), Granular A 8 T at list
//	Q3  ACCEPTED  C02  Sales          Brussels 150 PC at 12 percent off, below the floor and cleared by the
//	                                  Sales manager (a BYPASS row); edging 24 EA at 5 percent off above the
//	                                  floor (an UPDATE row); Concrete Sand 6 T at list. Ready to convert.
var demoQuotes = []quoteSpec{
	{Key: "Q1", Cust: "C02", Status: "DRAFT", Owner: personaQuoteSales, Branch: 1, AgeDays: 3,
		Project: "Marina Road patio", PO: "", Contact: "Site contact",
		Notes: "Draft for the patio, pavers and edging.",
		Lines: []quoteLineSpec{{Prod: "P02", Qty: 200}, {Prod: "P10", Qty: 30, DiscPct: 4}}},
	{Key: "Q2", Cust: "C01", Status: "SENT", Owner: personaQuoteManager, Branch: 1, AgeDays: 5,
		Project: "Front walk and drive apron", PO: "PO-DEMO-0042", Contact: "Site foreman",
		Notes: "Sent for review, the paver price is the account price.",
		Lines: []quoteLineSpec{{Prod: "P01", Qty: 240}, {Prod: "P07", Qty: 8}}, Link: true},
	{Key: "Q3", Cust: "C02", Status: "ACCEPTED", Owner: personaQuoteSales, Branch: 1, AgeDays: 8,
		Project: "Harbour Street steps", PO: "PO-DEMO-0077", Contact: "Site contact",
		Notes: "Accepted by the customer, ready to become an order.",
		Lines: []quoteLineSpec{
			{Prod: "P02", Qty: 150, DiscPct: 12, Cleared: true},
			{Prod: "P10", Qty: 24, DiscPct: 5},
			{Prod: "P08", Qty: 6},
		}, Link: true, Editor: personaQuoteManager},
}

// quoteRequestSpec is one pending floor request: the salesperson asked for a discount on one line of
// one of the quotes above that lands below the floor, and the branch's Sales manager has not decided.
type quoteRequestSpec struct {
	Key, Quote string
	LineNo     int
	DiscPct    int
	Note       string
	AgeHours   int
}

var demoQuoteRequests = []quoteRequestSpec{
	{"FR1", "Q1", 1, 12, "Customer is comparing a second yard, asking for twelve percent on the Brussels.", 20},
}

// quoteLineFacts is one line priced by the builder's own rules.
type quoteLineFacts struct {
	spec                  quoteLineSpec
	no                    int
	rule, gross, net      int64
	netUnit               int64
	cost                  int64
	ruleMargin, netMargin int64
	belowRule, belowFloor bool
	ruleBelowFloor        bool
}

func qMarginBps(price, cost int64) int64 {
	if price <= 0 {
		return 0
	}
	return ((price - cost) * 10000) / price
}

// qDiscountCents is the half up discount of cents, once, the arithmetic the discount write applies to
// the gross (and the floor decision applies to the unit price).
func qDiscountCents(cents int64, discPct int) int64 {
	return (cents*int64(100-discPct) + 50) / 100
}

// quoteFacts prices one quote's lines.
func (b *builder) quoteFacts(q quoteSpec) []quoteLineFacts {
	out := make([]quoteLineFacts, 0, len(q.Lines))
	for i, l := range q.Lines {
		f := quoteLineFacts{spec: l, no: i + 1}
		f.rule = b.unitPrice(q.Cust, l.Prod)
		f.gross = int64(l.Qty) * f.rule
		f.net = qDiscountCents(f.gross, l.DiscPct)
		f.netUnit = qDiscountCents(f.rule, l.DiscPct)
		f.cost = branchCost[l.Prod][q.Branch]
		f.ruleMargin = qMarginBps(f.rule, f.cost)
		f.netMargin = qMarginBps(f.netUnit, f.cost)
		f.belowRule = l.DiscPct > 0
		f.ruleBelowFloor = f.ruleMargin < demoFloorBps
		f.belowFloor = f.netMargin < demoFloorBps
		out = append(out, f)
	}
	return out
}

// quoteMoney is the header the terms write stamps: the subtotal of the lines, flat HST over it half up
// once, and the total.
func quoteMoney(lines []quoteLineFacts) (subtotal, tax, total int64) {
	for _, l := range lines {
		subtotal += l.net
	}
	tax = taxOn(subtotal)
	return subtotal, tax, subtotal + tax
}

// quoteNo mints a DEMO-prefixed quote number, never from quote_no_seq (plan.go's first ruling).
func (b *builder) quoteNo(branch int, key string) string {
	return fmt.Sprintf("%s%s-%s", demoPrefix, b.branchCode(branch), key)
}

// quoteToken is a SENT or ACCEPTED quote's raw share token: derived, so a tester's page can cite it,
// and printed next to its quote by `demoseed -plan` the way a will-call token is.
func quoteToken(key string) string { return demoRef("dqt_", "quotelink:"+key) }

// quoteWorld is the section's hook (the one call buildPlan makes). It builds nothing when either demo
// login is absent.
func (b *builder) quoteWorld() {
	sales, manager := b.in.Personas[personaQuoteSales], b.in.Personas[personaQuoteManager]
	if sales == "" || manager == "" {
		return
	}
	who := map[string]string{personaQuoteSales: sales, personaQuoteManager: manager}
	roleWord := map[string]string{personaQuoteSales: "Sales", personaQuoteManager: "Sales manager"}

	// add records the id of every row this section writes, so the proof can name exactly which rows
	// disappear when a login does.
	add := func(r row) {
		b.add(r)
		b.sum.QuoteRowIDs = append(b.sum.QuoteRowIDs, fmt.Sprint(r.vals[0]))
	}
	byKey := map[string]quoteSpec{}
	facts := map[string][]quoteLineFacts{}

	for _, q := range demoQuotes {
		byKey[q.Key] = q
		lines := b.quoteFacts(q)
		facts[q.Key] = lines
		subtotal, tax, total := quoteMoney(lines)
		owner := who[q.Owner]
		editor := owner
		if q.Editor != "" {
			editor = who[q.Editor]
		}
		created := b.agoDays(q.AgeDays)
		updated := b.agoDays(q.AgeDays - 1)
		quoteID := demoID("quote:" + q.Key)
		no := b.quoteNo(q.Branch, q.Key)
		add(insert("quotes", "(id)",
			"id", quoteID,
			"quote_no", no,
			"customer_id", demoID("customer:"+q.Cust),
			"branch_id", b.branchID(q.Branch),
			"status", q.Status,
			"valid_until", b.day(quoteValidDays),
			"subtotal_cents", subtotal, "tax_cents", tax, "total_cents", total,
			"created_by", owner, "updated_by", editor,
			"salesperson_id", owner,
			"contact_name", q.Contact, "project_job", q.Project, "customer_po", q.PO,
			"customer_notes", q.Notes,
			"fulfillment", "PICKUP",
			"metadata", demoMetadata,
			"created_at", created, "updated_at", updated,
		))
		for _, f := range lines {
			p := b.prod[f.spec.Prod]
			meta := map[string]any{"demo_seed": demoMarker}
			if f.spec.Cleared {
				// The clearing the write stamps (quote/floor.go floorClearingStamp): who cleared it, by
				// which authority, at what discount and margin. The manager's own scope, no approval.
				meta["floor_clear"] = map[string]any{
					"cleared_by": manager, "authority": "MANAGER_SCOPE",
					"disc_pct": f.spec.DiscPct, "margin_bps": f.netMargin,
				}
			}
			raw, _ := json.Marshal(meta)
			qty := fmt.Sprintf("%d.0000", f.spec.Qty)
			add(insert("quote_lines", "(id)",
				"id", demoID(fmt.Sprintf("quoteline:%s:%d", q.Key, f.no)),
				"quote_id", quoteID,
				"line_no", f.no,
				"product_id", demoID("product:"+f.spec.Prod),
				"description", p.Name,
				"qty_entered", qty, "uom_entered", p.UOM, "qty_base", qty,
				"unit_price_cents", f.rule, "price_uom", p.UOM,
				"line_total_cents", f.net, "pre_discount_total_cents", f.gross,
				"disc_pct", f.spec.DiscPct,
				"metadata", string(raw),
				"created_at", created, "updated_at", updated,
			))
		}

		// The pricing log, one row per decision the floor seam would have written for these lines.
		for _, f := range lines {
			entity := b.in.Categories[b.prod[f.spec.Prod].Category]
			if entity == "" {
				entity = demoID("product:" + f.spec.Prod) // FloorRecord's fallback keeps the NOT NULL id honest
			}
			logRow := func(suffix, action, reason, user string, ruleU, netU int64, disc int, margin int64) {
				cols := []any{
					"id", demoID(fmt.Sprintf("quotelog:%s:%d:%s", q.Key, f.no, suffix)),
					"entity_type", "margin_enforcement",
					"entity_id", entity,
					"action", action,
					"field_name", fmt.Sprintf("line_%d", f.no),
					"old_value", fmt.Sprintf("rule %d at %d%%", ruleU, disc),
					"new_value", fmt.Sprintf("net %d margin %d floor %d", netU, margin, demoFloorBps),
					"user_id", user,
					"ref_entity_type", "quote", "ref_entity_id", quoteID,
					"created_at", updated,
				}
				if reason != "" {
					cols = append(cols, "bypass_reason", reason)
				}
				add(insert("pricing_audit_log", "(id)", cols...))
			}
			switch {
			case f.belowRule && f.belowFloor && f.spec.Cleared:
				logRow("bypass", "BYPASS", "MANAGER_SCOPE_OVERRIDE", manager, f.rule, f.netUnit, f.spec.DiscPct, f.netMargin)
			case f.belowRule && !f.belowFloor:
				logRow("update", "UPDATE", "", owner, f.rule, f.netUnit, f.spec.DiscPct, f.netMargin)
			case !f.belowRule && f.ruleBelowFloor:
				// A line accepted at a rule price under the floor (CP0-3 answer A): the rule and the net
				// are the same price, logged for the pricing editors.
				logRow("rule", "UPDATE", "", owner, f.rule, f.rule, 0, f.ruleMargin)
			}
		}

		// The share link, and what stands behind an accepted quote.
		if q.Link {
			linkID := demoID("quotelink:" + q.Key)
			add(insert("quote_share_links", "(id)",
				"id", linkID,
				"quote_id", quoteID,
				"token_hash", demoTokenHash(quoteToken(q.Key)),
				"expires_at", b.day(quoteValidDays),
				"created_by", owner,
				"created_at", created,
			))
			b.sum.QuoteLinks = append(b.sum.QuoteLinks,
				fmt.Sprintf("%s · %s · %s · token %s", q.Key, no, q.Status, quoteToken(q.Key)))
			if q.Status == "ACCEPTED" {
				at := b.agoDays(q.AgeDays - 2)
				add(insert("quote_acceptances", "(id)",
					"id", demoID("quoteaccept:"+q.Key),
					"quote_id", quoteID,
					"share_link_id", linkID,
					"quote_no", no,
					"typed_name", "Customer representative (demo)",
					"accepted_subtotal_cents", subtotal,
					"accepted_tax_cents", tax,
					"accepted_total_cents", total,
					"accepted_at", at,
				))
				add(insert("quote_share_link_events", "(id)",
					"id", demoID("quoteevent:"+q.Key+":accept"),
					"share_link_id", linkID, "event", "ACCEPT", "occurred_at", at,
				))
			}
		}
		b.sum.Quotes = append(b.sum.Quotes, fmt.Sprintf("%s · %s · %s · owner %s · %s · %d lines · total %d",
			q.Key, no, b.cust[q.Cust].Name, roleWord[q.Owner], q.Status, len(lines), total))
	}

	// The pending floor requests, filed the way identity's FileFloorApproval files them.
	for _, r := range demoQuoteRequests {
		q := byKey[r.Quote]
		f := facts[r.Quote][r.LineNo-1]
		candidate := qDiscountCents(f.rule, r.DiscPct)
		margin := qMarginBps(candidate, f.cost)
		data := map[string]any{
			"demo_seed": demoMarker,
			"line_no":   r.LineNo, "disc_pct": r.DiscPct,
			"line_id":    demoID(fmt.Sprintf("quoteline:%s:%d", q.Key, r.LineNo)),
			"product_id": demoID("product:" + f.spec.Prod),
			"note":       r.Note,
		}
		raw, _ := json.Marshal(data)
		add(insert("permission_approval_requests", "(id)",
			"id", demoID("floorrequest:"+r.Key),
			"requested_by", who[q.Owner],
			"branch_id", b.branchID(q.Branch),
			"policy_type", "MARGIN_BELOW_MINIMUM",
			// quote/floor.go RequestFloorApproval's sentence, to the word: the quote number, the line,
			// the product, the asked discount, the resulting margin and the floor.
			"detail", fmt.Sprintf("quote %s line %d (%s): %d percent discount, resulting margin %d bps against the %d bps floor",
				b.quoteNo(q.Branch, q.Key), r.LineNo, demoID("product:"+f.spec.Prod), r.DiscPct, margin, demoFloorBps),
			"subject_ref", demoID("quote:"+q.Key),
			"data", string(raw),
			"status", "PENDING",
			"requested_at", b.ago(r.AgeHours),
		))
	}
}
