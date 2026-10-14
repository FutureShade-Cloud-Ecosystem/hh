//go:build demo

package main

// demoseed_managers_work_test.go: world proof for the managers' work items seeded per branch
// (Gable-Addresses-Seed Part B). The demo world plants:
//   - one PENDING MARGIN_BELOW_MINIMUM floor request per branch,
//   - one partial purchase order per branch so receiving differences exist everywhere,
//   - one bounced payment per branch.
//
// The test builds the full demo pipeline, then signs in as the personas the landings are for and
// reads the same surfaces the product's Home tiles draw from: the approvals list route for the
// Sales Manager, the dashboard Needs-you queue for the Yard Manager, and the bounced payments
// route for Payables and Receivables.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/customerquote"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/dashboard"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const (
	// qwPLMYardManagerUserID is the demo populate's Yard Manager account, granted at Trenton/Point Loma.
	qwPLMYardManagerUserID = "de110000-0000-4000-9000-000000000003"
	// qwJULManagerSubject is a test-only Sales Manager planted at the Kingston (JUL) branch for the
	// branch-scoped approvals leg. It is not one of the demo populate's fixed accounts: it exists only
	// inside this proof, so the test does not rely on a demo login reaching a branch it was never
	// granted.
	qwJULManagerSubject = "hh-demo-managers-work-proof-jul-sales-manager"
	// qwJULYardManagerSubject is a test-only Yard Manager planted at Kingston for the receiving
	// differences leg.
	qwJULYardManagerSubject = "hh-demo-managers-work-proof-jul-yard-manager"
)

