package main

// tax_rule_wire_test.go pins the queued composition leg (PR 101 round 1's
// P3 item 4): no leg drove posTaxRuleAdapter and the wire_tops wire against
// a MUTATED configuration row, so a fold that drops the WithTaxRuleSource
// line, or an adapter that stops answering the row, ships a counter whose
// tax-rate read freezes on the flat engine's constant word while every
// no-database suite stays green. Two halves, one row:
//
//   - the ADAPTER half drives posTaxRuleAdapter itself over the real
//     tax.Service on this real migrated database and asserts it answers the
//     org default tax_config row's word (branch_id NULL, seeded
//     INVOICE_TOTAL by migration 0042, the row GetConfig(ctx, "") reads).
//
//   - the WIRE half drives the SERVED read, GET /api/v1/pos/tax-rate
//     (posGetTaxRate), through the full composition (registerRoutes over
//     the identity environment map and the gate database, the
//     compfixture_test.go rig), and asserts the read's JSON carries
//     round_mode from that same row: wire_tops.go wires
//     WithTaxRuleSource(posTaxRuleAdapter{svc: taxSvc}) into pos.Service,
//     and unwired the service answers the constant INVOICE_TOTAL
//     (internal/pos/taxrate.go), which is exactly the bite the wire half's
//     PER_LINE leg takes.
//
// Both halves first answer the seeded row, then the row MUTATED to
// PER_LINE, the only other lawful word (internal/tax/service.go refuses
// everything else), so the leg proves the row DRIVES the word rather than
// that the word happens to match a constant. The rate side is not this
// leg's subject: rate_bps is asserted a non-negative integer and its value
// is never pinned.
//
// The mutated row is restored to the word it held on entry in a t.Cleanup
// that always runs, so the kit database is left exactly as found. The test
// gates on HH_CORE_TEST_DATABASE_URL like the other composition suites:
// absent the variable it skips politely.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/apigen"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

func TestComposition_TaxRuleWire_OrgDefaultRowDrivesAdapterAndServedRead(t *testing.T) {
	// The adapter half's own pool over the gate database, and the served
	// graph the wire half drives: two pools, one database, the rig's shape.
	pool := wiringPool(t)
	env := newCompEnv(t)
	rec, _ := registeredSurface(t, env)

	ctx := context.Background()

	// The org default row's word on entry, saved for the restore and
	// asserted the seeded word: migration 0042 plants branch_id NULL with
	// round_mode INVOICE_TOTAL, and a scratch database still holding a
	// mutated word means an earlier leg broke its own cleanup.
	var seeded string
	if err := pool.QueryRow(ctx,
		`SELECT round_mode FROM tax_config WHERE branch_id IS NULL`).Scan(&seeded); err != nil {
		t.Fatalf("read the org default tax_config row: %v", err)
	}
	if seeded != "INVOICE_TOTAL" {
		t.Fatalf("org default tax_config row holds %q, want the seeded INVOICE_TOTAL: the scratch database is not on the migration seed", seeded)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx,
			`UPDATE tax_config SET round_mode = $1 WHERE branch_id IS NULL`, seeded); err != nil {
			t.Errorf("restore the org default tax_config row: %v", err)
		}
	})

	// The adapter half over the real tax.Service on the real database: the
	// word the row holds is the word the adapter answers.
	adapter := posTaxRuleAdapter{svc: tax.NewService(tax.NewRepository(pool))}
	got, err := adapter.RoundMode(ctx)
	if err != nil {
		t.Fatalf("adapter RoundMode over the seeded row: %v", err)
	}
	if got != "INVOICE_TOTAL" {
		t.Fatalf("adapter answered %q over the seeded row, want INVOICE_TOTAL: the adapter is not reading tax_config", got)
	}

	// The wire half: the SERVED read through the full composition. Staff
	// gated, so the probe carries the kit's staff token and org header, the
	// same shape every composition probe carries.
	readTaxRate := func() apigen.PosTaxRate {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/pos/tax-rate", nil)
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, testkit.SubSalesManager))
		req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		if rsp.Code != http.StatusOK {
			t.Fatalf("GET /api/v1/pos/tax-rate = %d (%s), want 200", rsp.Code, rsp.Body.String())
		}
		var out apigen.PosTaxRate
		if err := json.Unmarshal(rsp.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode the served tax-rate body: %v", err)
		}
		return out
	}

	served := readTaxRate()
	if served.RoundMode != "INVOICE_TOTAL" {
		t.Fatalf("served read answered round_mode %q over the seeded row, want INVOICE_TOTAL: the wire dropped WithTaxRuleSource or the adapter is not the source", served.RoundMode)
	}
	if served.RateBps < 0 {
		t.Fatalf("served read answered rate_bps %d, want a non-negative integer (the rate side is not this leg's subject and its value is never pinned)", served.RateBps)
	}

	// The mutation: PER_LINE is the only other lawful word.
	if _, err := pool.Exec(ctx,
		`UPDATE tax_config SET round_mode = 'PER_LINE' WHERE branch_id IS NULL`); err != nil {
		t.Fatalf("mutate the org default row to PER_LINE: %v", err)
	}

	// Both halves must answer PER_LINE WHILE the row holds it, so the row
	// is re-read here, between the mutation and the assertions.
	var holding string
	if err := pool.QueryRow(ctx,
		`SELECT round_mode FROM tax_config WHERE branch_id IS NULL`).Scan(&holding); err != nil {
		t.Fatalf("re-read the org default row after the mutation: %v", err)
	}
	if holding != "PER_LINE" {
		t.Fatalf("org default row holds %q after the mutation, want PER_LINE: the mutation did not land", holding)
	}

	got, err = adapter.RoundMode(ctx)
	if err != nil {
		t.Fatalf("adapter RoundMode over the mutated row: %v", err)
	}
	if got != "PER_LINE" {
		t.Fatalf("adapter answered %q while the row holds PER_LINE, want PER_LINE: the adapter's word does not follow the row (an adapter frozen on a constant passes every seeded leg)", got)
	}

	served = readTaxRate()
	if served.RoundMode != "PER_LINE" {
		t.Fatalf("served read answered round_mode %q while the row holds PER_LINE, want PER_LINE: the served word does not follow the row (drop WithTaxRuleSource and the read falls back to the constant INVOICE_TOTAL)", served.RoundMode)
	}
	if served.RateBps < 0 {
		t.Fatalf("served read answered rate_bps %d over the mutated row, want a non-negative integer", served.RateBps)
	}
}
