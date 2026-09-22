//go:build integration

package portal

// dib479_integration_test.go, DIB-479 against the real Postgres the seeded DB provides.
//
// These prove the three things a fake cannot: that migration 0160 actually admits the two new role
// strings (the unit tests only prove Go agrees with itself), that the addresses SQL fences on
// customer_id in the database rather than in a handler, and that the product detail read composes
// the real product + inventory repositories without dragging a cost column across with it.
//
// NO PINNED CALENDAR DATES IN THIS FILE (DIB-427). Every instant is derived from one base captured
// at run time and moved with .Add(), so nothing here can start failing on a future morning.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/inventory"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/product"
)

// The demo contractor tenant seeded by migration 0055, it owns the one seeded project (0049),
// which gives the addresses read a real job site to find.
const custDemo = "df000000-0000-4000-9000-000000000001"

// -- roles: migration 0160 ----------------------------------------------------

// The DB must accept the two new role strings on BOTH role-bearing tables, and must still refuse a
// role that does not exist. Without 0160 the first insert fails on the CHECK constraint, which is
// exactly what this test is for: the Go switch statement agreeing with itself proves nothing about
// what the column will store.
func TestIntegration_DIB479_SplitBuyerRoles_AcceptedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	tag := time.Now().UnixNano() // uniqueness only, never compared to anything

	for _, role := range []string{string(RoleBuyerNoPay), string(RoleAPFinance)} {
		email := fmt.Sprintf("dib479+%s+%d@quinte.example", role, tag)
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE email = $1`, email)
			_, _ = pool.Exec(ctx, `DELETE FROM portal_invites WHERE email = $1`, email)
		})
		if _, err := pool.Exec(ctx, `
			INSERT INTO portal_users (customer_id, email, name, password_hash, role, status)
			VALUES ($1::uuid, $2, 'DIB-479 persona', 'x', $3, 'active')`,
			custQuinte, email, role); err != nil {
			t.Fatalf("portal_users must accept role %q after 0160: %v", role, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO portal_invites (customer_id, email, role, token_hash, expires_at)
			VALUES ($1::uuid, $2, $3, $4, now() + interval '1 day')`,
			custQuinte, email, role, fmt.Sprintf("hash-%s-%d", role, tag)); err != nil {
			t.Fatalf("portal_invites must accept role %q after 0160: %v", role, err)
		}
	}

	// The CHECK is still a CHECK: widening it must not have turned the column into free text.
	if _, err := pool.Exec(ctx, `
		INSERT INTO portal_users (customer_id, email, name, password_hash, role, status)
		VALUES ($1::uuid, $2, 'nope', 'x', 'super_admin', 'active')`,
		custQuinte, fmt.Sprintf("dib479+bogus+%d@quinte.example", tag)); err == nil {
		_, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE email = $1`,
			fmt.Sprintf("dib479+bogus+%d@quinte.example", tag))
		t.Fatal("an unknown role must still be refused by the CHECK constraint")
	}
}

// A real user carrying one of the new roles authenticates and resolves the right capability set
// end to end, DB row → Authenticate → PortalContext.Caps().
func TestIntegration_DIB479_NewRoleResolvesCapabilitiesThroughAuthenticate(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	email := fmt.Sprintf("dib479+ap+%d@quinte.example", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE email = $1`, email) })

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO portal_users (customer_id, email, name, password_hash, role, status)
		VALUES ($1::uuid, $2, 'Robin A/P', 'x', 'ap_finance', 'active') RETURNING id::text`,
		custQuinte, email).Scan(&userID); err != nil {
		t.Fatalf("seed ap_finance user: %v", err)
	}

	svc := NewService(NewRepository(pool), intSecret)
	token, err := issueJWT(intSecret, Claims{Sub: userID, CustomerID: custQuinte, Role: "ap_finance"}, svc.now())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	caller, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	caps := caller.Caps()
	if !caps.PayInvoices || caps.CreateOrders || caps.SubmitRFQ || caps.ManageUsers {
		t.Fatalf("ap_finance resolved wrong through the real path: %+v", caps)
	}
}

// -- refresh against the real repository --------------------------------------

// Sliding refresh, then the cap, driven through the real pgx repository (so the user reload on
// every refresh is a real query). The clock is injected; nothing sleeps.
func TestIntegration_DIB479_RefreshSlidesThenHitsTheCap(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	email := fmt.Sprintf("dib479+refresh+%d@quinte.example", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM portal_users WHERE email = $1`, email) })

	hash, err := hashPassword("hunter2pw-integration")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO portal_users (customer_id, email, name, password_hash, role, status)
		VALUES ($1::uuid, $2, 'Refresh Tester', $3, 'buyer_no_pay', 'active') RETURNING id::text`,
		custQuinte, email, hash).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}

	svc := NewService(NewRepository(pool), intSecret)
	base := svc.now() // one clock, captured once; every instant below is derived from it
	at := base
	svc.now = func() time.Time { return at }

	resp, err := svc.Login(ctx, email, "hunter2pw-integration")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	tok := resp.Token

	// Slide for six days, eleven hours at a time.
	for hours := 11; hours <= 6*24; hours += 11 {
		at = base.Add(time.Duration(hours) * time.Hour)
		r, err := svc.Refresh(ctx, tok)
		if err != nil {
			t.Fatalf("refresh at +%dh must succeed: %v", hours, err)
		}
		tok = r.Token
	}

	// Past the seven-day ceiling: refused, and the token stops authenticating at all.
	at = base.Add(sessionMaxAge + time.Minute)
	if _, err := svc.Refresh(ctx, tok); err == nil {
		t.Fatal("refresh past the absolute cap must be refused")
	}
	if _, err := svc.Authenticate(ctx, tok); err == nil {
		t.Fatal("a token whose session aged out must not authenticate")
	}

	// A deactivated user cannot refresh even well inside the window.
	at = base.Add(time.Hour)
	fresh, err := svc.Login(ctx, email, "hunter2pw-integration")
	if err != nil {
		t.Fatalf("re-login: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE portal_users SET status = 'inactive' WHERE id = $1::uuid`, userID); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	at = base.Add(2 * time.Hour)
	if _, err := svc.Refresh(ctx, fresh.Token); err == nil {
		t.Fatal("a deactivated user must not be able to refresh")
	}
}

