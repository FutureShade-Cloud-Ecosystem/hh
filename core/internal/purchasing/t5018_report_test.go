package purchasing

// t5018_report_test.go, the year's T5018 report's own vocabulary: the year window in the house
// zone, and the CSV the bookkeeper hands the contractor's book.
//
// The year window is the whole of the report's date rule, so it is proved here as a pure
// function: a payment made at 22:00 on the 31st of December is that year's payment in the
// dealer's own calendar, even where UTC has already rolled over to the next year.

import (
	"strings"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// TestT5018YearWindow_TheLastMinuteOfTheYearIsTheYearsOwn: the window runs on the house zone,
// from the instant the first of January begins to the instant the first of January of the next
// year begins. A payment at 22:00 on the 31st of December is inside the window, and the same
// instant read as UTC is already the next year: a report that filed it under UTC would answer the
// CRA a different year than the one the dealer paid in.
func TestT5018YearWindow_TheLastMinuteOfTheYearIsTheYearsOwn(t *testing.T) {
	zone := houseday.DefaultZone()
	start, end, err := t5018YearWindow(2026, zone)
	if err != nil {
		t.Fatalf("the year window: %v", err)
	}
	loc := zone.Location()
	if got := start.In(loc).Format("2006-01-02 15:04:05"); got != "2026-01-01 00:00:00" {
		t.Fatalf("the window opens at %s, want the first of January in the house zone", got)
	}
	if got := end.In(loc).Format("2006-01-02 15:04:05"); got != "2027-01-01 00:00:00" {
		t.Fatalf("the window closes at %s, want the first of January of the next year in the house zone", got)
	}

	// The payment the boundary is about: 22:00 on the 31st of December, house time.
	paidAt := time.Date(2026, time.December, 31, 22, 0, 0, 0, loc)
	if !paidAt.Before(end) || paidAt.Before(start) {
		t.Fatalf("the payment at %s is outside the year window %s to %s", paidAt, start, end)
	}
	// The same instant in UTC is already next year's.
	if got := paidAt.UTC().Year(); got != 2027 {
		t.Fatalf("the payment reads %d in UTC, want 2027 so the UTC reading would file it in the wrong year", got)
	}
}

// TestT5018YearWindow_TheHalfOpenEdgeKeepsTheYearsApart: the window's end is the next year's
// start, so the two years never both answer for the same instant.
func TestT5018YearWindow_TheHalfOpenEdgeKeepsTheYearsApart(t *testing.T) {
	zone := houseday.DefaultZone()
	_, end, err := t5018YearWindow(2026, zone)
	if err != nil {
		t.Fatalf("the year window: %v", err)
	}
	nextStart, _, err := t5018YearWindow(2027, zone)
	if err != nil {
		t.Fatalf("the next year window: %v", err)
	}
	if !end.Equal(nextStart) {
		t.Fatalf("2026 closes at %s and 2027 opens at %s, want the same instant", end, nextStart)
	}
}

// TestT5018ReportCSV_TheFileCarriesTheRowsTheMarkAndTheTotal: the CSV is the report the screen
// reads, in its file form. It opens with the construction income sentence as a header line, then
// the column names carrying Box 22 (with tax), the services subtotal before tax, the under $500
// mark and words, and the unmarked lines count and words. A contractor with no business number on
// file says so in the cell rather than leaving a blank a reader would take for an oversight, and
// the year paid cents, Box 22 cents and the year's totals are all in the file.
func TestT5018ReportCSV_TheFileCarriesTheRowsTheMarkAndTheTotal(t *testing.T) {
	unmarkedWords := t5018UnmarkedLinesNote(2)
	report := T5018Report{
		Year:     2026,
		RuleNote: T5018ConstructionIncomeNote,
		Items: []T5018Row{
			{
				VendorID: "v-1", VendorNo: "VM-0001", Name: "Marked, With A Number",
				BusinessNumber: "123456789", BusinessNumberMissing: false,
				PaidCents: 125000, Box22Cents: 104050, ServicesSubtotalCents: 92080,
				UnderThreshold: false, ThresholdNote: "",
				UnmarkedLineCount: 2, UnmarkedLinesNote: unmarkedWords,
			},
			{
				VendorID: "v-2", VendorNo: "VM-0002", Name: "Marked, Under Threshold Without A Number",
				BusinessNumber: "", BusinessNumberMissing: true,
				PaidCents: 54240, Box22Cents: 54240, ServicesSubtotalCents: 48000,
				UnderThreshold: true, ThresholdNote: T5018UnderThresholdNote,
				UnmarkedLineCount: 0, UnmarkedLinesNote: "",
			},
		},
		TotalCents:      179240,
		Box22TotalCents: 158290,
	}
	body, err := EncodeT5018ReportCSV(report)
	if err != nil {
		t.Fatalf("encode the report: %v", err)
	}
	text := string(body)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) != 6 {
		t.Fatalf("the file holds %d lines, want the rule note header, the column head, two rows, a blank and a total:\n%s", len(lines), text)
	}
	if lines[0] != T5018ConstructionIncomeNote {
		t.Fatalf("the header line reads %q, want %q", lines[0], T5018ConstructionIncomeNote)
	}
	wantCols := "vendor_no,vendor_name,business_number,paid_cents,box_22_cents,services_subtotal_cents,under_threshold,threshold_note,unmarked_line_count,unmarked_lines_note"
	if lines[1] != wantCols {
		t.Fatalf("the column head reads %q, want %q", lines[1], wantCols)
	}
	if !strings.Contains(lines[2], "123456789") || !strings.Contains(lines[2], "125000,104050,92080,false,,2,") || !strings.Contains(lines[2], unmarkedWords) {
		t.Fatalf("the first row reads %q, want business number, paid, box 22, services subtotal and unmarked words", lines[2])
	}
	if !strings.Contains(lines[3], T5018MissingNumber) || !strings.Contains(lines[3], "54240,54240,48000,true,\""+T5018UnderThresholdNote+"\",0,") {
		t.Fatalf("the second row reads %q, want the missing mark, box 22 with tax, services subtotal before tax, and the under $500 mark", lines[3])
	}
	if lines[4] != "" {
		t.Fatalf("the separator line reads %q, want a blank line", lines[4])
	}
	wantTotal := "Total,,,179240,158290,,,,,"
	if lines[5] != wantTotal {
		t.Fatalf("the total reads %q, want %q", lines[5], wantTotal)
	}
}

