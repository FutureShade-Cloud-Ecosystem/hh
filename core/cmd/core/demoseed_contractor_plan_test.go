package main

// demoseed_contractor_plan_test.go, the contractor world's promises asserted on the PLAN, without a
// database. The database half (each role reads the world through the tenant services, a second apply
// writes nothing) is internal/demo/world_contractor_test.go.

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// contractorRows pulls the contractor world's rows out of a plan, by table.
func contractorRows(rows []row, sum planSummary) map[string][]map[string]any {
	ids := map[string]bool{}
	for _, id := range sum.ContractorRowIDs {
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

func TestContractorPlan_WaitsForTheAccount(t *testing.T) {
	for name, f := range map[string]contractorFacts{
		"no account": {},
		"no branch":  {Ready: true},
	} {
		in := testInput(t)
		in.Contractor = f
		_, sum, err := buildPlan(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(sum.ContractorRowIDs) != 0 || len(sum.ContractorLines) != 0 {
			t.Errorf("%s: the contractor world built %d rows without the account", name, len(sum.ContractorRowIDs))
		}
	}
}

func TestContractorPlan_TheWorldHoldsTheStatesTheAppsNeed(t *testing.T) {
	in := testInput(t)
	rows, sum, err := buildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	c := contractorRows(rows, sum)
	if len(c["orders"]) != 8 || len(c["invoices"]) != 4 || len(c["quotes"]) != 3 || len(c["procurement_projects"]) != 3 ||
		len(c["portal_payment_methods"]) != 2 {
		t.Fatalf("world sizes: orders %d invoices %d quotes %d projects %d methods %d",
			len(c["orders"]), len(c["invoices"]), len(c["quotes"]), len(c["procurement_projects"]), len(c["portal_payment_methods"]))
	}
	if len(sum.ContractorRowIDs) != contractorWorldRows {
		t.Errorf("the world writes %d rows, contractorWorldRows says %d", len(sum.ContractorRowIDs), contractorWorldRows)
	}
	status := func(table string) map[string]int {
		m := map[string]int{}
		for _, f := range c[table] {
			m[f["status"].(string)]++
		}
		return m
	}
	for _, want := range []string{"CONFIRMED", "READY", "PARTIALLY_FULFILLED", "FULFILLED", "INVOICED"} {
		if status("orders")[want] == 0 {
			t.Errorf("no %s order: %v", want, status("orders"))
		}
	}
	if got := status("invoices"); got["PAID"] != 1 || got["PARTIALLY_PAID"] != 1 || got["FINALIZED"] != 2 {
		t.Errorf("invoice states %v, want one paid, one partly paid, two open", got)
	}
	if got := status("quotes"); got["SENT"] != 1 || got["ACCEPTED"] != 1 || got["LOST"] != 1 {
		t.Errorf("quote states %v", got)
	}
	if got := status("procurement_projects"); got["PLANNING"] != 1 || got["QUOTING"] != 2 {
		t.Errorf("procurement stages %v", got)
	}
	jobs := map[string]bool{}
	delivery := 0
	for _, f := range c["orders"] {
		jobs[f["project_id"].(string)] = true
		if f["delivery_instructions"] != nil {
			delivery++
		}
	}
	if len(jobs) != 3 || delivery < 3 {
		t.Errorf("orders touch %d jobs and %d carry delivery instructions", len(jobs), delivery)
	}
}

// Every figure is the core's own arithmetic at the California rate: lines add to the header, the header
// tax is half up once, the line taxes add to the header tax, an invoice mirrors its order, and a balance
// is the total less what its payment allocated.
func TestContractorPlan_MoneyIsIntegerCentsAtTheCaliforniaRate(t *testing.T) {
	rows, sum, err := buildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	c := contractorRows(rows, sum)

	sumLines := func(table, parent string) (total, tax map[string]int64) {
		total, tax = map[string]int64{}, map[string]int64{}
		for _, f := range c[table] {
			id := f[parent].(string)
			total[id] += f["line_total_cents"].(int64)
			if v, ok := f["tax_cents"].(int64); ok { // quote lines carry no tax column
				tax[id] += v
			}
		}
		return total, tax
	}
	check := func(doc string, header map[string]any, lines, lineTax int64) {
		sub, tax, total := header["subtotal_cents"].(int64), header["tax_cents"].(int64), header["total_cents"].(int64)
		if sub != lines {
			t.Errorf("%s: subtotal %d but lines sum to %d", doc, sub, lines)
		}
		if tax != (sub*775+5000)/10000 {
			t.Errorf("%s: tax %d is not 7.75 percent of %d half up", doc, tax, sub)
		}
		if lineTax != 0 && lineTax != tax {
			t.Errorf("%s: line taxes add to %d, header tax %d", doc, lineTax, tax)
		}
		if total != sub+tax {
			t.Errorf("%s: total %d != %d + %d", doc, total, sub, tax)
		}
	}
	ot, otx := sumLines("order_lines", "order_id")
	for _, f := range c["orders"] {
		id := f["id"].(string)
		check("order "+f["order_no"].(string), f, ot[id], otx[id])
	}
	it, itx := sumLines("invoice_lines", "invoice_id")
	for _, f := range c["invoices"] {
		id := f["id"].(string)
		check("invoice "+f["invoice_no"].(string), f, it[id], itx[id])
	}
	qt, _ := sumLines("quote_lines", "quote_id")
	for _, f := range c["quotes"] {
		check("quote "+f["quote_no"].(string), f, qt[f["id"].(string)], 0)
	}

	paid := map[string]int64{}
	payAmount := map[string]int64{}
	for _, f := range c["payments"] {
		payAmount[f["id"].(string)] = f["amount_cents"].(int64)
	}
	for _, f := range c["payment_allocations"] {
		paid[f["invoice_id"].(string)] += f["amount_cents"].(int64)
		if f["amount_cents"].(int64) != payAmount[f["payment_id"].(string)] {
			t.Errorf("a payment of %d allocates %d", payAmount[f["payment_id"].(string)], f["amount_cents"])
		}
	}
	for _, f := range c["invoices"] {
		id, total, balance := f["id"].(string), f["total_cents"].(int64), f["balance_cents"].(int64)
		if balance != total-paid[id] {
			t.Errorf("invoice %s: balance %d but total %d less paid %d", f["invoice_no"], balance, total, paid[id])
		}
	}
}

// Card data never touches HH: no digit run the length of a card number, no token that is not a sandbox
// one, no expiry on a bank, no dash of either length, no Dibbits word, and no cost column on a document.
func TestContractorPlan_CarriesNoCardDataNoDashesNoCosts(t *testing.T) {
	rows, sum, err := buildPlan(testInput(t))
	if err != nil {
		t.Fatal(err)
	}
	cardRun := regexp.MustCompile(`\d{13,19}`)
	for table, list := range contractorRows(rows, sum) {
		for _, f := range list {
			for col, v := range f {
				s, ok := v.(string)
				if !ok {
					continue
				}
				if strings.ContainsAny(s, "–—") {
					t.Errorf("%s.%s holds a dash: %q", table, col, s)
				}
				if col != "id" && !strings.Contains(col, "_id") && cardRun.MatchString(s) {
					t.Errorf("%s.%s holds a digit run the length of a card number: %q", table, col, s)
				}
				if strings.Contains(strings.ToLower(s), "dibbits") {
					t.Errorf("%s.%s names Dibbits: %q", table, col, s)
				}
			}
			for col := range f {
				if strings.Contains(col, "cost") || strings.Contains(col, "margin") {
					t.Errorf("%s carries %s, a dealer figure", table, col)
				}
			}
		}
	}
	for _, f := range contractorRows(rows, sum)["portal_payment_methods"] {
		token := f["gateway_token"].(string)
		if !strings.HasPrefix(token, "tok_sandbox_") || !regexp.MustCompile(`^000\d$`).MatchString(f["last4"].(string)) {
			t.Errorf("method %v is not a sandbox token view", f["id"])
		}
		if f["method_type"] == "BANK" && f["exp_month"] != nil {
			t.Errorf("a bank method carries an expiry")
		}
	}
}
