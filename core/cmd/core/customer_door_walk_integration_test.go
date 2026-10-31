//go:build integration

package main

// customer_door_walk_integration_test.go (UX-89 round 3): the walk over EVERY staff route in openapi.yaml that
// takes a customer. A route takes a customer when its path has /customers/{...}, a path or query parameter is
// named customer_id (any spelling), or its request body has a customer_id property. Each discovered route must
// be in the registry below, in one of three classes, or the walk fails with its name:
//
//	wall    the door holds the customer to the caller's reach. A branch scoped manager granted North A names an
//	        account homed at North B and an id that is no account: both must answer 404, byte for byte alike, and
//	        a shared (no home) account must not be answered "customer not found".
//	agree   the door is held by the document's branch (an order, a sale, an invoice), not by the account; the
//	        two answers must still be byte for byte alike.
//	exempt  not walkable as a scoped caller, with the reason named (a global admin door reaches every account;
//	        the receivables register is invoice branch held).
//
// A new customer door therefore cannot ship unwalled: it appears here as an unregistered route and the test
// fails until someone classifies it and, for a wall door, shows its leg.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/config"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/testkit"
)

type doorClass string

const (
	doorWall   doorClass = "wall"
	doorAgree  doorClass = "agree"
	doorExempt doorClass = "exempt"
)

type customerDoor struct {
	route  string // "METHOD /openapi/path"
	class  doorClass
	path   string // concrete path, %C% = the customer id
	body   string // request body, %C% = the customer id
	reason string // exempt and agree: why the door is not a plain wall
	words  string // wall only: the 404 body when it is not the seam's own
}

const (
	walkUUID1 = "3ef10000-0000-4000-8000-0000000000c1"
	walkUUID2 = "3ef10000-0000-4000-8000-0000000000c2"
)

