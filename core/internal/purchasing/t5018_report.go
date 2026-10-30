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
	// PaidCents is what the year paid the vendor through PayBill, in cents.
	PaidCents int64 `json:"paid_cents"`
}

// T5018Report is the year's report: the year it covers, one row per flagged vendor, and the
// year's total.
type T5018Report struct {
	Year       int        `json:"year"`
	Items      []T5018Row `json:"items"`
	TotalCents int64      `json:"total_cents"`
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
	report := T5018Report{Year: year, Items: []T5018Row{}}
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
	}
	return report, nil
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
	// The cash that left the bank, per PAID instalment, summed in Go: a foreign bill's instalment
	// holds the CAD it cleared at the BILL's rate, so its cash is the payment's own amount at the
	// payment's own rate, through the same half to even rounding PayBill credits 1000 with. A CAD
	// bill's instalment is its cash already.
	payRows, err := r.pool.Query(ctx, `
		SELECT b.vendor_id::text, b.currency::text, i.amount_cents, COALESCE(i.doc_amount_cents, 0),
		       round(COALESCE(i.fx_rate, 1) * 1000000)::bigint
		  FROM purchasing_bill_instalments i
		  JOIN purchasing_bills b ON b.id = i.bill_id
		  JOIN vendors pv ON pv.id = b.vendor_id
		 WHERE pv.org_id = $1::uuid
		   AND i.status = 'PAID'
		   AND i.paid_at >= $2::timestamptz
		   AND i.paid_at <  $3::timestamptz
		   AND ($4::boolean OR b.branch_id = ANY($5::uuid[]))`,
		orgID, start, end, reach.Global, branchIDs)
	if err != nil {
		return nil, fmt.Errorf("purchasing: the T5018 report: %w", err)
	}
	paid := map[string]int64{}
	for payRows.Next() {
		var vendorID, currency string
		var amountCents, docCents, rateMicros int64
		if err := payRows.Scan(&vendorID, &currency, &amountCents, &docCents, &rateMicros); err != nil {
			payRows.Close()
			return nil, fmt.Errorf("purchasing: the T5018 report: %w", err)
		}
		cash := amountCents
		if currency != "CAD" {
			cash = proRataHalfToEven(docCents, rateMicros, 1_000_000)
		}
		paid[vendorID] += cash
	}
	payRows.Close()
	if err := payRows.Err(); err != nil {
		return nil, fmt.Errorf("purchasing: the T5018 report: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT v.id::text, v.vendor_no::text, v.name, COALESCE(v.business_number, '')
		  FROM vendors v
		 WHERE v.org_id = $1::uuid
		   AND v.is_t5018_subcontractor
		   AND v.deleted_at IS NULL
		 ORDER BY v.name ASC, v.id ASC`,
		orgID)
	if err != nil {
		return nil, fmt.Errorf("purchasing: the T5018 report: %w", err)
	}
	defer rows.Close()

	out := make([]T5018Row, 0)
	for rows.Next() {
		var row T5018Row
		if err := rows.Scan(&row.VendorID, &row.VendorNo, &row.Name, &row.BusinessNumber); err != nil {
			return nil, fmt.Errorf("purchasing: the T5018 report: %w", err)
		}
		row.PaidCents = paid[row.VendorID]
		row.BusinessNumberMissing = strings.TrimSpace(row.BusinessNumber) == ""
		out = append(out, row)
	}
	return out, rows.Err()
}

// EncodeT5018ReportCSV is the report as a CSV: the year, one row per flagged vendor, and the
// year's total. A contractor with no business number on file says so in the cell rather than
// leaving a blank a reader would take for an oversight.
func EncodeT5018ReportCSV(report T5018Report) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"vendor_no", "vendor_name", "business_number", "paid_cents"}); err != nil {
		return nil, err
	}
	for _, row := range report.Items {
		number := strings.TrimSpace(row.BusinessNumber)
		if row.BusinessNumberMissing {
			number = T5018MissingNumber
		}
		if err := w.Write([]string{row.VendorNo, row.Name, number, strconv.FormatInt(row.PaidCents, 10)}); err != nil {
			return nil, err
		}
	}
	if err := w.Write(nil); err != nil {
		return nil, err
	}
	if err := w.Write([]string{"Total", "", "", strconv.FormatInt(report.TotalCents, 10)}); err != nil {
		return nil, err
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
