package tax

import (
	"context"
	"strings"
	"testing"
)

type fakeMonthBooks struct {
	salesHST  map[int]int64
	returnHST map[int]int64
	billITCs  map[int]int64
	revenue   map[int]int64
	gl2200    map[int]int64
}

func newFakeMonthBooks() *fakeMonthBooks {
	return &fakeMonthBooks{
		salesHST:  map[int]int64{},
		returnHST: map[int]int64{},
		billITCs:  map[int]int64{},
		revenue:   map[int]int64{},
		gl2200:    map[int]int64{},
	}
}

func (b *fakeMonthBooks) DocumentSalesHSTCents(_ context.Context, _, month int, _ string) (int64, int, int64, error) {
	return b.salesHST[month], 0, 0, nil
}

func (b *fakeMonthBooks) ReturnHSTCents(_ context.Context, _, month int, _ string) (int64, error) {
	return b.returnHST[month], nil
}

func (b *fakeMonthBooks) VendorBillITCCents(_ context.Context, _, month int, _ string) (int64, error) {
	return b.billITCs[month], nil
}

func (b *fakeMonthBooks) GLBalanceCents(_ context.Context, code string, _, month int, _ string) (int64, error) {
	if code == acctHSTPayable {
		return b.gl2200[month], nil
	}
	return 0, nil
}

func (b *fakeMonthBooks) GLRevenueCents(_ context.Context, _, month int, _ string) (int64, error) {
	return b.revenue[month], nil
}

// Leg 1 & Leg 2: Line 109 equals line 105 less line 108, and line 105 less line 108 equals GL 2200's
// net movement for the period (the same rule the box check ties). Also checks the balance lines and
// the manual-entry wording.
func TestWorksheet_Line109EqualsLine105LessLine108AndTiesToGL2200(t *testing.T) {
	f := newFakeStore()
	books := newFakeMonthBooks()
	// Q2 activity across April (month 4) and May (month 5):
	// Revenue: 100000 + 50000 = 150000
	// Sales HST (invoices): 13000 + 6500 = 19500
	// POS return HST: 1300
	// Line 103 collected: 19500 - 1300 = 18200
	// Vendor bill ITCs (line 106): 3900 + 1300 = 5200
	// Net movement on GL 2200: 18200 - 5200 = 13000
	books.revenue[4] = 100_000
	books.revenue[5] = 50_000
	books.salesHST[4] = 13_000
	books.salesHST[5] = 6_500
	books.returnHST[5] = 1_300
	books.billITCs[4] = 3_900
	books.billITCs[5] = 1_300
	books.gl2200[4] = 9_100
	books.gl2200[5] = 3_900

	svc, _ := newSvc(f)
	svc.WithSalesHSTs(books).WithReturnHSTs(books).WithBillITCs(books).WithGLBalances(books).WithGLRevenue(books)

	ws, err := svc.WorksheetReport(context.Background(), WorksheetPeriod{Year: 2026, Quarter: 2})
	if err != nil {
		t.Fatalf("WorksheetReport: %v", err)
	}
	if ws.Line101SalesCents != 150_000 {
		t.Errorf("line 101 sales = %d, want 150000", ws.Line101SalesCents)
	}
	if ws.Line103CollectedCents != 18_200 {
		t.Errorf("line 103 collected = %d, want 18200", ws.Line103CollectedCents)
	}
	if ws.Line105TotalHSTCents != 18_200 {
		t.Errorf("line 105 total HST = %d, want 18200", ws.Line105TotalHSTCents)
	}
	if ws.Line106ITCsCents != 5_200 {
		t.Errorf("line 106 ITCs = %d, want 5200", ws.Line106ITCsCents)
	}
	if ws.Line108TotalITCsCents != 5_200 {
		t.Errorf("line 108 total ITCs = %d, want 5200", ws.Line108TotalITCsCents)
	}
	wantNet := ws.Line105TotalHSTCents - ws.Line108TotalITCsCents
	if ws.Line109NetTaxCents != 13_000 || ws.Line109NetTaxCents != wantNet {
		t.Errorf("Leg 1: line 109 net tax = %d, want line 105 (%d) less line 108 (%d) = 13000",
			ws.Line109NetTaxCents, ws.Line105TotalHSTCents, ws.Line108TotalITCsCents)
	}
	if (ws.Line105TotalHSTCents-ws.Line108TotalITCsCents) != ws.GLPayableCents || ws.GLPayableCents != 13_000 || ws.VarianceCents != 0 || !ws.TiedOut {
		t.Errorf("Leg 2: line 105 less line 108 = %d, GL 2200 net movement = %d, variance = %d, tied_out = %v, want 13000/13000/0/true",
			ws.Line105TotalHSTCents-ws.Line108TotalITCsCents, ws.GLPayableCents, ws.VarianceCents, ws.TiedOut)
	}
	if ws.Line113BalanceCents != 13_000 || ws.Line115AmountOwingCents != 13_000 || ws.Line114RefundClaimedCents != 0 {
		t.Errorf("balance lines 113/114/115 = %d/%d/%d, want 13000/0/13000",
			ws.Line113BalanceCents, ws.Line114RefundClaimedCents, ws.Line115AmountOwingCents)
	}
	if !strings.Contains(ws.ManualEntryNote, "entered by hand") ||
		!strings.Contains(ws.ManualEntryNote, "104") ||
		!strings.Contains(ws.ManualEntryNote, "107") {
		t.Errorf("manual_entry_note = %q, must state in words which lines are entered by hand", ws.ManualEntryNote)
	}
	if len(ws.Lines) != 14 {
		t.Errorf("lines len = %d, want 14 CRA lines (101..115)", len(ws.Lines))
	}
}

