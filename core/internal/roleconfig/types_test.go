package roleconfig

import "testing"

// The vocabulary is the product's shape: the catalog's thirteen staff
// roles and the ten internal surfaces (ROLE-ACCESS-r3). A configuration
// can name nothing outside it.
func TestVocabulary(t *testing.T) {
	roles := Roles()
	if len(roles) != 13 {
		t.Fatalf("the vocabulary holds %d roles, wanted 13", len(roles))
	}
	surfaces := Surfaces()
	if len(surfaces) != 10 {
		t.Fatalf("the vocabulary holds %d surfaces, wanted 10", len(surfaces))
	}
	for _, role := range []string{"inside_sales", "sales_manager", "outside_sales", "yard_team",
		"yard_manager", "logistics_manager", "driver", "branch_manager", "procurement_manager",
		"payables_receivables", "financial_controller", "general_manager", "hr"} {
		if !IsRole(role) {
			t.Errorf("IsRole(%q) is false, the record names the role", role)
		}
	}
	for _, surface := range []string{"counter-till", "quote-desk", "yard-pick", "drive-run",
		"dispatch-board", "buying-and-stock", "money-desk", "manager-home", "house-messages",
		"dealer-admin"} {
		if !IsSurface(surface) {
			t.Errorf("IsSurface(%q) is false, the record names the surface", surface)
		}
	}
	if IsRole("sales_rep") || IsRole("") {
		t.Error("IsRole accepts a name outside the thirteen")
	}
	if IsSurface("calendar") || IsSurface("") {
		t.Error("IsSurface accepts a name outside the ten")
	}
	if !IsRole(RoleHR) {
		t.Error("RoleHR is not in the vocabulary")
	}
	roles[0] = "changed"
	if Roles()[0] == "changed" {
		t.Error("Roles handed out the vocabulary itself; a caller changed it")
	}
}

// The levels rank, so a guard can ask whether a role's level reaches a
// route's level.
func TestLevelAtLeast(t *testing.T) {
	if !LevelWork.AtLeast(LevelRead) {
		t.Error("work does not reach read")
	}
	if !LevelAdmin.AtLeast(LevelNone) {
		t.Error("admin does not reach none")
	}
	if LevelRead.AtLeast(LevelWork) {
		t.Error("read reaches work")
	}
	if LevelNone.AtLeast(LevelRead) {
		t.Error("none reaches read")
	}
}

// An unknown or empty level reaches nothing and is reached by nothing:
// AtLeast never answers true for one, whichever side it stands on (the
// wall X1's primitive must not fail open on a typo).
func TestLevelAtLeastRefusesUnknown(t *testing.T) {
	if Level("mange").AtLeast(LevelNone) {
		t.Error("an unknown level reaches none")
	}
	if LevelRead.AtLeast(Level("mange")) {
		t.Error("read reaches an unknown level")
	}
	if Level("").AtLeast(LevelNone) {
		t.Error("the empty level reaches none")
	}
	if LevelAdmin.AtLeast(Level("")) {
		t.Error("admin reaches the empty level")
	}
	if !LevelNone.AtLeast(LevelNone) {
		t.Error("none does not reach none")
	}
}
