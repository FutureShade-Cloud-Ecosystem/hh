package assist

import (
	"strings"
	"testing"
)

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

// The cost class is held by a caller with at least one cost branch and by no one else, and the tool
// description the model reads follows the same catalogue.
func TestRegistry_TheCostColumnIsInTheCatalogueAndTheToolTextOnlyForACallerWithCostBranches(t *testing.T) {
	const a = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	hasCost := func(c Caller) (inCatalogue, inText bool) {
		for _, v := range CatalogueFor(c) {
			if _, ok := v.Column("avg_cost_cents"); ok {
				inCatalogue = true
			}
		}
		return inCatalogue, strings.Contains(describeDataQuery(c), "avg_cost_cents")
	}
	for name, c := range map[string]struct {
		c    Caller
		want bool
	}{
		"cost at one branch":             {Caller{UserID: "u", Tier: 2, BranchIDs: []string{a}, CostBranchIDs: []string{a}, Margin: true}, true},
		"margin flag but no cost branch": {Caller{UserID: "u", Tier: 2, BranchIDs: []string{a}, Margin: true}, false},
		"reach, no cost":                 {Caller{UserID: "u", Tier: 2, BranchIDs: []string{a}}, false},
		"no role":                        {Caller{UserID: "u", CostBranchIDs: []string{a}}, false},
	} {
		in, text := hasCost(c.c)
		if in != c.want || text != c.want {
			t.Errorf("%s: catalogue has cost = %v, tool text names it = %v, want %v", name, in, text, c.want)
		}
	}
	if c, ok := classOfView("ai.stock_balances", "avg_cost_cents"); !ok || c != ClassCost {
		t.Errorf("avg_cost_cents is class %q", c)
	}
}

func classOfView(view, col string) (string, bool) {
	c, ok := RegisteredColumns()[view+"."+col]
	return c, ok
}
