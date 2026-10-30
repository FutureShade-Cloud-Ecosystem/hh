package purchasing

// t5018_report.go, the year's payments to the flagged vendors: what a CRA Form T5018 is built
// from.
//
// WHAT THE REPORT IS. For one calendar year, every vendor the Financial Controller has marked as
// a subcontractor, the amount the dealer paid that vendor during the year through a PayBill
// payment, and the contractor's business number, so the form can be filled from the books rather
// than from a shoebox of invoices.
//
// THE YEAR IS THE DEALER'S, NOT UTC'S. The year runs on the house zone: the instant the first of
// January begins in the dealer's own calendar to the instant the first of January of the next
// year begins. A payment made at 22:00 on the 31st of December is that year's payment in the
// dealer's calendar; in UTC it may already be the next year's, and a report that filed it under
// UTC would answer the CRA a different year than the one the dealer paid in.
//
// THE PAYMENT'S OWN INSTANT COUNTS, not the journal's. The instalment carries the instant the
// payment was made, and that is the fact the form asks about: what the dealer paid the
// contractor, and when. This is the same rule the input tax credit seam reads the period's vendor
// bills by (tax_seam.go: the bills' own bill_date), and it is the same rule the vendor bill's own
// date answers under. The GL's late filing rule still governs which period a JOURNAL lands in;
// it does not move a payment into another year's form.
//
// THE BRANCH FENCE STANDS. Every query is scoped by branch: a caller who may see one branch sees
// that branch's payments, a caller with the whole dealer sees them all. The vendors themselves are
// org scoped, so a vendor another org marked is not in this org's report.
//
// READ ONLY. There is no write on this report, and no route that writes one: the only thing that
// changes a year's report is the flag on a vendor (t5018.go) and the payments PayBill records.

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
)

// T5018MissingNumber is the word the report and its CSV carry where a contractor's business number
// is missing. A blank cell would read as an oversight; this says what it is.
const T5018MissingNumber = "(missing)"

// T5018ThresholdCents is the CRA $500.00 reporting threshold, measured on the vendor's services
// subtotal before tax over the year.
const T5018ThresholdCents int64 = 50000

// T5018UnderThresholdNote marks a subcontractor vendor whose pre-tax services subtotal for the
// year is under $500.00.
const T5018UnderThresholdNote = "under the $500 slip threshold, no slip."

// T5018GoodsSharePrefix opens the note on a mixed bill whose paid total includes goods excluded
// from Box 22.
const T5018GoodsSharePrefix = "Box 22 counts services only;"

// T5018GoodsShareSuffix closes the note on a mixed bill whose paid total includes goods excluded
// from Box 22.
const T5018GoodsShareSuffix = "of goods is not counted."

// T5018NothingPaidNote marks a subcontractor vendor who was paid nothing during the year.
const T5018NothingPaidNote = "nothing paid this year"

// T5018ConstructionIncomeNote states the CRA filing condition for Form T5018: the return applies
// only where construction is more than half the dealer's business income.
const T5018ConstructionIncomeNote = "T5018 applies only where construction is more than half the dealer's business income."

// T5018Row is one flagged vendor's line in the year's report.
type T5018Row struct {
	VendorID string `json:"vendor_id"`
	VendorNo string `json:"vendor_no"`
	Name     string `json:"name"`
	// BusinessNumber is the contractor's CRA business number. Empty when the contractor has not
	// given one; BusinessNumberMissing says so rather than leaving the reader to guess.
	BusinessNumber string `json:"business_number"`
	// BusinessNumberMissing is the mark that the number is not on file.
	BusinessNumberMissing bool `json:"business_number_missing"`
	// PaidCents is the total paid to the vendor during the year through PayBill, tax included,
	// goods and services alike.
	PaidCents int64 `json:"paid_cents"`
	// Box22Cents is the payment's services share of the amount paid in the year, tax included.
	Box22Cents int64 `json:"box_22_cents"`
	// ServicesSubtotalCents is the services subtotal before tax, summed over the year, used to
	// test the $500.00 CRA reporting threshold.
	ServicesSubtotalCents int64 `json:"services_subtotal_cents"`
	// UnderThreshold is true when ServicesSubtotalCents is under $500.00 (50000 cents).
	UnderThreshold bool `json:"under_threshold"`
	// ThresholdNote is "under the $500 threshold, no slip" when UnderThreshold is true.
	ThresholdNote string `json:"threshold_note"`
	// UnmarkedLineCount is the number of bill lines with supply_kind IS NULL on bills paid in the
	// year for this vendor.
	UnmarkedLineCount int `json:"unmarked_line_count"`
	// UnmarkedLinesNote explains that unmarked bill lines are counted as services until marked.
	UnmarkedLinesNote string `json:"unmarked_lines_note"`
}

