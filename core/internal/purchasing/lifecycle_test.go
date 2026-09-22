package purchasing

// lifecycle_test.go, the PO transition matrix, exhaustively. Every one of the 36 (from, to) pairs is
// asserted, so a future edit that ADDS a legal move has to change this table too, a matrix guarded only by
// its happy paths is a matrix that quietly grows.

import (
	"strings"
	"testing"
)

var allStatuses = []string{StatusDraft, StatusOrdered, StatusPartial, StatusReceived, StatusClosed, StatusCancelled}

// TestCheckTransition_FullMatrix walks every ordered pair of statuses and asserts legality against the
// ruled table. The `legal` set is written out longhand rather than derived from operatorMoves, because a
// test that derives its expectation from the code under test asserts nothing.
func TestCheckTransition_FullMatrix(t *testing.T) {
	legal := map[string]bool{
		"DRAFT>ORDERED":     true,
		"DRAFT>CANCELLED":   true,
		"ORDERED>CANCELLED": true,
		"ORDERED>CLOSED":    true,
		"PARTIAL>CLOSED":    true,
		"RECEIVED>CLOSED":   true,
	}
	for _, from := range allStatuses {
		for _, to := range allStatuses {
			key := from + ">" + to
			err := CheckTransition(Transition{From: from, To: to, HasLines: true, AnyReceived: false})
			if legal[key] && err != nil {
				t.Errorf("%s must be legal, got %v", key, err)
			}
			if !legal[key] && err == nil {
				t.Errorf("%s must be refused, got nil", key)
			}
		}
	}
}

// TestCheckTransition_OrderingAnEmptyPORefused, a PO with no lines promises nothing and can never be
// matched against, so it may not be sent to a vendor.
func TestCheckTransition_OrderingAnEmptyPORefused(t *testing.T) {
	err := CheckTransition(Transition{From: StatusDraft, To: StatusOrdered, HasLines: false})
	if err == nil {
		t.Fatal("ordering an empty purchase order must be refused")
	}
	if !strings.Contains(err.Error(), "empty") {
		t.Fatalf("the refusal must say the PO is empty, got %q", err)
	}
	if got := statusOf(err); got != 409 {
		t.Fatalf("status = %d, want 409", got)
	}
}

// TestCheckTransition_CancelAfterReceiptRefused, once goods are on the yard the PO is the only record of
// what we owe for them. Cancelling it would strand the GRNI accrual with no document behind it.
func TestCheckTransition_CancelAfterReceiptRefused(t *testing.T) {
	if err := CheckTransition(Transition{From: StatusOrdered, To: StatusCancelled, HasLines: true, AnyReceived: false}); err != nil {
		t.Fatalf("cancelling an untouched ORDERED PO must be legal: %v", err)
	}
	err := CheckTransition(Transition{From: StatusOrdered, To: StatusCancelled, HasLines: true, AnyReceived: true})
	if err == nil {
		t.Fatal("cancelling a PO with received goods must be refused")
	}
	if !strings.Contains(err.Error(), "close it instead") {
		t.Fatalf("the refusal must point at CLOSED, got %q", err)
	}
}

// TestCheckTransition_ReceiptDrivenStatesNotRequestable, a clerk must not be able to type a PO into
// RECEIVED. That state is a count of what landed, and the three-way match measures billed against it.
func TestCheckTransition_ReceiptDrivenStatesNotRequestable(t *testing.T) {
	for _, to := range []string{StatusPartial, StatusReceived} {
		err := CheckTransition(Transition{From: StatusOrdered, To: to, HasLines: true})
		if err == nil {
			t.Fatalf("%s must not be requestable", to)
		}
		if !strings.Contains(err.Error(), "receiving goods") {
			t.Fatalf("the refusal must explain how %s is reached, got %q", to, err)
		}
	}
}

