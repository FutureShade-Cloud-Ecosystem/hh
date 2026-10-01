package roleconfig

import (
	"encoding/json"
	"testing"
)

func mustDefault(t *testing.T) *Config {
	t.Helper()
	cfg, err := Default()
	if err != nil {
		t.Fatalf("the embedded default does not load: %v", err)
	}
	return cfg
}

func TestDefaultLoadsAndHolds130Cells(t *testing.T) {
	cfg := mustDefault(t)
	cells := cfg.Cells()
	if len(cells) != 130 {
		t.Fatalf("the default holds %d cells, wanted exactly 130", len(cells))
	}
	seen := make(map[cellKey]bool, 130)
	for _, cell := range cells {
		key := cellKey{role: cell.Role, surface: cell.Surface}
		if seen[key] {
			t.Fatalf("duplicate cell for %s on %s", cell.Role, cell.Surface)
		}
		seen[key] = true
	}
	if len(seen) != 13*10 {
		t.Fatalf("the cells cover %d role and surface pairs, wanted 130", len(seen))
	}
}

// The cycle's two demo roles (D49: the Counter Till shows a Sales and a
// Sales manager login), every one of their ten cells, against the decided
// values.
func TestDefaultDemoRolesCellByCell(t *testing.T) {
	cfg := mustDefault(t)
	want := []struct {
		role    string
		surface string
		level   Level
	}{
		{"inside_sales", "counter-till", LevelWork},
		{"inside_sales", "quote-desk", LevelWork},
		{"inside_sales", "yard-pick", LevelNone},
		{"inside_sales", "drive-run", LevelNone},
		{"inside_sales", "dispatch-board", LevelNone},
		{"inside_sales", "buying-and-stock", LevelRead},
		{"inside_sales", "money-desk", LevelRead},
		{"inside_sales", "manager-home", LevelNone},
		{"inside_sales", "house-messages", LevelWork},
		{"inside_sales", "dealer-admin", LevelNone},
		{"sales_manager", "counter-till", LevelManage},
		{"sales_manager", "quote-desk", LevelManage},
		{"sales_manager", "yard-pick", LevelNone},
		{"sales_manager", "drive-run", LevelNone},
		{"sales_manager", "dispatch-board", LevelRead},
		{"sales_manager", "buying-and-stock", LevelRead},
		{"sales_manager", "money-desk", LevelRead},
		{"sales_manager", "manager-home", LevelWork},
		{"sales_manager", "house-messages", LevelWork},
		{"sales_manager", "dealer-admin", LevelManage},
	}
	for _, w := range want {
		got, err := cfg.Level(w.role, w.surface)
		if err != nil {
			t.Fatalf("Level(%s, %s): %v", w.role, w.surface, err)
		}
		if got != w.level {
			t.Errorf("%s on %s is %s, decided %s", w.role, w.surface, got, w.level)
		}
	}
}

// Spot cells across the rest of the matrix, each against the decided
// value; the full matrix is held cell by cell in matrix_test.go.
func TestDefaultSpotCells(t *testing.T) {
	cfg := mustDefault(t)
	spots := []struct {
		role    string
		surface string
		want    Level
	}{
		{"hr", "money-desk", LevelNone},
		{"hr", "dealer-admin", LevelAdmin},
		{"hr", "house-messages", LevelWork},
		{"driver", "drive-run", LevelWork},
		{"driver", "house-messages", LevelWork},
		{"driver", "counter-till", LevelNone},
		{"yard_team", "yard-pick", LevelWork},
		{"yard_team", "dispatch-board", LevelRead},
		{"yard_team", "money-desk", LevelNone},
		{"yard_manager", "counter-till", LevelWork},
		{"yard_manager", "yard-pick", LevelManage},
		{"branch_manager", "counter-till", LevelManage},
		{"branch_manager", "dispatch-board", LevelManage},
		{"general_manager", "counter-till", LevelRead},
		{"general_manager", "dealer-admin", LevelAdmin},
		{"financial_controller", "money-desk", LevelAdmin},
		{"payables_receivables", "money-desk", LevelWork},
		{"procurement_manager", "buying-and-stock", LevelManage},
		{"outside_sales", "counter-till", LevelNone},
		{"outside_sales", "quote-desk", LevelWork},
		{"logistics_manager", "dispatch-board", LevelManage},
	}
	for _, s := range spots {
		got, err := cfg.Level(s.role, s.surface)
		if err != nil {
			t.Fatalf("Level(%s, %s): %v", s.role, s.surface, err)
		}
		if got != s.want {
			t.Errorf("%s on %s is %s, decided %s", s.role, s.surface, got, s.want)
		}
	}
}