// T5018Report is the year's report: the year it covers, the CRA construction-income rule note,
// one row per flagged vendor, and the year's Box 22 total.
type T5018Report struct {
	Year            int        `json:"year"`
	RuleNote        string     `json:"rule_note"`
	Items           []T5018Row `json:"items"`
	TotalCents      int64      `json:"total_cents"`
	Box22TotalCents int64      `json:"box_22_total_cents"`
}

// T5018ReportForCaller answers the report for one calendar year.
//
// The org is the caller's, from the guard's resolved scope, never from a header or a parameter:
// a vendor another org marked is not in this org's report. The year is the one the caller named,
// or the dealer's current house year when the caller named none. The branch reach is the caller's
// own: branch_id narrows it within the reach and is refused when it is outside it, exactly as
// every other purchasing read narrows.
func (s *Service) T5018ReportForCaller(ctx context.Context, orgID, rawYear, branchID string) (T5018Report, error) {
	if _, err := s.caller(ctx); err != nil {
		return T5018Report{}, err
	}
	if !isUUIDString(orgID) {
		return T5018Report{}, badReq("purchasing: the caller's org is not resolved, cannot read the report")
	}
	reach, err := s.reachFor(ctx, branchID)
	if err != nil {
		return T5018Report{}, err
	}
	zone, err := houseday.Resolve(ctx, s.zone)
	if err != nil {
		return T5018Report{}, err
	}
	year, err := t5018Year(rawYear, zone, s.now)
	if err != nil {
		return T5018Report{}, err
	}
	start, end, err := t5018YearWindow(year, zone)
	if err != nil {
		return T5018Report{}, err
	}
	report := T5018Report{
		Year:     year,
		RuleNote: T5018ConstructionIncomeNote,
		Items:    []T5018Row{},
	}
	if !reach.Global && len(reach.BranchIDs) == 0 {
		return report, nil
	}
	rows, err := s.repo.T5018Report(ctx, orgID, start, end, reach)
	if err != nil {
		return T5018Report{}, err
	}
	for _, row := range rows {
		report.Items = append(report.Items, row)
		report.TotalCents += row.PaidCents
		report.Box22TotalCents += row.Box22Cents
	}
	return report, nil
}

// t5018ProRataCents computes amountCents * partCents / wholeCents in integer cents, rounded half
// to even (banker's rounding). Returns 0 when any input is non-positive.
func t5018ProRataCents(amountCents, partCents, wholeCents int64) int64 {
	if amountCents <= 0 || partCents <= 0 || wholeCents <= 0 {
		return 0
	}
	if partCents == wholeCents {
		return amountCents
	}
	num := amountCents * partCents
	q := num / wholeCents
	rem := num % wholeCents
	twiceRem := rem * 2
	if twiceRem > wholeCents || (twiceRem == wholeCents && q%2 == 1) {
		q++
	}
	return q
}

