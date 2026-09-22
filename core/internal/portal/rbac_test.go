package portal

import "testing"

func TestCapabilitiesFor(t *testing.T) {
	cases := []struct {
		role string
		want Capabilities
	}{
		{"account_admin", Capabilities{
			ManageUsers: true, ViewBilling: true, PayInvoices: true, ManagePaymentMethods: true,
			SubmitRFQ: true, CreateOrders: true, ViewOrdersDeliveries: true,
			EditDeliveryInstructions: true, ManageFiles: true,
		}},
		{"buyer", Capabilities{
			ViewBilling: true, PayInvoices: true, ManagePaymentMethods: true,
			SubmitRFQ: true, CreateOrders: true, ViewOrdersDeliveries: true,
			EditDeliveryInstructions: true, ManageFiles: true,
		}},
		{"field_crew", Capabilities{ViewOrdersDeliveries: true, EditDeliveryInstructions: true}},
		{"nonsense", Capabilities{}}, // unknown grants nothing
	}
	for _, c := range cases {
		if got := CapabilitiesFor(c.role); got != c.want {
			t.Errorf("CapabilitiesFor(%q) = %+v, want %+v", c.role, got, c.want)
		}
	}
}

// Buyer must never hold ManageUsers (team management is an Owner power) and field_crew
// must never see billing or move money, the two guardrails the route guards lean on.
func TestCapabilitiesFor_RoleBoundaries(t *testing.T) {
	if CapabilitiesFor("buyer").ManageUsers {
		t.Error("buyer must not have ManageUsers")
	}
	fc := CapabilitiesFor("field_crew")
	if fc.ViewBilling || fc.PayInvoices || fc.ManagePaymentMethods || fc.ManageUsers || fc.SubmitRFQ || fc.CreateOrders {
		t.Errorf("field_crew must hold no financial/team/procurement caps, got %+v", fc)
	}
}
