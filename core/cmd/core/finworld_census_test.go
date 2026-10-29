//go:build finworld

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// finworldCensusTestPlan builds the world's plan the way every census leg
// sees it.
func finworldCensusTestPlan(t *testing.T) *FinWorldPlanT {
	t.Helper()
	plan, err := FinWorldPlan(1, YearMonth{Year: 2026, Month: 9}, "Quarryfen Hardscape Supply")
	if err != nil {
		t.Fatalf("FinWorldPlan: %v", err)
	}
	return plan
}

// The name rule refuses every id shape the brief names: a trailing counter, an
// id key shape, a UUID, an lc- or pay- prefix, and a run of digits.
func TestFinWorldCensusNameProblems(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"Sandstone Meadow Paving Co. 7", true},
		{"Freight Carrier (lc-s1-0003)", true},
		{"bankrec manual deposit pay-s1-010531", true},
		{"Cairnbrook Masonry Ltd.", false},
		{"The Baird Household", false},
		{"88a20000-0000-4000-8000-0000000000f2", true},
		{"Granite Valley Stone Supply", false},
	}
	for _, tc := range cases {
		if got := finworldNameProblems(tc.name) != ""; got != tc.want {
			t.Errorf("finworldNameProblems(%q) = %t, want %t", tc.name, got, tc.want)
		}
	}
}

// The memo rule refuses engine words and key shapes, and passes a
// bookkeeper's words. The keys themselves stay keys: a reference column is not
// a memo.
func TestFinWorldCensusMemoProblems(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"bankrec manual deposit pay-s1-010531", true},
		{"Card batch settlement cset-s1-00012", true},
		{"FinWorld opening inventory balance TRT", true},
		{"Deposit, Customer EFT receipt", false},
		{"Supplier cheque VINV-1-00042", false},
		{"Monthly yard loader fuel and maintenance accrual", false},
		{"LTL freight on a pallet delivery to the yard", false},
		{"Accrued yard operating expense", false},
	}
	for _, tc := range cases {
		if got := finworldMemoProblems(tc.text) != ""; got != tc.want {
			t.Errorf("finworldMemoProblems(%q) = %t, want %t", tc.text, got, tc.want)
		}
	}
}

// The late payable rule: an open payable more than 90 days past its terms is a
// finding unless the plan names it as intended with a reason; an entry naming
// no real late payable is itself a finding.
func TestFinWorldCensusLatePayables(t *testing.T) {
	plan := finworldCensusTestPlan(t)

	// The world's own landed costs run eleven months, so on the plan as it
	// stands (nothing settles them, nothing is marked intended) the census
	// finds late payables.
	if got := finworldCensusLatePayables(plan); len(got) == 0 {
		t.Fatalf("the plan's own landed costs carry no late payable findings; the census would pass an unpaid books world")
	}

	// Marking one as intended with a reason takes its finding away, but an
	// entry naming no late payable at all is refused.
	plan.IntendedLatePayables = []FinWorldIntendedLatePayable{
		{LandedCostKey: "lc-s1-9999", Reason: "the carrier is disputing the fuel surcharge"},
	}
	got := finworldCensusLatePayables(plan)
	var namedNotFound bool
	for _, f := range got {
		if strings.Contains(f.Why, "does not mark") || strings.Contains(f.Why, "is not one") {
			if strings.Contains(f.LandedCostKey, "9999") {
				namedNotFound = true
			}
		}
	}
	if !namedNotFound {
		t.Fatalf("an intended-late entry that names no late payable must itself be a finding, got %+v", got)
	}
}

// The source scan is not vacuous: on a planted tree it refuses an engine-word
// memo literal, a key interpolated into a memo, and passes clean bookkeeper
// words.
func TestFinWorldCensusSourceScan(t *testing.T) {
	dir := t.TempDir()
	src := `package probing

import "fmt"

const kindWord = "Freight Carrier"

func dirty(lineRef, bounceKey string) {
	_ = struct {
		Memo string
	}{Memo: "bankrec manual deposit " + lineRef}
	_ = map[string]any{"reason": "NSF returned cheque " + bounceKey}
	_ = map[string]any{"variance_notes": "finworld end-of-day count"}
}

func clean() {
	_ = struct {
		Memo string
	}{Memo: "Deposit, Customer EFT receipt"}
	_ = map[string]any{"reason": "Yard petty cash supplies"}
	_ = fmt.Sprintf("Supplier cheque %s", "VINV-1-00042")
}
`
	if err := os.WriteFile(filepath.Join(dir, "dirty.go"), []byte(src), 0600); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	got, err := finworldScanFileMemoPositions(filepath.Join(dir, "dirty.go"))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 source memo findings on the planted file, got %d: %v", len(got), got)
	}
	whys := make([]string, 0, len(got))
	for _, f := range got {
		whys = append(whys, f.Why)
	}
	joined := strings.Join(whys, "; ")
	for _, want := range []string{"engine words", "interpolates the key bounceKey", "engine words"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("the findings must cover %q, got: %s", want, joined)
		}
	}
}

// The census over the world's own plan: the guard that holds after every W2-1d
// item lands. Red on main, where contractor names end in counters, nothing
// settles the landed cost payables and the product code still writes engine
// words into journal memos; the findings the red run lists are the report's
// census section.
func TestFinWorldCensus_PlanReadsLikeARealDealersBooks(t *testing.T) {
	plan := finworldCensusTestPlan(t)
	res, err := finworldCensusPlan(plan, finworldCensusCoreRoot())
	if err != nil {
		t.Fatalf("census: %v", err)
	}
	if res.Clean() {
		return
	}
	var lines []string
	for _, f := range res.Names {
		lines = append(lines, "NAME "+f.String())
	}
	for _, f := range res.Memos {
		lines = append(lines, "MEMO "+f.String())
	}
	for _, f := range res.LatePayables {
		lines = append(lines, "LATE "+f.String())
	}
	for _, f := range res.SourceMemos {
		lines = append(lines, "SOURCE "+f.String())
	}
	t.Errorf("the world's books do not read like a real dealer's:\n%s", strings.Join(lines, "\n"))
}

// finworldCensusCoreRoot locates the core tree the tests run inside.
func finworldCensusCoreRoot() string {
	if root := finworldFindCoreRoot("."); root != "" {
		return root
	}
	return ".."
}