// The default matrix equals the decided record on every one of the 130
// cells (expectedMatrix, carried in matrix_test.go from the record).
func TestDefaultFullMatrix(t *testing.T) {
	cfg := mustDefault(t)
	if len(expectedMatrix) != 130 {
		t.Fatalf("the expected table holds %d cells, wanted 130", len(expectedMatrix))
	}
	for _, want := range expectedMatrix {
		got, err := cfg.Level(want.role, want.surface)
		if err != nil {
			t.Fatalf("Level(%s, %s): %v", want.role, want.surface, err)
		}
		if got != want.level {
			t.Errorf("%s on %s is %s, the record decides %s", want.role, want.surface, got, want.level)
		}
	}
}

// Rule X3: cost and margin go to Inside Sales, Outside Sales and every
// manager and leadership role except HR; hidden from the Yard Team and
// Drivers.
func TestDefaultCostVisibility(t *testing.T) {
	cfg := mustDefault(t)
	want := []string{"inside_sales", "outside_sales", "sales_manager", "yard_manager",
		"logistics_manager", "branch_manager", "procurement_manager", "payables_receivables",
		"financial_controller", "general_manager"}
	got := cfg.CostVisibility()
	if len(got) != len(want) {
		t.Fatalf("the cost and margin list holds %d roles, decided %d", len(got), len(want))
	}
	for i, role := range want {
		if got[i] != role {
			t.Errorf("the cost and margin list at %d is %s, decided %s", i, got[i], role)
		}
	}
	for _, hidden := range []string{"yard_team", "driver", "hr"} {
		seen, err := cfg.SeesCostAndMargin(hidden)
		if err != nil {
			t.Fatalf("SeesCostAndMargin(%s): %v", hidden, err)
		}
		if seen {
			t.Errorf("%s sees cost and margin, the record hides it", hidden)
		}
	}
	if _, err := cfg.SeesCostAndMargin("sales_rep"); err == nil {
		t.Error("SeesCostAndMargin answered for an unknown role")
	}
}

// Rule X4: each of the four exception kinds is approved by the Financial
// Controller and the General Manager (credit and COD by leadership without
// HR), one document per approval.
func TestDefaultExceptionApprovers(t *testing.T) {
	cfg := mustDefault(t)
	want := []string{"financial_controller", "general_manager"}
	for _, kind := range ExceptionKinds() {
		got, err := cfg.ExceptionApprovers(kind)
		if err != nil {
			t.Fatalf("ExceptionApprovers(%s): %v", kind, err)
		}
		if len(got) != len(want) {
			t.Fatalf("%s approvers hold %d roles, decided %d", kind, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s approver at %d is %s, decided %s", kind, i, got[i], want[i])
			}
		}
	}
	if _, err := cfg.ExceptionApprovers("grand_opening"); err == nil {
		t.Error("an unknown exception kind answered with a list and no error")
	}
}

