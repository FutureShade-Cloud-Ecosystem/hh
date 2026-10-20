package assist

import "testing"

func TestRegistry_EveryDeclaredViewColumnIsRegisteredAndNoneIsSecret(t *testing.T) {
	reg := RegisteredColumns()
	for _, col := range RawViewColumns() {
		class, ok := reg[col]
		if !ok {
			t.Errorf("view column %s is not in the classification registry", col)
		}
		if class == ClassSecret {
			t.Errorf("view column %s is registered as secret: a secret column is in no view", col)
		}
	}
	for col, class := range reg {
		switch class {
		case ClassPlain, ClassCost, ClassMargin, ClassPayment, ClassPIIContact, ClassPIILoc:
		default:
			t.Errorf("registry entry %s has class %q, which is not a view column class", col, class)
		}
	}
}

// A column nobody registered is secret, so it is in no asker's catalogue: a forgotten entry hides a column and
// never shows one.
func TestRegistry_AnUnregisteredColumnIsSecretAndAbsentFromEveryCatalogue(t *testing.T) {
	v := rawOrdersView()
	v.Columns = append(v.Columns, ColumnDef{Name: "forgotten_column", Label: "Forgotten", Type: typeText, Filter: true, Group: true})
	v = classify(v)
	forgotten, _ := v.col("forgotten_column")
	if forgotten.Class != ClassSecret {
		t.Fatalf("an unregistered column has class %q, want secret", forgotten.Class)
	}
	for _, c := range []Caller{dqManager, {UserID: "u", Tier: 1, Global: true, IsAdmin: true}} {
		for _, fv := range filterCatalogue([]ViewDef{v}, c) {
			if _, ok := fv.col("forgotten_column"); ok {
				t.Errorf("the unregistered column reached the catalogue of tier %d", c.Tier)
			}
		}
	}
	// And the class check names no class for it: it is simply unknown.
	sp, err := ParseSpec(`{"view":"ai.orders","select":["forgotten_column"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(sp, []ViewDef{v}, filterCatalogue([]ViewDef{v}, dqManager), dqToronto, dqClock); err == nil {
		t.Error("a query for an unregistered column compiled")
	}
}
