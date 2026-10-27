package billing

// credit_apply_route_test.go: THE ROUTE TEST for applying a credit to a later invoice (FW1-12b).
//
// The same shape credit_memo_route_test.go pinned for FW1-12a: the check reads the SERVED route table
// (the contract's pins, internal/platform/apigen), not the Go source, because the pins are what the
// deployment, the census and the front ends read. A route written in Go but absent from the pins is a
// route nobody can find.
//
// This file deliberately names NO new Go symbol. It is the red test: before the routes existed it
// compiled and failed, which is the honest shape of a missing feature.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// findRoute answers the route table's entry for one method and path, or nil.
func findRoute(method, path string) *apigen.Route {
	for i := range apigen.Routes {
		if apigen.Routes[i].Path == path && apigen.Routes[i].Method == method {
			return &apigen.Routes[i]
		}
	}
	return nil
}

// TestCreditApplyRoute_IsServedAndManagerGated pins the apply door.
//
// Before the fix: the served route table has no credit apply path, and this fails with the route's
// absence named in words.
func TestCreditApplyRoute_IsServedAndManagerGated(t *testing.T) {
	const path = "/api/v1/ar/credits/apply"
	found := findRoute("POST", path)
	if found == nil {
		t.Fatalf("no route %s in the served route table: there is no door for the desk to apply a credit to a later invoice", path)
	}
	if found.OperationID != "billingApplyCredit" {
		t.Fatalf("the apply route's operationId is %q, want billingApplyCredit", found.OperationID)
	}
	// The gate: applying held value from the receivables desk is the reapply family's act, a manager's,
	// and nobody else's.
	if found.XRole != "requireManager" {
		t.Fatalf("the credit apply route is gated %q, want requireManager", found.XRole)
	}
	if len(found.Surfaces) == 0 {
		t.Fatalf("the credit apply route carries no surface: nothing in the product would offer it")
	}
}

// TestCreditApplicationReverseRoute_IsServedAndManagerGated pins the undo door: an application is
// undone only by a reversal that restores both sides, and that reversal needs its own door.
func TestCreditApplicationReverseRoute_IsServedAndManagerGated(t *testing.T) {
	const path = "/api/v1/ar/credits/applications/reverse"
	found := findRoute("POST", path)
	if found == nil {
		t.Fatalf("no route %s in the served route table: there is no door to reverse a credit application", path)
	}
	if found.OperationID != "billingReverseCreditApplication" {
		t.Fatalf("the reverse route's operationId is %q, want billingReverseCreditApplication", found.OperationID)
	}
	if found.XRole != "requireManager" {
		t.Fatalf("the credit application reverse route is gated %q, want requireManager", found.XRole)
	}
}