func TestCheckTransition_UnknownStatuses(t *testing.T) {
	if err := CheckTransition(Transition{From: "SHIPPED", To: StatusClosed}); err == nil {
		t.Fatal("an unknown FROM must be refused")
	}
	err := CheckTransition(Transition{From: StatusDraft, To: "APPROVED"})
	if err == nil {
		t.Fatal("an unknown TO must be refused")
	}
	if got := statusOf(err); got != 400 {
		t.Fatalf("an unknown target status is the caller's mistake: status = %d, want 400", got)
	}
}

func TestCheckTransition_SameStatusRefused(t *testing.T) {
	for _, s := range allStatuses {
		if err := CheckTransition(Transition{From: s, To: s, HasLines: true}); err == nil {
			t.Fatalf("%s → %s must be refused", s, s)
		}
	}
}

func TestCanEditLines(t *testing.T) {
	for _, s := range allStatuses {
		want := s == StatusDraft
		if got := CanEditLines(s); got != want {
			t.Errorf("CanEditLines(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestCheckReceivable(t *testing.T) {
	cases := map[string]bool{
		StatusDraft: false, StatusOrdered: true, StatusPartial: true,
		StatusReceived: false, StatusClosed: false, StatusCancelled: false,
	}
	for status, ok := range cases {
		err := CheckReceivable(status)
		if ok && err != nil {
			t.Errorf("receiving against %s must be allowed, got %v", status, err)
		}
		if !ok && err == nil {
			t.Errorf("receiving against %s must be refused", status)
		}
	}
}

func TestStatusAfterReceipt(t *testing.T) {
	cases := []struct {
		name  string
		lines []LineProgress
		want  string
	}{
		{"nothing received", []LineProgress{{10, 0}}, StatusPartial},
		{"one line short", []LineProgress{{10, 10}, {5, 4}}, StatusPartial},
		{"all full", []LineProgress{{10, 10}, {5, 5}}, StatusReceived},
		// The epsilon case that would otherwise strand a PO on the worklist forever: DECIMAL(19,4) round-
		// tripped through float64 lands a hair under the ordered quantity.
		{"float noise still counts as full", []LineProgress{{20, 19.99999999999}}, StatusReceived},
		{"over-received still counts as full", []LineProgress{{10, 10.0001}}, StatusReceived},
		{"no lines is never RECEIVED", nil, StatusPartial},
	}
	for _, c := range cases {
		if got := StatusAfterReceipt(c.lines); got != c.want {
			t.Errorf("%s: StatusAfterReceipt = %s, want %s", c.name, got, c.want)
		}
	}
}

func TestCheckReceiptQty(t *testing.T) {
	if err := CheckReceiptQty(1, 4, 10, 6); err != nil {
		t.Fatalf("receiving exactly the outstanding balance must be allowed: %v", err)
	}
	if err := CheckReceiptQty(1, 10, 10, 0); err != nil {
		t.Fatalf("receiving the whole line must be allowed: %v", err)
	}
	err := CheckReceiptQty(3, 5, 10, 6)
	if err == nil {
		t.Fatal("over-receipt must be refused")
	}
	if got := statusOf(err); got != 409 {
		t.Fatalf("over-receipt status = %d, want 409", got)
	}
	// Both numbers in the message: the clerk needs to know what the system thinks is outstanding.
	if !strings.Contains(err.Error(), "5.0000") || !strings.Contains(err.Error(), "4.0000") {
		t.Fatalf("the over-receipt refusal must name requested AND outstanding, got %q", err)
	}
	if err := CheckReceiptQty(1, 0, 10, 0); err == nil {
		t.Fatal("a zero receive quantity must be refused")
	}
	if err := CheckReceiptQty(1, -1, 10, 0); err == nil {
		t.Fatal("a negative receive quantity must be refused")
	}
	// Float noise on an exactly-full receipt must not read as an over-receipt.
	if err := CheckReceiptQty(1, 20.0000000001, 20, 0); err != nil {
		t.Fatalf("float noise must not manufacture an over-receipt: %v", err)
	}
}

func statusOf(err error) int {
	if ce, ok := err.(clientError); ok {
		return ce.status
	}
	return 0
}