// t5018PaymentShare computes one payment's Box 22 services share (tax included) and its pre-tax
// services subtotal share (for the $500 threshold) from the bill's lines. A goods-only bill
// (billServicesSubtotalCents == 0) contributes nothing to either.
func t5018PaymentShare(paidCents, apCents, billSubtotalCents, billTotalCents, billServicesSubtotalCents int64) (box22Cents, servicesSubtotalCents int64) {
	if paidCents <= 0 || apCents <= 0 || billSubtotalCents <= 0 || billTotalCents <= 0 || billServicesSubtotalCents <= 0 {
		return 0, 0
	}
	box22Cents = t5018ProRataCents(paidCents, billServicesSubtotalCents, billSubtotalCents)
	servicesSubtotalCents = t5018ProRataCents(apCents, billServicesSubtotalCents, billTotalCents)
	return box22Cents, servicesSubtotalCents
}

// t5018UnmarkedLinesNote returns the per-vendor note when one or more paid bill lines have no
// supply_kind mark.
func t5018UnmarkedLinesNote(n int) string {
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "1 bill line is not marked goods or services; it is counted as services until marked"
	}
	return fmt.Sprintf("%d bill lines are not marked goods or services; they are counted as services until marked", n)
}

// t5018FormatDollars formats integer cents as dollars and cents (for example, 48000 -> "$480.00").
func t5018FormatDollars(cents int64) string {
	sign := ""
	if cents < 0 {
		sign = "-"
		cents = -cents
	}
	dollars := strconv.FormatInt(cents/100, 10)
	for i := len(dollars) - 3; i > 0; i -= 3 {
		dollars = dollars[:i] + "," + dollars[i:]
	}
	return fmt.Sprintf("%s$%s.%02d", sign, dollars, cents%100)
}

// t5018UnderThresholdNote formats the per-vendor under-threshold note naming the pre-tax services
// subtotal the $500 test is on.
func t5018UnderThresholdNote(servicesSubtotalCents int64) string {
	return fmt.Sprintf("Services before tax %s: %s", t5018FormatDollars(servicesSubtotalCents), T5018UnderThresholdNote)
}

// t5018GoodsShareNote formats the per-vendor note when a mixed bill's paid total includes goods
// not counted in Box 22.
func t5018GoodsShareNote(goodsShareCents int64) string {
	return fmt.Sprintf("%s %s %s", T5018GoodsSharePrefix, t5018FormatDollars(goodsShareCents), T5018GoodsShareSuffix)
}

// finalizeT5018Row stamps the derived marks and notes on a vendor's T5018 row.
func finalizeT5018Row(row T5018Row) T5018Row {
	row.BusinessNumberMissing = strings.TrimSpace(row.BusinessNumber) == ""
	row.UnderThreshold = row.ServicesSubtotalCents < T5018ThresholdCents
	if row.PaidCents == 0 && row.Box22Cents == 0 {
		row.ThresholdNote = T5018NothingPaidNote
	} else if row.UnderThreshold {
		row.ThresholdNote = t5018UnderThresholdNote(row.ServicesSubtotalCents)
	} else if row.PaidCents > row.Box22Cents {
		row.ThresholdNote = t5018GoodsShareNote(row.PaidCents - row.Box22Cents)
	} else {
		row.ThresholdNote = ""
	}
	row.UnmarkedLinesNote = t5018UnmarkedLinesNote(row.UnmarkedLineCount)
	return row
}

// t5018Year reads the year the report is for: the year named, or the house year when the caller
// named none. A year that is not a year is refused rather than guessed at.
func t5018Year(raw string, zone houseday.Zone, now func() time.Time) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		if now != nil {
			return now().In(zone.Location()).Year(), nil
		}
		return time.Now().In(zone.Location()).Year(), nil
	}
	year, err := strconv.Atoi(raw)
	if err != nil {
		return 0, badReq("purchasing: the T5018 year must be a year, as four digits")
	}
	if year < 1970 || year > 9999 {
		return 0, badReq("purchasing: the T5018 year must be between 1970 and 9999")
	}
	return year, nil
}

// t5018YearWindow is the year in the house zone: the instant the first of January begins, and the
// instant the first of January of the next year begins. The second is the half open edge, so the
// last instant of the year belongs to the year and the first instant of the next does not.
func t5018YearWindow(year int, zone houseday.Zone) (time.Time, time.Time, error) {
	start, err := zone.ParseDay(fmt.Sprintf("%04d-01-01", year))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("purchasing: the T5018 year window: %w", err)
	}
	end, err := zone.ParseDay(fmt.Sprintf("%04d-01-01", year+1))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("purchasing: the T5018 year window: %w", err)
	}
	return start, end, nil
}