// Rule X5: the Sales Manager and the Branch Manager clear the floor on
// their own quote price in their own branch, the setting on.
func TestDefaultFloorClearing(t *testing.T) {
	cfg := mustDefault(t)
	got := cfg.FloorClearersOwnBranch()
	want := []string{"sales_manager", "branch_manager"}
	if len(got) != len(want) {
		t.Fatalf("the floor clearers hold %d roles, decided %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("floor clearer at %d is %s, decided %s", i, got[i], want[i])
		}
	}
	if !cfg.ManagerOwnBranchFloorClearing() {
		t.Error("manager_own_branch_floor_clearing is off, the record seeds it on")
	}
}

// Rule X11: the Yard Manager, the Logistics Manager and the Branch Manager
// commit a delivery date; the Yard Team commits none.
func TestDefaultDeliveryDateCommitters(t *testing.T) {
	cfg := mustDefault(t)
	got := cfg.DeliveryDateCommitters()
	want := []string{"yard_manager", "logistics_manager", "branch_manager"}
	if len(got) != len(want) {
		t.Fatalf("the delivery date committers hold %d roles, decided %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("delivery date committer at %d is %s, decided %s", i, got[i], want[i])
		}
	}
}

// Rules X6, X7, X11, X13 and rows 352, 353, 457, 360: the nine dealer
// settings, each at its decided default. The refund amount is integer
// cents, 250.00 held as 25000.
func TestDefaultSettings(t *testing.T) {
	cfg := mustDefault(t)
	if !cfg.CreditAtTill() {
		t.Error("credit_at_till is off, the record seeds it shown (row 353)")
	}
	if got := cfg.RefundAmountWithoutApprovalCents(); got != 25000 {
		t.Errorf("the refund amount without approval is %d cents, decided 25000", got)
	}
	gotApprovers := cfg.RefundApprovers()
	wantApprovers := []string{"sales_manager", "branch_manager"}
	if len(gotApprovers) != len(wantApprovers) {
		t.Fatalf("the refund approvers hold %d roles, decided %d", len(gotApprovers), len(wantApprovers))
	}
	for i := range wantApprovers {
		if gotApprovers[i] != wantApprovers[i] {
			t.Errorf("refund approver at %d is %s, decided %s", i, gotApprovers[i], wantApprovers[i])
		}
	}
	for _, role := range []string{"inside_sales", "hr", "yard_team", "driver", "general_manager"} {
		ok, err := cfg.ApprovesRefundAboveAmount(role)
		if err != nil {
			t.Fatalf("ApprovesRefundAboveAmount(%s): %v", role, err)
		}
		if ok {
			t.Errorf("%s approves a refund above the amount, the record does not name it", role)
		}
	}
	for _, role := range wantApprovers {
		ok, err := cfg.ApprovesRefundAboveAmount(role)
		if err != nil || !ok {
			t.Errorf("ApprovesRefundAboveAmount(%s) is %v, %v; wanted true", role, ok, err)
		}
	}
	gotEditors := cfg.PricingEditors()
	wantEditors := []string{"sales_manager", "general_manager"}
	if len(gotEditors) != len(wantEditors) {
		t.Fatalf("the pricing editors hold %d roles, decided %d", len(gotEditors), len(wantEditors))
	}
	for i := range wantEditors {
		if gotEditors[i] != wantEditors[i] {
			t.Errorf("pricing editor at %d is %s, decided %s", i, gotEditors[i], wantEditors[i])
		}
	}
	gotShortage := cfg.ShortageResolvers()
	wantShortage := []string{"yard_team", "yard_manager"}
	if len(gotShortage) != len(wantShortage) {
		t.Fatalf("the shortage resolvers hold %d roles, decided %d", len(gotShortage), len(wantShortage))
	}
	for i := range wantShortage {
		if gotShortage[i] != wantShortage[i] {
			t.Errorf("shortage resolver at %d is %s, decided %s", i, gotShortage[i], wantShortage[i])
		}
	}
	gotDispatch := cfg.DispatchPolicySetters()
	wantDispatch := []string{"logistics_manager", "branch_manager"}
	if len(gotDispatch) != len(wantDispatch) {
		t.Fatalf("the dispatch policy setters hold %d roles, decided %d", len(gotDispatch), len(wantDispatch))
	}
	for i := range wantDispatch {
		if gotDispatch[i] != wantDispatch[i] {
			t.Errorf("dispatch policy setter at %d is %s, decided %s", i, gotDispatch[i], wantDispatch[i])
		}
	}
	if cfg.GeneralManagerSelling() {
		t.Error("general_manager_selling is on, the record seeds it off (row 457)")
	}
}

