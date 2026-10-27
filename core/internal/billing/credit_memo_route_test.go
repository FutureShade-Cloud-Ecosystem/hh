package billing

// credit_memo_route_test.go: THE ROUTE TEST. The route exists, and it is gated.
//
// The audit's line was "openapi.yaml has no credit memo, adjustment or write off path", so the first
// thing this file checks is the plainest possible reading of that: is there a route for it at all?
//
// The check reads the SERVED route table (the contract's pins, internal/platform/apigen), not the Go
// source, because the pins are what the deployment, the census and the front ends read. A route that
// is written in Go but absent from the pins is a route nobody can find.
//
// The second half checks the GATE, and the gate is the whole point of the route: a credit is money
// leaving the company, so it is a manager's or the Controller's act, and no one else's.
//
// This file deliberately names NO new Go symbol. It is the red test: before the route existed it
// compiled and failed, which is the honest shape of a missing feature.

import (
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
)

// TestCreditMemoRoute_IsServedAndManagerGated is the route test.
//
// Before the fix: the served route table has no credit memo path, and this fails with the route's
// absence named in words.
func TestCreditMemoRoute_IsServedAndManagerGated(t *testing.T) {
	const path = "/api/v1/ar/credit-memos"
	var found *apigen.Route
	for i := range apigen.Routes {
		if apigen.Routes[i].Path == path {
			found = &apigen.Routes[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("no route %s in the served route table: there is no door for finance to issue a credit memo", path)
	}
	if found.Method != "POST" {
		t.Fatalf("the credit memo route is %s %s, want POST %s", found.Method, found.Path, path)
	}
	// The gate: a manager or the Controller, and nobody else.
	if found.XRole != "requireManager" {
		t.Fatalf("the credit memo route is gated %q, want requireManager (a manager or the Controller, and nobody else)", found.XRole)
	}
	// The surface: the Money Desk is where finance raises a credit.
	if len(found.Surfaces) == 0 {
		t.Fatalf("the credit memo route carries no surface: nothing in the product would offer it")
	}
}
