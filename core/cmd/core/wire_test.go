package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/shims"
	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/pricing"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestServeSetRegistration_NoDuplicatePatterns is the operations batch's
// half of the S-3 backstop, made permanent: the served mux is the identity
// shims plus the money spine plus the operations cluster, and this test
// registers all three over one ServeMux with DB-free constructions. The
// ServeMux duplicate-registration panic is the backstop that makes
// "remove the shim in the same change that registers the real route"
// self-enforcing, and a registration that completes is the proof the
// served set holds no collision: nothing this batch registers duplicates
// a shim pattern or a sibling module's pattern.
//
// The route counts it pins: shims.RegisterRoutes serves the tenant set,
// the sync pair and the capability route (six patterns); the money spine
// and the operations cluster register their own patterns beside them.
// The modules' repositories hold a nil pool here: registration touches no
// table, so the pattern table is provable without a database.
func TestServeSetRegistration_NoDuplicatePatterns(t *testing.T) {
	sg := testkit.NewStaffGuards(t)
	mux := http.NewServeMux()
	rec := shims.NewRecorder(16)
	shims.RegisterRoutes(mux, sg.Guards, rec)
	spine := wireMoney(mux, nil, sg.Guards, nil, "", mustRoleConfigForWire())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ops := wireOperations(mux, nil, sg.Guards, log, spine, "test-ticket-key", nil)
	if spine == nil {
		t.Fatal("wireMoney returned no spine")
	}
	// The tops modules join the served set (the fold): one belt over a nil
	// pool, the contact source dark, every registrar registering beside
	// the shims and the two earlier clusters with no duplicate pattern.
	belt := buildSmsBelt(nil, log)
	tops := wireTops(mux, nil, sg.Guards, log, spine, ops, config.Load(), mustRoleConfigForWire(), belt)
	if tops == nil {
		t.Fatal("wireTops returned no spine")
	}
	// The rail's surface registers beside the shims; a spot check that
	// the decide route answers through the guard (401 for an anonymous
	// caller) proves the registration is live, not merely unpanicked.
	rr := muxHandler(mux, http.MethodPost, "/api/v1/staff/approvals/00000000-0000-0000-0000-000000000000/decide")
	if rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("rail decide route = %d for an anonymous caller, want the gate's refusal", rr.Code)
	}
	// One shim from each remaining family still serves: the tenant set,
	// the sync pair and the capability route belong to Phase D and the
	// tops batch, and nothing in this batch may have displaced them.
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/tenant/me"); rr.Code == http.StatusNotFound {
		t.Fatal("the tenant shim set no longer serves GET /api/v1/tenant/me")
	}
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/sync/anything"); rr.Code == http.StatusNotFound {
		t.Fatal("the sync shim pair no longer serves GET /api/v1/sync/{scope}")
	}
	// The tops batch's behavioral spot checks: a branch route answers the
	// gate, a billing register route refuses an anonymous caller, and the
	// rail's decide route still answers through the guard.
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/branches"); rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("branches route = %d for an anonymous caller, want the gate's refusal", rr.Code)
	}
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/ar/landing"); rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("AR landing route = %d for an anonymous caller, want the gate's refusal", rr.Code)
	}
	if rr := muxHandler(mux, http.MethodGet, "/api/v1/tenant/addresses"); rr.Code != http.StatusUnauthorized && rr.Code != http.StatusForbidden {
		t.Fatalf("portal address route = %d for an anonymous caller, want the tenant gate's refusal", rr.Code)
	}
}

// muxHandler drives one request through the mux for the spot checks.
func muxHandler(mux *http.ServeMux, method, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	return rr
}

// mustRoleConfigForWire loads the decided default for the wiring tests'
// wireMoney calls, whose refusal stops the test the way it stops the core.
func mustRoleConfigForWire() *roleconfig.Config {
	cfg, err := roleconfig.Default()
	if err != nil {
		panic("wire test: role configuration refused: " + err.Error())
	}
	return cfg
}

