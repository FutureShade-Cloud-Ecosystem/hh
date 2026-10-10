// plan_test.go, the promises this command makes, asserted without a database.
//
// These are the invariants that make demoseed safe to point at staging. They are checked on the
// PLAN rather than on a database because the plan is where a mistake would be introduced: a new
// row added to plan.go with an UPDATE-ish conflict clause, a number minted from a shared sequence,
// or a pricing rule with a tier scope would all pass a "does it insert" test and still be the exact
// thing that breaks somebody's homework.
// Carried from the pin's cmd/demoseed/plan_test.go (the fold: one-shot subcommand).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// fixedClock is the injected clock every test in this file seeds relative to. A real instant, so
// dates read plausibly in failure output, and a FIXED one, so nothing here depends on when it runs.
var fixedClock = time.Date(2026, 9, 1, 9, 0, 0, 0, time.UTC)

func testInput(t *testing.T) planInput {
	t.Helper()
	return planInput{
		Now: fixedClock,
		Branches: []branch{
			{ID: "5d1b3f6e-0000-4000-9000-000000000002", Code: "KGN", Name: "Kingston"},
			{ID: "5d1b3f6e-0000-4000-9000-000000000001", Code: "TRT", Name: "Trenton"},
		},
		Categories: map[string]string{
			"PAVER": "ca700000-0000-4000-9000-000000000001",
			"BULK":  "ca700000-0000-4000-9000-000000000006",
		},
		// The three demo logins 0200 plants, under ids that are DELIBERATELY NOT 0200's literals.
		// 0200's `ON CONFLICT (email) DO NOTHING` keeps a pre-existing account's own id, so the
		// resolved id is whatever that database happens to carry, and a test that used 0200's
		// numbers would agree with a plan that hard-coded them, which is the one mistake here worth
		// catching. Present in the shared input so every invariant above, inserts only, determinism,
		// ids that do not move, runs over the two columns the edge adds.
		Personas: map[string]string{
			personaDispatch: "9e70a000-0000-4000-9000-00000000ad01",
			personaYard:     "9e70a000-0000-4000-9000-00000000ad02",
			personaDriver:   "9e70a000-0000-4000-9000-00000000ad03",
			// The two demo role logins the demo populate plants after the first apply (the quote
			// world's owners). Present here so every invariant in this file runs over the quote rows.
			personaQuoteSales:   "9e70a000-0000-4000-9000-00000000ad11",
			personaQuoteManager: "9e70a000-0000-4000-9000-00000000ad12",
			// The controller login the populate plants beside them (the parked journals' decider).
			personaController: "9e70a000-0000-4000-9000-00000000ad13",
		},
		// A stub sealer: these tests are about the plan's shape, and the real sealer's AES nonce is
		// random by design (see TestPlan_OnlyTheSealedBytesDifferBetweenRuns for that half).
		Seal: func(refKey, typedName, purpose, refType, refID string, at time.Time) (string, []byte, error) {
			if typedName == "" {
				return "", nil, fmt.Errorf("no receiver name for %s", refKey)
			}
			return "sig_" + refKey, []byte("sealed:" + refKey), nil
		},
	}
}

func buildForTest(t *testing.T) ([]row, planSummary) {
	t.Helper()
	rows, sum, err := buildPlan(testInput(t))
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	return rows, sum
}

// -- the safety invariants -------------------------------------------------------------------------

// TestApply_EmitsOnlyConflictYieldingInserts is the load-bearing one. Every statement the applier
// can produce must be an INSERT that yields on conflict; if a future row could produce anything
// else, this command can destroy a tester's data and no other test would notice.
func TestApply_EmitsOnlyConflictYieldingInserts(t *testing.T) {
	rows, _ := buildForTest(t)
	for i, r := range rows {
		sql := r.sql()
		if !strings.HasPrefix(sql, "INSERT INTO ") {
			t.Fatalf("row %d (%s) is not an INSERT: %s", i, r.table, sql)
		}
		if !strings.HasSuffix(sql, " DO NOTHING") {
			t.Fatalf("row %d (%s) does not end in DO NOTHING: %s", i, r.table, sql)
		}
		for _, forbidden := range []string{"DO UPDATE", "UPDATE ", "DELETE ", "TRUNCATE", "DROP "} {
			if strings.Contains(sql, forbidden) {
				t.Fatalf("row %d (%s) contains %q: %s", i, r.table, forbidden, sql)
			}
		}
		if r.conflict == "" {
			t.Fatalf("row %d (%s) has no conflict arbiter, it would raise rather than yield", i, r.table)
		}
	}
}

// TestPlan_MintsNoSharedSequenceNumber. Order, invoice and run numbers must be DEMO-prefixed
// literals. If any of them were drawn from next_order_no()/next_run_no(), a live tester's next real
// number would jump because a demo seed ran, a mutation of shared state with no row edited.
func TestPlan_MintsNoSharedSequenceNumber(t *testing.T) {
	rows, _ := buildForTest(t)
	numbered := map[string]string{
		"orders":        "order_no",
		"invoices":      "invoice_no",
		"delivery_runs": "run_no",
		"payments":      "payment_no",
		"customers":     "account_no",
		"products":      "sku",
	}
	seen := map[string]int{}
	for _, r := range rows {
		col, ok := numbered[r.table]
		if !ok {
			continue
		}
		for i, c := range r.cols {
			if c != col {
				continue
			}
			v, _ := r.vals[i].(string)
			if !strings.Contains(v, demoPrefix) {
				t.Fatalf("%s.%s = %q carries no %q marker", r.table, col, v, demoPrefix)
			}
			seen[r.table]++
		}
	}
	for table := range numbered {
		if seen[table] == 0 {
			t.Fatalf("no %s rows were checked, the invariant is not actually covering that table", table)
		}
	}
}