// -- addresses: the fence is in the SQL ---------------------------------------

// The addresses read returns the caller's sites and NOTHING of another account's, proven against
// the real union query, with a second tenant's site inserted for the run.
func TestIntegration_DIB479_AddressesAreFencedInSQL(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	repo := NewProjectRepository(pool)
	svc := NewProjectsService(repo)

	// A job site for the OTHER tenant, so "the fence works" is a claim with something to fail on.
	tag := time.Now().UnixNano()
	otherSite := fmt.Sprintf("99 Secret Lane, Kingston (%d)", tag)
	var otherProjectID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (customer_id, name, job_address) VALUES ($1::uuid, $2, $3) RETURNING id::text`,
		custQuinte, fmt.Sprintf("Quinte private job %d", tag), otherSite).Scan(&otherProjectID); err != nil {
		t.Fatalf("seed other tenant project: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM projects WHERE id = $1::uuid`, otherProjectID) })

	demo, err := svc.ListAddresses(ctx, custDemo)
	if err != nil {
		t.Fatalf("list addresses: %v", err)
	}
	if len(demo) == 0 {
		t.Fatal("the demo contractor has a seeded job site and a billing address, got none")
	}
	blob, _ := json.Marshal(demo)
	if strings.Contains(string(blob), "Secret Lane") {
		t.Fatalf("another tenant's site crossed the fence: %s", blob)
	}
	var kinds []string
	for _, a := range demo {
		kinds = append(kinds, a.Kind)
		if a.ID == "" || a.Kind == "" {
			t.Fatalf("every address needs a stable id and a kind: %+v", a)
		}
	}
	t.Logf("demo contractor sites: %d (%s)", len(demo), strings.Join(kinds, ", "))

	// THE IDOR: the other tenant's real address id, asked for by the demo contractor → not found.
	crossID := "project:" + otherProjectID
	if _, err := svc.GetAddress(ctx, custDemo, crossID); err == nil {
		t.Fatal("a cross-tenant address id must not resolve")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-tenant miss must be a not-found, got %v", err)
	}
	// ...and it DOES resolve for its owner, so the test above failed for the right reason.
	if got, err := svc.GetAddress(ctx, custQuinte, crossID); err != nil || got.Line1 != otherSite {
		t.Fatalf("the owner must be able to read their own site: %+v (%v)", got, err)
	}
}

