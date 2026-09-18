package identity

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The capability mapping, carried verbatim from the ERP: every guardable
// capability asserted in both directions per role, the reserved field
// unreachable through Has, and an unknown role granting nothing (C31).
func TestCapabilitiesForCarriedVerbatim(t *testing.T) {
	cases := []struct {
		role string
		want Capabilities
	}{
		{PortalRoleAccountAdmin, Capabilities{
			ManageUsers: true, ViewBilling: true, PayInvoices: true, ManagePaymentMethods: true,
			SubmitRFQ: true, CreateOrders: true, ViewOrdersDeliveries: true, EditDeliveryInstructions: true,
			ManageFiles: true,
		}},
		{PortalRoleBuyer, Capabilities{
			ViewBilling: true, PayInvoices: true, ManagePaymentMethods: true,
			SubmitRFQ: true, CreateOrders: true, ViewOrdersDeliveries: true, EditDeliveryInstructions: true,
			ManageFiles: true,
		}},
		{PortalRoleBuyerNoPay, Capabilities{
			ViewBilling: true, SubmitRFQ: true, CreateOrders: true, ViewOrdersDeliveries: true,
			EditDeliveryInstructions: true, ManageFiles: true,
		}},
		{PortalRoleAPFinance, Capabilities{
			ViewBilling: true, PayInvoices: true, ManagePaymentMethods: true,
			ViewOrdersDeliveries: true, ManageFiles: true,
		}},
		{PortalRoleFieldCrew, Capabilities{
			ViewOrdersDeliveries: true, EditDeliveryInstructions: true,
		}},
	}
	for _, c := range cases {
		got := CapabilitiesFor(c.role)
		if got != c.want {
			t.Fatalf("%s: got %+v want %+v", c.role, got, c.want)
		}
	}
	if got := CapabilitiesFor("owner"); got != (Capabilities{}) {
		t.Fatalf("HH Pro role granted capabilities directly: %+v", got)
	}
	if got := CapabilitiesFor(""); got != (Capabilities{}) {
		t.Fatalf("empty role granted capabilities: %+v", got)
	}
}

// Has answers exactly the eight guardable names and refuses everything
// else, including the reserved model field's own spelling.
func TestCapabilitiesHasGuardableOnly(t *testing.T) {
	full := CapabilitiesFor(PortalRoleAccountAdmin)
	for _, cap := range []Capability{
		CapManageUsers, CapViewBilling, CapPayInvoices, CapManagePaymentMethods,
		CapSubmitRFQ, CapCreateOrders, CapViewOrdersDeliveries, CapEditDeliveryInstructions,
	} {
		if !full.Has(cap) {
			t.Fatalf("admin lacks %s", cap)
		}
	}
	if full.Has("manage_files") {
		t.Fatalf("the reserved field is guardable through Has")
	}
	if full.Has("ninth") {
		t.Fatalf("an unknown capability is guardable")
	}
	none := CapabilitiesFor(PortalRoleFieldCrew)
	if none.Has(CapManageUsers) || none.Has(CapViewBilling) || none.Has(CapPayInvoices) ||
		none.Has(CapManagePaymentMethods) || none.Has(CapSubmitRFQ) || none.Has(CapCreateOrders) {
		t.Fatalf("field crew holds a financial or management capability")
	}
}