// T5018Report reads the report's rows: one row per flagged vendor in the org, with the year's
// payments to that vendor inside the reach.
//
// The payments are aggregated FIRST and joined onto the vendors, so a flagged vendor nobody was
// paid this year still appears, with nothing paid. A vendor is on the form because the Controller
// marked it a subcontractor, not because a payment happened to exist; a vendor with no payments is
// a line with a zero on it, and dropping the line would hide a contractor the dealer still owes.
//
// The payments are the PAID instalments, the ones PayBill wrote, each at the cash that left the bank, and the branch
// fence sits in the payments query beside the year, so a branch the caller cannot see contributes nothing.
func (r *Repository) T5018Report(ctx context.Context, orgID string, start, end time.Time, reach BranchReach) ([]T5018Row, error) {
	if r == nil || r.pool == nil {
		return nil, errNoDB
	}
	branchIDs := reach.BranchIDs
	if branchIDs == nil {
		branchIDs = []string{}
	}
	vrows, err := r.pool.Query(ctx, `
		SELECT v.id::text, v.vendor_no::text, v.name, COALESCE(v.business_number, '')
		  FROM vendors v
		 WHERE v.org_id = $1::uuid
		   AND v.is_t5018_subcontractor
		   AND v.deleted_at IS NULL
		 ORDER BY v.name ASC, v.id ASC`, orgID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: the T5018 report: %w", err)
	}
	defer vrows.Close()

	out := make([]T5018Row, 0)
	byVendor := make(map[string]int)
	for vrows.Next() {
		var row T5018Row
		if err := vrows.Scan(&row.VendorID, &row.VendorNo, &row.Name, &row.BusinessNumber); err != nil {
			return nil, fmt.Errorf("purchasing: the T5018 report: %w", err)
		}
		byVendor[row.VendorID] = len(out)
		out = append(out, row)
	}
	if err := vrows.Err(); err != nil {
		return nil, fmt.Errorf("purchasing: the T5018 report: %w", err)
	}
	vrows.Close()

	if len(out) == 0 {
		return out, nil
	}

	prows, err := r.pool.Query(ctx, `
		SELECT b.vendor_id::text,
		       b.id::text,
		       i.amount_cents,
		       b.currency::text,
		       COALESCE(i.doc_amount_cents, 0),
		       round(COALESCE(i.fx_rate, 1) * 1000000)::bigint,
		       b.subtotal_cents,
		       b.total_cents,
		       COALESCE(lines.services_subtotal_cents, 0),
		       COALESCE(lines.unmarked_line_count, 0)
		  FROM purchasing_bill_instalments i
		  JOIN purchasing_bills b ON b.id = i.bill_id
		  JOIN vendors v ON v.id = b.vendor_id
		  LEFT JOIN (
		       SELECT bl.bill_id,
		              SUM(CASE WHEN bl.supply_kind IS NULL OR bl.supply_kind = 'SERVICES'
		                       THEN bl.line_total_cents ELSE 0 END) AS services_subtotal_cents,
		              COUNT(*) FILTER (WHERE bl.supply_kind IS NULL) AS unmarked_line_count
		         FROM purchasing_bill_lines bl
		        GROUP BY bl.bill_id
		  ) lines ON lines.bill_id = b.id
		 WHERE v.org_id = $1::uuid
		   AND v.is_t5018_subcontractor
		   AND v.deleted_at IS NULL
		   AND i.status = 'PAID'
		   AND i.paid_at >= $2::timestamptz
		   AND i.paid_at <  $3::timestamptz
		   AND ($4::boolean OR b.branch_id = ANY($5::uuid[]))
		 ORDER BY i.paid_at ASC, i.id ASC`,
		orgID, start, end, reach.Global, branchIDs)
	if err != nil {
		return nil, fmt.Errorf("purchasing: the T5018 report payments: %w", err)
	}
	defer prows.Close()

	seenBill := make(map[string]bool)
	for prows.Next() {
		var vendorID, billID, currency string
		var apCents, docCents, rateMicros, subtotalCents, totalCents, servicesSubtotalCents int64
		var unmarkedCount int
		if err := prows.Scan(&vendorID, &billID, &apCents, &currency, &docCents, &rateMicros, &subtotalCents, &totalCents, &servicesSubtotalCents, &unmarkedCount); err != nil {
			return nil, fmt.Errorf("purchasing: the T5018 report payments: %w", err)
		}
		idx, ok := byVendor[vendorID]
		if !ok {
			continue
		}
		// The cash that left the bank: a foreign bill's instalment holds the CAD it cleared at the
		// BILL's rate in amount_cents, but the bank paid the payment's own amount at the payment's own
		// rate, through the same half to even rounding PayBill credits 1000 with. A CAD instalment's
		// amount is its cash already.
		paidCents := apCents
		if currency != "CAD" {
			paidCents = proRataHalfToEven(docCents, rateMicros, 1_000_000)
		}
		box22, svcBeforeTax := t5018PaymentShare(paidCents, apCents, subtotalCents, totalCents, servicesSubtotalCents)
		out[idx].PaidCents += paidCents
		out[idx].Box22Cents += box22
		out[idx].ServicesSubtotalCents += svcBeforeTax
		if !seenBill[billID] {
			seenBill[billID] = true
			out[idx].UnmarkedLineCount += unmarkedCount
		}
	}
	if err := prows.Err(); err != nil {
		return nil, fmt.Errorf("purchasing: the T5018 report payments: %w", err)
	}

	for i := range out {
		out[i] = finalizeT5018Row(out[i])
	}
	return out, nil
}

