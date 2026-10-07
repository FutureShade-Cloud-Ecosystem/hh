package roleconfig

// low_margin_report_test.go, who reads the low margin report: the managers who clear a floor and the
// roles above them (the floor lists the quote flow reads), held to the cost and margin list, because
// a margin beside a price is the cost in another form.
import (
	"encoding/json"
	"sort"
	"testing"
)

func readers(t *testing.T, cfg *Config) []string {
	t.Helper()
	got := cfg.LowMarginReportReaders()
	sort.Strings(got)
	return got
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDefaultLowMarginReportReaders(t *testing.T) {
	cfg, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"branch_manager", "financial_controller", "general_manager", "sales_manager"}
	if got := readers(t, cfg); !equalStrings(got, want) {
		t.Fatalf("readers = %v, want %v", got, want)
	}
	for _, role := range want {
		if ok, err := cfg.ReadsLowMarginReport(role); err != nil || !ok {
			t.Errorf("%s must read the report: %v %v", role, ok, err)
		}
	}
	for _, role := range []string{"inside_sales", "outside_sales", "yard_manager", "logistics_manager", "procurement_manager", "payables_receivables", "yard_team", "hr"} {
		if ok, err := cfg.ReadsLowMarginReport(role); err != nil || ok {
			t.Errorf("%s must not read the report: %v %v", role, ok, err)
		}
	}
	if ok, err := cfg.ReadsLowMarginReport("not_a_role"); err == nil || ok {
		t.Errorf("an unknown role is false with an error, got %v %v", ok, err)
	}
}

// A role off the cost and margin list never reads the report, whatever floor list it is on.
func TestLowMarginReportReadersAreHeldToCostVisibility(t *testing.T) {
	doc := DefaultDocument(t)
	var vis []string
	if err := json.Unmarshal(doc.Settings[SettingCostVisibility], &vis); err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, r := range vis {
		if r != "sales_manager" && r != "financial_controller" {
			kept = append(kept, r)
		}
	}
	raw, _ := json.Marshal(kept)
	doc.Settings[SettingCostVisibility] = raw
	cfg, err := Validate(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"branch_manager", "general_manager"}
	if got := readers(t, cfg); !equalStrings(got, want) {
		t.Fatalf("with the Sales manager and the Financial Controller off the cost list, readers = %v, want %v", got, want)
	}
}

// The readers follow the configuration's floor lists, never a constant: the floor clearers setting
// off drops the branch manager (a clearer only), the Sales manager stays through the branch approvers
// list; a role added to the floor approvers list reads the report.
func TestLowMarginReportReadersFollowTheFloorLists(t *testing.T) {
	doc := DefaultDocument(t)
	var settings map[string]json.RawMessage = doc.Settings
	settings[SettingManagerOwnBranchFloorClearing] = json.RawMessage(`false`)
	cfg, err := Validate(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"financial_controller", "general_manager", "sales_manager"}
	if got := readers(t, cfg); !equalStrings(got, want) {
		t.Fatalf("clearing off: readers = %v, want %v", got, want)
	}

	doc = DefaultDocument(t)
	doc.Lists.ExceptionApprovers[ExceptionFloorQuote] = append(doc.Lists.ExceptionApprovers[ExceptionFloorQuote], "procurement_manager")
	cfg, err = Validate(doc)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := cfg.ReadsLowMarginReport("procurement_manager"); !ok {
		t.Errorf("a role added to the floor approvers reads the report: %v", readers(t, cfg))
	}
}
