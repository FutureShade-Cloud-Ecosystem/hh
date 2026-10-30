package billing

// write_off_route_test.go: THE ROUTE TEST for writing off an invoice's remaining balance (W3 item
// 0.10), the credit apply route test's own shape: the check reads the SERVED route table (the
// contract's pins, internal/platform/apigen), not the Go source, because the pins are what the
// deployment, the census and the front ends read.
//
// It deliberately names NO new Go symbol beyond the route table: before the routes existed it
// compiled and failed, which is the honest shape of a missing feature.

import "testing"

// TestWriteOffRoute_IsServedAndManagerGated pins the write off door.
func TestWriteOffRoute_IsServedAndManagerGated(t *testing.T) {
	const path = "/api/v1/ar/write-offs"
	found := findRoute("POST", path)
	if found == nil {
		t.Fatalf("no route %s in the served route table: there is no door for a manager to write off an invoice", path)
	}
	if found.OperationID != "billingWriteOffInvoice" {
		t.Fatalf("the write off route's operationId is %q, want billingWriteOffInvoice", found.OperationID)
	}
	// The gate: writing off a receivable is a manager's act, and only the controller role passes the
	// branch's limit beside the gate.
	if found.XRole != "requireManager" {
		t.Fatalf("the write off route is gated %q, want requireManager", found.XRole)
	}
	for _, s := range found.Surfaces {
		if s == "money-desk" {
			return
		}
	}
	t.Fatalf("the write off route carries surfaces %v, want money-desk among them", found.Surfaces)
}

// TestWriteOffReversalRoute_IsServedAndManagerGated pins the reversal door.
func TestWriteOffReversalRoute_IsServedAndManagerGated(t *testing.T) {
	const path = "/api/v1/ar/write-offs/reverse"
	found := findRoute("POST", path)
	if found == nil {
		t.Fatalf("no route %s in the served route table: there is no door for a manager to reverse a write off", path)
	}
	if found.OperationID != "billingReverseWriteOff" {
		t.Fatalf("the reversal route's operationId is %q, want billingReverseWriteOff", found.OperationID)
	}
	if found.XRole != "requireManager" {
		t.Fatalf("the reversal route is gated %q, want requireManager", found.XRole)
	}
}