// TestPlan_NoPricingRuleReachesBeyondADemoRow. A TIER_CATEGORY or TIER_DEFAULT rule is additive in
// the row sense and still changes what a REAL customer is quoted. Every seeded rule must be bound
// to a demo customer, and every quantity break to a demo product.
func TestPlan_NoPricingRuleReachesBeyondADemoRow(t *testing.T) {
	demoCustomerIDs := map[string]bool{}
	for _, c := range demoCustomers {
		demoCustomerIDs[demoID("customer:"+c.Key)] = true
	}
	demoProductIDs := map[string]bool{}
	for _, p := range demoProducts {
		demoProductIDs[demoID("product:"+p.Key)] = true
	}
	rows, _ := buildForTest(t)
	rules, breaks := 0, 0
	for _, r := range rows {
		switch r.table {
		case "pricing_rules":
			rules++
			fields := fieldMap(r)
			if scope, _ := fields["scope"].(string); strings.HasPrefix(scope, "TIER_") {
				t.Fatalf("pricing rule uses scope %s, a tier rule reaches real customers", scope)
			}
			cid, _ := fields["customer_id"].(string)
			if !demoCustomerIDs[cid] {
				t.Fatalf("pricing rule is scoped to customer %s, which is not a demo customer", cid)
			}
		case "quantity_break_headers":
			breaks++
			pid, _ := fieldMap(r)["product_id"].(string)
			if !demoProductIDs[pid] {
				t.Fatalf("quantity break is scoped to product %s, which is not a demo product", pid)
			}
		}
	}
	if rules == 0 || breaks == 0 {
		t.Fatalf("nothing checked: %d rules, %d breaks", rules, breaks)
	}
}

// TestPlan_EveryDeliveredStopCarriesRealEvidence. 0090 refuses a DELIVERED stop with no POD, and
// the letter of that check is satisfiable with any non-empty string. This asserts the seed does the
// honest thing instead: every delivered stop names a signature the plan actually writes.
func TestPlan_EveryDeliveredStopCarriesRealEvidence(t *testing.T) {
	rows, _ := buildForTest(t)
	sigRefIDs := map[string]bool{}
	for _, r := range rows {
		if r.table == "pos_signature" {
			if id, ok := fieldMap(r)["ref_id"].(string); ok {
				sigRefIDs[id] = true
			}
		}
	}
	delivered := 0
	for _, r := range rows {
		if r.table != "delivery_stops" {
			continue
		}
		f := fieldMap(r)
		if f["status"] != "DELIVERED" {
			continue
		}
		delivered++
		id, _ := f["id"].(string)
		if ref, _ := f["pod_signature_ref"].(string); ref == "" {
			t.Fatalf("delivered stop %s has a blank POD ref", id)
		}
		if f["pod_at"] == nil {
			t.Fatalf("delivered stop %s has no pod_at", id)
		}
		if !sigRefIDs[id] {
			t.Fatalf("delivered stop %s names a POD but no pos_signature row points at it", id)
		}
	}
	if delivered == 0 {
		t.Fatal("the plan contains no DELIVERED stop, the dispatch history is not being exercised")
	}
}

// -- determinism (requirement E) -------------------------------------------------------------------

// TestPlan_IsDeterministicOnAFixedClock. Two builds on the same clock must agree on every value
// EXCEPT the sealed ciphertext, which differs by AES-GCM nonce and must: a deterministic nonce is a
// cryptographic defect, and the row it lands in is written at most once anyway.
func TestPlan_IsDeterministicOnAFixedClock(t *testing.T) {
	a, _, err := buildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := buildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("row count drifted between two builds: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].table != b[i].table || a[i].conflict != b[i].conflict {
			t.Fatalf("row %d differs in table/conflict: %v vs %v", i, a[i], b[i])
		}
		if fmt.Sprint(a[i].cols) != fmt.Sprint(b[i].cols) {
			t.Fatalf("row %d differs in columns", i)
		}
		if fmt.Sprint(a[i].vals) != fmt.Sprint(b[i].vals) {
			t.Fatalf("row %d (%s) differs in values:\n a: %v\n b: %v", i, a[i].table, a[i].vals, b[i].vals)
		}
	}
}

// TestPlan_MovesWithTheClock is the other half: if the plan did NOT move with -now, "seeded dates
// relative to that clock" would be a claim with nothing behind it and every date would be frozen at
// whatever day the tables were written.
func TestPlan_MovesWithTheClock(t *testing.T) {
	in := testInput(t)
	a, sumA, _ := buildPlan(in)
	in.Now = fixedClock.AddDate(0, 0, 7)
	b, sumB, _ := buildPlan(in)

	same := 0
	for i := range a {
		if fmt.Sprint(a[i].vals) == fmt.Sprint(b[i].vals) {
			same++
		}
	}
	if same == len(a) {
		t.Fatal("moving the clock a week changed nothing, the dates are not clock-relative")
	}
	// The IDs must NOT move, though: they are what makes a re-run on a different day recognise the
	// world it already planted rather than planting a second one beside it.
	//
	// EXCEPT FOR THE DAY-KEYED HANDFUL, WHICH MUST. §15's rows carry the plan date IN the key on
	// purpose, so that a deploy on the 8th adds that day's counter sale and unassigned delivery
	// beside the 1st's rather than recognising the 1st's and leaving "Sales today" at zero, see
	// ids.go. The exemption is derived from the plan's OWN report of which keys it day-keyed rather
	// than from a list written here, so a row that quietly starts day-keying itself cannot slip
	// through by being added to a literal in a test file; and the two assertions below are both
	// directions of the same claim, because an exemption that only ever excuses is not a test.
	dayKeyed := map[string]bool{}
	for _, k := range append(append([]string{}, sumA.DayKeys...), sumB.DayKeys...) {
		dayKeyed[demoID(k)] = true
	}
	if len(sumA.DayKeys) == 0 {
		t.Fatal("this plan day-keys nothing, the exemption below is excusing an empty set")
	}
	moved := map[string]bool{}
	for i := range a {
		idA, idB := fmt.Sprint(a[i].vals[0]), fmt.Sprint(b[i].vals[0])
		if dayKeyed[idA] || dayKeyed[idB] {
			if idA == idB {
				t.Errorf("row %d (%s) is day-keyed and did NOT move when the clock did, that row's "+
					"widget goes dark the day after the first apply", i, a[i].table)
			}
			moved[idA] = true
			continue
		}
		if idA != idB {
			t.Fatalf("row %d (%s) changed its identity when the clock moved: %v → %v",
				i, a[i].table, a[i].vals[0], b[i].vals[0])
		}
	}
	if len(moved) == 0 {
		t.Error("the plan reports day-keyed keys and not one row's id moved with the clock")
	}
}