func walkDoors() []customerDoor {
	A := testkit.BranchNorthAID
	prod := testkit.ProductID
	line := `{"product_id":"` + prod + `","qty_entered":1,"uom_entered":"EA"}`
	return []customerDoor{
		{route: "GET /api/v1/ar/customers/{customerID}/ap-contact", class: doorWall, path: "/api/v1/ar/customers/%C%/ap-contact"},
		{route: "PUT /api/v1/ar/customers/{customerID}/ap-contact", class: doorWall, path: "/api/v1/ar/customers/%C%/ap-contact", body: `{"contact_name":"Walk","email":"walk@example.test"}`},
		{route: "DELETE /api/v1/ar/customers/{customerID}/ap-contact", class: doorWall, path: "/api/v1/ar/customers/%C%/ap-contact"},
		// W3 FC-1b: the waiver is the Financial Controller's act on the account, not on a branch's books.
		// RequireAdmin is the leadership tier AND a global scope (a branch scoped caller is refused at the
		// gate), the handler narrows it to the Controller roles, and the waiver service refuses every role
		// but the Financial Controller, whose scope is global, so no caller the doors admit has a branch
		// wall to be held to (the finance charge route legs prove the 403 for every other role).
		{route: "PUT /api/v1/ar/customers/{customerID}/finance-charge-waiver", class: doorExempt, reason: "requireAdmin is a global scope role, which reaches every account; the waiver service then admits the Financial Controller alone"},
		{route: "POST /api/v1/ar/customers/{customerID}/finance-charge-waiver/clear", class: doorExempt, reason: "requireAdmin is a global scope role, which reaches every account; the waiver service then admits the Financial Controller alone"},
		{route: "GET /api/v1/ar/customers/{customerID}/payment-policy", class: doorWall, path: "/api/v1/ar/customers/%C%/payment-policy"},
		{route: "PUT /api/v1/ar/customers/{customerID}/payment-policy", class: doorWall, path: "/api/v1/ar/customers/%C%/payment-policy", body: `{"ach_first":true,"surcharge_rate_bps":100,"min_ach_cents":1}`},
		{route: "GET /api/v1/ar/customers/{customerID}/statement", class: doorWall, path: "/api/v1/ar/customers/%C%/statement"},
		{route: "POST /api/v1/ar/holds", class: doorWall, path: "/api/v1/ar/holds", body: `{"customer_id":"%C%","reason":"walk"}`},
		{route: "GET /api/v1/ar/credit-changes", class: doorWall, path: "/api/v1/ar/credit-changes?customer_id=%C%"},
		{route: "POST /api/v1/ar/credit-changes", class: doorWall, path: "/api/v1/ar/credit-changes", body: `{"customer_id":"%C%","new_limit_cents":10000,"reason":"walk"}`},
		{route: "POST /api/v1/ar/credit-memos", class: doorWall, path: "/api/v1/ar/credit-memos", body: `{"customer_id":"%C%","branch_id":"` + A + `","amount_cents":100,"reason":"walk"}`},
		{route: "POST /api/v1/ar/credit-memos/quote", class: doorWall, path: "/api/v1/ar/credit-memos/quote", body: `{"customer_id":"%C%","branch_id":"` + A + `","amount_cents":100}`},
		{route: "POST /api/v1/ar/credits/apply", class: doorAgree, path: "/api/v1/ar/credits/apply", body: `{"customer_id":"%C%","credit_kind":"MEMO","credit_id":"` + walkUUID1 + `","invoice_id":"` + walkUUID2 + `","amount_cents":100,"reason":"walk","idempotency_key":"walk-%C%"}`, reason: "the credit and the invoice are both the account's own and both inside the caller's branch reach, so an account out of reach names no invoice the caller may touch: the answer is alike (not found), not the wall's 404"},
		{route: "POST /api/v1/ar/payment-attempts", class: doorWall, path: "/api/v1/ar/payment-attempts", body: `{"idempotency_key":"walk-%C%","customer_id":"%C%","amount_cents":100,"method":"CARD","processor":"x","result":"DECLINED","branch_id":"` + A + `"}`},
		{route: "POST /api/v1/ar/payments/apply", class: doorAgree, path: "/api/v1/ar/payments/apply", body: `{"customer_id":"%C%","amount_cents":100,"method":"CASH","idempotency_key":"walk-%C%"}`},
		{route: "POST /api/v1/ar/payments/paylink", class: doorWall, path: "/api/v1/ar/payments/paylink", body: `{"customer_id":"%C%","channel":"EMAIL"}`},
		{route: "GET /api/v1/ar/promises", class: doorWall, path: "/api/v1/ar/promises?customer_id=%C%"},
		{route: "GET /api/v1/ar/register", class: doorExempt, reason: "the receivables register is invoice branch held: a customer filter serves only the invoices of the caller's own branches"},
		{route: "GET /api/v1/ar/register/export", class: doorExempt, reason: "the receivables register export is invoice branch held, as the register list"},
		{route: "GET /api/v1/ar/reminders", class: doorWall, path: "/api/v1/ar/reminders?customer_id=%C%"},
		{route: "POST /api/v1/ar/reminders", class: doorWall, path: "/api/v1/ar/reminders", body: `{"customer_id":"%C%","channel":"EMAIL"}`},
		{route: "POST /api/v1/ar/reminders/enrol", class: doorWall, path: "/api/v1/ar/reminders/enrol", body: `{"customer_id":"%C%","rule_id":"` + walkUUID1 + `"}`},
		{route: "POST /api/v1/ar/reminders/exempt", class: doorWall, path: "/api/v1/ar/reminders/exempt", body: `{"customer_id":"%C%","rule_id":"` + walkUUID1 + `","note":"walk"}`},
		{route: "POST /api/v1/ar/statement-runs", class: doorWall, path: "/api/v1/ar/statement-runs", body: `{"customer_id":"%C%","mode":"INTERIM"}`},
		{route: "POST /api/v1/billing/ar/customers/{customer_id}/notes", class: doorWall, path: "/api/v1/billing/ar/customers/%C%/notes", body: `{"body":"walk"}`},
		{route: "GET /api/v1/billing/ar/customers/{customer_id}/statements/{statement_id}/preview", class: doorWall, path: "/api/v1/billing/ar/customers/%C%/statements/" + walkUUID2 + "/preview"},
		{route: "GET /api/v1/billing/ar/customers/{customer_id}/workspace", class: doorWall, path: "/api/v1/billing/ar/customers/%C%/workspace"},
		{route: "GET /api/v1/customers/{id}", class: doorWall, path: "/api/v1/customers/%C%", words: `{"error":"not found"}`},
		{route: "PUT /api/v1/customers/{id}/billing-address", class: doorWall, path: "/api/v1/customers/%C%/billing-address", body: `{"postal":"K8V 5P7","province":"ON"}`},
		{route: "GET /api/v1/customers/{id}/sms-consent", class: doorWall, path: "/api/v1/customers/%C%/sms-consent"},
		{route: "PUT /api/v1/customers/{id}/sms-consent", class: doorWall, path: "/api/v1/customers/%C%/sms-consent", body: `{"consent":false}`},
		// JA-1's job accounts: every door reads the customer through GetAccount first, so a customer outside the
		// caller's reach is not found on all five (the 715 fix round, P2-1), in the customer read's own words, as
		// GET /api/v1/customers/{id} answers.
		{route: "GET /api/v1/customers/{id}/jobs", class: doorWall, path: "/api/v1/customers/%C%/jobs", words: `{"error":"not found"}`},
		{route: "POST /api/v1/customers/{id}/jobs", class: doorWall, path: "/api/v1/customers/%C%/jobs", body: `{"job_no":"WALK-1","name":"Walk job"}`, words: `{"error":"not found"}`},
		{route: "GET /api/v1/customers/{id}/jobs/{jobId}", class: doorWall, path: "/api/v1/customers/%C%/jobs/" + walkUUID2, words: `{"error":"not found"}`},
		{route: "PUT /api/v1/customers/{id}/jobs/{jobId}", class: doorWall, path: "/api/v1/customers/%C%/jobs/" + walkUUID2, body: `{"name":"Walk job"}`, words: `{"error":"not found"}`},
		{route: "POST /api/v1/customers/{id}/jobs/{jobId}/close", class: doorWall, path: "/api/v1/customers/%C%/jobs/" + walkUUID2 + "/close", words: `{"error":"not found"}`},
		{route: "POST /api/v1/lien/jobs", class: doorWall, path: "/api/v1/lien/jobs", body: `{"job_no":"WALK-1","customer_id":"%C%","branch_id":"` + A + `","description":"walk"}`},
		{route: "POST /api/v1/messaging/inbound/customer", class: doorWall, path: "/api/v1/messaging/inbound/customer", body: `{"customer_id":"%C%","body":"walk"}`},
		{route: "DELETE /api/v1/messaging/rep-assignments", class: doorExempt, reason: "requireAdmin is a global scope role, which reaches every account"},
		{route: "GET /api/v1/messaging/rep-assignments", class: doorWall, path: "/api/v1/messaging/rep-assignments?customer_id=%C%"},
		{route: "POST /api/v1/messaging/rep-assignments", class: doorExempt, reason: "requireAdmin is a global scope role, which reaches every account"},
		{route: "GET /api/v1/messaging/rep-route", class: doorWall, path: "/api/v1/messaging/rep-route?customer_id=%C%"},
		{route: "GET /api/v1/pos/account-standing", class: doorWall, path: "/api/v1/pos/account-standing?customer_id=%C%&branch_id=" + A},
		{route: "POST /api/v1/pos/counter-prices", class: doorWall, path: "/api/v1/pos/counter-prices", body: `{"branch_id":"` + A + `","customer_id":"%C%","items":[` + line + `]}`},
		{route: "POST /api/v1/pos/margin-glance", class: doorWall, path: "/api/v1/pos/margin-glance", body: `{"branch_id":"` + A + `","customer_id":"%C%","lines":[` + line + `]}`},
		{route: "POST /api/v1/pos/parked", class: doorWall, path: "/api/v1/pos/parked", body: `{"branch_id":"` + A + `","customer_id":"%C%","lines":[` + line + `]}`},
		{route: "POST /api/v1/pos/pricing-orders", class: doorWall, path: "/api/v1/pos/pricing-orders", body: `{"branch_id":"` + A + `","customer_id":"%C%","lines":[` + line + `]}`},
		{route: "GET /api/v1/pos/quick-picks", class: doorAgree, path: "/api/v1/pos/quick-picks?customer_id=%C%&branch_id=" + A, reason: "walled through CounterPrices (customerFor); priceQuickPicks swallows the 404 into the unpriced RETAIL grid, so the answer is alike, not a 404"},
		{route: "POST /api/v1/pos/returns-across", class: doorAgree, path: "/api/v1/pos/returns-across", body: `{"customer_id":"%C%","orders":[]}`},
		{route: "GET /api/v1/pos/returns/customer-lines", class: doorAgree, path: "/api/v1/pos/returns/customer-lines?customer_id=%C%&branch_id=" + A},
		{route: "POST /api/v1/pos/returns/preview", class: doorAgree, path: "/api/v1/pos/returns/preview", body: `{"customer_id":"%C%","orders":[]}`},
		{route: "POST /api/v1/pos/sale-preview", class: doorWall, path: "/api/v1/pos/sale-preview", body: `{"branch_id":"` + A + `","customer_id":"%C%","lines":[` + line + `]}`},
		{route: "POST /api/v1/pos/sales", class: doorWall, path: "/api/v1/pos/sales", body: `{"idempotency_key":"3ef10000-0000-4000-8000-0000000000d1","branch_id":"` + A + `","customer_id":"%C%","tender":"CASH","lines":[` + line + `]}`},
		{route: "POST /api/v1/pos/swaps", class: doorAgree, path: "/api/v1/pos/swaps", body: `{"customer_id":"%C%","orders":[],"lines":[` + line + `]}`},
		{route: "POST /api/v1/pos/swaps/preview", class: doorAgree, path: "/api/v1/pos/swaps/preview", body: `{"customer_id":"%C%","orders":[],"lines":[` + line + `]}`},
		{route: "GET /api/v1/pricing/customers/{customerId}/overrides", class: doorWall, path: "/api/v1/pricing/customers/%C%/overrides"},
		{route: "POST /api/v1/pricing/customers/{customerId}/overrides", class: doorWall, path: "/api/v1/pricing/customers/%C%/overrides", body: `{"category_id":"` + walkUUID2 + `","calc_type":"PCT_OFF_LIST","discount_bps":5000,"price_uom":"EA"}`},
		{route: "POST /api/v1/pricing/floor-preview", class: doorWall, path: "/api/v1/pricing/floor-preview", body: `{"kind":"override","customer_id":"%C%","override":{"category_id":"` + walkUUID2 + `","calc_type":"PCT_OFF_LIST","discount_bps":5000,"price_uom":"EA"}}`},
		{route: "POST /api/v1/pricing/resolve", class: doorWall, path: "/api/v1/pricing/resolve", body: `{"product_id":"` + prod + `","customer_id":"%C%","qty_entered":1,"uom_entered":"EA"}`},
		{route: "POST /api/v1/pricing/rules", class: doorWall, path: "/api/v1/pricing/rules", body: `{"scope":"CUSTOMER_PRODUCT","customer_id":"%C%","product_id":"` + prod + `","calc_type":"ABSOLUTE","price_cents":1,"price_uom":"EA"}`},
		{route: "PUT /api/v1/pricing/rules/{id}", class: doorWall, path: "/api/v1/pricing/rules/" + walkUUID2, body: `{"customer_id":"%C%","version":1}`},
		{route: "POST /api/v1/quotes", class: doorWall, path: "/api/v1/quotes", body: `{"customer_id":"%C%","branch_id":"` + A + `"}`},
		{route: "POST /api/v1/warranty/claims", class: doorWall, path: "/api/v1/warranty/claims", body: `{"customer_id":"%C%","description":"walk"}`},
	}
}