// The portal role resolver against real rows: the linked user in the
// token's tenant resolves; a different tenant, a tombstoned row and an
// absent link all resolve to no authority (fail closed).
// TestPortalRoleResolverStatusLegs carries the status half (F-2).
func TestPortalRoleResolverLegs(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewPortalRoleResolver(f.pool)
	if err != nil {
		t.Fatalf("NewPortalRoleResolver: %v", err)
	}
	tenantA := "11111111-1111-4111-8111-111111111111"
	tenantB := "22222222-2222-4222-8222-222222222222"
	platformUser := f.platformUser + "-portal"
	var customerID, portalUserID string
	err = f.pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name)
		VALUES ($1, 'B3 Proof Contractor')
		RETURNING id::text`,
		"B3PROOF"+time.Now().Format("150405.000000000")).Scan(&customerID)
	if err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM portal_users WHERE customer_id = $1`, customerID); err != nil {
			t.Fatalf("clean portal users: %v", err)
		}
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, customerID); err != nil {
			t.Fatalf("clean customer: %v", err)
		}
	})
	err = f.pool.QueryRow(ctx, `
		INSERT INTO portal_users (customer_id, email, password_hash, role, tenant_id, platform_user_id)
		VALUES ($1, $2, 'b3-placeholder-not-a-hash', 'account_admin', $3, $4)
		RETURNING id::text`,
		customerID, "b3portal"+time.Now().Format("150405.000000000")+"@example.invalid", tenantA, platformUser).Scan(&portalUserID)
	if err != nil {
		t.Fatalf("insert portal user: %v", err)
	}

	role, ok, err := resolver.PortalRole(ctx, platformUser, tenantA)
	if err != nil || !ok || role != PortalRoleAccountAdmin {
		t.Fatalf("resolve: role=%q ok=%v err=%v", role, ok, err)
	}
	if role, ok, err := resolver.PortalRole(ctx, platformUser, tenantB); err != nil || ok {
		t.Fatalf("other tenant resolved: role=%q ok=%v err=%v", role, ok, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE portal_users SET deleted_at = now() WHERE id = $1`, portalUserID); err != nil {
		t.Fatalf("tombstone portal user: %v", err)
	}
	if role, ok, err := resolver.PortalRole(ctx, platformUser, tenantA); err != nil || ok {
		t.Fatalf("tombstoned row resolved: role=%q ok=%v err=%v", role, ok, err)
	}
	if role, ok, err := resolver.PortalRole(ctx, "b3-platform-unlinked", tenantA); err != nil || ok {
		t.Fatalf("unlinked user resolved: role=%q ok=%v err=%v", role, ok, err)
	}
}

// The status half (review finding F-2), both directions and both
// non-active values: an active row resolves its role; an inactive and a
// pending row each return ErrPortalUserInactive, never a role; restoring
// active resolves again.
func TestPortalRoleResolverStatusLegs(t *testing.T) {
	f := newB3Fixture(t)
	ctx := context.Background()
	resolver, err := NewPortalRoleResolver(f.pool)
	if err != nil {
		t.Fatalf("NewPortalRoleResolver: %v", err)
	}
	tenantA := "11111111-1111-4111-8111-111111111111"
	platformUser := f.platformUser + "-status"
	suffix := time.Now().Format("150405.000000000")
	var customerID, portalUserID string
	err = f.pool.QueryRow(ctx, `
		INSERT INTO customers (account_no, name)
		VALUES ($1, 'B3 Proof Status Contractor')
		RETURNING id::text`, "B3STAT"+suffix).Scan(&customerID)
	if err != nil {
		t.Fatalf("insert customer: %v", err)
	}
	t.Cleanup(func() {
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM portal_users WHERE customer_id = $1`, customerID); err != nil {
			t.Fatalf("clean portal users: %v", err)
		}
		if _, err := f.pool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, customerID); err != nil {
			t.Fatalf("clean customer: %v", err)
		}
	})
	err = f.pool.QueryRow(ctx, `
		INSERT INTO portal_users (customer_id, email, password_hash, role, status, tenant_id, platform_user_id)
		VALUES ($1, $2, 'b3-placeholder-not-a-hash', 'buyer', 'active', $3, $4)
		RETURNING id::text`,
		customerID, "b3status"+suffix+"@example.invalid", tenantA, platformUser).Scan(&portalUserID)
	if err != nil {
		t.Fatalf("insert portal user: %v", err)
	}

	role, ok, err := resolver.PortalRole(ctx, platformUser, tenantA)
	if err != nil || !ok || role != PortalRoleBuyer {
		t.Fatalf("active resolve: role=%q ok=%v err=%v", role, ok, err)
	}
	for _, status := range []string{"inactive", "pending"} {
		if _, err := f.pool.Exec(ctx, `UPDATE portal_users SET status = $1 WHERE id = $2`, status, portalUserID); err != nil {
			t.Fatalf("set status %s: %v", status, err)
		}
		_, _, err = resolver.PortalRole(ctx, platformUser, tenantA)
		if !errors.Is(err, ErrPortalUserInactive) {
			t.Fatalf("status %s: err=%v, want ErrPortalUserInactive", status, err)
		}
	}
	if _, err := f.pool.Exec(ctx, `UPDATE portal_users SET status = 'active' WHERE id = $1`, portalUserID); err != nil {
		t.Fatalf("restore status: %v", err)
	}
	if role, ok, err := resolver.PortalRole(ctx, platformUser, tenantA); err != nil || !ok || role != PortalRoleBuyer {
		t.Fatalf("restored resolve: role=%q ok=%v err=%v", role, ok, err)
	}
}
