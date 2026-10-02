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

// hh180WireQuoteAwayID is a quote the kit does not plant (its two fixture
// quotes both sit in branch A), inserted and removed by this test so the
// quote adapter's away leg has a real row to resolve.
const hh180WireQuoteAwayID = "3ef10000-0000-4000-8000-0aa1800000b1"

// hh180WireUnknownID is a well-formed uuid no fixture and no test ever
// plants: the not-found leg of every adapter.
const hh180WireUnknownID = "3ef10000-0000-4000-8000-0aa1800000ff"

func TestComposition_HH180ReopenFence_AdaptersAndWiring(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)

	ctx := context.Background()
	if _, err := graph.pool.Exec(ctx,
		`INSERT INTO quotes (id, quote_no, customer_id, branch_id, status, org_id) VALUES
			($1::uuid, 'FX-HH180-WIRE-Q1', $2::uuid, $3::uuid, 'DRAFT', $4::uuid)`,
		hh180WireQuoteAwayID, testkit.TenantAlphaID, testkit.BranchNorthBID, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant away quote: %v", err)
	}
	for _, e := range []struct{ entityType, entityID string }{
		{"order", testkit.OrderAlphaReadyID},
		{"order", testkit.OrderAlphaXBranchID},
		{"quote", testkit.QuoteAlphaID},
		{"quote", hh180WireQuoteAwayID},
	} {
		if _, err := graph.pool.Exec(ctx,
			`INSERT INTO audit_log (action, entity_type, entity_id, changes, request_id) VALUES
				('ORDER_CHANGED', $1, $2::uuid,
				 '{"field":"unit_price_cents","line_no":1,"before":"1000","after":"1250"}'::jsonb,
				 $3)`, e.entityType, e.entityID, hh180WireRequestID); err != nil {
			t.Fatalf("plant row for %s %s: %v", e.entityType, e.entityID, err)
		}
	}
	t.Cleanup(func() {
		if _, err := graph.pool.Exec(ctx,
			`DELETE FROM audit_log WHERE request_id = $1`, hh180WireRequestID); err != nil {
			t.Fatalf("cleanup audit rows: %v", err)
		}
		if _, err := graph.pool.Exec(ctx,
			`DELETE FROM quotes WHERE id = $1`, hh180WireQuoteAwayID); err != nil {
			t.Fatalf("cleanup away quote: %v", err)
		}
	})

	probe := func(t *testing.T, sub, entityType, entityID string) (int, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/audit/transaction/"+entityType+"/"+entityID, nil)
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, sub))
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		return rsp.Code, rsp.Body.String()
	}

	// The branch-scoped sales manager (granted branch A) reading the branch B
	// order: the adapter resolves the entity, the fence refuses the branch, and
	// the answer is the empty trail, never the rows and never a refusal.
	code, body := probe(t, testkit.SubSalesManager, "order", testkit.OrderAlphaXBranchID)
	if code != http.StatusOK {
		t.Fatalf("scoped caller, away order: status %d, want 200 with an empty trail (body %s)", code, body)
	}
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("scoped caller, away order: body %s, want the empty trail [] (the adapters must resolve the entity and the wiring must fence the served route)", body)
	}

	// The same scoped caller reading their own branch's order: the row they
	// are entitled to, branch-less production shape included.
	code, body = probe(t, testkit.SubSalesManager, "order", testkit.OrderAlphaReadyID)
	if code != http.StatusOK {
		t.Fatalf("scoped caller, own order: status %d, want 200 (body %s)", code, body)
	}
	if !strings.Contains(body, "unit_price_cents") {
		t.Errorf("scoped caller, own order: body %s, want the production-shaped edit row", body)
	}

	// The quote adapter, the same three legs: the away quote (branch B, planted
	// by this test) is empty for the scoped caller and served to the global
	// caller; the fixture quote in branch A is the scoped caller's own.
	code, body = probe(t, testkit.SubSalesManager, "quote", hh180WireQuoteAwayID)
	if code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Errorf("scoped caller, away quote: status %d body %s, want 200 with the empty trail []", code, body)
	}
	code, body = probe(t, testkit.SubSalesManager, "quote", testkit.QuoteAlphaID)
	if code != http.StatusOK || !strings.Contains(body, "unit_price_cents") {
		t.Errorf("scoped caller, own quote: status %d body %s, want 200 with the planted row", code, body)
	}
	code, body = probe(t, testkit.SubGeneralMgr, "quote", hh180WireQuoteAwayID)
	if code != http.StatusOK || !strings.Contains(body, "unit_price_cents") {
		t.Errorf("global caller, away quote: status %d body %s, want 200 with the planted row", code, body)
	}

	// A well-formed unknown order id: the adapter's not-found mapping answers
	// the empty trail with 200, never a 503 and never a refusal. A broken
	// mapping (an unknown id surfacing as an owner read error) fails here.
	code, body = probe(t, testkit.SubSalesManager, "order", hh180WireUnknownID)
	if code != http.StatusOK {
		t.Fatalf("scoped caller, unknown order: status %d, want 200 (a broken not-found mapping turns unknown ids into server errors)", code)
	}
	if strings.TrimSpace(body) != "[]" {
		t.Errorf("scoped caller, unknown order: body %s, want the empty trail []", body)
	}
	code, body = probe(t, testkit.SubSalesManager, "quote", hh180WireUnknownID)
	if code != http.StatusOK || strings.TrimSpace(body) != "[]" {
		t.Errorf("scoped caller, unknown quote: status %d body %s, want 200 with the empty trail []", code, body)
	}

	// The global general manager keeps the full trail on both orders.
	code, body = probe(t, testkit.SubGeneralMgr, "order", testkit.OrderAlphaXBranchID)
	if code != http.StatusOK || !strings.Contains(body, "unit_price_cents") {
		t.Errorf("global caller, away order: status %d body %s, want 200 with the row", code, body)
	}
	code, body = probe(t, testkit.SubGeneralMgr, "order", testkit.OrderAlphaReadyID)
	if code != http.StatusOK || !strings.Contains(body, "unit_price_cents") {
		t.Errorf("global caller, own order: status %d body %s, want 200 with the row", code, body)
	}
}
