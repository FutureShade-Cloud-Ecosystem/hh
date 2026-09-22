package order

// w6c_invoice_rules_test.go, the DOUBLE-BILL GUARD (DIB-422).
//
// W5-A's readiness ruling was written when nothing could bill: "invoiceable" meant "complete", and
// that was correct because a completed group could never already be on a document. 0147 gives a
// group a document, and the moment it can, that definition becomes a double-bill.
//
// The scenario is not exotic. It is the ORDINARY RHYTHM of a phased job under ON_DEMAND_SUBSET:
//
//   June , groups 1 and 2 are delivered and invoiced.
//   July , group 3 is delivered. The contractor asks for a progress invoice.
//
// With the W5-A definition, July's readiness reports all three complete groups as invoiceable and
// the writer bills all three. The customer pays twice for June's two truckloads. Nobody in the
// building notices; the first person to find out reads their own statement.
//
// These tests pin the corrected rule, which is that "invoiceable" means COMPLETE AND UNBILLED.

import "testing"

// billed marks a group as already carrying a document (the 0147 link).
func billed(g ShipmentGroup) ShipmentGroup {
	g.InvoiceID = "inv-" + g.Status
	g.Invoiced = true
	return g
}

func TestComputeReadiness_DoesNotReBillAnAlreadyInvoicedGroup(t *testing.T) {
	t.Parallel()

	t.Run("the phased job: July bills only July's truckload", func(t *testing.T) {
		r := computeReadiness(InvoiceOnDemandSubset, []ShipmentGroup{
			billed(grp(GroupComplete, 40_000)), // June
			billed(grp(GroupComplete, 35_000)), // June
			grp(GroupComplete, 25_000),         // July, the only thing owing
		}, true)
		if !r.Ready {
			t.Fatal("a newly delivered, unbilled group must be invoiceable")
		}
		if r.InvoiceableCents != 25_000 {
			t.Errorf("invoiceable = %d, want 25000, the June groups have already been billed",
				r.InvoiceableCents)
		}
		if r.GroupsInvoiced != 2 {
			t.Errorf("groups_invoiced = %d, want 2", r.GroupsInvoiced)
		}
		// The fulfilment counts are UNCHANGED by billing: three truckloads went out, and that stays
		// true whatever the invoicing did. Conflating the two is what put the ladder and the document
		// in the same column in the first place.
		if r.GroupsComplete != 3 || r.GroupsTotal != 3 {
			t.Errorf("billing must not alter the fulfilment counts; got %d/%d complete",
				r.GroupsComplete, r.GroupsTotal)
		}
	})

	t.Run("everything delivered and everything billed is DONE, not READY", func(t *testing.T) {
		r := computeReadiness(InvoiceOnAllComplete, []ShipmentGroup{
			billed(grp(GroupComplete, 40_000)),
			billed(grp(GroupComplete, 60_000)),
		}, true)
		if r.Ready {
			t.Fatal("a fully-billed order must not offer an invoice button")
		}
		if r.InvoiceableCents != 0 {
			t.Errorf("invoiceable = %d, want 0", r.InvoiceableCents)
		}
		// The sentence matters: this is the happy end of the ladder, and phrasing it as a refusal
		// would send an operator hunting for a problem that does not exist.
		if r.Reason == "" {
			t.Error("the finished state must still say what it is")
		}
	})

	t.Run("ON_ALL_COMPLETE bills the remainder when a subset was billed early", func(t *testing.T) {
		// A policy flip mid-job, or a subset invoice raised before the policy froze. The last
		// invoice must cover what is LEFT, not the whole order again.
		r := computeReadiness(InvoiceOnAllComplete, []ShipmentGroup{
			billed(grp(GroupComplete, 40_000)),
			grp(GroupComplete, 60_000),
		}, true)
		if !r.Ready || r.InvoiceableCents != 60_000 {
			t.Fatalf("ready=%v invoiceable=%d, want true/60000 (the unbilled remainder only)",
				r.Ready, r.InvoiceableCents)
		}
	})

	t.Run("ON_DEMAND_SUBSET with the delivered subset already billed waits", func(t *testing.T) {
		r := computeReadiness(InvoiceOnDemandSubset, []ShipmentGroup{
			billed(grp(GroupComplete, 40_000)),
			grp(GroupDispatched, 60_000), // on the truck, not delivered
		}, true)
		if r.Ready {
			t.Fatal("nothing new has been delivered, there is nothing to bill")
		}
		if r.Reason == "" {
			t.Error("the wait must be explained")
		}
	})

	t.Run("a CANCELLED group is not billable and not counted", func(t *testing.T) {
		r := computeReadiness(InvoiceOnAllComplete, []ShipmentGroup{
			grp(GroupComplete, 40_000),
			grp(GroupCancelled, 99_000),
		}, true)
		if !r.Ready || r.InvoiceableCents != 40_000 {
			t.Fatalf("ready=%v invoiceable=%d, want true/40000, a cancelled truck carries no money",
				r.Ready, r.InvoiceableCents)
		}
		if r.GroupsTotal != 1 {
			t.Errorf("groups_total = %d, want 1", r.GroupsTotal)
		}
	})
}