var walkCustomerName = regexp.MustCompile(`(?i)^customer_?id$`)

// discoverCustomerRoutes reads core/api/openapi.yaml and returns every "METHOD /path" that takes a customer.
func discoverCustomerRoutes(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse openapi: %v", err)
	}
	var deref func(v any, depth int) map[string]any
	deref = func(v any, depth int) map[string]any {
		m, _ := v.(map[string]any)
		for depth < 8 && m != nil {
			ref, _ := m["$ref"].(string)
			if ref == "" {
				break
			}
			var cur any = doc
			for _, seg := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
				cm, _ := cur.(map[string]any)
				cur = cm[seg]
			}
			m, _ = cur.(map[string]any)
			depth++
		}
		return m
	}
	var props func(schema any, depth int) []string
	props = func(schema any, depth int) []string {
		s := deref(schema, 0)
		if s == nil || depth > 4 {
			return nil
		}
		var out []string
		if p, ok := s["properties"].(map[string]any); ok {
			for k := range p {
				out = append(out, k)
			}
		}
		for _, k := range []string{"allOf", "oneOf", "anyOf"} {
			if subs, ok := s[k].([]any); ok {
				for _, sub := range subs {
					out = append(out, props(sub, depth+1)...)
				}
			}
		}
		return out
	}
	var found []string
	paths, _ := doc["paths"].(map[string]any)
	for path, item := range paths {
		im, _ := item.(map[string]any)
		for method, opv := range im {
			switch method {
			case "get", "put", "post", "delete", "patch":
			default:
				continue
			}
			op, _ := opv.(map[string]any)
			hit := strings.Contains(path, "/customers/{")
			var params []any
			if pp, ok := im["parameters"].([]any); ok {
				params = append(params, pp...)
			}
			if pp, ok := op["parameters"].([]any); ok {
				params = append(params, pp...)
			}
			for _, p := range params {
				if pm := deref(p, 0); pm != nil {
					if name, _ := pm["name"].(string); walkCustomerName.MatchString(name) {
						hit = true
					}
				}
			}
			if rb := deref(op["requestBody"], 0); rb != nil {
				if content, ok := rb["content"].(map[string]any); ok {
					for _, c := range content {
						cm, _ := c.(map[string]any)
						for _, k := range props(cm["schema"], 0) {
							if walkCustomerName.MatchString(k) {
								hit = true
							}
						}
					}
				}
			}
			if hit {
				found = append(found, strings.ToUpper(method)+" "+path)
			}
		}
	}
	sort.Strings(found)
	return found
}

