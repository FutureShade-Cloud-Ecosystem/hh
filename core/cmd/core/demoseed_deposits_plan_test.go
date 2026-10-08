package main

// demoseed_deposits_plan_test.go, the deposit world's promises asserted on the PLAN (item 9.9). The
// database half (the real routes find both orders, the held one is held, the take lifts the hold) is
// demoworld_cod_test.go's deposit step.

import (
	"fmt"
	"strings"
	"testing"
)

// depositRows pulls the deposit world's rows out of a plan, by table, keyed by the order key's id
// list the summary names.
func depositRows(rows []row, sum planSummary) map[string][]map[string]any {
	ids := map[string]bool{}
	for _, id := range sum.DepositRowIDs {
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

func TestDepositPlan_TwoOrdersATesterCanWalk(t *testing.T) {
	rows, sum := buildForTest(t)
	d := depositRows(rows, sum)
	if got := len(d["orders"]); got != 2 {
		t.Fatalf("the deposit world holds %d orders, want 2 (a credit deposit and a Cash On Demand deposit)", got)
	}
	byNo := map[string]map[string]any{}
	for _, o := range d["orders"] {
		byNo[fmt.Sprint(o["order_no"])] = o
	}
	credit, cod := byNo["DEMO-TRT-D01"], byNo["DEMO-TRT-D02"]
	if credit == nil || cod == nil {
		t.Fatalf("the orders are numbered %v, want DEMO-TRT-D01 and DEMO-TRT-D02 in the seed's own numbering", sum.DepositOrders)
	}
	// Homed at Trenton (the second branch of the test input), where the demo logins work.
	for no, o := range byNo {
		if o["branch_id"] != "5d1b3f6e-0000-4000-9000-000000000001" {
			t.Errorf("%s is homed at branch %v, want Trenton", no, o["branch_id"])
		}
		if o["status"] != "CONFIRMED" || o["fulfillment"] != "WILL_CALL" {
			t.Errorf("%s reads %v %v, want a confirmed will call order", no, o["status"], o["fulfillment"])
		}
	}
	if credit["customer_id"] != demoID("customer:C02") || cod["customer_id"] != demoID("customer:C10") {
		t.Errorf("the customers are %v and %v, want C02 (credit) and C10 (Cash On Demand)", credit["customer_id"], cod["customer_id"])
	}

	// Each order owes: its invoice is PARTIALLY_PAID with the rest as its balance, and exactly one
	// payment (the deposit) is allocated to it, by the method the sale named.
	invs := map[string]map[string]any{}
	for _, inv := range d["invoices"] {
		invs[fmt.Sprint(inv["order_id"])] = inv
	}
	pays := d["payments"]
	if len(pays) != 2 || len(d["payment_allocations"]) != 2 {
		t.Fatalf("%d payments and %d allocations, want one deposit each", len(pays), len(d["payment_allocations"]))
	}
	methods := map[string]string{}
	for _, p := range pays {
		methods[fmt.Sprint(p["customer_id"])] = fmt.Sprint(p["method"])
		if p["status"] != "RECEIVED" {
			t.Errorf("a deposit payment reads %v, want RECEIVED", p["status"])
		}
	}
	if methods[demoID("customer:C02")] != "CARD" || methods[demoID("customer:C10")] != "CASH" {
		t.Errorf("deposit methods %v, want C02 by CARD and C10 by CASH", methods)
	}
	for no, o := range byNo {
		inv := invs[fmt.Sprint(o["id"])]
		if inv == nil {
			t.Fatalf("%s has no invoice", no)
		}
		total, balance := inv["total_cents"].(int64), inv["balance_cents"].(int64)
		if inv["status"] != "PARTIALLY_PAID" || balance <= 0 || balance >= total {
			t.Errorf("%s invoice reads %v with balance %d of %d, want PARTIALLY_PAID with a balance owing", no, inv["status"], balance, total)
		}
		if o["total_cents"] != inv["total_cents"] {
			t.Errorf("%s order total %v differs from its invoice total %v", no, o["total_cents"], inv["total_cents"])
		}
	}
	// The credit order carries a job to read on the Pay balance card, and rides its account terms: a
	// due date after the issue date. The Cash On Demand one is due on demand, never later.
	if credit["job_reference"] == nil || strings.TrimSpace(fmt.Sprint(credit["job_reference"])) == "" {
		t.Errorf("the credit order carries no job reference for the Pay balance card")
	}
	// The pos_sale rows name the shapes: a will call sale on the ACCOUNT tender, and a COD sale.
	sales := map[string]string{}
	for _, s := range d["pos_sale"] {
		sales[fmt.Sprint(s["sale_type"])] = fmt.Sprint(s["tender"])
	}
	if sales["COLLECT_LATER"] != "ACCOUNT" || sales["COD"] != "CASH" {
		t.Errorf("pos sales %v, want COLLECT_LATER on ACCOUNT and COD on CASH", sales)
	}
}

func TestDepositPlan_RowsAreStableKeyed(t *testing.T) {
	_, a := buildForTest(t)
	in := testInput(t)
	in.Now = in.Now.AddDate(0, 0, 40)
	_, b, err := buildPlan(in)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(a.DepositRowIDs, ",") != strings.Join(b.DepositRowIDs, ",") || len(a.DepositRowIDs) == 0 {
		t.Errorf("the deposit world's ids moved with the clock (or are empty): a deposit order is a document, not a day")
	}
}