func TestLevelQueryErrors(t *testing.T) {
	cfg := mustDefault(t)
	if _, err := cfg.Level("sales_rep", "counter-till"); err == nil {
		t.Error("Level answered for an unknown role")
	}
	if _, err := cfg.Level("inside_sales", "calendar"); err == nil {
		t.Error("Level answered for an unknown surface")
	}
	ok, err := cfg.Holds("inside_sales", "counter-till", LevelWork)
	if err != nil || !ok {
		t.Errorf("Holds(inside_sales, counter-till, work) is %v, %v; wanted true", ok, err)
	}
	ok, err = cfg.Holds("inside_sales", "counter-till", LevelManage)
	if err != nil || ok {
		t.Errorf("Holds(inside_sales, counter-till, manage) is %v, %v; wanted false", ok, err)
	}
	ok, err = cfg.Holds("sales_rep", "counter-till", LevelRead)
	if err == nil || ok {
		t.Errorf("Holds answered for an unknown role: %v, %v", ok, err)
	}
}

// The want is read as a string from a route's annotation, so a mistyped or
// empty wanted level is an error from Holds, never a silent admit (the
// wall X1's primitive must fail closed).
func TestHoldsRefusesUnknownWant(t *testing.T) {
	cfg := mustDefault(t)
	for _, want := range []Level{Level("mange"), Level(""), Level("NONE")} {
		ok, err := cfg.Holds("driver", "money-desk", want)
		if err == nil || ok {
			t.Errorf("Holds(driver, money-desk, %q) is %v, %v; wanted an error and false", string(want), ok, err)
		}
	}
}

