package pricing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// pricefile_money_test.go, the acceptance clause "a clerk role sees no cost bytes anywhere in the
// import payloads", tested the only way that claim can be tested: on the marshalled bytes.
//
// EVERY TEST HERE CARRIES A POSITIVE CONTROL, and that is not ceremony. A byte scan that finds
// nothing is indistinguishable from a byte scan that was pointed at nothing, an empty preview, a
// field that was renamed, a fixture whose cost column never parsed. So each test marshals the
// ALLOWED caller's payload FIRST, asserts the cost bytes ARE there, and only then asserts that the
// refused caller's payload does not contain them.
//
// AND IT LOOKS FOR THE LITERAL `null`. Three separate trains on this codebase have found the same
// bug: a gated pointer without omitempty marshals as `"cost_cents": null`, which is byte-absent of
// the NUMBER and byte-present of the ADMISSION that a number exists. `null` is not absence.

// costFixtureCSV is the file every test here shares. TWO rows, and it takes two: a cost-anchored row
// is the only one that carries a markup, and a trade-anchored row is the only one that carries an
// estimated freight, so a one-row fixture can only ever positively control half the gated struct.
//
// The numbers are chosen so no gated value is a substring of an ungated one. Cost 1500 and trade 2000
// must not appear in a refused payload, and the retail figures a clerk IS allowed (1234, 1357, 2775,
// 2045) and their variances share no digits with them. A fixture where cost happened to equal the
// list price would make this whole file pass by coincidence.
const costFixtureCSV = "sku,cost,retail,trade\n" +
	"DBS-TB-BLU60,15.00,24.00,\n" +
	"DBS-TB-TRADED,,30.00,20.00\n"

func costHarness(t *testing.T) *pfHarness {
	t.Helper()
	h := newPFHarness(t)
	blu, traded := int64(1234), int64(1357)
	h.stock("DBS-TB-BLU60", "Blu 60 Smooth", &blu)
	h.stock("DBS-TB-TRADED", "Aberdeen", &traded)
	return h
}