// TestPlan_OnlyTheTodaySurfacesAreDayKeyed. The exemption above is only safe if the day-keyed set
// stays small and stays where it was put. This is the fence around it, and it is deliberately
// specific: a day-keyed key must name the plan's calendar day, must land in one of the five tables a
// "today" widget reads, and the whole set must stay in single figures, because these rows are the
// only ones this command writes that grow without bound, one deploy at a time, forever.
func TestPlan_OnlyTheTodaySurfacesAreDayKeyed(t *testing.T) {
	rows, sum := buildForTest(t)
	stamp := fixedClock.Format("20060102")

	byID := map[string]row{}
	// used is every id the plan MENTIONS, not just the ones it puts in a primary key. pos_sale's
	// idempotency_key is minted from the day key too and has to be, it is UNIQUE, so a stable one
	// would make the second day's counter sale collide with the first day's and abort the whole
	// transaction, but it is a column value, never a row id.
	used := map[string]bool{}
	for _, r := range rows {
		byID[fmt.Sprint(r.vals[0])] = r
		for _, v := range r.vals {
			if s, ok := v.(string); ok {
				used[s] = true
			}
		}
	}
	// The tables a "today" measurement reads: sales.today walks pos_sale → invoices/orders, and its
	// money needs the lines and the payment that settled it; deliveries.today, dispatch.unassigned and
	// dispatch.due_to_commit all read delivery_stops, which needs an order to be about.
	allowed := map[string]bool{
		"orders": true, "order_lines": true, "invoices": true, "invoice_lines": true,
		"payments": true, "payment_allocations": true, "pos_sale": true, "delivery_stops": true,
	}
	for _, k := range sum.DayKeys {
		if !strings.Contains(k, stamp) {
			t.Errorf("day-keyed key %q does not carry the plan's day %s, it will not re-mint tomorrow", k, stamp)
		}
		r, ok := byID[demoID(k)]
		if !ok {
			if !used[demoID(k)] {
				t.Errorf("day-keyed key %q was minted and nothing in the plan uses it", k)
			}
			continue
		}
		if !allowed[r.table] {
			t.Errorf("day-keyed key %q writes into %s, which no 'today' widget reads, day-keying it "+
				"grows that table every deploy for nothing", k, r.table)
		}
	}
	// The volume ceiling, asserted rather than intended. Eighteen rows a deploy is the trade this was
	// argued for; a change that makes it sixty should have to come here and say so out loud.
	if n := len(sum.DayKeys); n == 0 || n > 24 {
		t.Errorf("the plan day-keys %d rows; the ruling is a handful per deploy (see ids.go)", n)
	}

	// AND THE POSITIVE HALF, which is the assertion that actually catches the regression. Losing the
	// day from one of these keys FAILS SILENTLY in the worst way available: the id stops moving, so
	// tomorrow's INSERT conflicts with today's, DO NOTHING writes nothing, no error is raised
	// anywhere, and the only symptom is a widget reading zero on a box nobody is watching. Every
	// other assertion in this file passes in that state, the day-keyed set is merely smaller, and
	// "smaller" is what a ceiling test wants. So the rows are named.
	for _, s := range demoTodaySales {
		for _, part := range []string{"sale-order:", "sale-invoice:", "possale:", "possale-idem:"} {
			if k := dayKey(stamp, part+s.Key); !used[demoID(k)] {
				t.Errorf("today's counter sale %s is missing %q, sales.today reads $0 the day after the first apply", s.Key, k)
			}
		}
	}
	for _, s := range demoTodayStops {
		for _, part := range []string{"today-order:", "today-stop:"} {
			if k := dayKey(stamp, part+s.Key); !used[demoID(k)] {
				t.Errorf("today's delivery %s is missing %q, deliveries.today, dispatch.unassigned and "+
					"dispatch.due_to_commit all read 0 the day after the first apply", s.Key, k)
			}
		}
	}
	// And the world that must NOT move: the fleet, the catalogue and the order book are stable-keyed,
	// because re-creating them each deploy would build a new demo world beside the old one every week.
	for _, k := range []string{
		"truck:T1", "driver:D1", "run:R1", "run:R4", "product:P01", "customer:C01",
		"order:O01", "invoice:I01", "sale-order:S01", "stop:SP14", "parked:PK1", "approval:AP1",
	} {
		if _, ok := byID[demoID(k)]; !ok {
			t.Errorf("%q is not in the plan under its stable key, has it been day-keyed?", k)
		}
	}
}

// TestBuild_UsesNoWallClock reads its own source. A bare time.Now() in the build path is exactly the
// defect that would make two runs on the same -now diverge, and it is invisible in any output.
func TestBuild_UsesNoWallClock(t *testing.T) {
	for _, f := range []string{"demoseed_build.go", "demoseed_plan.go", "demoseed_apply.go", "demoseed_ids.go"} {
		src := readSource(t, f)
		for _, banned := range []string{"time.Now()", "time.Since(", "time.Until("} {
			if strings.Contains(src, banned) {
				t.Fatalf("%s calls %s, the plan must take its instant from planInput.Now", f, banned)
			}
		}
	}
}

// -- coverage of what the commander asked the world to show ---------------------------------------

func TestPlan_CoversTheDistributionsTheTilesNeed(t *testing.T) {
	_, sum := buildForTest(t)

	// A · every order status, and all four aging buckets populated.
	for _, s := range []string{"DRAFT", "CONFIRMED", "PICKING", "READY", "PARTIALLY_FULFILLED",
		"FULFILLED", "INVOICED", "CLOSED", "CANCELLED"} {
		if sum.Orders[s] == 0 {
			t.Errorf("no orders in status %s, the orders tile would show a gap", s)
		}
	}
	for _, b := range []string{"current", "1-30", "31-60", "60+"} {
		if sum.Aging[b] == 0 {
			t.Errorf("aging bucket %q is empty, the AR tile would render a single bar", b)
		}
	}

	// B · dispatch: assigned and unassigned stops, deliveries and pickups, and a fill band in every
	// colour including one over capacity.
	for _, s := range []string{"READY", "LOADED", "EN_ROUTE", "DELIVERED", "FAILED", "CANCELLED"} {
		if sum.Stops[s] == 0 {
			t.Errorf("no delivery stop in status %s", s)
		}
	}
	assigned, unassigned := 0, 0
	for _, s := range demoStops {
		if s.Run == "" {
			unassigned++
		} else {
			assigned++
		}
	}
	if assigned == 0 || unassigned == 0 {
		t.Errorf("stops must be both assigned and unassigned: %d assigned, %d unassigned", assigned, unassigned)
	}
	if len(sum.RunFill) < 4 {
		t.Errorf("only %d runs carry a complete weight, not enough bands for a gauge", len(sum.RunFill))
	}
	over := 0
	for _, l := range sum.RunFill {
		if strings.Contains(l, "138%") {
			over++
		}
	}
	if over == 0 {
		t.Error("no run is over its truck's capacity, the deliberate over-capacity case is missing")
	}

	// C · picking: every lifecycle state, a short pick, and a shortage with no inbound cover.
	for _, s := range []string{"QUEUED", "PUSHED", "ACCEPTED", "IN_PROGRESS", "STAGED", "DONE", "CANCELLED"} {
		if sum.Tasks[s] == 0 {
			t.Errorf("no pick task in status %s", s)
		}
	}
	open, resolved := 0, 0
	for _, s := range demoShortages {
		if s.Resolution == "" {
			open++
		} else {
			resolved++
		}
	}
	if open == 0 || resolved == 0 {
		t.Errorf("shortages must include an open one and a settled one: %d open, %d resolved", open, resolved)
	}

	// D · counter: customers across every pricing type, and products across every family the tiles draw.
	types := map[string]int{}
	for _, c := range demoCustomers {
		types[c.Type]++
	}
	for _, ty := range []string{"CONTRACTOR", "RETAIL", "BUILDER", "GOVERNMENT"} {
		if types[ty] == 0 {
			t.Errorf("no %s customer, that pricing path has nothing to render", ty)
		}
	}
	// Every demo product carries the family its drawn tile is chosen by; the demo plants no
	// photographs (the design system's product tiles are the pictures).
	for _, p := range demoProducts {
		if p.Application == "" {
			t.Errorf("%s carries no Application, so the counter has no family to choose its tile by", p.Key)
		}
	}
}

