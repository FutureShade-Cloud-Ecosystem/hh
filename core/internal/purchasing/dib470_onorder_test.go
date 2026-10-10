package purchasing

// dib470_onorder_test.go, the aggregate behind inventory's on-order seam, DB-free.
//
// What is worth proving without a database: that the aggregate and the DIB-460 detail list are the
// same read of the same rows. The two numbers disagreeing on one product is what DIB-470 was filed
// about, so the SQL text they share is asserted here, and the arithmetic over the same rows is
// asserted through the seam.

import (
	"context"
	"strings"
	"testing"
)

// fakeOnOrderStore is the aggregate's persistence seam.
type fakeOnOrderStore struct {
	rows        []OutstandingOnOrder
	gotBranch   string
	gotProducts []string
	drafts      []OpenDraft
}

func (f *fakeOnOrderStore) OpenDraftsFor(_ context.Context, branchID string, productIDs []string) ([]OpenDraft, error) {
	f.gotBranch, f.gotProducts = branchID, productIDs
	return f.drafts, nil
}

func (f *fakeOnOrderStore) OutstandingOnOrderBase(_ context.Context, branchID string, productIDs []string) ([]OutstandingOnOrder, error) {
	f.gotBranch, f.gotProducts = branchID, productIDs
	return f.rows, nil
}

// TestDIB470_TheAggregateAndTheIncomingListAreTheSameREAD, the two queries are composed from the
// same fragments, so "still coming" cannot mean one thing on the product page and another on the
// replenishment worklist.
//
// This asserts the SQL TEXT, deliberately. Two queries that agree today because someone kept them in
// step by hand is exactly the arrangement that drifted; sharing the fragment is the fix, and this is
// what notices if a later edit un-shares it.
func TestDIB470_TheAggregateAndTheIncomingListAreTheSameREAD(t *testing.T) {
	for _, frag := range []struct{ name, sql string }{
		{"the source tables", openPOLineSource},
		{"the open predicate", openPOLineIsOpen},
		{"the outstanding expression", openPOLineOutstanding},
	} {
		if !strings.Contains(openPOLinesSQL, frag.sql) {
			t.Errorf("the DIB-460 detail read no longer contains %s, the two reads can now disagree", frag.name)
		}
		if !strings.Contains(outstandingOnOrderSQL, frag.sql) {
			t.Errorf("the DIB-470 aggregate no longer contains %s, the two reads can now disagree", frag.name)
		}
	}
	// The predicate takes the status set as a parameter in BOTH, at the same position, so
	// openPOStatuses stays the single definition of "still coming".
	if !strings.Contains(openPOLineIsOpen, "$2::text[]") {
		t.Error("the status set is no longer a parameter, a literal IN list can drift from openPOStatuses")
	}
	// Base units, in the expression both reads sum. Sell units here would be the whole bug back.
	if !strings.Contains(openPOLineOutstanding, "qty_ordered_base") ||
		!strings.Contains(openPOLineOutstanding, "qty_received_base") {
		t.Errorf("the outstanding expression is not ordered-minus-received in BASE units: %s", openPOLineOutstanding)
	}
}

// TestDIB470_OnlyAPlacedOrderIsOnOrder, the status set the aggregate counts is ORDERED and PARTIAL,
// written out longhand. A draft never covers a shortfall (the principal's ruling, row 1303): it is
// not a promise to the vendor, so the worklist must keep listing a row a draft was started for. The
// incoming LIST keeps its own set, DRAFT included, and the two are different sets on purpose.
func TestDIB470_OnlyAPlacedOrderIsOnOrder(t *testing.T) {
	want := map[string]bool{StatusOrdered: true, StatusPartial: true}
	if len(onOrderStatuses) != len(want) {
		t.Fatalf("the on-order set is %v, want exactly ORDERED and PARTIAL", onOrderStatuses)
	}
	for _, s := range onOrderStatuses {
		if !want[s] {
			t.Errorf("%s counts as on order, only a placed order is supply", s)
		}
	}
	for _, not := range []string{StatusDraft, StatusReceived, StatusClosed, StatusCancelled} {
		for _, s := range onOrderStatuses {
			if s == not {
				t.Errorf("%s is counted as on order", not)
			}
		}
	}
	if !strings.Contains(outstandingOnOrderSQL, "$2::text[]") {
		t.Error("the aggregate no longer takes its status set as a parameter")
	}
	// The draft pointer is built from the same fragments, so "outstanding" means one thing.
	for _, frag := range []string{openPOLineSource, openPOLineIsOpen} {
		if !strings.Contains(openDraftsSQL, frag) {
			t.Errorf("the draft pointer no longer shares the open-line fragment %q", frag)
		}
	}
}

// TestDIB470_TheSeamPassesTheScopeThrough, branch and product filters reach the store unchanged; an
// empty branch means every branch, which is what an all-branch worklist asks.
func TestDIB470_TheSeamPassesTheScopeThrough(t *testing.T) {
	st := &fakeOnOrderStore{rows: []OutstandingOnOrder{
		{ProductID: "p1", BranchID: "b1", QtyBase: 160},
	}}
	svc := NewService(nil).withOnOrderStore(st)

	rows, err := svc.OutstandingOnOrderBase(context.Background(), "b1", []string{"p1"})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if st.gotBranch != "b1" || len(st.gotProducts) != 1 || st.gotProducts[0] != "p1" {
		t.Fatalf("scope arrived as branch %q products %v", st.gotBranch, st.gotProducts)
	}
	if len(rows) != 1 || rows[0].QtyBase != 160 {
		t.Fatalf("rows = %+v", rows)
	}

	if _, err := svc.OutstandingOnOrderBase(context.Background(), "", nil); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if st.gotBranch != "" || st.gotProducts != nil {
		t.Fatalf("an unscoped read arrived as branch %q products %v", st.gotBranch, st.gotProducts)
	}
}

// TestDIB470_NoStoreAndNoPoolIsAnError, an aggregate with nowhere to read from must say so rather
// than return an empty slice, which the worklist would grade as "nothing on order".
func TestDIB470_NoStoreAndNoPoolIsAnError(t *testing.T) {
	if _, err := NewService(nil).OutstandingOnOrderBase(context.Background(), "b1", nil); err == nil {
		t.Fatal("an unconfigured aggregate returned no error, the worklist would read it as nothing on order")
	}
}
