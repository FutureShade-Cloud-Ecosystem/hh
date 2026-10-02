//go:build integration

package pos

// tax_reason_gate_integration_test.go, the money-path proof of the ungated relief-reason hole at
// Trenton, over the real composition on the throwaway database (the taxrate rig's world): the real
// handler behind the real guards, the tax assessor wired over the real tax.Service the way cmd/core
// wires it (the served mirror of posTaxAdapter, which a test cannot import, taxrate rig's own). The
// planted row is fixture-scoped damage: one tax_rates row, keyed by its notes marker, deleted on the
// way out.
//
// The two shapes the defect has, both rung through the real surface as a relief sale:
//
//  1. FNT_PRODUCT at 0 bps, one of the eleven the snapshot CHECK lawfully carries, neither evidence
//     gated nor the manual exception: main BINDS the planted row and commits the sale untaxed with a
//     2xx, the counter's own read still answering the standard. The leg demands the refusal and that
//     no order row was written. (After the fix the ring answers the writeErr default for this shape:
//     the table is misconfigured against the gate map, a server fault the admin edge is what keeps
//     off the ring, CreateRate refusing to plant it. The money protection is the refusal itself, the
//     sale never commits.)
//  2. A fully free-form reason outside the eleven: on main the engine falls back to the standard
//     rate, the writes run, and the sale dies at the snapshot CHECK as a bare 500 rollback. The leg
//     demands the 400 refusal, the reason named to the cashier.
//
// Run (own throwaway container, removed afterwards, an unused port):
//
//	docker run --name hh-taxrelief-ungated-pg -e POSTGRES_USER=hardscape \
//	  -e POSTGRES_PASSWORD=hardscape -e POSTGRES_DB=hardscapeos \
//	  -p 5655:5432 -d postgres:16
//	cd core
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5655/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core migrate
//	DATABASE_URL='postgres://hardscape:hardscape@localhost:5655/hardscapeos?sslmode=disable' \
//	  APP_ENV=dev go run ./cmd/core seed
//	CI=true DATABASE_URL='postgres://hardscape:hardscape@localhost:5655/hardscapeos?sslmode=disable' \
//	  go test -tags integration ./internal/pos/ -run TestIntegration_Trenton_ReliefReasonGate -p 1 -v

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/tax"
)

// The reasons the leg rings. Literals, not constants: the leg must compile against main, where the
// reason universe does not exist yet (commit 1 is tests only). FNT_PRODUCT is one of the eleven;
// the free-form code is outside it.
const (
	legFNTProduct = "FNT_PRODUCT"
	legFreeForm   = "FROB_NOMINAL_RELIEF"
)

// reliefReasonSaleBody is one CASH ring of the world's exact-cents paver, carrying a relief decision
// naming the reason under test.
func reliefReasonSaleBody(drawer, idemKey, reason string) map[string]any {
	return map[string]any{
		"idempotency_key": idemKey,
		"branch_id":       trentonBranchID,
		"till_session_id": drawer,
		"sale_type":       SaleCollectNow,
		"tender":          TenderCash,
		"relief":          map[string]any{"reason_code": reason},
		"lines":           []map[string]any{{"product_id": l2cProductID, "qty_entered": 2, "uom_entered": "SF"}},
	}
}

// assertNoSaleWritten holds at the database: the refused sale must have written nothing, not a
// pos_sale row under its key and nothing beside it.
func assertNoSaleWritten(t *testing.T, s *trentonStack, idemKey string) {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM pos_sale WHERE idempotency_key = $1`, idemKey).Scan(&n); err != nil {
		t.Fatalf("count pos_sale under the rung key: %v", err)
	}
	if n != 0 {
		t.Fatalf("the refused sale wrote %d pos_sale row(s) under key %s", n, idemKey)
	}
}

func TestIntegration_Trenton_ReliefReasonGate(t *testing.T) {
	s := newTrentonStack(t)
	// The cmd/core composition's assessor wire, over the real engine on this stack too, so the ring
	// binds through the same engine the counter's read answers (the taxrate rig's wire).
	taxSvc := tax.NewService(tax.NewRepository(s.pool))
	s.svc = s.svc.WithTaxAssessor(servedTaxAssessor{svc: taxSvc})

	// The misconfigured row, planted the only way the hole allows it (CreateRate will refuse the
	// plant once the fix lands): FNT_PRODUCT at 0 bps. Fixture-scoped: cleared before the plant and
	// deleted on the way out, keyed by the notes marker.
	const marker = "ungated-relief-row fixture (tax reason gate leg)"
	if _, err := s.pool.Exec(context.Background(),
		`DELETE FROM tax_rates WHERE reason_code = $1 AND notes = $2`, legFNTProduct, marker); err != nil {
		t.Fatalf("clear the fixture row: %v", err)
	}
	if _, err := s.pool.Exec(context.Background(),
		`INSERT INTO tax_rates (jurisdiction, reason_code, rate_bps, notes) VALUES ('ON', $1, 0, $2)`,
		legFNTProduct, marker); err != nil {
		t.Fatalf("plant the ungated zero-rate row: %v", err)
	}
	t.Cleanup(func() {
		if _, err := s.pool.Exec(context.Background(),
			`DELETE FROM tax_rates WHERE reason_code = $1 AND notes = $2`, legFNTProduct, marker); err != nil {
			t.Errorf("teardown of the planted rate row failed: %v", err)
		}
	})

	// Leg 1, the ungated shape: the relief sale naming FNT_PRODUCT must be refused and write nothing.
	key1 := key()
	rec := s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(trentonClerkSub), "",
		reliefReasonSaleBody(s.trentonTill, key1, legFNTProduct))
	if rec.Code >= 200 && rec.Code < 300 {
		t.Fatalf("the ungated relief sale COMMITTED with %d: %s", rec.Code, rec.Body.String())
	}
	assertNoSaleWritten(t, s, key1)

	// Leg 2, the free-form shape: the same sale under a reason outside the eleven must reach the
	// cashier as the 400 refusal with the reason named, never the bare 500 the snapshot CHECK
	// rollback answers on main.
	key2 := key()
	rec = s.do(http.MethodPost, "/api/v1/pos/sales", s.bearer(trentonClerkSub), "",
		reliefReasonSaleBody(s.trentonTill, key2, legFreeForm))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("the free-form relief sale must be the 400 refusal, got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), legFreeForm) {
		t.Fatalf("the 400 must name the offending reason to the cashier: %s", rec.Body.String())
	}
	assertNoSaleWritten(t, s, key2)
}
