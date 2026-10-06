package main

// qdsr_grant_test.go, the Counter Till payment grant read (Quote Desk server reads, read four):
// over the registered surface, the answer for each role is held to what the gate itself does on
// the payment routes, not to a table of its own: a caller the grant read says can take payment is
// admitted by the gate on the till sale and on the payment door, and a caller it says cannot is
// refused by the gate on both. Roles with neither the Counter Till nor the Quote Desk meet the
// gate's 403 on the read itself. A dealer's edited configuration moves the answer with no code
// change.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	qdsrSubOutsideSales  = "fxstaff-outside-sales-qdsr"
	qdsrUserOutsideSales = "3ef10000-0000-4000-8000-000000001411"
	qdsrGrantOutside     = "3ef10000-0000-4000-8000-000000001421"
)

// qdsrPlantOutsideSales plants the one role the matrix fixtures lack: outside sales, a Quote Desk
// seat with no Counter Till cell, the person this read exists for. Idempotent and never cleaned,
// the matrix fixtures' own discipline.
func qdsrPlantOutsideSales(t *testing.T, env *compEnv, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
		VALUES ($1, 'fxstaff.outside.sales.qdsr@fixture.test', 'Fixture Outside Sales QDSR', 'ACTIVE', $2, $3)
		ON CONFLICT (id) DO NOTHING`, qdsrUserOutsideSales, qdsrSubOutsideSales, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant outside sales user: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_role_grant (id, platform_user_id, role_id, org_id, branch_id)
		VALUES ($1, $2, 'outside_sales', $3, $4) ON CONFLICT (id) DO NOTHING`,
		qdsrGrantOutside, qdsrSubOutsideSales, testkit.OrgNorthID, testkit.BranchNorthAID); err != nil {
		t.Fatalf("plant outside sales grant: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES ($1, $2, $3)
		ON CONFLICT (user_id, branch_id) DO NOTHING`, qdsrUserOutsideSales, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant outside sales branch grant: %v", err)
	}
	env.appwrite.setTeam(qdsrSubOutsideSales, "org:"+testkit.OrgNorthSlug)
}

// qdsrGrantAnswer reads the grant for one subject: the status and, on 200, the answer.
func qdsrGrantAnswer(t *testing.T, fx *xrFixture, sub string) (int, string, bool) {
	t.Helper()
	code, body := qdsrGet(t, fx, sub, "/api/v1/pos/payment-grant")
	if code != http.StatusOK {
		return code, body, false
	}
	var out struct {
		CanTakePayment *bool `json:"can_take_payment"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.CanTakePayment == nil {
		t.Fatalf("the answer is not {can_take_payment}: %q (%v)", body, err)
	}
	return code, body, *out.CanTakePayment
}

// qdsrAssertGrantMatchesTheGate holds the grant read to the gate on both payment routes.
func qdsrAssertGrantMatchesTheGate(t *testing.T, fx *xrFixture, sub string, want bool) {
	t.Helper()
	code, body, got := qdsrGrantAnswer(t, fx, sub)
	if code != http.StatusOK {
		t.Fatalf("%s: the grant read answers %d %s, want 200", sub, code, body)
	}
	if got != want {
		t.Fatalf("%s: can_take_payment %v, want %v", sub, got, want)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/pos/sales"},
		{http.MethodPost, "/api/v1/quotes/{id}/order-payment"},
	} {
		c, b := rgProbe(t, fx, route.method, route.path, rgHeaders(t, fx, sub))
		if want {
			rgAdmitted(t, sub+" on "+route.path+" (the grant read says yes)", c, b)
		} else {
			rgRefused(t, sub+" on "+route.path+" (the grant read says no)", c, b)
		}
	}
}

func TestQDSRPaymentGrantIsTheGatesOwnAnswer(t *testing.T) {
	fx := newXRoleFixture(t)
	pool := fx.env.fixturePool()
	t.Cleanup(pool.Close)
	qdsrPlantOutsideSales(t, fx.env, pool)

	// Holds Counter Till at Work or more: yes.
	qdsrAssertGrantMatchesTheGate(t, fx, testkit.SubInsideSales, true)
	qdsrAssertGrantMatchesTheGate(t, fx, testkit.SubSalesManager, true)
	// The whole row (X10): general manager reads the till, inside sales works it, the one person is yes.
	qdsrAssertGrantMatchesTheGate(t, fx, xrSubTwoGrants, true)
	// Counter Till at Read only: no.
	qdsrAssertGrantMatchesTheGate(t, fx, testkit.SubGeneralMgr, false)
	// A Quote Desk seat with no Counter Till cell: the case the read exists for. Answered, not refused.
	qdsrAssertGrantMatchesTheGate(t, fx, qdsrSubOutsideSales, false)

	// Neither surface at read: the gate's own 403 on the read itself.
	for _, sub := range []string{xrSubYardTeam, testkit.SubDriver, xrSubHR} {
		code, body := qdsrGet(t, fx, sub, "/api/v1/pos/payment-grant")
		rgRefused(t, sub+" is held off the grant read", code, body)
	}
	// And the unauthenticated caller is refused by the gate family too.
	code, body := fx.probe(t, http.MethodGet, "/api/v1/pos/payment-grant", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("no credential answers %d %s, want 401", code, body)
	}
}

// TestQDSRPaymentGrantFollowsTheDealersConfiguration is the no-code-change leg: a configuration
// that lowers inside sales to Read on the Counter Till flips both the gate and the read.
func TestQDSRPaymentGrantFollowsTheDealersConfiguration(t *testing.T) {
	env := newCompEnv(t)
	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("decode the default document: %v", err)
	}
	changed := false
	for i := range doc.Cells {
		if doc.Cells[i].Role == "inside_sales" && doc.Cells[i].Surface == "counter-till" {
			doc.Cells[i].Level = roleconfig.LevelRead
			changed = true
		}
	}
	if !changed {
		t.Fatal("the default document lacks the cell the leg edits")
	}
	cfg, err := roleconfig.Validate(&doc)
	if err != nil {
		t.Fatalf("Validate on the modified document: %v", err)
	}
	rec := &recordingRegistrar{inner: http.NewServeMux()}
	graph, err := registerRoutesWithRoleConfig(rec, env.getenv, cfg)
	if err != nil {
		t.Fatalf("registerRoutesWithRoleConfig: %v", err)
	}
	t.Cleanup(graph.pool.Close)
	fx := &xrFixture{env: env, reg: rec, registered: rec.patterns}

	qdsrAssertGrantMatchesTheGate(t, fx, testkit.SubInsideSales, false)
	qdsrAssertGrantMatchesTheGate(t, fx, testkit.SubSalesManager, true)
}