func TestDemoWorld_ManagersWorkItemsPerBranch(t *testing.T) {
	dsn := os.Getenv(tillDemoDBEnvVar)
	if dsn == "" {
		t.Skip(tillDemoDBEnvVar + " is not set: the demo world proof needs its own throwaway local Postgres")
	}
	requireLocalDSN(t, dsn)
	ctx := context.Background()

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		t.Fatalf("generate vault key: %v", err)
	}
	pool, testDSN := qwFreshDB(t, ctx, dsn, "demoworld_managers_work")
	t.Setenv("DATABASE_URL", testDSN)
	t.Setenv("CORE_DATABASE_URL", testDSN)
	t.Setenv("PAYMENT_VAULT_KEY", hex.EncodeToString(rawKey))
	t.Setenv("APP_ENV", "dev")
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the first apply created nothing on a fresh database")
	}
	now := qwClock
	if _, err := demo.RunPopulate(ctx, demo.Options{Getenv: os.Getenv, Now: &now}); err != nil {
		t.Fatalf("demo populate: %v", err)
	}
	if _, created := qwApply(t); created == 0 {
		t.Fatal("the second apply created nothing, the manager work items never landed")
	}

	var orgID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM org WHERE slug = $1`, demoOrgSlug).Scan(&orgID); err != nil {
		t.Fatalf("the demo org: %v", err)
	}

	// Plant the test-only JUL personas: a Sales Manager for the approvals leg and a Yard Manager for
	// the receiving-differences leg. Both are scoped to the Kingston branch only.
	const kingstonBranchID = "5d1b3f6e-0000-4000-9000-000000000002"
	for _, p := range []struct {
		email, subject, role string
	}{
		{"jul.sales.manager@hh-demo.invalid", qwJULManagerSubject, "sales_manager"},
		{"jul.yard.manager@hh-demo.invalid", qwJULYardManagerSubject, "yard_manager"},
	} {
		if _, err := pool.Exec(ctx, `
			WITH u AS (
			  INSERT INTO users (email, display_name, status, org_id, platform_user_id)
			  VALUES ($1, $2, 'ACTIVE', $3, $4) RETURNING id)
			INSERT INTO user_branch_grants (user_id, branch_id, org_id) SELECT id, $5, $3 FROM u`,
			p.email, p.subject+" (work proof)", orgID, p.subject, kingstonBranchID); err != nil {
			t.Fatalf("plant the JUL %s login: %v", p.role, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO staff_role_grant (platform_user_id, role_id, org_id, branch_id) VALUES ($1, $2, $3, $4)`,
			p.subject, p.role, orgID, kingstonBranchID); err != nil {
			t.Fatalf("plant the JUL %s grant: %v", p.role, err)
		}
	}

	appwrite := newAppwriteFake(t)
	for _, sub := range []string{qwSalesUserID, qwManagerUserID, qwPLMYardManagerUserID, qwJULManagerSubject, qwJULYardManagerSubject, prPayRecSubject} {
		appwrite.setTeam(sub, "org-"+demoOrgSlug)
	}
	keys := testkit.NewKeys(t)
	acceptKey := make([]byte, 32)
	if _, err := rand.Read(acceptKey); err != nil {
		t.Fatalf("generate acceptance key: %v", err)
	}
	env := &compEnv{
		keys: keys, minter: &testkit.Minter{Keys: keys, Clock: testkit.NewClock()},
		keySrv: testkit.NewKeyServer(t, keys), appwrite: appwrite, dsn: testDSN,
	}
	values := map[string]string{
		"CORE_TOKEN_ISSUER": testkit.KitIssuer, "CORE_STAFF_AUDIENCE": testkit.KitStaffAud,
		"CORE_TENANT_AUDIENCE": testkit.KitTenantAud, "CORE_JWKS_URL": env.keySrv.URL(),
		"CORE_DATABASE_URL": testDSN, "CORE_APPWRITE_ENDPOINT": appwrite.server.URL,
		"CORE_APPWRITE_PROJECT": "fx-appwrite-project", "CORE_APPWRITE_API_KEY": "fx-appwrite-key-placeholder",
		customerquote.EnvKeyID: "fx-accept-sign-key", customerquote.EnvKey: hex.EncodeToString(acceptKey),
	}
	consoleSessionValues(t, env.minter, values)
	env.getenv = func(k string) string { return values[k] }
	rec, _ := registeredSurface(t, env)

	plmManager := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwManagerUserID), who: "PLM Sales manager"}
	julManager := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwJULManagerSubject), who: "JUL Sales manager"}
	plmYardManager := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwPLMYardManagerUserID), who: "PLM Yard manager"}
	julYardManager := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, qwJULYardManagerSubject), who: "JUL Yard manager"}
	payRec := &tillClient{t: t, mux: rec.inner, token: env.minter.StaffToken(t, prPayRecSubject), who: "Payables and Receivables"}

	fr1 := demoID("floorrequest:FR1")
	fr2 := demoID("floorrequest:FR2")

	// 1. One PENDING MARGIN_BELOW_MINIMUM request exists per branch.
	var pendingCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM permission_approval_requests
		 WHERE policy_type = 'MARGIN_BELOW_MINIMUM' AND status = 'PENDING'`).Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 2 {
		t.Fatalf("pending MARGIN_BELOW_MINIMUM requests: %d, want 2 (one per branch)", pendingCount)
	}

	// 2. The PLM Sales Manager's approvals list shows FR1 (PLM) and never FR2 (JUL).
	plmList := plmManager.call("GET", "/api/v1/staff/approvals?kind=MARGIN_BELOW_MINIMUM&status=PENDING&limit=200", nil)
	plmList.ok(t, http.StatusOK)
	if !strings.Contains(string(plmList.body), fr1) {
		t.Errorf("the PLM Sales Manager's approvals list lacks FR1: %s", plmList.body)
	}
	if strings.Contains(string(plmList.body), fr2) {
		t.Errorf("the PLM Sales Manager's approvals list shows FR2, which is a JUL request: %s", plmList.body)
	}

	// 3. The JUL Sales Manager's approvals list shows FR2 (JUL) and never FR1 (PLM).
	julList := julManager.call("GET", "/api/v1/staff/approvals?kind=MARGIN_BELOW_MINIMUM&status=PENDING&limit=200", nil)
	julList.ok(t, http.StatusOK)
	if !strings.Contains(string(julList.body), fr2) {
		t.Errorf("the JUL Sales Manager's approvals list lacks FR2: %s", julList.body)
	}
	if strings.Contains(string(julList.body), fr1) {
		t.Errorf("the JUL Sales Manager's approvals list shows FR1, which is a PLM request: %s", julList.body)
	}

	// 4. Receiving differences exist per branch and each branch's Yard Manager sees only its own.
	var receivingDiffCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM purchasing_pos
		 WHERE status = 'PARTIAL'
		   AND EXISTS (SELECT 1 FROM purchasing_po_lines pl
		                WHERE pl.po_id = purchasing_pos.id AND pl.qty_ordered_base > pl.qty_received_base)`).Scan(&receivingDiffCount); err != nil {
		t.Fatal(err)
	}
	if receivingDiffCount != 2 {
		t.Fatalf("partial purchase orders with a short line: %d, want 2 (one per branch)", receivingDiffCount)
	}

	needsRow := func(c *tillClient, sourceKey string) (count int, found bool) {
		t.Helper()
		rsp := c.call("GET", "/api/v1/dashboard/needs", nil)
		rsp.ok(t, http.StatusOK)
		var q dashboard.NeedsQueue
		if err := json.Unmarshal(rsp.body, &q); err != nil {
			t.Fatalf("decode needs queue: %v", err)
		}
		for _, r := range q.Rows {
			if r.SourceKey == sourceKey {
				return r.Count, true
			}
		}
		return 0, false
	}

	if n, ok := needsRow(plmYardManager, dashboard.NeedsReceivingDifferences); !ok || n != 1 {
		t.Errorf("PLM Yard Manager receiving-differences row: count=%d found=%v, want 1", n, ok)
	}
	if n, ok := needsRow(julYardManager, dashboard.NeedsReceivingDifferences); !ok || n != 1 {
		t.Errorf("JUL Yard Manager receiving-differences row: count=%d found=%v, want 1", n, ok)
	}

	// 5. Bounced payments exist per branch and the global Payables and Receivables persona sees both.
	var bounceCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE status = 'BOUNCED'`).Scan(&bounceCount); err != nil {
		t.Fatal(err)
	}
	if bounceCount != 2 {
		t.Fatalf("bounced payments: %d, want 2 (one per branch)", bounceCount)
	}

	bounced := payRec.call("GET", "/api/v1/ar/bounced", nil)
	bounced.ok(t, http.StatusOK)
	body := string(bounced.body)
	if !strings.Contains(body, "DEMO-PAY-B01") {
		t.Errorf("the bounced queue lacks B01: %s", body)
	}
	if !strings.Contains(body, "DEMO-PAY-B02") {
		t.Errorf("the bounced queue lacks B02: %s", body)
	}
}