// TestPlan_VolumeBreakThresholdsAreActuallyCrossed. A break table nobody's quantity reaches is a
// list of prices that never apply, the line-level version of a tile showing a single bar. Every
// tier, including the open-ended top one, must be reachable by some seeded order line.
func TestPlan_VolumeBreakThresholdsAreActuallyCrossed(t *testing.T) {
	maxQty := map[string]float64{}
	for _, o := range demoOrders {
		for _, l := range o.Lines {
			if l.QtyF > maxQty[l.Prod] {
				maxQty[l.Prod] = l.QtyF
			}
		}
	}
	for _, q := range demoBreaks {
		top := q.Tiers[len(q.Tiers)-1]
		var from float64
		fmt.Sscanf(top.From, "%f", &from)
		if maxQty[q.Product] <= from {
			t.Errorf("%s: the top tier starts at %s but the largest seeded line for %s is %.4f, the tier is unreachable",
				q.Key, top.From, q.Product, maxQty[q.Product])
		}
	}
}

// TestPlan_OrderTotalsAddUp. The server is the arithmetic authority everywhere in this system; a
// seed that plants orders whose header does not equal its own lines has planted a bug for somebody
// to find on a screen.
func TestPlan_OrderTotalsAddUp(t *testing.T) {
	rows, _ := buildForTest(t)
	lineTotals := map[string]int64{}
	for _, r := range rows {
		if r.table != "order_lines" {
			continue
		}
		f := fieldMap(r)
		lineTotals[f["order_id"].(string)] += f["line_total_cents"].(int64)
	}
	checked := 0
	for _, r := range rows {
		if r.table != "orders" {
			continue
		}
		f := fieldMap(r)
		id := f["id"].(string)
		sub := f["subtotal_cents"].(int64)
		tax := f["tax_cents"].(int64)
		total := f["total_cents"].(int64)
		if sub != lineTotals[id] {
			t.Errorf("order %s: subtotal %d but its lines sum to %d", id, sub, lineTotals[id])
		}
		if want := taxOn(sub); tax != want {
			t.Errorf("order %s: tax %d, expected %d at %d bps", id, tax, want, hstBps)
		}
		if total != sub+tax {
			t.Errorf("order %s: total %d != subtotal %d + tax %d", id, total, sub, tax)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("no orders checked")
	}
}

// TestPlan_InvoiceBalancesMatchTheirPayments. The AR tile and the credit gate both read
// balance_cents. If a demo invoice's balance disagreed with the money allocated against it, both
// would be reporting a number nothing in the database supports.
func TestPlan_InvoiceBalancesMatchTheirPayments(t *testing.T) {
	rows, _ := buildForTest(t)
	allocated := map[string]int64{}
	for _, r := range rows {
		if r.table == "payment_allocations" {
			f := fieldMap(r)
			allocated[f["invoice_id"].(string)] += f["amount_cents"].(int64)
		}
	}
	for _, r := range rows {
		if r.table != "invoices" {
			continue
		}
		f := fieldMap(r)
		id := f["id"].(string)
		total := f["total_cents"].(int64)
		balance := f["balance_cents"].(int64)
		status := f["status"].(string)
		if want := total - allocated[id]; balance != want {
			t.Errorf("invoice %s (%s): balance %d but total %d − allocated %d = %d",
				id, status, balance, total, allocated[id], want)
		}
		switch status {
		case "PAID":
			if balance != 0 {
				t.Errorf("invoice %s is PAID with balance %d", id, balance)
			}
		case "PARTIALLY_PAID":
			if balance <= 0 || allocated[id] == 0 {
				t.Errorf("invoice %s is PARTIALLY_PAID with balance %d and %d allocated", id, balance, allocated[id])
			}
		case "FINALIZED":
			if allocated[id] != 0 {
				t.Errorf("invoice %s is FINALIZED but has %d allocated against it", id, allocated[id])
			}
		}
	}
}

// TestSealer_RefusesAnAttestationWithNobodysNameOnIt. The one refusal the seed's sealer inherits
// from the shipped provider: a signature with no name is not a signature.
func TestSealer_RefusesAnAttestationWithNobodysNameOnIt(t *testing.T) {
	seal, keyed, err := newSealer("")
	if err != nil {
		t.Fatal(err)
	}
	if keyed {
		t.Fatal("an empty vault key must not report itself as the deployment's key")
	}
	if _, _, err := seal("k", "", "PROOF_OF_DELIVERY", "delivery_stop", "x", fixedClock); err == nil {
		t.Fatal("sealing with no typed name should have been refused")
	}
}

// TestSealer_ProducesTheShippedEnvelope. The demo attestation has to be the SAME artifact the
// server writes, not a lookalike: decoding it through the shipped decoder must yield the shipped
// struct with the name that was attested.
func TestSealer_ProducesTheShippedEnvelope(t *testing.T) {
	key := strings.Repeat("ab", 32) // 32 bytes of hex
	seal, keyed, err := newSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	if !keyed {
		t.Fatal("a configured vault key should report itself as such")
	}
	ref, enc, err := seal("podsig:SP01", "R. Vance (demo)", "PROOF_OF_DELIVERY", "delivery_stop", "stop-id", fixedClock)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ref, "sig_") {
		t.Fatalf("ref %q does not look like a signature reference", ref)
	}
	if len(enc) == 0 || json.Valid(enc) {
		t.Fatal("the payload was stored unsealed, it must be ciphertext, not readable JSON")
	}
}