// The folded predicates are the rules the guards call: each folds its
// setting into its list, on and off, so no guard recombines the two by
// hand (the predicate is the rule; the list is the data).
func TestFoldingPredicates(t *testing.T) {
	withSetting := func(t *testing.T, name string, value string) *Config {
		t.Helper()
		doc := docFromDefault(t)
		doc.Settings[name] = json.RawMessage(value)
		cfg, err := loadDoc(t, doc)
		if err != nil {
			t.Fatalf("the loader refused %s = %s: %v", name, value, err)
		}
		return cfg
	}

	// The default carries the floor clearing on and general_manager_selling
	// off; both predicates read the folded rule.
	cfg := mustDefault(t)
	for _, role := range []string{"sales_manager", "branch_manager"} {
		if ok, err := cfg.ClearsOwnBranchFloor(role); err != nil || !ok {
			t.Errorf("ClearsOwnBranchFloor(%s) is %v, %v; wanted true with the setting on", role, ok, err)
		}
	}
	for _, role := range []string{RoleInsideSales, "yard_manager", "general_manager", RoleHR} {
		if ok, err := cfg.ClearsOwnBranchFloor(role); err != nil || ok {
			t.Errorf("ClearsOwnBranchFloor(%s) is %v, %v; wanted false", role, ok, err)
		}
	}
	for _, role := range []string{"yard_manager", "logistics_manager", "branch_manager"} {
		if ok, err := cfg.CommitsDeliveryDate(role); err != nil || !ok {
			t.Errorf("CommitsDeliveryDate(%s) is %v, %v; wanted true", role, ok, err)
		}
	}
	for _, role := range []string{RoleGeneralManager, "yard_team", RoleInsideSales} {
		if ok, err := cfg.CommitsDeliveryDate(role); err != nil || ok {
			t.Errorf("CommitsDeliveryDate(%s) is %v, %v; wanted false with general_manager_selling off", role, ok, err)
		}
	}

	// Off, the floor clears nobody: the setting is the rule.
	off := withSetting(t, SettingManagerOwnBranchFloorClearing, `false`)
	if ok, err := off.ClearsOwnBranchFloor("sales_manager"); err != nil || ok {
		t.Errorf("ClearsOwnBranchFloor(sales_manager) is %v, %v with the setting off; wanted false", ok, err)
	}
	if ok, err := off.ClearsOwnBranchFloor("branch_manager"); err != nil || ok {
		t.Errorf("ClearsOwnBranchFloor(branch_manager) is %v, %v with the setting off; wanted false", ok, err)
	}
	// On, explicitly, it clears for the two managers again.
	on := withSetting(t, SettingManagerOwnBranchFloorClearing, `true`)
	if ok, _ := on.ClearsOwnBranchFloor("sales_manager"); !ok {
		t.Error("ClearsOwnBranchFloor(sales_manager) is false with the setting on")
	}

	// On, general_manager_selling adds the General Manager and nobody else.
	gmSells := withSetting(t, SettingGeneralManagerSelling, `true`)
	if ok, err := gmSells.CommitsDeliveryDate(RoleGeneralManager); err != nil || !ok {
		t.Errorf("CommitsDeliveryDate(general_manager) is %v, %v with general_manager_selling on; wanted true", ok, err)
	}
	if ok, _ := gmSells.CommitsDeliveryDate("yard_team"); ok {
		t.Error("CommitsDeliveryDate(yard_team) is true; the Yard Team commits no delivery date (X11)")
	}
	if ok, _ := gmSells.CommitsDeliveryDate("logistics_manager"); !ok {
		t.Error("CommitsDeliveryDate(logistics_manager) is false with general_manager_selling on")
	}
	// The list stays the data: it never grows to hold the folded role.
	if committers := gmSells.DeliveryDateCommitters(); len(committers) != 3 {
		t.Errorf("DeliveryDateCommitters holds %d roles with general_manager_selling on, wanted the configured 3", len(committers))
	}

	// An unknown name is an error on both, never a silent answer.
	if _, err := cfg.ClearsOwnBranchFloor("sales_rep"); err == nil {
		t.Error("ClearsOwnBranchFloor answered for an unknown role")
	}
	if _, err := cfg.CommitsDeliveryDate("sales_rep"); err == nil {
		t.Error("CommitsDeliveryDate answered for an unknown role")
	}
}

// The queries hand out copies: a guard that mutates a returned list cannot
// widen another guard's world.
func TestQueriesReturnCopies(t *testing.T) {
	cfg := mustDefault(t)

	approvers := cfg.RefundApprovers()
	approvers[0] = "hr"
	if again := cfg.RefundApprovers(); again[0] != "sales_manager" {
		t.Fatalf("mutating a returned list changed the configuration: refund approver is now %s", again[0])
	}

	visible := cfg.CostVisibility()
	visible[0] = "hr"
	if again, _ := cfg.SeesCostAndMargin("hr"); again {
		t.Error("mutating a returned list put hr on the cost and margin list")
	}
	if again, _ := cfg.SeesCostAndMargin("inside_sales"); !again {
		t.Error("mutating a returned list removed inside_sales from the cost and margin list")
	}

	kind := ExceptionKinds()
	kind[0] = "changed"
	if ExceptionKinds()[0] != ExceptionFloorQuote {
		t.Error("ExceptionKinds handed out the vocabulary itself; a caller changed it")
	}

	cells := cfg.Cells()
	cells[0].Level = LevelAdmin
	if again, _ := cfg.Level("inside_sales", "counter-till"); again != LevelWork {
		t.Errorf("mutating a returned cell list changed a level: inside_sales on counter-till is now %s", again)
	}
}
