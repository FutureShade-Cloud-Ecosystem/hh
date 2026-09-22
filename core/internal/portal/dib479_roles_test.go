package portal

// dib479_roles_test.go, the two split-buyer personas (OR-2).
//
// The point of these tests is not "the switch statement has two more arms". It is that the two new
// roles are EXACT, that they are exact as DELTAS of buyer (so nobody can quietly grant one of them
// a capability by editing a list), and that adding them did not loosen the two guardrails the
// portal's whole permission story rests on: only account_admin manages users, and an account can
// never be left with no admin.

import (
	"context"
	"testing"
)

// TestCapabilitiesFor_SplitBuyerRoles pins both capability sets EXACTLY. Capabilities is a
// comparable struct, so this is a whole-value assertion: a capability added to either role by
// accident fails here, not just a missing one.
func TestCapabilitiesFor_SplitBuyerRoles(t *testing.T) {
	cases := []struct {
		role string
		want Capabilities
	}{
		// PM: buys, plans, takes delivery. Cannot move money.
		{"buyer_no_pay", Capabilities{
			ViewBilling: true, SubmitRFQ: true, CreateOrders: true,
			ViewOrdersDeliveries: true, EditDeliveryInstructions: true, ManageFiles: true,
		}},
		// A/P: pays, holds the payment methods, reads what arrived. Cannot buy.
		{"ap_finance", Capabilities{
			ViewBilling: true, PayInvoices: true, ManagePaymentMethods: true,
			ViewOrdersDeliveries: true, ManageFiles: true,
		}},
	}
	for _, c := range cases {
		if got := CapabilitiesFor(c.role); got != c.want {
			t.Errorf("CapabilitiesFor(%q) = %+v, want %+v", c.role, got, c.want)
		}
	}
}

// TestSplitBuyerRoles_AreExactDeltasOfBuyer states the design in the form a reviewer can check at a
// glance: each new role is `buyer` with a named set subtracted and NOTHING else moved. If someone
// later "helpfully" gives ap_finance CreateOrders, this fails with the field named.
func TestSplitBuyerRoles_AreExactDeltasOfBuyer(t *testing.T) {
	buyer := CapabilitiesFor(string(RoleBuyer))

	wantPM := buyer
	wantPM.PayInvoices = false
	wantPM.ManagePaymentMethods = false
	if got := CapabilitiesFor(string(RoleBuyerNoPay)); got != wantPM {
		t.Errorf("buyer_no_pay must be buyer minus {PayInvoices, ManagePaymentMethods}\n got: %+v\nwant: %+v", got, wantPM)
	}

	wantAP := buyer
	wantAP.SubmitRFQ = false
	wantAP.CreateOrders = false
	wantAP.EditDeliveryInstructions = false
	if got := CapabilitiesFor(string(RoleAPFinance)); got != wantAP {
		t.Errorf("ap_finance must be buyer minus {SubmitRFQ, CreateOrders, EditDeliveryInstructions}\n got: %+v\nwant: %+v", got, wantAP)
	}
}

// The one-sentence version of the personas, asserted directly: PM buys but cannot pay; A/P pays but
// cannot buy. Neither may manage the roster, that stays an Owner power.
func TestSplitBuyerRoles_PersonaBoundaries(t *testing.T) {
	pm := CapabilitiesFor(string(RoleBuyerNoPay))
	if !pm.CreateOrders || !pm.SubmitRFQ {
		t.Errorf("buyer_no_pay must be able to buy: %+v", pm)
	}
	if pm.PayInvoices || pm.ManagePaymentMethods {
		t.Errorf("buyer_no_pay must NOT be able to pay: %+v", pm)
	}

	ap := CapabilitiesFor(string(RoleAPFinance))
	if !ap.PayInvoices || !ap.ManagePaymentMethods {
		t.Errorf("ap_finance must be able to pay: %+v", ap)
	}
	if ap.SubmitRFQ || ap.CreateOrders {
		t.Errorf("ap_finance must NOT be able to buy: %+v", ap)
	}

	if pm.ManageUsers || ap.ManageUsers {
		t.Error("neither split-buyer persona may manage the company's users")
	}
}

// The invite + role-change paths accept the new personas because validRole is the single gate both
// run through. This test proves that gate widened, and that it did not widen to "anything".
func TestValidRole_AcceptsTheNewPersonasAndNothingElse(t *testing.T) {
	for _, ok := range []string{"account_admin", "buyer", "buyer_no_pay", "ap_finance", "field_crew"} {
		if !validRole(ok) {
			t.Errorf("validRole(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "pm", "ap", "owner", "BUYER_NO_PAY", "admin"} {
		if validRole(bad) {
			t.Errorf("validRole(%q) = true, want false", bad)
		}
	}
}

// An account_admin may invite a PM and an A/P, and may move an existing teammate onto either role.
func TestInviteAndRoleChange_AcceptTheNewPersonas(t *testing.T) {
	for _, role := range []string{string(RoleBuyerNoPay), string(RoleAPFinance)} {
		store := newFakeStore()
		store.addUser(User{ID: "admin", CustomerID: "c1", Email: "a@x.test", Role: "account_admin", Status: "active"})
		store.addUser(User{ID: "u2", CustomerID: "c1", Email: "b@x.test", Role: "field_crew", Status: "active"})
		svc := NewService(store, testSecret)
		admin := PortalContext{UserID: "admin", CustomerID: "c1", Role: "account_admin"}

		if _, err := svc.Invite(context.Background(), admin, "new@x.test", role); err != nil {
			t.Fatalf("invite as %s: %v", role, err)
		}
		if err := svc.UpdateRole(context.Background(), admin, "u2", role); err != nil {
			t.Fatalf("update role to %s: %v", role, err)
		}
		if got, _ := store.GetUser(context.Background(), "u2"); got.Role != role {
			t.Fatalf("role not applied: got %q want %q", got.Role, role)
		}
	}
}

// THE GUARDRAIL. Demoting the last active account_admin is refused whatever the destination role
// is, including the two new ones. A new role that could be used as a back door around this check
// would leave an account with nobody able to manage it, which is unrecoverable without staff.
func TestLastAdminGuardrail_HoldsAgainstTheNewPersonas(t *testing.T) {
	for _, dest := range []string{string(RoleBuyer), string(RoleBuyerNoPay), string(RoleAPFinance), string(RoleFieldCrew)} {
		store := newFakeStore()
		store.addUser(User{ID: "only-admin", CustomerID: "c1", Email: "a@x.test", Role: "account_admin", Status: "active"})
		svc := NewService(store, testSecret)
		admin := PortalContext{UserID: "only-admin", CustomerID: "c1", Role: "account_admin"}

		if err := svc.UpdateRole(context.Background(), admin, "only-admin", dest); err == nil {
			t.Fatalf("demoting the last admin to %q must be refused", dest)
		}
		// And deactivation stays refused too, the other half of the same invariant.
		if err := svc.UpdateStatus(context.Background(), admin, "only-admin", "inactive"); err == nil {
			t.Fatal("deactivating the last admin must be refused")
		}
		if got, _ := store.GetUser(context.Background(), "only-admin"); got.Role != "account_admin" || got.Status != "active" {
			t.Fatalf("the last admin must be untouched, got %+v", got)
		}
	}
}