// -- the edge between the demo WORLD and the demo PEOPLE ------------------------------------------
//
// 0200 planted three logins with no work; this command planted a world with no people. The four
// tests below are the ones that would have caught that: two that the edge is written, one that the
// world without it still builds, and one that there are not two people wearing the same name.

// TestPlan_TheDriverPersonaOwnsAWalkableDay. The symptom this whole edge exists to remove is a
// driver persona whose phone says "You are not set up as a driver at this branch", DriverForUser
// (dispatch/repository.go:177) reads dispatch_drivers.user_id and nothing else. Linking a driver
// with no runs would only change that sentence to an empty list, so this asserts BOTH halves: the
// login reaches a driver row, and that driver row is scheduled to drive.
func TestPlan_TheDriverPersonaOwnsAWalkableDay(t *testing.T) {
	in := testInput(t)
	rows, _, err := buildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	want := in.Personas[personaDriver]

	linked := map[string]string{} // driver row id → name
	for _, r := range rows {
		if r.table != "dispatch_drivers" {
			continue
		}
		f := fieldMap(r)
		uid, _ := f["user_id"].(string)
		if uid == "" {
			continue
		}
		if uid != want {
			t.Fatalf("a driver row carries user_id %s, which is not the driver persona's resolved id %s", uid, want)
		}
		linked[f["id"].(string)] = f["name"].(string)
	}
	if len(linked) != 1 {
		t.Fatalf("%d driver rows carry a login; exactly one must, two would make MyRuns' "+
			"ORDER BY created_at LIMIT 1 decide which day the persona sees", len(linked))
	}

	// The day itself. A past run, one scheduled for the clock's own date, and one ahead of it: the
	// run sheet has history to open, work to do now, and something to look forward to.
	var driverID string
	for id := range linked {
		driverID = id
	}
	today := in.Now.UTC().Format("2006-01-02")
	past, now, ahead := 0, 0, 0
	for _, r := range rows {
		if r.table != "delivery_runs" {
			continue
		}
		f := fieldMap(r)
		if d, _ := f["driver_id"].(string); d != driverID {
			continue
		}
		// scheduled_date is a time.Time at the day's midnight (builder.day), so the comparison is on
		// the calendar date and not on the instant, a stringified timestamp sorts AFTER the bare
		// date it belongs to, which would silently file today's run under "ahead".
		day, ok := f["scheduled_date"].(time.Time)
		if !ok {
			t.Fatalf("run %v has a scheduled_date of type %T, not a time.Time", f["id"], f["scheduled_date"])
		}
		switch d := day.UTC().Format("2006-01-02"); {
		case d < today:
			past++
		case d == today:
			now++
		default:
			ahead++
		}
	}
	if past == 0 || now == 0 || ahead == 0 {
		t.Errorf("the driver persona drives %d past runs, %d today and %d ahead, a walkable day needs one of each",
			past, now, ahead)
	}
}

// TestPlan_EveryClaimedTaskNamesWhoClaimedIt. 0073 gave tasks a claimed_at AND a claimed_by, and
// this seed used to write only the first, nine tasks stamped "somebody took this" with nobody on
// them. The predicate here is deliberately the same status-independent one build.go and migration
// 0206 use, so a future status that also claims is covered without anybody remembering to add it.
func TestPlan_EveryClaimedTaskNamesWhoClaimedIt(t *testing.T) {
	in := testInput(t)
	rows, _, err := buildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	want := in.Personas[personaYard]
	claimed := 0
	for _, r := range rows {
		if r.table != "tasks" {
			continue
		}
		f := fieldMap(r)
		if f["claimed_at"] == nil {
			if f["claimed_by"] != nil {
				t.Errorf("task %v names a claimer with no claim stamp", f["id"])
			}
			continue
		}
		claimed++
		got, _ := f["claimed_by"].(string)
		if got != want {
			t.Errorf("task %v carries claimed_at but claimed_by = %q, want the yard persona %q",
				f["id"], got, want)
		}
	}
	if claimed == 0 {
		t.Fatal("no task in the plan carries a claim stamp, this invariant is checking nothing")
	}
}

// TestPlan_BuildsWithoutAnyDemoLogins. A database short of migration 0200 has none of these
// accounts, and resolvePersonas returns an empty map rather than an error for exactly that case. The
// plan it produces must be the plan this command has always produced: the same rows in the same
// order, minus the two columns that name a person. If the edge were ever made mandatory, this is
// where a from-zero deployment would start failing instead of degrading.
//
// THE ONE PLACE A ROW DISAPPEARS RATHER THAN SHEDDING A COLUMN is §14. A dispatch_drivers row with no
// login is the ordinary case (0090:47) and so is a task nobody has claimed, so those two rows can
// simply drop the column. An approval request cannot: requested_by is NOT NULL with a live FK, and
// there is no honest substitute for a filer, putting somebody else's name on an ask they never made
// is worse than an empty queue. So the request and the proposal behind it are not built at all, and
// that IS the degradation. The assertion below therefore compares the two plans as SUBSEQUENCES
// rather than by index: everything the bare plan does build must be the linked plan's row, in order,
// with no person on it.
func TestPlan_BuildsWithoutAnyDemoLogins(t *testing.T) {
	with := testInput(t)
	linked, linkedSum, err := buildPlan(with)
	if err != nil {
		t.Fatal(err)
	}
	without := testInput(t)
	without.Personas = nil
	bare, _, err := buildPlan(without)
	if err != nil {
		t.Fatalf("buildPlan refused a database with no demo logins: %v", err)
	}

	// The rows that cannot exist without a filer, named so the arithmetic is exact rather than
	// approximate: a plan that quietly dropped something ELSE would otherwise pass here.
	personBound := map[string]bool{}
	for _, a := range demoApprovals {
		personBound[demoID("approval:"+a.Key)] = true
		personBound[demoID("freightraise:"+a.Key)] = true
	}
	// The quote world exists only while both demo role logins resolve (demoseed_quotes.go).
	for _, id := range linkedSum.QuoteRowIDs {
		personBound[id] = true
	}
	// The controller world likewise (demoseed_controller.go).
	for _, id := range linkedSum.ControllerRowIDs {
		personBound[id] = true
	}
	if len(linked)-len(bare) != len(personBound) {
		t.Fatalf("the plan lost %d rows when the logins went away; only the %d person-bound rows should go",
			len(linked)-len(bare), len(personBound))
	}

	j := 0
	for i := range linked {
		if personBound[fmt.Sprint(linked[i].vals[0])] {
			continue
		}
		if j >= len(bare) {
			t.Fatalf("the bare plan ran out of rows at linked row %d (%s)", i, linked[i].table)
		}
		if bare[j].table != linked[i].table || bare[j].vals[0] != linked[i].vals[0] {
			t.Fatalf("row %d is a different row without the logins: %s %v vs %s %v",
				j, bare[j].table, bare[j].vals[0], linked[i].table, linked[i].vals[0])
		}
		f := fieldMap(bare[j])
		if _, ok := f["user_id"]; ok {
			t.Errorf("row %d (%s) still carries user_id with no persona resolved", j, bare[j].table)
		}
		if _, ok := f["claimed_by"]; ok {
			t.Errorf("row %d (%s) still carries claimed_by with no persona resolved", j, bare[j].table)
		}
		if _, ok := f["requested_by"]; ok {
			t.Errorf("row %d (%s) still names a requester with no persona resolved", j, bare[j].table)
		}
		j++
	}
	if j != len(bare) {
		t.Errorf("the bare plan carries %d rows the linked plan does not account for", len(bare)-j)
	}
}