// TestT5018ProRataAndPaymentShare_HalfToEvenAndCRARules proves the pure pro-rata banker's rounding
// and the five CRA rules (goods-only excluded, mixed services share, $480 + 13% HST under $500
// threshold, Box 22 carries the tax, unmarked line counts as services with its note).
func TestT5018ProRataAndPaymentShare_HalfToEvenAndCRARules(t *testing.T) {
	// Half-to-even ties: 0.5 -> 0 (even), 1.5 -> 2 (even), 2.5 -> 2 (even), 3.5 -> 4 (even).
	if got := t5018ProRataCents(1, 1, 2); got != 0 {
		t.Fatalf("0.5 half-to-even = %d, want 0", got)
	}
	if got := t5018ProRataCents(3, 1, 2); got != 2 {
		t.Fatalf("1.5 half-to-even = %d, want 2", got)
	}
	if got := t5018ProRataCents(5, 1, 2); got != 2 {
		t.Fatalf("2.5 half-to-even = %d, want 2", got)
	}
	if got := t5018ProRataCents(7, 1, 2); got != 4 {
		t.Fatalf("3.5 half-to-even = %d, want 4", got)
	}

	// Leg 1: goods-only bill contributes nothing.
	if b22, svc := t5018PaymentShare(90400, 80000, 90400, 0); b22 != 0 || svc != 0 {
		t.Fatalf("goods-only share = (%d, %d), want (0, 0)", b22, svc)
	}

	// Leg 2: mixed bill ($600 services + $400 goods = $1,000 subtotal, + $130 HST = $1,130 paid).
	if b22, svc := t5018PaymentShare(113000, 100000, 113000, 60000); b22 != 67800 || svc != 60000 {
		t.Fatalf("mixed share = (box22=%d, svc=%d), want (67800, 60000)", b22, svc)
	}

	// Legs 3 & 4: $480.00 services + 13% HST ($62.40) = $542.40 paid -> Box 22 = 54240, pre-tax = 48000 (< 50000).
	b22, svc := t5018PaymentShare(54240, 48000, 54240, 48000)
	if b22 != 54240 || svc != 48000 {
		t.Fatalf("$480 + HST share = (box22=%d, svc=%d), want (54240, 48000)", b22, svc)
	}
	row := finalizeT5018Row(T5018Row{
		VendorID: "v-under", VendorNo: "VM-0010", Name: "Under Threshold",
		BusinessNumber: "123456789", Box22Cents: b22, ServicesSubtotalCents: svc,
		UnmarkedLineCount: 1,
	})
	if !row.UnderThreshold || row.ThresholdNote != T5018UnderThresholdNote {
		t.Fatalf("under-threshold row = %+v, want UnderThreshold=true and note %q", row, T5018UnderThresholdNote)
	}
	wantUnmarked := "1 bill lines are not marked goods or services; they are counted as services until marked"
	if row.UnmarkedLinesNote != wantUnmarked {
		t.Fatalf("unmarked_lines_note = %q, want %q", row.UnmarkedLinesNote, wantUnmarked)
	}
}
