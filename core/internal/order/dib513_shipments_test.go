package order

import (
	"strings"
	"testing"
)

// DIB-513 · the orders group.
//
// The shipments panel stated one fact twice, on screen at once. Its empty state reads
//
//	"This order ships as one delivery. Splitting it across trucks starts here."
//
// and a few rows below it, beside the Invoice button, the SERVER's readiness reason opened word for
// word the same way: "This order ships as one delivery, multi-ship invoicing does not apply."
//
// The empty state is the one that also says what to DO about it, so the reason keeps only its own
// job: naming why the button beside it is refusing. This pins that the two cannot converge again.
//
// Measured before the change, against a live stack, as the manager who owns the order, both
// sentences present in one panel measurement (verification/orders/before-manager-report.json,
// panels."shipments".sentences).

// theClientEmptyState is the copy order-shipments.ts renders when a plan has no groups. Held here
// so this gate fails if the two are ever edited towards each other from either side.
const theClientEmptyState = "This order ships as one delivery. Splitting it across trucks starts here."

func TestReadinessReasonDoesNotRepeatTheClientEmptyState(t *testing.T) {
	r := computeReadiness(InvoiceOnAllComplete, nil, true)

	if r.Ready {
		t.Fatalf("a plan with no groups has nothing to invoice; got Ready=true")
	}
	if r.Reason == "" {
		t.Fatal("a refusing Invoice button must still say why")
	}

	// The defect, stated as the thing that must not be true: the two sentences sharing an opening.
	const shared = "This order ships as one delivery"
	if strings.Contains(r.Reason, shared) {
		t.Errorf("the readiness reason repeats the panel's own empty state.\n"+
			"  empty state: %q\n  reason:      %q\n"+
			"Both render at once; one fact stated twice is the duplication DIB-513 exists to remove.",
			theClientEmptyState, r.Reason)
	}

	// And it must still do its own job, name the blocker, so the disabled button is explained.
	if !strings.Contains(strings.ToLower(r.Reason), "shipment group") {
		t.Errorf("the reason no longer names the blocker (no shipment groups): %q", r.Reason)
	}
}

// The other reasons on the same ladder were already distinct; this pins that none of them drifts
// into the empty state's opening either.
func TestNoReadinessReasonOpensLikeTheEmptyState(t *testing.T) {
	complete := []ShipmentGroup{{Status: GroupComplete, Invoiced: true, SubtotalCents: 1000}}
	partial := []ShipmentGroup{
		{Status: GroupComplete, Invoiced: false, SubtotalCents: 1000},
		{Status: GroupPlanned, SubtotalCents: 500},
	}
	for _, tc := range []struct {
		name   string
		policy string
		groups []ShipmentGroup
	}{
		{"no groups", InvoiceOnAllComplete, nil},
		{"all complete, all billed", InvoiceOnAllComplete, complete},
		{"partly complete, bills as one", InvoiceOnAllComplete, partial},
		{"on-demand, nothing complete", InvoiceOnDemandSubset, []ShipmentGroup{{Status: GroupPlanned}}},
		{"on-demand, all billed", InvoiceOnDemandSubset, complete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := computeReadiness(tc.policy, tc.groups, true)
			if r.Reason != "" && strings.HasPrefix(r.Reason, "This order ships as one delivery") {
				t.Errorf("reason repeats the client's empty state: %q", r.Reason)
			}
		})
	}
}
