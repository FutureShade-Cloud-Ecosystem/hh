package main

// wire_roles_test.go, the composition-level proof of the till's
// role-configuration rules (RT-B, the lead's P1-1 ruling). Two halves:
//
// The pin: the serve composition and the demo compose must carry the
// till-rules wiring, read from the wiring sources themselves (the
// viz_label_vocabulary pattern): a wiring call deleted from either site
// fails here, not at the counter.
//
// The behavior: over the composition fixture database (HH_CORE_TEST_DATABASE_URL,
// the composition suites' gate variable, skip politely when absent), the
// REAL wiring functions loadRoleConfig and wireTillRules build the pos
// service the serve role builds, and that service answers the two questions
// the rows name: the margin glance for the clerk (X3, the configuration's
// list decides) and the 300.00 refusal (X7, the configuration's amount),
// the audit trail holding the refusal row.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

// TestTillRules_WiringIsPinned reads the two composition sites and refuses
// to let the till-rules wiring drift away from them.
func TestTillRules_WiringIsPinned(t *testing.T) {
	assertContains := func(file, want string) {
		t.Helper()
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !containsStr(string(raw), want) {
			t.Fatalf("%s must wire the till rules through %q; the composition-level leg (RT-B P1-1) depends on it", file, want)
		}
	}
	assertContains("wire_tops.go", "wireTillRules(")
	assertContains("wire_tops.go", "loadRoleConfig()")
	assertContains("demoseed_compose.go", "WithRoleConfig(loadRoleConfig())")
}

func containsStr(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}