// scanBody is the payload with its DIGEST blanked, and only then scanned for values.
//
// The digest is 64 hex characters, so "1500" and "2000" appear in it by chance roughly one run in a
// few thousand. A gate that fails one morning a year for a reason nobody can reproduce is a gate
// people delete, so the one field that is deliberately random is removed before any value is looked
// for. Every KEY assertion still runs against the full payload.
func scanBody(t *testing.T, pv PriceFilePreview) string {
	t.Helper()
	pv.Digest = ""
	raw, err := json.Marshal(pv)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func previewJSON(t *testing.T, h *pfHarness, userID string, mapping map[string]string) ([]byte, PriceFilePreview) {
	t.Helper()
	in := PriceFileInput{VendorID: vendorTecho, UserID: userID, FileName: "techo.csv", Data: []byte(costFixtureCSV), Mapping: mapping}
	pv, err := h.svc.PreviewPriceFile(context.Background(), in)
	if err != nil {
		t.Fatalf("preview as %q: %v", userID, err)
	}
	raw, err := json.Marshal(pv)
	if err != nil {
		t.Fatal(err)
	}
	return raw, pv
}

// costMarkers are the strings that must all be present for an allowed caller and all absent for a
// refused one. The VALUES are in the list, not only the field names: a payload could rename
// `file_cost_cents` tomorrow and this gate would still catch the 1000 going out the door.
var costKeyMarkers = []string{
	`"costs"`,
	`"file_cost_cents"`,
	`"file_trade_cents"`,
	`"markup_bps"`,
	`"fob_freight_cents"`,
}

// The VALUES, which is the half that survives a field rename.
var costValueMarkers = []string{`1500`, `2000`, `18500`}

// - 1 · the manager sees cost; the clerk's bytes do not contain it -

func TestPriceFilePreviewCostIsByteAbsentForAClerk(t *testing.T) {
	h := costHarness(t)
	mapping := map[string]string{MapSKU: "sku", MapCost: "cost", MapRetail: "retail", MapTrade: "trade"}

	// POSITIVE CONTROL FIRST. If the manager's payload does not carry these bytes there is nothing to
	// hide, and the clerk assertion below would pass against a broken fixture.
	mgrRaw, mgrPv := previewJSON(t, h, "mgr", mapping)
	if !mgrPv.CostsVisible {
		t.Fatal("the manager was not admitted to cost, the positive control is not controlling")
	}
	mgrBody := scanBody(t, mgrPv)
	for _, marker := range append(append([]string{}, costKeyMarkers...), costValueMarkers...) {
		if !strings.Contains(mgrBody, marker) {
			t.Fatalf("the ALLOWED caller's payload is missing %s, this scan is looking at nothing\n%s", marker, mgrBody)
		}
	}
	_ = mgrRaw

	clerkRaw, clerkPv := previewJSON(t, h, "clerk", mapping)
	if clerkPv.CostsVisible {
		t.Fatal("a clerk was admitted to cost under MANAGER_UP")
	}
	clerkBody := scanBody(t, clerkPv)
	for _, marker := range costKeyMarkers {
		if strings.Contains(string(clerkRaw), marker) {
			t.Fatalf("a clerk's payload carries %s\n%s", marker, clerkRaw)
		}
	}
	for _, marker := range costValueMarkers {
		if strings.Contains(clerkBody, marker) {
			t.Fatalf("a clerk's payload carries the cost value %s\n%s", marker, clerkBody)
		}
	}
	// `null` IS NOT ABSENCE. A gated pointer that lost its omitempty would sail past every assertion
	// above and fail this one.
	if strings.Contains(string(clerkRaw), "null") {
		t.Fatalf("a clerk's payload contains the literal null, a gated field lost its omitempty\n%s", clerkRaw)
	}
	// And the diff itself still works for the clerk: the refusal is about cost, not about the screen.
	if clerkPv.Counts.Rows != 2 || clerkPv.Counts.Changed != 2 {
		t.Fatalf("the clerk's diff is degraded, not just gated: %+v", clerkPv.Counts)
	}
	for _, l := range clerkPv.Lines {
		if l.NewRetailCents == nil || l.CurrentRetailCents == nil || l.DerivedFrom == "" {
			t.Fatalf("a clerk lost a NON-cost figure they are entitled to: %+v", l)
		}
	}
}

// - 2 · every arm of the dealer's own switch -

func TestPriceFileCostVisibilityHonoursEveryPolicyArm(t *testing.T) {
	cases := []struct {
		name       string
		visibility string
		user       string
		// rewire replaces the role seam. Nil leaves the harness's own seam (mgr is a manager, anybody
		// else is not), which is the production shape; the two that set it are the two failure modes
		// the seam has, never wired, and wired but unable to answer.
		rewire func(*pfHarness)
		want   bool
	}{
		{name: "MANAGER_UP admits a manager", visibility: "MANAGER_UP", user: "mgr", want: true},
		{name: "MANAGER_UP refuses a clerk", visibility: "MANAGER_UP", user: "clerk", want: false},
		{name: "ALL_STAFF admits a clerk", visibility: "ALL_STAFF", user: "clerk", want: true},
		{name: "HIDDEN refuses even a manager", visibility: "HIDDEN", user: "mgr", want: false},
		{name: "MANAGER_UP refuses an anonymous caller", visibility: "MANAGER_UP", user: "", want: false},
		{
			name: "an unwired role seam refuses everyone", visibility: "MANAGER_UP", user: "mgr", want: false,
			rewire: func(h *pfHarness) { h.svc.WithCostVisibility(nil) },
		},
		{
			name: "a role seam that errors refuses", visibility: "MANAGER_UP", user: "mgr", want: false,
			rewire: func(h *pfHarness) { h.svc.WithCostVisibility(&fakeCostVisibility{err: errors.New("identity down")}) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := costHarness(t)
			h.store.visibility = tc.visibility
			if tc.rewire != nil {
				tc.rewire(h)
			}
			raw, pv := previewJSON(t, h, tc.user, map[string]string{MapSKU: "sku", MapCost: "cost"})
			if pv.CostsVisible != tc.want {
				t.Fatalf("costs_visible = %v, want %v", pv.CostsVisible, tc.want)
			}
			has := strings.Contains(string(raw), `"file_cost_cents"`)
			if has != tc.want {
				t.Fatalf("cost bytes present = %v, want %v\n%s", has, tc.want, raw)
			}
			if !tc.want && strings.Contains(string(raw), "null") {
				t.Fatalf("a refused payload contains the literal null\n%s", raw)
			}
		})
	}
}

// - 3 · the apply result and the audit list are payloads too -

// THE PREVIEW IS THE ONE EVERYBODY REMEMBERS. These are the two everybody forgets: the object apply
// hands back, and the audit list the screen shows afterwards. Both are reachable by a clerk (the run
// list is behind the read guard) and both would be a leak.
func TestNoCostBytesInTheApplyResultOrTheAuditList(t *testing.T) {
	h := costHarness(t)
	mapping := map[string]string{MapSKU: "sku", MapCost: "cost", MapRetail: "retail"}
	_, pv := previewJSON(t, h, "mgr", mapping)

	in := PriceFileInput{VendorID: vendorTecho, UserID: "mgr", FileName: "techo.csv",
		Data: []byte(costFixtureCSV), Mapping: mapping, Digest: pv.Digest}
	res, err := h.svc.ApplyPriceFile(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	resRaw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	// POSITIVE CONTROL: the result really does describe this import, so a clean scan means something.
	if !strings.Contains(string(resRaw), `"staged"`) || res.Counts.Changed != 2 || res.Staged != 2 {
		t.Fatalf("the apply result is not describing the import: %s", resRaw)
	}
	for _, marker := range []string{`"file_cost_cents"`, `"costs"`, `"markup_bps"`} {
		if strings.Contains(string(resRaw), marker) {
			t.Fatalf("the apply result carries %s\n%s", marker, resRaw)
		}
	}

	runs, err := h.svc.PriceImportRuns(context.Background(), vendorTecho, 10)
	if err != nil {
		t.Fatal(err)
	}
	runsRaw, err := json.Marshal(runs)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || !strings.Contains(string(runsRaw), `"techo.csv"`) {
		t.Fatalf("the audit list is not describing the import: %s", runsRaw)
	}
	for _, marker := range []string{`"file_cost_cents"`, `"costs"`, `"markup_bps"`, `"cost_cents"`} {
		if strings.Contains(string(runsRaw), marker) {
			t.Fatalf("the audit list carries %s\n%s", marker, runsRaw)
		}
	}
}

// - 4 · the gate has exactly one door -

// A SOURCE SCAN, because the property being pinned is structural: as long as there is exactly ONE
// constructor of a PriceFileCosts, "is this caller allowed cost" is one decision made in one place.
// A second constructor is a second decision, and the second one is the one that gets forgotten.
//
// Comments are stripped first, this file and the model's header both QUOTE the constructor, and a
// gate that counts its own documentation is a gate that fails for the wrong reason.
func TestExactlyOneConstructorOfTheGatedCostStruct(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	blockComment := regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment := regexp.MustCompile(`(?m)^\s*//.*$`)
	total, seenIn := 0, []string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, rerr := os.ReadFile(f)
		if rerr != nil {
			t.Fatal(rerr)
		}
		src := lineComment.ReplaceAllString(blockComment.ReplaceAllString(string(raw), ""), "")
		n := strings.Count(src, "&PriceFileCosts{") + strings.Count(src, "PriceFileCosts{")
		// The composite-literal count above double-counts `&PriceFileCosts{`; normalise to the number
		// of literals actually written.
		n = strings.Count(src, "PriceFileCosts{")
		if n > 0 {
			total += n
			seenIn = append(seenIn, f)
		}
	}
	// GUARD THE GUARD: the scan must have found the package and the one literal. A glob that matched
	// nothing, or a rename, would otherwise report a serene zero.
	if len(files) < 10 {
		t.Fatalf("only %d Go files scanned, this gate is looking at the wrong directory", len(files))
	}
	if total != 1 {
		t.Fatalf("found %d PriceFileCosts literals in %v, want exactly 1 (costsFor in pricefile_model.go)", total, seenIn)
	}
	if len(seenIn) != 1 || seenIn[0] != "pricefile_model.go" {
		t.Fatalf("the one cost constructor lives in %v, want pricefile_model.go", seenIn)
	}
}

// - 5 · a guess never chooses the pricing basis -

// Not a money-absence test, but the same class of defect: a number changing without anybody deciding
// it should. guessMapping is not allowed to propose the trade column, because the trade column is the
// highest-precedence derivation anchor and proposing it reprices the whole file on a heuristic.
func TestTheMappingGuessNeverProposesTheTradeColumn(t *testing.T) {
	headers := []string{"SKU", "Dealer Cost", "Suggested Retail", "Trade Price", "Contractor"}
	got := guessMapping(headers)
	// POSITIVE CONTROL: the guesser is working, or its silence on trade proves nothing.
	if got[MapSKU] != "SKU" || got[MapCost] != "Dealer Cost" || got[MapRetail] != "Suggested Retail" {
		t.Fatalf("the guesser found nothing to guess: %+v", got)
	}
	if v, ok := got[MapTrade]; ok {
		t.Fatalf("the guess proposed trade_col = %q, it may fill a column, never choose the arithmetic", v)
	}
}
