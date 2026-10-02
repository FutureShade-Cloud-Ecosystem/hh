package main

// The HH-180 reopen fence's production wiring, pinned at the composition
// root (round 3 of the pull request's review): the audit module's own
// suites prove the fence decision over stub resolvers, but nothing in them
// would catch a dropped wiring line in wire_money.go or a broken not-found
// mapping in the two production adapters, which would silently empty the
// order and quote trails for every scoped caller (or turn an unknown id
// into a 503) while every suite stays green. This test drives the FULL
// served surface over the fixture database, exactly as the binary wires
// it, with the two fixture orders the testkit plants in the two north
// branches: a branch-scoped sales manager (branch A) and a global general
// manager read the reopen route for an order each of them can and cannot
// reach, with the trail rows written in the PRODUCTION shape (an
// ORDER_CHANGED edit row carrying no branch, the way the order edit
// writers stamp nothing). The assertions pin the adapters (the entity
// resolves, and resolves by its own branch) and the wiring (the served
// route answers through the fence at all); without either, the scoped
// legs below answer differently.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// hh180WireRequestID names this test's audit rows for exact cleanup.
const hh180WireRequestID = "hh180-wire-test"

func TestComposition_HH180ReopenFence_AdaptersAndWiring(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)

	ctx := context.Background()
	for _, orderID := range []string{testkit.OrderAlphaReadyID, testkit.OrderAlphaXBranchID} {
		if _, err := graph.pool.Exec(ctx,
			`INSERT INTO audit_log (action, entity_type, entity_id, changes, request_id) VALUES
				('ORDER_CHANGED', 'order', $1::uuid,
				 '{"field":"unit_price_cents","line_no":1,"before":"1000","after":"1250"}'::jsonb,
				 $2)`, orderID, hh180WireRequestID); err != nil {
			t.Fatalf("plant row for %s: %v", orderID, err)
		}
	}
	t.Cleanup(func() {
		if _, err := graph.pool.Exec(ctx,
			`DELETE FROM audit_log WHERE request_id = $1`, hh180WireRequestID); err != nil {
			t.Fatalf("cleanup: %v", err)
		}
	})

	probe := func(t *testing.T, sub, orderID string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/audit/transaction/order/"+orderID, nil)
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, sub))
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		return rsp.Code, rsp.Body.String()
	}

	// The branch-scoped sales manager (granted branch A) reading the branch B
	// order: the adapter resolves the entity, the fence refuses the branch, and
	// the answer is the empty trail, never the rows and never a refusal.
	code, body := probe(t, testkit.SubSalesManager, testkit.OrderAlphaXBranchID)
	if code != http.StatusOK {
		t.Fatalf("scoped caller, away order: status %d, want 200 with an empty trail (body %s)", code, body)
	}
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("scoped caller, away order: body %s, want the empty trail [] (the adapters must resolve the entity and the wiring must fence the served route)", body)
	}

	// The same scoped caller reading their own branch's order: the row they
	// are entitled to, branch-less production shape included.
	code, body = probe(t, testkit.SubSalesManager, testkit.OrderAlphaReadyID)
	if code != http.StatusOK {
		t.Fatalf("scoped caller, own order: status %d, want 200 (body %s)", code, body)
	}
	if !strings.Contains(body, "unit_price_cents") {
		t.Errorf("scoped caller, own order: body %s, want the production-shaped edit row", body)
	}

	// The global general manager keeps the full trail on both orders.
	code, body = probe(t, testkit.SubGeneralMgr, testkit.OrderAlphaXBranchID)
	if code != http.StatusOK || !strings.Contains(body, "unit_price_cents") {
		t.Errorf("global caller, away order: status %d body %s, want 200 with the row", code, body)
	}
}