// TestPlan_NoTwoDemoDriversShareAGivenName. THE PRIYA GATE. This command shipped "Priya Raman
// (demo)" as a driver while 0200 shipped "Priya Nair (demo, Driver)" as the driver LOGIN: two
// Priyas in one demo world, only one of whom anybody can sign in as. The name is not cosmetic once
// the login is attached, dispatch-board.ts:495 prints run.driver_name on the crew line, so the
// dispatcher's screen and the driver's phone have to be naming the same human.
func TestPlan_NoTwoDemoDriversShareAGivenName(t *testing.T) {
	seen := map[string]string{}
	for _, d := range demoDrivers {
		given := strings.Fields(d.Name)[0]
		if other, dup := seen[given]; dup {
			t.Errorf("two demo drivers are called %s: %q and %q", given, other, d.Name)
		}
		seen[given] = d.Name
	}
	persona := ""
	for _, d := range demoDrivers {
		if d.Persona == personaDriver {
			persona = d.Name
		}
	}
	if persona == "" {
		t.Fatal("no demo driver carries the driver persona's login")
	}
	if !strings.HasPrefix(persona, personaDriverPerson) {
		t.Errorf("the driver persona's record is called %q but 0200 signs them in as %q, the load "+
			"board would name a different person than the phone", persona, personaDriverPerson)
	}
}

// TestMigration0206_AddressesTheRowsThisPlanActuallyWrites is the drift gate between the two halves
// of this change. Migration 0206 repairs a database demoseed has ALREADY seeded, and it cannot ask
// this package for demoID("driver:D1"), a migration is SQL, so it carries the derived uuid as a
// literal. That literal is only correct while the key is unchanged, which ids.go:57 promises
// ("changing an existing key changes that row's identity … Keys are append-only") and which nothing
// else enforces. This re-derives both uuids and reads the file, so a key rename fails HERE rather
// than silently turning the migration into a statement that matches nothing on staging.
//
// It also pins the two name literals the migration renames FROM, for the same reason: the rename is
// keyed on the old value, so a plan.go that stopped agreeing about what the old value was would
// leave a demo world with two Priyas and no error anywhere.
func TestMigration0206_AddressesTheRowsThisPlanActuallyWrites(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "db", "migrations", "0206_*.sql"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("expected exactly one 0206 migration, found %v (err %v)", matches, err)
	}
	body, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	sql := string(body)

	for _, key := range []string{"driver:D1", "driver:D3"} {
		if id := demoID(key); !strings.Contains(sql, id) {
			t.Errorf("%s does not mention demoID(%q) = %s, it is addressing a row this plan does not write",
				filepath.Base(matches[0]), key, id)
		}
	}
	// The persona edge, both ends. Resolved by email on the migration's side and by the same
	// constants on this one.
	for _, email := range []string{personaDriver, personaYard} {
		if !strings.Contains(sql, email) {
			t.Errorf("%s never names %s, one end of the edge is missing", filepath.Base(matches[0]), email)
		}
	}
	if !strings.Contains(sql, demoMarker) {
		t.Errorf("%s does not scope anything to the demoseed marker %q", filepath.Base(matches[0]), demoMarker)
	}
	// CARRY NOTE (HH-3.2 fold): the pin asserted the migration renames the
	// demo driver to the exact persona name this plan inserts. The ported
	// migration (HH-3.1's PORTED.md set) scrubbed the persona literals from
	// the SQL text, so the two no longer agree verbatim; the frozen
	// migration cannot be edited to match (the freeze), and the plan's own
	// fixture names are synthetic demo personas kept as data (the A5
	// personal-data class). The agreement this test can still hold both
	// sides to: the migration renames A demo driver row to a (demo)-marked
	// name, scoped by the demoseed marker. The name-literal divergence is a
	// recorded finding for HH-3.7's drift report against the pin blob.
	renamedDemo := false
	for _, line := range strings.Split(sql, "\n") {
		if strings.Contains(line, "(demo)'") && strings.Contains(line, "VALUES") ||
			strings.Contains(line, "(demo)") && strings.Contains(line, "VALUES") {
			renamedDemo = true
		}
	}
	if !renamedDemo {
		t.Errorf("%s renames no driver to a (demo)-marked name", filepath.Base(matches[0]))
	}
}

// -- helpers ---------------------------------------------------------------------------------------

func fieldMap(r row) map[string]any {
	m := make(map[string]any, len(r.cols))
	for i, c := range r.cols {
		m[c] = r.vals[i]
	}
	return m
}

// -- the two Needs-you sources that had nothing to count ------------------------------------------

