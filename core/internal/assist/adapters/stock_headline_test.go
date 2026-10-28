package adapters

// stock_headline_test.go, FLAKE-10: the stock lookup's headline check reads the PARSED rows, never
// a bare digit substring over the headline.
//
// The headline names the product and its SKU, and the fixture's product name and SKU both carry
// the clock's own nanosecond fraction (internal/assist/adapters/integration_test.go, the tag is
// time.Now().UnixNano()%1_000_000_000). The headline prints the name AND the SKU
// (internal/assist/commands.go, func productLabel), so a hunt for a bare three digit figure over
// the whole headline was answered by the clock: measured over two million real tags, the tag
// holds "140" in one run in 163. The check that was hunting the figure therefore stopped guarding
// it, and nothing went red: the silent kind of false pass.
//
// The check lives here, untagged, so the unit leg below runs in the untagged suite and the
// integration leg calls the same helper.

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist"
)

// stockHeadlineLeak reports the mismatch between the availability the stock lookup's PARSED rows
// carry and the availability its headline states.
//
// The rows are the source of truth: each branch line carries an "Available" field, and the
// headline must state their sum and the branch count. The headline's figure is matched against
// the rows' sum as a WHOLE phrase, anchored to the sentence's own separator and words ("140 EA
// available across 2 branches."), never as a bare digit substring over the headline: the
// headline carries the product's name and its SKU, and those carry the clock, so a bare hunt over
// the headline can be answered by the clock and stops guarding the figure.
func stockHeadlineLeak(headline string, items []assist.Item) []string {
	var leaks []string
	total := 0.0
	uom := ""
	read := 0
	for _, item := range items {
		qty, unit, ok := availableOf(item)
		if !ok {
			leaks = append(leaks, fmt.Sprintf("the row %q carries no available figure", item.Title))
			continue
		}
		total += qty
		uom = unit
		read++
	}
	if read == 0 {
		return append(leaks, "no row carries an available figure")
	}

	want := fmt.Sprintf(": %s %s available across %d branches.", qtyOf(total), uom, len(items))
	if len(items) == 1 {
		want = fmt.Sprintf(": %s %s available.", qtyOf(total), uom)
	}
	if !strings.Contains(headline, want) {
		leaks = append(leaks, fmt.Sprintf("the headline does not state %q", strings.TrimSpace(want)))
	}
	return leaks
}

// availableOf reads a branch line's available figure and its unit from the PARSED row.
//
// The value is the assistant's own rendering of the figure ("100 EA"), so the number is read out
// of the value and the unit is what is left of it.
func availableOf(item assist.Item) (float64, string, bool) {
	for _, field := range item.Fields {
		if field.Label != "Available" {
			continue
		}
		text := strings.TrimSpace(field.Value)
		number, unit, _ := strings.Cut(text, " ")
		qty, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		if err != nil {
			return 0, "", false
		}
		return qty, strings.TrimSpace(unit), true
	}
	return 0, "", false
}

// qtyOf renders a quantity the way the assistant's own formatter does (internal/assist/format.go,
// func qty): whole numbers print whole, fractions keep at most three decimals with the trailing
// zeros trimmed.
func qtyOf(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	s = strings.TrimSuffix(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}

// TestStockHeadlineLeak_ReadsTheRowsNotTheClock is the unit leg of the check itself.
//
// The first headline states an availability of 100 while its rows carry 140, and the product's
// NAME carries a clock derived tag that spells 140. The old bare digit hunt passed that headline,
// because the digits it found were the name's, not the figure's: a false pass, and the silent
// kind. The fixed check must fail it.
//
// The second headline states the figure the rows carry, with the same clock derived tag in the
// name: the fixed check must pass it.
func TestStockHeadlineLeak_ReadsTheRowsNotTheClock(t *testing.T) {
	rows := []assist.Item{
		{Title: "Trenton", Fields: []assist.Field{
			{Label: "On hand", Value: "120 EA"},
			{Label: "Allocated", Value: "20 EA"},
			{Label: "Available", Value: "100 EA"},
		}},
		{Title: "Kingston", Fields: []assist.Field{
			{Label: "On hand", Value: "40 EA"},
			{Label: "Allocated", Value: "0 EA"},
			{Label: "Available", Value: "40 EA"},
		}},
	}

	// The old check: a bare digit hunt over the whole headline.
	oldHeadline := "M3 Scratch Slab 140000000 (M3SCRATCH-140000000): 100 EA available across 2 branches."
	if !strings.Contains(oldHeadline, "140") || !strings.Contains(oldHeadline, "2 branches") {
		t.Fatal("the crafted headline must hold the digits the old raw hunt read as the availability")
	}
	// The old check passes this headline, and the headline is wrong: the rows carry 140.
	if leaks := stockHeadlineLeak(oldHeadline, rows); len(leaks) == 0 {
		t.Fatal("the check let a headline that states 100 available pass: the digits were the product name's")
	}

	goodHeadline := "M3 Scratch Slab 140000000 (M3SCRATCH-140000000): 140 EA available across 2 branches."
	if leaks := stockHeadlineLeak(goodHeadline, rows); len(leaks) != 0 {
		t.Fatalf("the check must pass the headline that states the rows' figure: %v", leaks)
	}
}

// TestStockHeadlineLeak_ReadsTheOneBranchSentence is the unit leg's neighbour: the one branch
// sentence names the branch instead of counting the branches, and the figure must still be read.
func TestStockHeadlineLeak_ReadsTheOneBranchSentence(t *testing.T) {
	rows := []assist.Item{
		{Title: "Trenton", Fields: []assist.Field{
			{Label: "On hand", Value: "120 EA"},
			{Label: "Allocated", Value: "20 EA"},
			{Label: "Available", Value: "100 EA"},
		}},
	}
	good := "DIB-431B Stocked Slab 140000000 (DIB431B-STOCKED-140000000) at Trenton: 100 EA available."
	if leaks := stockHeadlineLeak(good, rows); len(leaks) != 0 {
		t.Fatalf("the one branch sentence must pass: %v", leaks)
	}
	wrong := "DIB-431B Stocked Slab 140000000 (DIB431B-STOCKED-140000000) at Trenton: 140 EA available."
	if leaks := stockHeadlineLeak(wrong, rows); len(leaks) == 0 {
		t.Fatal("the check let a headline that states the wrong figure pass: the digits were the product name's")
	}
}