// Leg 3: The period follows the setting: a quarter is three months; monthly and yearly change only the dates.
func TestWorksheet_PeriodFollowsFilingFrequencySetting(t *testing.T) {
	f := newFakeStore()
	books := newFakeMonthBooks()
	books.revenue[5] = 100_000
	books.salesHST[5] = 13_000
	books.billITCs[5] = 3_900
	books.gl2200[5] = 9_100

	svc, _ := newSvc(f)
	svc.WithSalesHSTs(books).WithReturnHSTs(books).WithBillITCs(books).WithGLBalances(books).WithGLRevenue(books)
	ctx := context.Background()

	// Default is QUARTERLY: Q2 is three months (2026-04-01 through 2026-06-30).
	qtr, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2026, Quarter: 2, Month: 5})
	if err != nil {
		t.Fatalf("quarterly WorksheetReport: %v", err)
	}
	if qtr.FilingFrequency != "QUARTERLY" || qtr.FromDate != "2026-04-01" || qtr.ToDate != "2026-06-30" {
		t.Errorf("Leg 3 quarterly: freq=%q from=%q to=%q, want QUARTERLY 2026-04-01..2026-06-30",
			qtr.FilingFrequency, qtr.FromDate, qtr.ToDate)
	}

	// Switch dealer setting to MONTHLY: May is one month (2026-05-01 through 2026-05-31), figures unchanged.
	if _, err := svc.UpdateConfig(ctx, Config{RoundMode: "INVOICE_TOTAL", FilingFrequency: "MONTHLY"}); err != nil {
		t.Fatalf("UpdateConfig MONTHLY: %v", err)
	}
	mon, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2026, Quarter: 2, Month: 5})
	if err != nil {
		t.Fatalf("monthly WorksheetReport: %v", err)
	}
	if mon.FilingFrequency != "MONTHLY" || mon.FromDate != "2026-05-01" || mon.ToDate != "2026-05-31" {
		t.Errorf("Leg 3 monthly: freq=%q from=%q to=%q, want MONTHLY 2026-05-01..2026-05-31",
			mon.FilingFrequency, mon.FromDate, mon.ToDate)
	}
	if mon.Line109NetTaxCents != qtr.Line109NetTaxCents || mon.Line103CollectedCents != qtr.Line103CollectedCents || mon.Line106ITCsCents != qtr.Line106ITCsCents {
		t.Errorf("Leg 3 monthly figures changed: got 103=%d 106=%d 109=%d, want %d %d %d",
			mon.Line103CollectedCents, mon.Line106ITCsCents, mon.Line109NetTaxCents,
			qtr.Line103CollectedCents, qtr.Line106ITCsCents, qtr.Line109NetTaxCents)
	}

	// Switch dealer setting to YEARLY: twelve months (2026-01-01 through 2026-12-31), figures unchanged.
	if _, err := svc.UpdateConfig(ctx, Config{RoundMode: "INVOICE_TOTAL", FilingFrequency: "YEARLY"}); err != nil {
		t.Fatalf("UpdateConfig YEARLY: %v", err)
	}
	yr, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2026, Quarter: 2, Month: 5})
	if err != nil {
		t.Fatalf("yearly WorksheetReport: %v", err)
	}
	if yr.FilingFrequency != "YEARLY" || yr.FromDate != "2026-01-01" || yr.ToDate != "2026-12-31" {
		t.Errorf("Leg 3 yearly: freq=%q from=%q to=%q, want YEARLY 2026-01-01..2026-12-31",
			yr.FilingFrequency, yr.FromDate, yr.ToDate)
	}
	if yr.Line109NetTaxCents != qtr.Line109NetTaxCents || yr.Line103CollectedCents != qtr.Line103CollectedCents || yr.Line106ITCsCents != qtr.Line106ITCsCents {
		t.Errorf("Leg 3 yearly figures changed: got 103=%d 106=%d 109=%d, want %d %d %d",
			yr.Line103CollectedCents, yr.Line106ITCsCents, yr.Line109NetTaxCents,
			qtr.Line103CollectedCents, qtr.Line106ITCsCents, qtr.Line109NetTaxCents)
	}
}