// t5018CSVColumns is the column header row of the T5018 CSV export.
var t5018CSVColumns = []string{
	"vendor_no",
	"vendor_name",
	"business_number",
	"paid_cents",
	"box_22_cents",
	"services_subtotal_cents",
	"under_threshold",
	"threshold_note",
	"unmarked_line_count",
	"unmarked_lines_note",
}

// EncodeT5018ReportCSV is the report as a CSV: the construction-income rule note as a header line,
// the column header row, one row per flagged vendor with Box 22 (with tax), the services subtotal
// before tax, the under $500 mark and note, and the unmarked lines count and words, then a blank
// line and the year's totals. A contractor with no business number on file says so in the cell
// rather than leaving a blank a reader would take for an oversight.
func EncodeT5018ReportCSV(report T5018Report) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	ruleNote := strings.TrimSpace(report.RuleNote)
	if ruleNote == "" {
		ruleNote = T5018ConstructionIncomeNote
	}
	if err := w.Write([]string{ruleNote}); err != nil {
		return nil, err
	}
	if err := w.Write(t5018CSVColumns); err != nil {
		return nil, err
	}
	for _, row := range report.Items {
		number := strings.TrimSpace(row.BusinessNumber)
		if row.BusinessNumberMissing {
			number = T5018MissingNumber
		}
		if err := w.Write([]string{
			row.VendorNo,
			row.Name,
			number,
			strconv.FormatInt(row.PaidCents, 10),
			strconv.FormatInt(row.Box22Cents, 10),
			strconv.FormatInt(row.ServicesSubtotalCents, 10),
			strconv.FormatBool(row.UnderThreshold),
			row.ThresholdNote,
			strconv.Itoa(row.UnmarkedLineCount),
			row.UnmarkedLinesNote,
		}); err != nil {
			return nil, err
		}
	}
	if err := w.Write(nil); err != nil {
		return nil, err
	}
	if err := w.Write([]string{
		"Total",
		"",
		"",
		strconv.FormatInt(report.TotalCents, 10),
		strconv.FormatInt(report.Box22TotalCents, 10),
		"",
		"",
		"",
		"",
		"",
	}); err != nil {
		return nil, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
