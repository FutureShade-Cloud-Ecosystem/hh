package main

// The low margin report's gate over the served surface: the managers who clear a floor and the roles
// above them read it, a seller does not, and a role off the cost and margin list is refused whatever
// floor list it is on (a margin beside a price is the cost in another form, row 343). Both legs run the
// real registerRoutes over the composition harness, as the role gate's other composition tests do.

import (
	"encoding/json"
	"net/http"
	"testing"

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
