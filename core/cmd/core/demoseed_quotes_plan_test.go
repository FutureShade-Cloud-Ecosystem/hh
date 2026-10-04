package main

// demoseed_quotes_plan_test.go, the quote world's promises asserted on the PLAN, without a database.
// The database half (the app reads these rows back, the token opens the public view, the accepted
// quote converts, the seed is idempotent) is demoseed_quotes_world_test.go.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// quoteRows pulls the quote world's rows out of a plan, by table.
func quoteRows(rows []row, sum planSummary) map[string][]map[string]any {
	ids := map[string]bool{}
	for _, id := range sum.QuoteRowIDs {
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

func TestQuotePlan_NeedsBothDemoLogins(t *testing.T) {
	for name, drop := range map[string][]string{
		"neither":  {personaQuoteSales, personaQuoteManager},
		"no sales": {personaQuoteSales}, "no manager": {personaQuoteManager},
	} {
		in := testInput(t)
		for _, p := range drop {
			delete(in.Personas, p)
		}
		_, sum, err := buildPlan(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(sum.QuoteRowIDs) != 0 || len(sum.Quotes) != 0 {
			t.Errorf("%s: the quote world built %d rows with a demo login missing", name, len(sum.QuoteRowIDs))
		}
	}
}

func TestQuotePlan_TheWorldHoldsTheStatesATesterNeeds(t *testing.T) {
	in := testInput(t)
	rows, sum, err := buildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	q := quoteRows(rows, sum)
	if got := len(q["quotes"]); got != 3 {
		t.Fatalf("the world holds %d quotes, want 3", got)
	}
	sales, manager := in.Personas[personaQuoteSales], in.Personas[personaQuoteManager]
	byStatus := map[string]map[string]any{}
	owners := map[string]bool{}
	custs := map[string]bool{}
	for _, f := range q["quotes"] {
		byStatus[fmt.Sprint(f["status"])] = f
		owners[fmt.Sprint(f["salesperson_id"])] = true
		custs[fmt.Sprint(f["customer_id"])] = true
		if f["salesperson_id"] != f["created_by"] {
			t.Errorf("quote %v: the salesperson is not the creator", f["quote_no"])
		}
		if f["salesperson_id"] == "" || f["salesperson_id"] == nil {
			t.Errorf("quote %v is ownerless", f["quote_no"])
		}
		if f["branch_id"] != in.Branches[1].ID {
			t.Errorf("quote %v is at %v, want the second branch by code (Trenton, the demo logins' branch)", f["quote_no"], f["branch_id"])
		}
		if !strings.HasPrefix(fmt.Sprint(f["quote_no"]), demoPrefix) {
			t.Errorf("quote number %v is not DEMO-prefixed", f["quote_no"])
		}
	}
	for _, s := range []string{"DRAFT", "SENT", "ACCEPTED"} {
		if byStatus[s] == nil {
			t.Errorf("no %s quote", s)
		}
	}
	if len(owners) != 2 || !owners[sales] || !owners[manager] {
		t.Errorf("owners %v, want exactly the Sales and the Sales manager logins", owners)
	}
	if !custs[demoID("customer:C01")] || !custs[demoID("customer:C02")] || len(custs) != 2 {
		t.Errorf("customers %v, want C01 and C02 only", custs)
	}
	// Every quote line is a priced, whole-quantity line on a demo product.
	if len(q["quote_lines"]) != 7 {
		t.Errorf("the world holds %d quote lines, want 7", len(q["quote_lines"]))
	}
	// A tester can open a SENT quote and an ACCEPTED quote by a derived token, and the stored hash is
	// the hash of that printed token, never the token.
	if len(q["quote_share_links"]) != 2 || len(sum.QuoteLinks) != 2 {
		t.Fatalf("share links %d, printed %d, want 2 and 2", len(q["quote_share_links"]), len(sum.QuoteLinks))
	}
	for _, key := range []string{"Q2", "Q3"} {
		tok := quoteToken(key)
		found := false
		for _, l := range q["quote_share_links"] {
			if l["token_hash"] == demoTokenHash(tok) {
				found = true
			}
		}
		if !found {
			t.Errorf("no share link stores the hash of %s's printed token", key)
		}
		printed := false
		for _, l := range sum.QuoteLinks {
			if strings.HasPrefix(l, key+" ") && strings.HasSuffix(l, " token "+tok) {
				printed = true
			}
		}
		if !printed {
			t.Errorf("the plan summary does not print %s's token", key)
		}
	}
	if len(q["quote_acceptances"]) != 1 {
		t.Errorf("acceptance records %d, want 1 (the ACCEPTED quote only)", len(q["quote_acceptances"]))
	}
}

// The money: every figure is the builder's own integer arithmetic over the stored gross.
func TestQuotePlan_TheMoneyIsWhatTheBuilderWrites(t *testing.T) {
	rows, sum := buildForTest(t)
	q := quoteRows(rows, sum)
	lineSum := map[string]int64{}
	for _, l := range q["quote_lines"] {
		gross := l["pre_discount_total_cents"].(int64)
		net := l["line_total_cents"].(int64)
		disc := l["disc_pct"].(int)
		if want := (gross*int64(100-disc) + 50) / 100; net != want {
			t.Errorf("line %v: net %d, the discount write would store %d", l["id"], net, want)
		}
		unit := l["unit_price_cents"].(int64)
		qty := strings.TrimSuffix(fmt.Sprint(l["qty_entered"]), ".0000")
		var n int64
		fmt.Sscanf(qty, "%d", &n)
		if gross != n*unit {
			t.Errorf("line %v: gross %d is not %d x %d", l["id"], gross, n, unit)
		}
		lineSum[fmt.Sprint(l["quote_id"])] += net
	}
	for _, f := range q["quotes"] {
		id := fmt.Sprint(f["id"])
		sub := f["subtotal_cents"].(int64)
		tax := f["tax_cents"].(int64)
		total := f["total_cents"].(int64)
		if sub != lineSum[id] {
			t.Errorf("quote %v: subtotal %d, lines sum to %d", f["quote_no"], sub, lineSum[id])
		}
		if tax != taxOn(sub) {
			t.Errorf("quote %v: tax %d, flat HST over the subtotal is %d", f["quote_no"], tax, taxOn(sub))
		}
		if total != sub+tax {
			t.Errorf("quote %v: total %d, want %d", f["quote_no"], total, sub+tax)
		}
	}
}

// The floor stories: a rule price below the floor, a discount above it, a discount below it that the
// Sales manager cleared, and one request waiting. Each log row, stamp and binding is read back.
func TestQuotePlan_TheFloorStoriesAreAllThere(t *testing.T) {
	in := testInput(t)
	rows, sum, err := buildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	q := quoteRows(rows, sum)
	sales, manager := in.Personas[personaQuoteSales], in.Personas[personaQuoteManager]

	logs := map[string]map[string]any{}
	for _, l := range q["pricing_audit_log"] {
		logs[fmt.Sprint(l["ref_entity_id"])+"/"+fmt.Sprint(l["field_name"])+"/"+fmt.Sprint(l["action"])] = l
	}
	// Q2 line 1: Holland at the account price, the rule price itself below the floor: marked, logged
	// as an UPDATE with the rule and the net the same price, no manager involved.
	l := logs[demoID("quote:Q2")+"/line_1/UPDATE"]
	if l == nil {
		t.Fatal("no UPDATE row for the Holland line whose rule price sits below the floor")
	}
	if l["user_id"] != manager || !strings.HasPrefix(fmt.Sprint(l["old_value"]), "rule 488 at 0%") {
		t.Errorf("the rule price row is %v / %v", l["user_id"], l["old_value"])
	}
	// Q3 line 1: below the floor and cleared by the Sales manager: a BYPASS row naming the authority,
	// and the stamp on the line the write would have set.
	l = logs[demoID("quote:Q3")+"/line_1/BYPASS"]
	if l == nil {
		t.Fatal("no BYPASS row for the cleared line")
	}
	if l["bypass_reason"] != "MANAGER_SCOPE_OVERRIDE" || l["user_id"] != manager {
		t.Errorf("the BYPASS row is %v by %v", l["bypass_reason"], l["user_id"])
	}
	// What the log row names as its entity is what the real write path names (pricing.entityIDOf):
	// the PRODUCT for the rule price row (no category rides the rule price answer, floor.go
	// rulePriceLogRows), the CATEGORY for the rows that come from a discount decision. The pricing
	// log reader filters on entity_id, so a row naming the wrong one is invisible to its editors.
	wantEntity := map[string]string{
		demoID("quote:Q2") + "/line_1/UPDATE": demoID("product:P01"),
		demoID("quote:Q1") + "/line_2/UPDATE": in.Categories[quoteProdCategory(t, "P10")],
		demoID("quote:Q3") + "/line_1/BYPASS": in.Categories[quoteProdCategory(t, "P02")],
		demoID("quote:Q3") + "/line_2/UPDATE": in.Categories[quoteProdCategory(t, "P10")],
	}
	if len(wantEntity) != len(q["pricing_audit_log"]) {
		t.Errorf("the plan holds %d pricing log rows, the entity pin covers %d", len(q["pricing_audit_log"]), len(wantEntity))
	}
	for k, want := range wantEntity {
		if row := logs[k]; row == nil || row["entity_id"] != want || row["entity_type"] != "margin_enforcement" {
			t.Errorf("log row %s names entity %v, the real write path names %s", k, row["entity_id"], want)
		}
	}
	// Q3 line 2 and Q1 line 2: below the rule price, above the floor, accepted with no manager.
	for _, k := range []string{demoID("quote:Q3") + "/line_2/UPDATE", demoID("quote:Q1") + "/line_2/UPDATE"} {
		u := logs[k]
		if u == nil {
			t.Errorf("no UPDATE row at %s", k)
			continue
		}
		if u["user_id"] != sales {
			t.Errorf("the above floor discount at %s is logged under %v, want the salesperson", k, u["user_id"])
		}
	}
	for _, line := range q["quote_lines"] {
		if line["id"] != demoID("quoteline:Q3:1") {
			continue
		}
		var meta struct {
			Clear struct {
				By        string `json:"cleared_by"`
				Authority string `json:"authority"`
				Disc      int    `json:"disc_pct"`
				Margin    int64  `json:"margin_bps"`
			} `json:"floor_clear"`
		}
		if err := json.Unmarshal([]byte(fmt.Sprint(line["metadata"])), &meta); err != nil {
			t.Fatal(err)
		}
		if meta.Clear.By != manager || meta.Clear.Authority != "MANAGER_SCOPE" || meta.Clear.Disc != 12 {
			t.Errorf("the clearing stamp is %+v", meta.Clear)
		}
		if meta.Clear.Margin >= demoFloorBps {
			t.Errorf("the cleared line's margin %d is not below the %d floor", meta.Clear.Margin, demoFloorBps)
		}
		if want := fmt.Sprintf("net 577 margin %d floor %d", meta.Clear.Margin, demoFloorBps); logs[demoID("quote:Q3")+"/line_1/BYPASS"]["new_value"] != want {
			t.Errorf("the BYPASS row says %v, want %q", logs[demoID("quote:Q3")+"/line_1/BYPASS"]["new_value"], want)
		}
	}
	// Exactly one pending floor request, filed by Sales (never by the manager who decides it), bound to
	// the line's own row and product, in the quote's branch.
	if len(q["permission_approval_requests"]) != 1 {
		t.Fatalf("floor requests: %d, want 1", len(q["permission_approval_requests"]))
	}
	req := q["permission_approval_requests"][0]
	if req["requested_by"] != sales || req["requested_by"] == manager {
		t.Errorf("the request is filed by %v, want the Sales login and not the deciding manager", req["requested_by"])
	}
	if req["policy_type"] != "MARGIN_BELOW_MINIMUM" || req["status"] != "PENDING" {
		t.Errorf("the request is %v %v", req["policy_type"], req["status"])
	}
	if req["subject_ref"] != demoID("quote:Q1") || req["branch_id"] != in.Branches[1].ID {
		t.Errorf("the request names subject %v at %v", req["subject_ref"], req["branch_id"])
	}
	var data struct {
		LineNo    int    `json:"line_no"`
		DiscPct   int    `json:"disc_pct"`
		LineID    string `json:"line_id"`
		ProductID string `json:"product_id"`
	}
	if err := json.Unmarshal([]byte(fmt.Sprint(req["data"])), &data); err != nil {
		t.Fatal(err)
	}
	if data.LineID != demoID("quoteline:Q1:1") || data.ProductID != demoID("product:P02") || data.LineNo != 1 || data.DiscPct != 12 {
		t.Errorf("the request binding is %+v", data)
	}
	lineFound := false
	for _, line := range q["quote_lines"] {
		if line["id"] == data.LineID {
			lineFound = line["product_id"] == data.ProductID && line["quote_id"] == demoID("quote:Q1")
		}
	}
	if !lineFound {
		t.Error("the request's line id does not name a line of the quote it is filed on, with that product")
	}
	if want := "margin 1126 bps against the 2000 bps floor"; !strings.Contains(fmt.Sprint(req["detail"]), want) {
		t.Errorf("the request detail %q lacks %q", req["detail"], want)
	}
}

// A seeded quote line prices in the world's own arithmetic: the rule price is what the order book
// prices the same customer and product at.
func TestQuotePlan_RulePricesAreTheOrderBooksPrices(t *testing.T) {
	b := &builder{prod: map[string]prodSpec{}}
	for _, p := range demoProducts {
		b.prod[p.Key] = p
	}
	for _, c := range []struct {
		cust, prod string
		want       int64
	}{{"C01", "P01", 488}, {"C02", "P02", 656}, {"C02", "P10", 1848}, {"C02", "P08", 3850}, {"C01", "P07", 3250}} {
		if got := b.unitPrice(c.cust, c.prod); got != c.want {
			t.Errorf("%s %s rule price %d, the quote stories assume %d", c.cust, c.prod, got, c.want)
		}
	}
}

// quoteProdCategory is the category key a demo product belongs to.
func quoteProdCategory(t *testing.T, prod string) string {
	t.Helper()
	for _, p := range demoProducts {
		if p.Key == prod {
			return p.Category
		}
	}
	t.Fatalf("no demo product %s", prod)
	return ""
}

// An accepted quote carries what a real accept leaves: the QUOTE_ACCEPTED audit row in the change
// trail (the actor the link and not a staff user, only allowlisted details), the acceptance record
// anchored to it, and an updated_at that is not earlier than the acceptance (share_repository.go
// sets both at the accept).
func TestQuotePlan_TheAcceptanceIsAnchoredAndItsQuoteUpdatedAtTheAccept(t *testing.T) {
	rows, sum := buildForTest(t)
	q := quoteRows(rows, sum)
	if len(q["audit_log"]) != 1 {
		t.Fatalf("the world holds %d audit rows, want the one QUOTE_ACCEPTED row", len(q["audit_log"]))
	}
	a := q["audit_log"][0]
	if a["action"] != "QUOTE_ACCEPTED" || a["entity_type"] != "quote" || a["entity_id"] != demoID("quote:Q3") {
		t.Errorf("the audit row is %v on %v %v", a["action"], a["entity_type"], a["entity_id"])
	}
	if a["actor_id"] != nil || a["branch_id"] != nil {
		t.Errorf("the audit row names actor %v branch %v, an acceptance has no staff principal", a["actor_id"], a["branch_id"])
	}
	var changes map[string]any
	if err := json.Unmarshal([]byte(fmt.Sprint(a["changes"])), &changes); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{"purpose": "CUSTOMER_QUOTE_ACCEPTANCE", "reason": "status", "field": "status",
		"before": "SENT", "after": "ACCEPTED", "ref_type": "quote_share_link"} {
		if changes[k] != want {
			t.Errorf("audit change %s is %v, want %v", k, changes[k], want)
		}
	}
	if len(changes) != 7 {
		t.Errorf("the audit row carries %d detail keys (%v), the real accept writes 7 allowlisted ones", len(changes), changes)
	}
	if len(q["quote_acceptances"]) != 1 {
		t.Fatalf("acceptance records %d, want 1", len(q["quote_acceptances"]))
	}
	acc := q["quote_acceptances"][0]
	anchor, ok := acc["audit_seq"].(auditAnchor)
	if !ok || string(anchor) != fmt.Sprint(a["id"]) {
		t.Errorf("the acceptance's audit_seq is %#v, want an anchor to the audit row %v", acc["audit_seq"], a["id"])
	}
	if changes["amount_cents"] != float64(acc["accepted_total_cents"].(int64)) {
		t.Errorf("the audit amount %v is not the accepted total %v", changes["amount_cents"], acc["accepted_total_cents"])
	}
	for _, f := range q["quotes"] {
		if f["status"] != "ACCEPTED" {
			continue
		}
		upd, acceptedAt := f["updated_at"].(time.Time), acc["accepted_at"].(time.Time)
		if upd.Before(acceptedAt) {
			t.Errorf("the accepted quote was updated %v, before its acceptance at %v", upd, acceptedAt)
		}
	}
}

// The applier binds the anchor as a parameter and renders the lookup around it; nothing is inlined.
func TestApply_AnAuditAnchorIsABoundLookup(t *testing.T) {
	r := insert("quote_acceptances", "(id)", "id", "x", "audit_seq", auditAnchor("a-1"))
	if got, want := r.sql(), "INSERT INTO quote_acceptances (id, audit_seq) VALUES ($1, (SELECT seq FROM audit_log WHERE id = $2::uuid)) ON CONFLICT (id) DO NOTHING"; got != want {
		t.Errorf("sql is\n%s\nwant\n%s", got, want)
	}
	if args := r.args(); len(args) != 2 || args[1] != "a-1" {
		t.Errorf("args %#v, want the id bound as text", args)
	}
}