// TestW5COverrideAuthority_ManagerScopeIsTheConfiguredList pins the
// pricing override adapter's manager scope arm to the dealer's configured
// floor clearers (D6, the QD-1 review's P2-5): the arm once cleared for
// ANY manager that reached the branch, the Yard manager included, where
// the configuration names the Sales manager and the Branch manager alone.
// The approvals arm is pinned the other way: an APPROVED margin card with
// a subject of its own must not grant a generic resolve, because the
// adapter carries no subject and an empty subject match is exactly the
// one-card-clears-everything hole (D3).
func TestW5COverrideAuthority_ManagerScopeIsTheConfiguredList(t *testing.T) {
	pool := wiringPool(t)
	ctx := context.Background()
	// A real branch row: the planted approval card carries an FK to it.
	var branch string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE loc_type = 'BRANCH' LIMIT 1`).Scan(&branch); err != nil {
		t.Fatalf("read a branch: %v", err)
	}
	userID := plantW5CProbeUser(t, pool, branch)

	auth := w5cOverrideAuthority{roles: mustRoleConfigForWire()}
	as := func(role string, tier identity.Tier) context.Context {
		return identity.ContextWithStaff(ctx, identity.StaffContext{
			UserID: userID, RoleCode: role, Tier: tier, Status: identity.StatusActive,
			Branches: map[string]bool{branch: true},
		})
	}

	// The configured clearers grant on their own scope.
	for _, tc := range []struct {
		role string
		tier identity.Tier
	}{
		{"SALES_MANAGER", identity.TierLeadership},
		{"BRANCH_MANAGER", identity.TierManager},
	} {
		g, err := auth.MarginOverrideAuthority(as(tc.role, tc.tier), userID, branch)
		if err != nil {
			t.Fatalf("%s: %v", tc.role, err)
		}
		if !g.Granted || g.Source != pricing.AuthorityManagerScope {
			t.Errorf("%s is on the configured floor clearers list and reaches the branch: want MANAGER_SCOPE, got %+v", tc.role, g)
		}
	}

	// The Yard manager: a manager under the old IsManager reading, but not
	// on the configuration's list. No grant.
	g, err := auth.MarginOverrideAuthority(as("YARD_MANAGER", identity.TierManager), userID, branch)
	if err != nil {
		t.Fatalf("yard manager: %v", err)
	}
	if g.Granted {
		t.Errorf("the Yard manager is not on the configured floor clearers list, yet the adapter granted %+v", g)
	}

	// An APPROVED margin card for another subject must not grant: the
	// adapter has no subject to check, so no approval arm may grant at all.
	if _, err := pool.Exec(ctx, `
		INSERT INTO permission_approval_requests (id, requested_by, branch_id, policy_type, detail, subject_ref, data, status)
		VALUES ('5c2a0000-0000-4000-8000-000000000001', $1, $2, 'MARGIN_BELOW_MINIMUM',
		        'w5c probe: an approval bound to another quote', $3, '{}', 'APPROVED')`,
		userID, branch, "11111111-1111-4111-8111-111111111111"); err != nil {
		t.Fatalf("plant the approved card: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM permission_approval_requests WHERE id = '5c2a0000-0000-4000-8000-000000000001'`)
		_, _ = pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})
	if g, err := auth.MarginOverrideAuthority(as("INSIDE_SALES", identity.TierFrontline), userID, branch); err != nil || g.Granted {
		t.Errorf("an approved margin card for another subject must not grant a generic resolve, got %+v err %v", g, err)
	}
}

// plantW5CProbeUser plants the caller the adapter test resolves as.
func plantW5CProbeUser(t *testing.T, pool *pgxpool.Pool, branch string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO users (email, display_name, platform_user_id, status)
		VALUES ('w5c-probe@wire.invalid', 'W5C Probe', 'w5c-probe-sub', 'ACTIVE')
		ON CONFLICT (email) DO UPDATE SET display_name = EXCLUDED.display_name
		RETURNING id::text`).Scan(&id); err != nil {
		t.Fatalf("plant the probe user: %v", err)
	}
	return id
}