// TestTillRules_WiredCompositionRefusesAndShows drives the REAL
// composition path (registerRoutes, the one composition point the binary
// wires, through the same harness the parity and x-role suites use) over
// the fixture database, and asserts the built surface answers the rows'
// two questions: the margin glance for the clerk (X3, the configuration's
// list decides) and the 300.00 refusal (X7, the configuration's amount,
// the approver roles named in the contract's body), the audit trail
// holding the refusal row.
func TestTillRules_WiredCompositionRefusesAndShows(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)
	pool := graph.pool
	ctx := context.Background()

	// The fixture product priced at an exact 10.00 per EA at the north
	// branch, with a cost row so the glance has a margin to show, and one
	// open drawer for the cash rails. The sale and return keys are uuids
	// salted per run (the DIB-384 pattern).
	salt := fmt.Sprintf("%08x", os.Getpid())[:8]
	saleKey := "5d1be000-0000-4000-9000-" + salt + "0a01"
	returnKey := "5d1be000-0000-4000-9000-" + salt + "0a02"
	station := fmt.Sprintf("tillrules-%d", os.Getpid())
	for _, stmt := range []string{
		fmt.Sprintf(`INSERT INTO product_uoms (product_id, uom_code, qty_in_base, is_base)
			VALUES ('%s', 'EA', 1, TRUE) ON CONFLICT (product_id, uom_code) DO NOTHING`, testkit.ProductID),
		fmt.Sprintf(`INSERT INTO pricing_list_prices (product_id, list_price_cents, price_uom)
			VALUES ('%s', 1000, 'EA') ON CONFLICT (product_id) DO NOTHING`, testkit.ProductID),
		fmt.Sprintf(`INSERT INTO product_branch_costs (product_id, branch_id, avg_unit_cost_cents)
			VALUES ('%s', '%s', 600) ON CONFLICT (product_id, branch_id) DO NOTHING`,
			testkit.ProductID, testkit.BranchNorthAID),
		fmt.Sprintf(`INSERT INTO till_sessions (branch_id, station, status, opened_by, float_cents)
			SELECT '%s', '%s', 'OPEN', u.id, 100000 FROM users u WHERE u.platform_user_id = '%s'
			AND NOT EXISTS (SELECT 1 FROM till_sessions WHERE station = '%s')`,
			testkit.BranchNorthAID, station, testkit.SubInsideSales, station),
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("fixture plant: %v", err)
		}
	}
	var drawerID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM till_sessions WHERE station = $1`, station).Scan(&drawerID); err != nil {
		t.Fatalf("drawer: %v", err)
	}

	do := func(t *testing.T, method, path string, body any) (int, map[string]any) {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+env.minter.StaffToken(t, testkit.SubInsideSales))
		req.Header.Set("X-Org", testkit.OrgNorthSlug)
		rsp := httptest.NewRecorder()
		rec.inner.ServeHTTP(rsp, req)
		var out map[string]any
		_ = json.Unmarshal(rsp.Body.Bytes(), &out)
		return rsp.Code, out
	}

	// X3 at the composition: the glance answers margin for the clerk,
	// because the configuration's list names the role. The bytes went
	// through the REAL registerRoutes, the wiring the serve role runs.
	code, glance := do(t, http.MethodPost, "/api/v1/pos/margin-glance", map[string]any{
		"branch_id": testkit.BranchNorthAID, "customer_type": "RETAIL",
		"lines": []map[string]any{{"product_id": testkit.ProductID, "qty_entered": 10, "uom_entered": "EA"}},
	})
	if code != http.StatusOK {
		t.Fatalf("the wired glance must answer the clerk, got %d: %v", code, glance)
	}
	lines, isSlice := glance["lines"].([]any)
	if !isSlice || len(lines) == 0 {
		t.Fatalf("the wired glance must carry margin for the configured clerk: %v", glance)
	}
	if lines[0].(map[string]any)["margin_bps"] == nil {
		t.Fatalf("margin_bps must be present for the configured clerk: %v", lines[0])
	}

	// X7 at the composition: the 300.00 return is refused with the
	// contract's body, no configuration-free admission.
	code, sale := do(t, http.MethodPost, "/api/v1/pos/sales", map[string]any{
		"idempotency_key": saleKey,
		"branch_id":       testkit.BranchNorthAID, "till_session_id": drawerID,
		"sale_type": "COLLECT_NOW", "tender": "CASH",
		"lines": []map[string]any{{"product_id": testkit.ProductID, "qty_entered": 30, "uom_entered": "EA"}},
	})
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("ring the sale: %d %v", code, sale)
	}
	orderID, _ := sale["order_id"].(string)
	if orderID == "" {
		t.Fatalf("no order id: %v", sale)
	}
	var lineID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM order_lines WHERE order_id = $1::uuid LIMIT 1`, orderID).Scan(&lineID); err != nil {
		t.Fatalf("line: %v", err)
	}
	code, refused := do(t, http.MethodPost, "/api/v1/pos/returns", map[string]any{
		"idempotency_key": returnKey,
		"order_id":        orderID, "refund_method": "CASH", "till_session_id": drawerID,
		"lines": []map[string]any{{"order_line_id": lineID, "qty_base": 30}},
	})
	if code != http.StatusConflict {
		t.Fatalf("the wired composition must refuse the 300.00 return, got %d: %v", code, refused)
	}
	if refused["error"] != "refund_approval_required" {
		t.Fatalf("the refusal's error string wrong: %v", refused["error"])
	}
	approval, isMap := refused["approval"].(map[string]any)
	if !isMap || approval["amount_cents"] != float64(25000) {
		t.Fatalf("the refusal must name the configured 250.00: %v", refused)
	}
	approvers, isSlice := approval["approvers"].([]any)
	if !isSlice || len(approvers) != 2 || approvers[0] != "sales_manager" || approvers[1] != "branch_manager" {
		t.Fatalf("the refusal must name both approver roles in order: %v", approval)
	}

	// The trail: the refusal's own audit row, filed under the sale.
	var refusalRows int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_log WHERE action = 'REFUND_APPROVAL_REFUSED' AND entity_id = $1::uuid`,
		orderID).Scan(&refusalRows); err != nil {
		t.Fatalf("trail: %v", err)
	}
	if refusalRows == 0 {
		t.Fatal("the wired composition must write the refusal's audit row")
	}
}
