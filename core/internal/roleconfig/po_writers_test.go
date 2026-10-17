package roleconfig

// po_writers_test.go, who may write a purchase order: the Procurement Manager and the General
// Manager (the principal's ruling, row 1303), written out longhand. The yard manager keeps
// receiving only and is not on the list; the platform super admin joins it at the gate.
import "testing"

func TestDefaultPurchaseOrderWriters(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"procurement_manager", "general_manager"}
	if got := cfg.PurchaseOrderWriters(); !equalStrings(got, want) {
		t.Fatalf("purchase order writers = %v, want %v", got, want)
	}
	for _, role := range Roles() {
		on := false
		for _, w := range cfg.PurchaseOrderWriters() {
			on = on || w == role
		}
		if (role == "procurement_manager" || role == "general_manager") != on {
			t.Errorf("%s on the list = %v", role, on)
		}
	}
}