// walkEveryCustomerDoor mounts the whole served surface on the kit's staff guards double (the real handlers, the
// real pool, the real customer seam) as a branch scoped manager granted North A only, and walks the registry.
func walkEveryCustomerDoor(t *testing.T, pool *pgxpool.Pool, custB, custShared, unknown string) {
	t.Helper()
	registry := map[string]customerDoor{}
	for _, d := range walkDoors() {
		if _, dup := registry[d.route]; dup {
			t.Fatalf("door %s is registered twice", d.route)
		}
		registry[d.route] = d
	}
	discovered := discoverCustomerRoutes(t)
	seen := map[string]bool{}
	for _, route := range discovered {
		seen[route] = true
		if _, ok := registry[route]; !ok {
			t.Errorf("NEW CUSTOMER DOOR %s takes a customer and is not in the walk: classify it in walkDoors (wall, agree or exempt) and wall it", route)
		}
	}
	for route := range registry {
		if !seen[route] {
			t.Errorf("walkDoors names %s, which openapi.yaml does not list as a customer taking route: remove or fix it", route)
		}
	}
	t.Logf("customer doors found in openapi.yaml: %d", len(discovered))
	for _, route := range discovered {
		d := registry[route]
		t.Logf("  %-6s %s %s", d.class, route, d.reason)
	}

	// The reminder send is part of accelerated AR (the PRO tier); the walk turns the dealer default on for its own
	// length so the send door reaches its customer wall, and puts it back.
	var oldTier string
	if err := pool.QueryRow(context.Background(), `SELECT ar_tier FROM ar_landing_policy WHERE branch_id IS NULL`).Scan(&oldTier); err != nil {
		t.Fatalf("read the AR landing tier: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE ar_landing_policy SET ar_tier = 'PRO' WHERE branch_id IS NULL`); err != nil {
		t.Fatalf("turn accelerated AR on: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `UPDATE ar_landing_policy SET ar_tier = $1 WHERE branch_id IS NULL`, oldTier)
	})

	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano()%1_000_000_000)
	one := func(q string, args ...any) string {
		t.Helper()
		var v string
		if err := pool.QueryRow(ctx, q, args...).Scan(&v); err != nil {
			t.Fatalf("plant (%.60s): %v", q, err)
		}
		return v
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatalf("plant (%.60s): %v", q, err)
		}
	}
	// North B's account holds a statement from a run at North A, as a North A caller's interim run files it, so
	// the statement read has something to leak (a run with no branch is never shown to a scoped caller, so a
	// no branch run would let an unwalled door pass for want of data).
	runID := one(`INSERT INTO ar_statement_runs (mode, branch_id, as_of_date, status) VALUES ('INTERIM', $1::uuid, current_date, 'COMPLETED') RETURNING id::text`, testkit.BranchNorthAID)
	exec(`INSERT INTO ar_statements (run_id, customer_id, as_of_date) VALUES ($1::uuid, $2::uuid, current_date)`, runID, custB)

	// Round 4 fixtures: North B's account with an invoice and a promise at each branch, and rep assignments.
	invoice := func(branch, tag string) string {
		return one(`INSERT INTO invoices (invoice_no, customer_id, branch_id, status, issue_date, due_date, subtotal_cents, tax_cents, total_cents, balance_cents, finalized_at, org_id)
			VALUES ($1, $2::uuid, $3::uuid, 'FINALIZED', (now() AT TIME ZONE 'America/Toronto')::date, (now() AT TIME ZONE 'America/Toronto')::date, 700, 0, 700, 700, now(), $4::uuid) RETURNING id::text`,
			"R4-INV-"+tag+"-"+suffix, custB, branch, testkit.OrgNorthID)
	}
	invA, invB := invoice(testkit.BranchNorthAID, "A"), invoice(testkit.BranchNorthBID, "B")
	for inv, note := range map[string]string{invA: "R4-PROMISE-A-" + suffix, invB: "R4-PROMISE-B-" + suffix} {
		exec(`INSERT INTO ar_promises_to_pay (invoice_id, customer_id, promised_cents, promised_date, promised_by, note)
			VALUES ($1::uuid, $2::uuid, 700, current_date + 5, '', $3)`, inv, custB, note)
	}
	custB2 := one(`INSERT INTO customers (account_no, name, customer_type, home_branch_id, org_id) VALUES ($1, 'UX89 R4 B two', 'CONTRACTOR', $2::uuid, $3::uuid) RETURNING id::text`,
		"UX89R4-B2-"+suffix, testkit.BranchNorthBID, testkit.OrgNorthID)
	// custB is in the caller's own book; custB2 in another rep's; the shared account in another rep's.
	exec(`INSERT INTO rep_assignments (customer_id, rep_user_id, source) VALUES ($1::uuid, $2::uuid, 'MANUAL')`, custB, testkit.UserSalesMgrID)
	exec(`INSERT INTO rep_assignments (customer_id, rep_user_id, source) VALUES ($1::uuid, $2::uuid, 'MANUAL')`, custB2, testkit.UserInsideSalesID)
	exec(`INSERT INTO rep_assignments (customer_id, rep_user_id, source) VALUES ($1::uuid, $2::uuid, 'MANUAL')`, custShared, testkit.UserInsideSalesID)
	t.Cleanup(func() {
		bg := context.Background()
		ids := []string{custB, custB2, custShared}
		_, _ = pool.Exec(bg, `DELETE FROM rep_assignments WHERE customer_id::text = ANY($1)`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM ar_promises_to_pay WHERE customer_id::text = ANY($1)`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM invoices WHERE customer_id::text = ANY($1)`, ids)
		_, _ = pool.Exec(bg, `DELETE FROM ar_statements WHERE run_id = $1`, runID)
		_, _ = pool.Exec(bg, `DELETE FROM ar_statement_runs WHERE id = $1`, runID)
		_, _ = pool.Exec(bg, `DELETE FROM customers WHERE id = $1`, custB2)
	})
	before := customerRowCounts(t, pool, custB)

	// The walk runs as three roles, each granted North A only. A manager door is a 403 for the base tier role,
	// the same for both ids; the wall assertions apply where the role reaches the door.
	roles := []struct {
		name string
		auth identity.StaffAuthority
	}{
		{"sales_manager", identity.StaffAuthority{RoleID: "sales_manager", Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{testkit.BranchNorthAID}}},
		{"payables_receivables", identity.StaffAuthority{RoleID: "payables_receivables", Tier: identity.TierManager, Scope: identity.ScopeBranch, Branches: []string{testkit.BranchNorthAID}}},
		{"inside_sales", identity.StaffAuthority{RoleID: "inside_sales", Tier: 3, Scope: identity.ScopeBranch, Branches: []string{testkit.BranchNorthAID}}},
	}
	const nf = `{"error":"customer not found"}`
	for ri, role := range roles {
		sg := testkit.NewStaffGuards(t)
		sg.Harness.Membership.SetMember(testkit.SubSalesManager, testkit.OrgNorthSlug, true)
		sg.SetAuthority(role.auth)
		log := slog.New(slog.NewTextHandler(io.Discard, nil))
		mux := http.NewServeMux()
		belt := buildSmsBelt(pool, log)
		money := wireMoney(mux, pool, sg.Guards, log, "", mustRoleConfigForWire())
		ops := wireOperations(mux, pool, sg.Guards, log, money, "fx-ticket-key", belt.outbox)
		wireTops(mux, pool, sg.Guards, log, money, ops, config.Config{PaymentVaultKey: dashVaultKey(t)}, mustRoleConfigForWire(), belt)
		token := sg.Harness.Minter.StaffToken(t, testkit.SubSalesManager)
		raw := func(method, path, body string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set(authz.HeaderXOrg, testkit.OrgNorthSlug)
			req.Header.Set("Content-Type", "application/json")
			rsp := httptest.NewRecorder()
			mux.ServeHTTP(rsp, req)
			return rsp
		}
		call := func(d customerDoor, cust string) *httptest.ResponseRecorder {
			method := strings.SplitN(d.route, " ", 2)[0]
			return raw(method, strings.ReplaceAll(d.path, "%C%", cust), strings.ReplaceAll(d.body, "%C%", cust))
		}
		for _, route := range discovered {
			d, ok := registry[route]
			if !ok || d.class == doorExempt {
				continue
			}
			b, u := call(d, custB), call(d, unknown)
			// A body may echo the id asked about (customer lines does); the answers are compared with the id masked.
			bBody := strings.ReplaceAll(b.Body.String(), custB, "<id>")
			uBody := strings.ReplaceAll(u.Body.String(), unknown, "<id>")
			if b.Code != u.Code || bBody != uBody {
				t.Errorf("[%s] %s (%s): North B's account answered %d %q, an unknown id %d %q: the door does not agree", role.name, route, d.class, b.Code, b.Body.String(), u.Code, u.Body.String())
			}
			if d.class != doorWall || (b.Code == http.StatusForbidden && u.Code == http.StatusForbidden) {
				continue // the role gate refused both alike; nothing behind it is reached
			}
			if strings.HasSuffix(d.route, "/pos/margin-glance") && b.Code == http.StatusOK && strings.TrimSpace(b.Body.String()) == "{}" {
				continue // a role without margin visibility gets the empty glance before any account is read
			}
			if b.Code != http.StatusNotFound {
				t.Errorf("[%s] %s: a wall door must answer 404 for North B's account, got %d %q", role.name, route, b.Code, b.Body.String())
			}
			// The seam's own words, so a door that answers 404 for want of data cannot pass as a wall. The one
			// exception is the account read, whose 404 word has always been "not found".
			want := nf
			if d.words != "" {
				want = d.words
			}
			if b.Code == http.StatusNotFound && strings.TrimSpace(b.Body.String()) != want {
				t.Errorf("[%s] %s: a wall door answers %s, got %q", role.name, route, want, b.Body.String())
			}
			// The shared account is probed on reads only: a write for it would land and tie the fixture down.
			if strings.HasPrefix(d.route, "GET ") {
				if sh := call(d, custShared); sh.Code == http.StatusNotFound && strings.Contains(sh.Body.String(), "customer not found") {
					t.Errorf("[%s] %s: the shared (no home) account must not be answered customer not found, got %d %q", role.name, route, sh.Code, sh.Body.String())
				}
			}
		}
		if ri != 0 {
			continue
		}

		// Round 4 list legs and real document legs, as the North A sales manager.
		get := func(path string) string {
			rsp := raw(http.MethodGet, path, "")
			if rsp.Code != http.StatusOK {
				t.Fatalf("GET %s: %d %s", path, rsp.Code, rsp.Body.String())
			}
			return rsp.Body.String()
		}
		// P2-A: the unfiltered promises list is held to the invoice's branch.
		promises := get("/api/v1/ar/promises")
		if !strings.Contains(promises, "R4-PROMISE-A-"+suffix) || strings.Contains(promises, "R4-PROMISE-B-"+suffix) {
			t.Errorf("the promises list must hold the North A invoice's promise and not the North B invoice's: %s", promises)
		}
		// P2-B: the rep assignments list is held to the customer rule, with the one open arm.
		own := get("/api/v1/messaging/rep-assignments?rep_user_id=" + testkit.UserSalesMgrID)
		if !strings.Contains(own, custB) {
			t.Errorf("a rep lists their own book across branches (rep_user_id is the caller's own users.id): %s", own)
		}
		other := get("/api/v1/messaging/rep-assignments?rep_user_id=" + testkit.UserInsideSalesID)
		if strings.Contains(other, custB2) || !strings.Contains(other, custShared) {
			t.Errorf("a chosen other rep_user_id meets the filter (no North B account, the shared one stays): %s", other)
		}
		all := get("/api/v1/messaging/rep-assignments")
		if strings.Contains(all, custB) || strings.Contains(all, custB2) || !strings.Contains(all, custShared) {
			t.Errorf("an unfiltered rep assignments list is held to reach: %s", all)
		}
		// A real document leg for payments apply: North B's account naming its North A invoice is a document the
		// branch holds (preview applies); naming its North B invoice answers exactly as an unknown account does.
		apply := func(cust, inv string) *httptest.ResponseRecorder {
			return raw(http.MethodPost, "/api/v1/ar/payments/apply", fmt.Sprintf(`{"customer_id":%q,"method":"CASH","amount_cents":100,"invoice_ids":[%q],"idempotency_key":"r4-%s","preview":true}`, cust, inv, suffix))
		}
		if rsp := apply(custB, invA); rsp.Code != http.StatusOK {
			t.Errorf("apply preview on the North A invoice of a yard held account: want 200, got %d %s", rsp.Code, rsp.Body.String())
		}
		bB, uB := apply(custB, invB), apply(unknown, invB)
		if bB.Code != uB.Code || strings.ReplaceAll(bB.Body.String(), custB, "<id>") != strings.ReplaceAll(uB.Body.String(), unknown, "<id>") {
			t.Errorf("apply naming the North B invoice must answer as an unknown account does: %d %q | %d %q", bB.Code, bB.Body.String(), uB.Code, uB.Body.String())
		}
		if bB.Code == http.StatusOK {
			t.Errorf("apply preview reached North B's invoice: %s", bB.Body.String())
		}
	}
	// No refused write landed: every table that carries a customer_id holds the same rows for North B's account
	// after the walk as before it.
	after := customerRowCounts(t, pool, custB)
	for table, n := range before {
		if after[table] != n {
			t.Errorf("a refused door wrote on North B's account: %s held %d rows for it before the walk and %d after", table, n, after[table])
		}
	}
}

// customerRowCounts counts, in every public table that has a customer_id column, the rows naming one customer.
func customerRowCounts(t *testing.T, pool *pgxpool.Pool, customerID string) map[string]int {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `SELECT table_name FROM information_schema.columns
		WHERE table_schema = 'public' AND column_name = 'customer_id'
		  AND table_name IN (SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE')
		ORDER BY table_name`)
	if err != nil {
		t.Fatalf("list customer tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	out := map[string]int{}
	for _, table := range tables {
		var n int
		if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT count(*) FROM %q WHERE customer_id::text = $1`, table), customerID).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		out[table] = n
	}
	return out
}