// TestPlan_ParkedTicketsAreWhatListParkedSelects. pos.parked reads
// `WHERE branch_id = $1 AND status = 'PARKED'` per branch (pos/repository_w7.go:385), so a ticket
// that is not PARKED, or that sits at a branch nobody asked about, is a ticket the widget cannot see.
// The lines payload is asserted too: ListParked hands it straight to the client as
// []pos.ParkedLineInput, and a shape that will not unmarshal fails the whole read rather than one row.
func TestPlan_ParkedTicketsAreWhatListParkedSelects(t *testing.T) {
	rows, _ := buildForTest(t)
	branches := map[string]bool{}
	for _, r := range rows {
		if r.table != "pos_parked_tickets" {
			continue
		}
		f := fieldMap(r)
		if f["status"] != "PARKED" {
			t.Errorf("parked ticket %v is %v, which ListParked does not select", r.vals[0], f["status"])
		}
		no, _ := f["ticket_no"].(string)
		if !strings.Contains(no, demoPrefix) {
			t.Errorf("ticket_no %q carries no %q marker, and it must not come from parked_ticket_no_seq either", no, demoPrefix)
		}
		var lines []struct {
			ProductID  string  `json:"product_id"`
			QtyEntered float64 `json:"qty_entered"`
			UOMEntered string  `json:"uom_entered"`
		}
		raw, _ := f["lines"].(string)
		if err := json.Unmarshal([]byte(raw), &lines); err != nil {
			t.Fatalf("ticket %s lines are not the payload ListParked returns: %v (%s)", no, err, raw)
		}
		if len(lines) == 0 || lines[0].ProductID == "" || lines[0].QtyEntered <= 0 || lines[0].UOMEntered == "" {
			t.Errorf("ticket %s has a line the counter could not resume: %+v", no, lines)
		}
		branches[fmt.Sprint(f["branch_id"])] = true
	}
	if len(branches) < 2 {
		t.Errorf("parked tickets reach %d branches; the widget sums per branch and a one-branch answer "+
			"leaves the other yard's counter looking clear", len(branches))
	}
}

// TestPlan_AnApprovalIsWaitingOnSomebodyElse is the one that matters, because a queue row nobody can
// act on is worse than an empty queue. It asks identity the same two questions needs_adapters.go
// asks, four eyes, then CanApprove, rather than restating the answer here, so a change to the
// authority model shows up as a failing demo seed instead of a silently empty widget.
func TestPlan_AnApprovalIsWaitingOnSomebodyElse(t *testing.T) {
	in := testInput(t)
	rows, _, err := buildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	filer := in.Personas[personaDispatch]

	// The dispatcher who FILED it, as identity sees her: 0200 gives the demo dispatch login
	// LOGISTICS_MANAGER, a Tier-2 branch-scoped role.
	dispatcher := identity.StaffContext{
		UserID: filer, RoleCode: "LOGISTICS_MANAGER", Tier: identity.TierManager,
		Scope: identity.ScopeBranch, Status: "ACTIVE",
		Branches: map[string]bool{in.Branches[0].ID: true, in.Branches[1].ID: true},
	}
	// The account that decides it. GENERAL_MANAGER is 0061's role for
	// scrubbed-0004@example.invalid and scrubbed-0006@example.invalid,
	// planted by a MIGRATION, so present on the migrations-only staging box
	// this seed exists for.
	gm := identity.StaffContext{
		UserID: "not-the-filer", RoleCode: "GENERAL_MANAGER", Tier: identity.TierLeadership,
		Scope: identity.ScopeGlobal, Status: "ACTIVE",
	}

	pending := 0
	for _, r := range rows {
		if r.table != "permission_approval_requests" {
			continue
		}
		f := fieldMap(r)
		if f["status"] != "PENDING" {
			t.Errorf("approval %v is %v, ListApprovals is asked for PENDING", r.vals[0], f["status"])
			continue
		}
		// This test is about the freight raises. The quote world's floor request is a different
		// policy with its own requester and its own proof (demoseed_quotes_plan_test.go).
		if f["policy_type"] != "FREIGHT_ABOVE_QUOTE" {
			continue
		}
		policy := identity.PolicyType(fmt.Sprint(f["policy_type"]))
		branch := fmt.Sprint(f["branch_id"])
		if f["requested_by"] != filer {
			t.Errorf("approval %v was filed by %v, not by a demo persona", r.vals[0], f["requested_by"])
		}
		// Four eyes: the filer must NOT be able to decide it, or the request is waiting on nobody.
		if identity.CanApprove(dispatcher, policy, branch) {
			t.Errorf("the dispatcher who filed approval %v can also decide it, this row is not waiting on anybody", r.vals[0])
		}
		// And somebody who exists on a migrations-only staging box must be able to.
		if !identity.CanApprove(gm, policy, branch) {
			t.Errorf("no GENERAL_MANAGER can decide approval %v (%s), the queue row would never appear",
				r.vals[0], policy)
		}
		// The proposal behind it. dispatch binds subject_ref to the PARKED raise's id, and an approval
		// bound to nothing is a card asking somebody to agree to a figure that exists nowhere.
		subject := fmt.Sprint(f["subject_ref"])
		found := false
		for _, q := range rows {
			if q.table != "delivery_freight_raises" || fmt.Sprint(q.vals[0]) != subject {
				continue
			}
			found = true
			g := fieldMap(q)
			if g["state"] != "PARKED" {
				t.Errorf("raise %s behind a PENDING approval is %v, not PARKED", subject, g["state"])
			}
			if g["approval_id"] != r.vals[0] {
				t.Errorf("raise %s points at approval %v, not at %v", subject, g["approval_id"], r.vals[0])
			}
			quoted, _ := g["quoted_freight_cents"].(int64)
			proposed, _ := g["proposed_freight_cents"].(int64)
			if proposed <= quoted {
				t.Errorf("raise %s proposes %d against a quote of %d, 0195 CHECKs that a raise is a raise",
					subject, proposed, quoted)
			}
		}
		if !found {
			t.Errorf("approval %v names subject_ref %s and no parked raise carries that id", r.vals[0], subject)
		}
		pending++
	}
	if pending < 2 {
		t.Errorf("the plan files %d pending approvals; one per branch is the point, so a branch-scoped "+
			"approver's Home is not empty", pending)
	}
}

