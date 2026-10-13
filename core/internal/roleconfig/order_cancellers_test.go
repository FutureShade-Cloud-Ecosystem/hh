package roleconfig

// order_cancellers_test.go, who may cancel an order and keep a special order's supplier deposit: the
// Sales Manager, the Branch Manager and the General Manager (the principal's ruling, row 1579),
// written out longhand. The super admin joins it at the gate.
import "testing"

func TestDefaultOrderCancellers(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"general_manager", "branch_manager", "sales_manager"}
	got := cfg.OrderCancellers()
	on := map[string]bool{}
	for _, r := range got {
		on[r] = true
	}
	if len(got) != len(want) {
		t.Fatalf("order cancellers = %v, want the three %v", got, want)
	}
	for _, role := range Roles() {
		listed := role == "sales_manager" || role == "branch_manager" || role == "general_manager"
		if on[role] != listed {
			t.Errorf("%s on the list = %v, want %v", role, on[role], listed)
		}
	}
}