// The delivery arm: sites this account's orders were actually delivered to, deduped to one row per
// place with the most recent delivery's timestamp on it. This is the arm the seed has no data for,
// so the test brings its own.
func TestIntegration_DIB479_DeliveredSitesAppearOnceWithTheirLastUse(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := NewProjectsService(NewProjectRepository(pool))
	tag := time.Now().UnixNano()

	var branchID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM locations WHERE branch_id IS NOT NULL LIMIT 1`).Scan(&branchID); err != nil {
		t.Fatalf("no branch to hang an order on: %v", err)
	}
	line := fmt.Sprintf("%d Contractor Way", tag%10000)
	base := time.Now().UTC().Truncate(time.Second)

	// TWO orders, each with one stop, both delivered to the SAME place, the ordinary case of a
	// contractor taking two loads to one job. (An order may hold only one LIVE stop, 0090, so this
	// is also the only way the situation arises.) The read must fold them into one site.
	for i, at := range []time.Time{base.Add(-48 * time.Hour), base.Add(-1 * time.Hour)} {
		var orderID string
		if err := pool.QueryRow(ctx, `
			INSERT INTO orders (order_no, customer_id, branch_id, status, fulfillment)
			VALUES ($1, $2::uuid, $3::uuid, 'CONFIRMED', 'DELIVERY') RETURNING id::text`,
			fmt.Sprintf("DIB479-%d-%d", tag, i), custDemo, branchID).Scan(&orderID); err != nil {
			t.Fatalf("seed order %d: %v", i, err)
		}
		t.Cleanup(func() {
			_, _ = pool.Exec(ctx, `DELETE FROM delivery_stops WHERE order_id = $1::uuid`, orderID)
			_, _ = pool.Exec(ctx, `DELETE FROM orders WHERE id = $1::uuid`, orderID)
		})
		if _, err := pool.Exec(ctx, `
			INSERT INTO delivery_stops (branch_id, order_id, stop_no, scheduled_date, status,
			                            address_line, city, postal_code, contact_name,
			                            pod_signature_ref, pod_received_by, pod_at, delivered_at)
			VALUES ($1::uuid, $2::uuid, 1, $3::date, 'DELIVERED', $4, 'Trenton', 'K8V 5P7', 'Dana Reyes',
			        'ref', 'Dana Reyes', $5, $5)`,
			branchID, orderID, at, line, at); err != nil {
			t.Fatalf("seed delivery stop %d: %v", i, err)
		}
	}

	list, err := svc.ListAddresses(ctx, custDemo)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var found []Address
	for _, a := range list {
		if a.Line1 == line {
			found = append(found, a)
		}
	}
	if len(found) != 1 {
		t.Fatalf("two deliveries to one place must be ONE site, got %d: %+v", len(found), found)
	}
	site := found[0]
	if site.Kind != AddressKindDelivery || site.City != "Trenton" || site.Postal != "K8V 5P7" {
		t.Fatalf("delivery site projected wrong: %+v", site)
	}
	if site.LastUsedAt == nil || !site.LastUsedAt.Equal(base.Add(-1*time.Hour)) {
		t.Fatalf("last_used_at must be the MOST RECENT delivery: %+v", site.LastUsedAt)
	}
	// Delivered sites sort ahead of job sites and the account address.
	if list[0].Kind != AddressKindDelivery {
		t.Fatalf("a place somebody has driven to must sort first: %+v", list[0])
	}
	// And it resolves by id, within the tenant.
	if got, err := svc.GetAddress(ctx, custDemo, site.ID); err != nil || got.Line1 != line {
		t.Fatalf("delivery site must resolve by id: %+v (%v)", got, err)
	}
	if _, err := svc.GetAddress(ctx, custQuinte, site.ID); err == nil {
		t.Fatal("another tenant must not resolve this site id")
	}
}

// -- catalog detail: real product + real inventory ----------------------------

// The detail read composes the real product repository with the real inventory service, and the
// projection that comes out the other side carries availability, lead time, and no money at all.
func TestIntegration_DIB479_CatalogProductDetailIsCleanAndComplete(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)

	// A seeded product that actually has stock somewhere, so availability has something to report.
	var productID, sku, branchID string
	if err := pool.QueryRow(ctx, `
		SELECT p.id::text, p.sku, sb.branch_id::text
		  FROM products p
		  JOIN stock_balances sb ON sb.product_id = p.id AND sb.on_hand_base > 0
		 ORDER BY p.sku
		 LIMIT 1`).Scan(&productID, &sku, &branchID); err != nil {
		t.Fatalf("no seeded product with stock to test against: %v", err)
	}

	// Give it a lead-time policy at that branch for the run.
	if _, err := pool.Exec(ctx, `
		INSERT INTO product_reorder_points (product_id, branch_id, reorder_point_base, reorder_qty_base, lead_time_days, is_active)
		VALUES ($1::uuid, $2::uuid, 10, 40, 6, TRUE)
		ON CONFLICT (product_id, branch_id) DO UPDATE SET lead_time_days = 6, is_active = TRUE`,
		productID, branchID); err != nil {
		t.Fatalf("seed reorder point: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM product_reorder_points WHERE product_id = $1::uuid AND branch_id = $2::uuid`,
			productID, branchID)
	})

	invSvc := inventory.NewService(inventory.NewRepository(pool))
	prodSvc := product.NewService(product.NewRepository(pool)).WithBranchStock(invSvc)

	detail, err := prodSvc.GetDetail(ctx, productID)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	if len(detail.OnHandByBranch) == 0 {
		t.Fatal("a product with stock must carry per-branch on-hand")
	}
	if detail.LeadTimeDaysByBranch[branchID] != 6 {
		t.Fatalf("lead time for %s = %v, want 6", branchID, detail.LeadTimeDaysByBranch)
	}

	projected := projectCatalogProduct(detail)
	if projected.SKU != sku || len(projected.Availability) == 0 {
		t.Fatalf("projection lost the product or its availability: %+v", projected)
	}
	if projected.LeadTimeDays == nil {
		t.Fatal("the headline lead time must resolve for a stocked, managed product")
	}
	blob, err := json.Marshal(projected)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// The counter holds against a REAL row, whatever the seed put in the columns this projection
	// deliberately does not copy.
	lower := strings.ToLower(string(blob))
	for _, bad := range forbiddenOnTheCounter {
		if strings.Contains(lower, `"`+bad) || strings.Contains(lower, bad+`":`) {
			t.Fatalf("the real-row projection leaks %q: %s", bad, blob)
		}
	}
	t.Logf("projection: %s", blob)
}
