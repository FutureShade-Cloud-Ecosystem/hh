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

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	qdsrSubOutsideSales  = "fxstaff-outside-sales-qdsr"
	qdsrUserOutsideSales = "3ef10000-0000-4000-8000-000000001411"
	qdsrGrantOutside     = "3ef10000-0000-4000-8000-000000001421"
)

// qdsrPlantRole plants one staff principal holding one role: a users row, the grant, the branch
// grant for a branch role and the directory membership. Idempotent and never cleaned, the matrix
// fixtures' own discipline.
func qdsrPlantRole(t *testing.T, env *compEnv, pool *pgxpool.Pool, sub, userID, grantID, role string, branchRole bool) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email, display_name, status, platform_user_id, org_id)
		VALUES ($1, $2, $3, 'ACTIVE', $4, $5) ON CONFLICT (id) DO NOTHING`,
		userID, sub+"@fixture.test", "Fixture "+role+" QDSR", sub, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant %s user: %v", role, err)
	}
	var branch any
	if branchRole {
		branch = testkit.BranchNorthAID
	}
	if _, err := pool.Exec(ctx, `INSERT INTO staff_role_grant (id, platform_user_id, role_id, org_id, branch_id)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT (id) DO NOTHING`,
		grantID, sub, role, testkit.OrgNorthID, branch); err != nil {
		t.Fatalf("plant %s grant: %v", role, err)
	}
	if branchRole {
		if _, err := pool.Exec(ctx, `INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES ($1, $2, $3)
			ON CONFLICT (user_id, branch_id) DO NOTHING`, userID, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
			t.Fatalf("plant %s branch grant: %v", role, err)
		}
	}
	env.appwrite.setTeam(sub, "org-"+testkit.OrgNorthSlug)
}

// qdsrPlantOutsideSales plants the one role the matrix fixtures lack: outside sales, a Quote Desk
// seat with no Counter Till cell, the person the grant read exists for.
func qdsrPlantOutsideSales(t *testing.T, env *compEnv, pool *pgxpool.Pool) {
	t.Helper()
	qdsrPlantRole(t, env, pool, qdsrSubOutsideSales, qdsrUserOutsideSales, qdsrGrantOutside, "outside_sales", true)
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
	qdsrPlantRole(t, fx.env, pool, "fxstaff-yard-manager-qdsr", "3ef10000-0000-4000-8000-000000001412", "3ef10000-0000-4000-8000-000000001422", "yard_manager", true)

	// Holds Counter Till at Work or more: yes.
	qdsrAssertGrantMatchesTheGate(t, fx, testkit.SubInsideSales, true)
	qdsrAssertGrantMatchesTheGate(t, fx, testkit.SubSalesManager, true)
	// The whole row (X10): general manager reads the till, inside sales works it, the one person is yes.
	qdsrAssertGrantMatchesTheGate(t, fx, xrSubTwoGrants, true)
	// Counter Till at Read only: no.
	qdsrAssertGrantMatchesTheGate(t, fx, testkit.SubGeneralMgr, false)
	// A Quote Desk seat with no Counter Till cell: the case the read exists for. Answered, not refused.
	qdsrAssertGrantMatchesTheGate(t, fx, qdsrSubOutsideSales, false)

	// Neither surface at read: the gate's own 403 on the read itself. The yard manager joins
	// this group with decision log row 1156, which removed its Counter Till and Quote Desk cells.
	for _, sub := range []string{xrSubYardTeam, testkit.SubDriver, xrSubHR, "fxstaff-yard-manager-qdsr"} {
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
// TestQDSRPaymentGrantEqualsWhatTheDoorDoes holds the flag, per role, to the door: the gate admits
// the till sale and the payment door (probed on the real routes), AND the door's own not-selling-role
// rule (identity.CanConvertAndPay, the one predicate the door itself calls) would let the caller
// convert and pay. The platform roles dealer_admin and fb_operator pass every gate level and are
// refused the convert by the door, so the flag must say false for them; the integration-tag file
// qdsr_orderpay_fulfilment_integration_test.go proves the door's own answer for every role.
func TestQDSRPaymentGrantEqualsWhatTheDoorDoes(t *testing.T) {
	fx := newXRoleFixture(t)
	pool := fx.env.fixturePool()
	t.Cleanup(pool.Close)
	type fixtureRole struct{ sub, role string }
	// answered: the roles whose Counter Till or Quote Desk cell at read admits the grant read
	// itself, so the flag is the question they came to ask. refused: the yard manager, whose
	// two cells decision log row 1156 removed, meets the gate's 403 on the read before the
	// question is reached, exactly as the roles with neither surface always have.
	answered := []fixtureRole{
		{testkit.SubInsideSales, "inside_sales"},
		{testkit.SubSalesManager, "sales_manager"},
		{testkit.SubGeneralMgr, "general_manager"},
		{xrSubBranchManager, "branch_manager"},
		{qdsrSubOutsideSales, "outside_sales"},
		{"fxstaff-dealer-admin-qdsr", "dealer_admin"},
		{"fxstaff-fb-operator-qdsr", "fb_operator"},
	}
	refused := []fixtureRole{{"fxstaff-yard-manager-qdsr", "yard_manager"}}
	qdsrPlantOutsideSales(t, fx.env, pool)
	qdsrPlantRole(t, fx.env, pool, "fxstaff-yard-manager-qdsr", "3ef10000-0000-4000-8000-000000001412", "3ef10000-0000-4000-8000-000000001422", "yard_manager", true)
	qdsrPlantRole(t, fx.env, pool, "fxstaff-dealer-admin-qdsr", "3ef10000-0000-4000-8000-000000001413", "3ef10000-0000-4000-8000-000000001423", "dealer_admin", false)
	qdsrPlantRole(t, fx.env, pool, "fxstaff-fb-operator-qdsr", "3ef10000-0000-4000-8000-000000001414", "3ef10000-0000-4000-8000-000000001424", "fb_operator", false)
	for _, r := range refused {
		for _, route := range []struct{ method, path string }{
			{http.MethodPost, "/api/v1/pos/sales"},
			{http.MethodPost, "/api/v1/quotes/{id}/order-payment"},
		} {
			c, b := rgProbe(t, fx, route.method, route.path, rgHeaders(t, fx, r.sub))
			rgRefused(t, r.role+" on "+route.path, c, b)
		}
		code, body := qdsrGet(t, fx, r.sub, "/api/v1/pos/payment-grant")
		rgRefused(t, r.role+" on the grant read", code, body)
	}
	for _, r := range answered {
		code, _ := identity.ERPCodeFor(r.role)
		door := identity.CanConvertAndPay(identity.StaffContext{RoleCode: code, Status: identity.StatusActive})
		admitted := true
		for _, route := range []struct{ method, path string }{
			{http.MethodPost, "/api/v1/pos/sales"},
			{http.MethodPost, "/api/v1/quotes/{id}/order-payment"},
		} {
			c, b := rgProbe(t, fx, route.method, route.path, rgHeaders(t, fx, r.sub))
			if c == http.StatusForbidden && b == string(authz.BodyForbidden) {
				admitted = false
			}
		}
		code2, body, got := qdsrGrantAnswer(t, fx, r.sub)
		if code2 != http.StatusOK {
			t.Fatalf("%s: the grant read answers %d %s", r.role, code2, body)
		}
		if want := admitted && door; got != want {
			t.Fatalf("%s: can_take_payment %v, want %v (gate admits both routes %v, door's selling rule %v)", r.role, got, want, admitted, door)
		}
	}
	for _, role := range []string{"dealer_admin", "fb_operator"} {
		for _, r := range answered {
			if r.role == role {
				if _, _, got := qdsrGrantAnswer(t, fx, r.sub); got {
					t.Fatalf("%s passes every gate level but the door refuses its convert, the flag must be false", role)
				}
			}
		}
	}
	if _, _, got := qdsrGrantAnswer(t, fx, testkit.SubInsideSales); !got {
		t.Fatal("inside sales can take payment")
	}
}

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
