package main

// demoseed_payables_receivables_plan_test.go, the receivables world's promises asserted on the PLAN
// (item 64, Payables and Receivables). The persona signs in against the 18 seeded invoices across the
// aging buckets and finds one bounced payment in the queue.

import (
	"fmt"
	"strings"
	"testing"
)

func TestReceivablesPlan_TheEighteenInvoicesSpanTheAgingBuckets(t *testing.T) {
	_, sum := buildForTest(t)
	// The book's own invoices (I01 to I18), by the bucket the plan counts them in. The deposit world
	// adds two current invoices of its own (D01, D02), counted in the same map.
	want := map[string]int{"current": 5 + 2, "1-30": 4, "31-60": 3, "60+": 3, "settled": 3}
	total := 0
	for bucket, n := range want {
		if sum.Aging[bucket] != n {
			t.Errorf("aging bucket %q holds %d invoices, want %d", bucket, sum.Aging[bucket], n)
		}
		total += sum.Aging[bucket]
	}
	if len(demoInvoices) != 18 {
		t.Fatalf("the book carries %d invoices, want the 18 the receivables desk works", len(demoInvoices))
	}
	if total != 18+2 {
		t.Fatalf("the buckets hold %d invoices, want the 18 plus the two deposit invoices", total)
	}
}

func TestReceivablesPlan_OneBouncedPaymentTheQueueShows(t *testing.T) {
	rows, _ := buildForTest(t)
	var bounced []map[string]any
	nsf := map[string]map[string]any{}
	allocated := map[string]bool{}
	invoices := map[string]map[string]any{}
	for _, r := range rows {
		f := fieldMap(r)
		switch r.table {
		case "payments":
			if f["status"] == "BOUNCED" {
				bounced = append(bounced, f)
			}
		case "payment_nsf":
			nsf[fmt.Sprint(f["payment_id"])] = f
		case "payment_allocations":
			allocated[fmt.Sprint(f["payment_id"])] = true
		case "invoices":
			invoices[fmt.Sprint(f["customer_id"])+"|"+fmt.Sprint(f["id"])] = f
		}
	}
	if len(bounced) != 1 {
		t.Fatalf("the plan holds %d bounced payments, want exactly one", len(bounced))
	}
	p := bounced[0]
	id := fmt.Sprint(p["id"])
	// The queue is payments joined to their NSF record: without the record the payment is invisible.
	n, ok := nsf[id]
	if !ok {
		t.Fatalf("the bounced payment has no payment_nsf row, the bounced queue joins on it")
	}
	if fmt.Sprint(n["customer_id"]) != fmt.Sprint(p["customer_id"]) || n["reason"] == "" {
		t.Errorf("the NSF record must name the payment's customer and a reason, got %v", n)
	}
	if allocated[id] {
		t.Errorf("a bounced payment is reversed: it must carry no allocation")
	}
	if !strings.Contains(fmt.Sprint(p["payment_no"]), demoPrefix) || p["method"] != "CHEQUE" {
		t.Errorf("the payment is a DEMO cheque, got %v %v", p["payment_no"], p["method"])
	}
	// The invoice it was tendered against stands open for the whole of it, which is what a bounce
	// leaves behind: the account's open balance includes the cheque's amount.
	var open bool
	for k, inv := range invoices {
		if !strings.HasPrefix(k, fmt.Sprint(p["customer_id"])+"|") {
			continue
		}
		if inv["status"] == "FINALIZED" && inv["balance_cents"] == p["amount_cents"] {
			open = true
		}
	}
	if !open {
		t.Errorf("the bounced cheque's customer must hold an open invoice for exactly its amount (%v)", p["amount_cents"])
	}
}
