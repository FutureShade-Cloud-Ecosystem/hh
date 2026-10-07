package main

// The low margin report's gate over the served surface: the managers who clear a floor and the roles
// above them read it, a seller does not, and a role off the cost and margin list is refused whatever
// floor list it is on (a margin beside a price is the cost in another form, row 343). Both legs run the
// real registerRoutes over the composition harness, as the role gate's other composition tests do.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/roleconfig"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

const lowMarginReportPath = "/api/v1/pricing/reports/low-margin/scan"

func TestLowMarginReportGateAtTheCounter(t *testing.T) {
	fx := newXRoleFixture(t)
	for _, c := range []struct {
		name string
		sub  string
		ok   bool
	}{
		{"the Sales manager", testkit.SubSalesManager, true},
		{"the branch manager", xrSubBranchManager, true},
		{"the Financial Controller", xrSubFinancialController, true},
		{"the General Manager", testkit.SubGeneralMgr, true},
		{"inside sales", testkit.SubInsideSales, false},
		{"the yard team", xrSubYardTeam, false},
		{"hr", xrSubHR, false},
	} {
		code, body := rgProbe(t, fx, http.MethodGet, lowMarginReportPath, rgHeaders(t, fx, c.sub))
		if c.ok {
			rgAdmitted(t, c.name+" reads the low margin report", code, body)
		} else {
			rgRefused(t, c.name+" is held off the low margin report", code, body)
		}
	}
}

// Cost visibility honoured: with the Sales manager taken off the cost and margin list, the Sales
// manager is refused the report (and still clears floors); the branch manager, still on the list, reads it.
func TestLowMarginReportGateHonoursTheCostVisibilityList(t *testing.T) {
	env := newCompEnv(t)
	var doc roleconfig.Document
	if err := json.Unmarshal(roleconfig.DefaultData(), &doc); err != nil {
		t.Fatalf("decode the default document: %v", err)
	}
	var vis []string
	if err := json.Unmarshal(doc.Settings[roleconfig.SettingCostVisibility], &vis); err != nil {
		t.Fatalf("decode the cost visibility list: %v", err)
	}
	var kept []string
	for _, r := range vis {
		if r != "sales_manager" {
			kept = append(kept, r)
		}
	}
	raw, err := json.Marshal(kept)
	if err != nil {
		t.Fatal(err)
	}
	doc.Settings[roleconfig.SettingCostVisibility] = raw
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
	plantMatrixStaff(t, env, graph.pool)
	fx := &xrFixture{env: env, reg: rec, registered: rec.patterns}

	code, body := rgProbe(t, fx, http.MethodGet, lowMarginReportPath, rgHeaders(t, fx, testkit.SubSalesManager))
	rgRefused(t, "a Sales manager off the cost list is refused the report", code, body)
	code, body = rgProbe(t, fx, http.MethodGet, lowMarginReportPath, rgHeaders(t, fx, xrSubBranchManager))
	rgAdmitted(t, "the branch manager, still on the cost list, reads the report", code, body)
}

// The branch wall through the served route: a Branch manager granted North A reads the documents of
// North A and nothing at Trenton (the seeded orders live there), with no parameters, as with any; a
// branch it is not granted answers 403 in words; a global caller reads across. Real tokens, the real
// role gate and the real report query.
func TestLowMarginReportBranchWallAtTheCounter(t *testing.T) {
	fx := newXRoleFixture(t)
	// A branch scoped caller's reach is its user_branch_grants rows; the planted Branch manager holds
	// the role grant alone, so give it the North A branch grant for this test and take it away after.
	pool, err := pgxpool.New(context.Background(), os.Getenv(coreDBEnvVar))
	if err != nil {
		t.Fatalf("open the gate database: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO user_branch_grants (user_id, branch_id, org_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`,
		xrUserBranchManagerID, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
		t.Fatalf("grant the Branch manager North A: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM user_branch_grants WHERE user_id = $1`, xrUserBranchManagerID)
	})
	docs := func(code int, body string) map[string]bool {
		t.Helper()
		if code != http.StatusOK {
			t.Fatalf("status %d: %s", code, body)
		}
		var page struct {
			Items []struct {
				DocNo    string `json:"doc_no"`
				BranchID string `json:"branch_id"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("decode: %v: %s", err, body)
		}
		out := map[string]bool{}
		for _, it := range page.Items {
			out[it.DocNo] = true
		}
		return out
	}
	const trenton = "5d1b3f6e-0000-4000-9000-000000000001"

	scoped := rgHeaders(t, fx, xrSubBranchManager)
	got := docs(rgProbe(t, fx, http.MethodGet, lowMarginReportPath, scoped))
	if !got["FXQTE-A-1"] || !got["FXQTE-B-1"] {
		t.Errorf("the North A Branch manager must see North A's documents: %v", got)
	}
	if got["#SO-DPA3-CRIT"] || got["#SO-DPA3-WARN"] {
		t.Errorf("the North A Branch manager sees Trenton's orders: %v", got)
	}
	code, body := rgProbe(t, fx, http.MethodGet, lowMarginReportPath+"?branch_id="+trenton, scoped)
	if code != http.StatusForbidden || body == string(authz.BodyForbidden) {
		t.Errorf("a branch outside the grant: %d %s, want a 403 that says why", code, body)
	}
	got = docs(rgProbe(t, fx, http.MethodGet, lowMarginReportPath+"?branch_id="+testkit.BranchNorthAID, scoped))
	if !got["FXQTE-A-1"] {
		t.Errorf("the granted branch named: %v", got)
	}
	// The Sales manager is branch scoped too.
	got = docs(rgProbe(t, fx, http.MethodGet, lowMarginReportPath, rgHeaders(t, fx, testkit.SubSalesManager)))
	if got["#SO-DPA3-CRIT"] {
		t.Errorf("the North A Sales manager sees Trenton's orders: %v", got)
	}
	// A global caller reads across the branches.
	got = docs(rgProbe(t, fx, http.MethodGet, lowMarginReportPath, rgHeaders(t, fx, testkit.SubGeneralMgr)))
	if !got["FXQTE-A-1"] || !got["#SO-DPA3-CRIT"] {
		t.Errorf("the General Manager reads every branch: %v", got)
	}
}
