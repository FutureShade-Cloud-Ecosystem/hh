package main

// The low margin report's gate over the served surface: the managers who clear a floor and the roles
// above them read it, a seller does not, and a role off the cost and margin list is refused whatever
// floor list it is on (a margin beside a price is the cost in another form, row 343). Both legs run the
// real registerRoutes over the composition harness, as the role gate's other composition tests do.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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
	// The North A documents are this test's own (the shared fixtures are tidied by other legs): one
	// priced quote at North A, removed again after.
	quoteNo := fmt.Sprintf("LMW-A-%d", time.Now().UnixNano())
	const quoteID = "0d4a0000-0000-4000-9000-000000000601"
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO quotes (id, quote_no, customer_id, branch_id, status, org_id) VALUES ($1, $2, $3, $4, 'SENT', $5)`,
		quoteID, quoteNo, testkit.TenantAlphaID, testkit.BranchNorthAID, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant the North A quote: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM quotes WHERE id = $1`, quoteID) })
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO quote_lines (quote_id, line_no, product_id, description, qty_entered, uom_entered, qty_base, unit_price_cents, price_uom, line_total_cents, org_id)
		VALUES ($1, 1, $2, 'Wall line', 10, 'EA', 10, 1, 'EA', 10, $3)`,
		quoteID, testkit.ProductID, testkit.OrgNorthID); err != nil {
		t.Fatalf("plant the North A quote line: %v", err)
	}
	// A busy gate database carries many documents, so the walk follows the cursor to the end rather
	// than trust the first page to hold the fixtures.
	type result struct {
		code int
		body string
		docs map[string]bool
	}
	read := func(path string, headers map[string]string) result {
		t.Helper()
		out := result{docs: map[string]bool{}}
		cursor := ""
		for i := 0; i < 200; i++ {
			sep := "?"
			if strings.Contains(path, "?") {
				sep = "&"
			}
			url := path + sep + "limit=200"
			if cursor != "" {
				url += "&cursor=" + cursor
			}
			code, body := rgProbe(t, fx, http.MethodGet, url, headers)
			out.code, out.body = code, body
			if code != http.StatusOK {
				return out
			}
			var page struct {
				Items []struct {
					DocNo string `json:"doc_no"`
				} `json:"items"`
				NextCursor *string `json:"next_cursor"`
			}
			if err := json.Unmarshal([]byte(body), &page); err != nil {
				t.Fatalf("decode: %v: %.300s", err, body)
			}
			for _, it := range page.Items {
				out.docs[it.DocNo] = true
			}
			if page.NextCursor == nil {
				return out
			}
			cursor = *page.NextCursor
		}
		t.Fatal("the report's cursor never ended")
		return out
	}
	const trenton = "5d1b3f6e-0000-4000-9000-000000000001"

	scoped := rgHeaders(t, fx, xrSubBranchManager)
	got := read(lowMarginReportPath, scoped).docs
	if !got[quoteNo] {
		t.Errorf("the North A Branch manager must see North A's quote %s: %v", quoteNo, got)
	}
	if got["#SO-DPA3-CRIT"] || got["#SO-DPA3-WARN"] {
		t.Errorf("the North A Branch manager sees Trenton's orders: %v", got)
	}
	if r := read(lowMarginReportPath+"?branch_id="+trenton, scoped); r.code != http.StatusForbidden || r.body == string(authz.BodyForbidden) {
		t.Errorf("a branch outside the grant: %d %.200s, want a 403 that says why", r.code, r.body)
	}
	got = read(lowMarginReportPath+"?branch_id="+testkit.BranchNorthAID, scoped).docs
	if !got[quoteNo] {
		t.Errorf("the granted branch named: %v", got)
	}
	// The Sales manager is branch scoped too.
	got = read(lowMarginReportPath, rgHeaders(t, fx, testkit.SubSalesManager)).docs
	if got["#SO-DPA3-CRIT"] {
		t.Errorf("the North A Sales manager sees Trenton's orders: %v", got)
	}
	// A global caller reads across the branches.
	got = read(lowMarginReportPath, rgHeaders(t, fx, testkit.SubGeneralMgr)).docs
	if !got[quoteNo] || !got["#SO-DPA3-CRIT"] {
		t.Errorf("the General Manager reads every branch: %v", got)
	}
}