// Leg 4 & Leg 5: A credit memo's HST reduces line 103, and a vendor bill's ITC raises line 106.
func TestWorksheet_CreditMemoReducesLine103AndVendorBillRaisesLine106(t *testing.T) {
	f := newFakeStore()
	books := newFakeMonthBooks()
	books.revenue[5] = 100_000
	books.salesHST[5] = 13_000
	books.gl2200[5] = 13_000

	svc, _ := newSvc(f)
	svc.WithSalesHSTs(books).WithReturnHSTs(books).WithBillITCs(books).WithGLBalances(books).WithGLRevenue(books)
	ctx := context.Background()

	before, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2026, Quarter: 2})
	if err != nil {
		t.Fatalf("baseline WorksheetReport: %v", err)
	}

	// Leg 4: A credit memo with -2600 HST reduces the period's sales HST from 13000 to 10400.
	books.salesHST[5] = 13_000 - 2_600
	books.gl2200[5] = 10_400
	afterCM, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2026, Quarter: 2})
	if err != nil {
		t.Fatalf("after credit memo WorksheetReport: %v", err)
	}
	if before.Line103CollectedCents != 13_000 || afterCM.Line103CollectedCents != 10_400 {
		t.Errorf("Leg 4: credit memo HST (-2600) must reduce line 103 from 13000 to 10400, got before=%d after=%d",
			before.Line103CollectedCents, afterCM.Line103CollectedCents)
	}

	// Leg 5: A posted vendor bill with 3900 ITC raises line 106 from 0 to 3900.
	books.billITCs[5] = 3_900
	books.gl2200[5] = 10_400 - 3_900
	afterBill, err := svc.WorksheetReport(ctx, WorksheetPeriod{Year: 2026, Quarter: 2})
	if err != nil {
		t.Fatalf("after vendor bill WorksheetReport: %v", err)
	}
	if afterCM.Line106ITCsCents != 0 || afterBill.Line106ITCsCents != 3_900 {
		t.Errorf("Leg 5: vendor bill ITC (3900) must raise line 106 from 0 to 3900, got before=%d after=%d",
			afterCM.Line106ITCsCents, afterBill.Line106ITCsCents)
	}
	if afterBill.Line109NetTaxCents != 6_500 || !afterBill.TiedOut {
		t.Errorf("after credit memo and vendor bill: line 109 = %d, tied_out = %v, want 6500/true",
			afterBill.Line109NetTaxCents, afterBill.TiedOut)
	}
}