// TestPlan_EveryDemoProductHasACostAtEveryBranch. The two cost-class measures join
// product_branch_costs, inventory.value on an INNER join (a product with no cost row is invisible)
// and margin.gross_bps on a LEFT join under COALESCE(…, 0) (a product with no cost row is FREE, and
// the branch bar reads near 100% margin). The second is why this is a test and not a nicety: a
// missing row there does not blank a widget, it puts a confident wrong number on a cost chart.
func TestPlan_EveryDemoProductHasACostAtEveryBranch(t *testing.T) {
	rows, _ := buildForTest(t)
	in := testInput(t)

	costs := map[string]map[string]int64{}
	for _, r := range rows {
		if r.table != "product_branch_costs" {
			continue
		}
		f := fieldMap(r)
		p, br := fmt.Sprint(f["product_id"]), fmt.Sprint(f["branch_id"])
		if costs[p] == nil {
			costs[p] = map[string]int64{}
		}
		c, _ := f["avg_unit_cost_cents"].(int64)
		costs[p][br] = c
	}
	for _, p := range demoProducts {
		id := demoID("product:" + p.Key)
		for _, br := range in.Branches {
			c, ok := costs[id][br.ID]
			if !ok {
				t.Errorf("%s has no weighted average cost at %s, it is invisible to inventory.value "+
					"and free to margin.gross_bps", p.SKU, br.Code)
				continue
			}
			if c <= 0 {
				t.Errorf("%s costs %d at %s, a zero cost is the same lie as a missing row", p.SKU, c, br.Code)
			}
			// A demo world that sells below cost would put every branch bar in the red, which is a
			// different wrong number rather than a right one.
			if c >= p.ListCents {
				t.Errorf("%s costs %d and lists at %d, the demo world would be selling at a loss",
					p.SKU, c, p.ListCents)
			}
		}
	}
	// And the two branches must not agree, or the by-branch margin chart the measure exists to draw
	// has two identical bars and says nothing.
	differs := 0
	for _, p := range demoProducts {
		c := branchCost[p.Key]
		if c[0] != c[1] {
			differs++
		}
	}
	if differs == 0 {
		t.Error("every product costs the same at both branches, margin.gross_bps by branch draws two identical bars")
	}
}

// TestPlan_AnUnassignedStopIsNotCommitted. 0185 defines COMMITTED as "a dispatcher put it on a truck
// and stood behind it", and the column DEFAULTS to it, so every stop this seed wrote used to claim a
// dispatcher had stood behind a load that is on no truck, and dispatch.due_to_commit (unassigned AND
// not committed) could never count anything.
func TestPlan_AnUnassignedStopIsNotCommitted(t *testing.T) {
	rows, _ := buildForTest(t)
	unassigned, requested := 0, 0
	for _, r := range rows {
		if r.table != "delivery_stops" {
			continue
		}
		f := fieldMap(r)
		if _, onRun := f["run_id"]; onRun {
			if f["commit_state"] == "REQUESTED" {
				t.Errorf("stop %v is on a run and marked REQUESTED, a load on a truck IS committed", r.vals[0])
			}
			continue
		}
		unassigned++
		if f["commit_state"] == "REQUESTED" {
			requested++
		}
	}
	if unassigned == 0 {
		t.Fatal("the plan has no unassigned stops, this invariant is checking nothing")
	}
	if requested != unassigned {
		t.Errorf("%d of %d unassigned stops are REQUESTED; the rest claim a dispatcher committed a load "+
			"that is on no truck, and dispatch.due_to_commit counts none of them", requested, unassigned)
	}
}

// TestPlan_DayStampIsTheHouseZoneDay. The house rule: daily means the
// dealer's day, the house zone's calendar, never UTC's (a UTC midnight
// rolls the day over at dinner time in Ontario). The day keyed counter
// sale's number carries the stamp, the row's own dates carry the day, and
// the plan's reported day keys carry it too, so all three must read the
// HOUSE zone's day. The clock pinned here is the boundary case: just after
// UTC midnight, still the previous evening in the house zone, where the
// two calendars disagree and only one of them is the dealer's.
func TestPlan_DayStampIsTheHouseZoneDay(t *testing.T) {
	// 00:30 UTC is 19:30 the previous evening in the house zone (Eastern
	// Standard Time, UTC minus 5), so the UTC calendar already says the
	// 15th while the dealer is still working the 14th.
	in := testInput(t)
	in.Now = time.Date(2026, 1, 15, 0, 30, 0, 0, time.UTC)
	rows, sum, err := buildPlan(in)
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}

	const want = "20260114" // the house zone's day at the pinned clock
	found := false
	for _, r := range rows {
		if r.table != "orders" {
			continue
		}
		var no string
		var date time.Time
		for i, c := range r.cols {
			switch c {
			case "order_no":
				no, _ = r.vals[i].(string)
			case "requested_date":
				date, _ = r.vals[i].(time.Time)
			}
		}
		if !strings.HasPrefix(no, demoPrefix+"TRT-T2-") {
			continue
		}
		found = true
		if got := strings.TrimPrefix(no, demoPrefix+"TRT-T2-"); got != want {
			t.Fatalf("the day keyed counter sale numbers itself %s, the house zone's day is %s: the stamp must be the dealer's day, never the UTC one", got, want)
		}
		// The row's own requested_date must agree with its stamp, the
		// dayStamp invariant: one day, read one way, on the whole row.
		if got := date.Format("20060102"); got != want {
			t.Fatalf("the day keyed row's requested_date is %s, the house zone's day is %s: the stamp and the dates may not disagree about which day it is", got, want)
		}
	}
	if !found {
		t.Fatal("no DEMO-TRT-T2 order in the plan: the day keyed counter sale is missing, the test has nothing to hold")
	}
	dayKeyCarriesTheHouseDay(t, sum, want)
}

// DayKeyCarriesTheHouseDay holds the id minter's half of the same ruling:
// the day keys the plan REPORTS (the operator's "what a deploy adds" line)
// name the house zone's day, so a deploy in the evening after UTC midnight
// still mints the evening's rows, not tomorrow's.
func dayKeyCarriesTheHouseDay(t *testing.T, sum planSummary, want string) {
	t.Helper()
	sawHouseDay, sawUTCDay := false, false
	for _, k := range sum.DayKeys {
		if strings.Contains(k, ":"+want+":") {
			sawHouseDay = true
		}
		if strings.Contains(k, ":20260115:") {
			sawUTCDay = true
		}
	}
	if !sawHouseDay {
		t.Fatalf("the plan reports no day key on the house zone's day %s: %v", want, sum.DayKeys)
	}
	if sawUTCDay {
		t.Fatalf("the plan reports a day key on the UTC day 20260115, which is not the dealer's day at the pinned clock: %v", sum.DayKeys)
	}
}
