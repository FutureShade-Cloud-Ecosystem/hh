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
// reads, in its file form. A contractor with no business number on file says so in the cell
// rather than leaving a blank a reader would take for an oversight, and the year paid cents and
// the year's total are both in the file.
func TestT5018ReportCSV_TheFileCarriesTheRowsTheMarkAndTheTotal(t *testing.T) {
	report := T5018Report{
		Year: 2026,
		Items: []T5018Row{
			{VendorID: "v-1", VendorNo: "VM-0001", Name: "Marked, With A Number",
				BusinessNumber: "123456789", BusinessNumberMissing: false, PaidCents: 125000},
			{VendorID: "v-2", VendorNo: "VM-0002", Name: "Marked, Without A Number",
				BusinessNumber: "", BusinessNumberMissing: true, PaidCents: 0},
		},
		TotalCents: 125000,
	}
	body, err := EncodeT5018ReportCSV(report)
	if err != nil {
		t.Fatalf("encode the report: %v", err)
	}
	text := string(body)
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("the file holds %d lines, want a head, two rows, a blank and a total:\n%s", len(lines), text)
	}
	if !strings.HasPrefix(lines[0], "vendor_no,") {
		t.Fatalf("the head reads %q, want the column names", lines[0])
	}
	if !strings.Contains(lines[1], "123456789") || !strings.Contains(lines[1], "125000") {
		t.Fatalf("the first row reads %q, want the business number and the year paid cents", lines[1])
	}
	if !strings.Contains(lines[2], T5018MissingNumber) {
		t.Fatalf("the second row reads %q, want the missing mark", lines[2])
	}
	if !strings.Contains(lines[4], "Total") || !strings.Contains(lines[4], "125000") {
		t.Fatalf("the total reads %q, want the year's total", lines[4])
	}
}
