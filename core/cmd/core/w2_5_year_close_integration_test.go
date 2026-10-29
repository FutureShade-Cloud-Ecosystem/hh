//go:build integration

package main

// w2_5_year_close_integration_test.go, W2-5 sub item 4: the year end close's
// per month pre flight reads the stock ledger as of each MONTH'S END, through
// the same runClosePreFlight the month close runs: closing a year seals no
// month a later year moved the stock on, and refuses a year a later year
// corrected.
//
// THE LEG. The closed year is a real year that has ended (the service clock
// refuses a running year's close by design), planted the way the month walk's
// year end leg plants it: twelve fiscal periods, an opening stock journal
// dated inside the year against the company's stock value, and one revenue and
// one expense journal so the close sweeps a real P&L. The NEXT year then moves
// the stock for real (an order fulfilment through the production seams) and
// writes 1200 down book only, both dated now, inside the next year. Closing
// the closed year runs the pre flight for every month it seals; each month's
// stock tie reads the ledger as of that month's end, where the year's own
// opening stock stands and the next year's activity has no place, so the close
// seals all twelve months. On the code before this item the same close is
// refused: the tie reads both sides now, and the next year's book only write
// down names a present discrepancy no month end owns.
//
//	Run: DATABASE_URL='postgres://.../postgres?sslmode=disable' \
//	     HH_CORE_TEST_DATABASE_URL='postgres://.../integration_core_gate?sslmode=disable' \
//	     go test -tags integration ./cmd/core/ -run W25Year -count=1 -v

import (
	"context"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/gl"
	staffctx "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

// w25ControllerCtx is the Financial Controller the year end close is narrowed to (FW1-09c):
// the year leg closes as the Controller, the way the service demands.
func w25ControllerCtx(ctx context.Context) context.Context {
	return staffctx.ContextWithStaff(ctx, staffctx.StaffContext{
		UserID: "d7000000-0000-4000-9000-000000000006", RoleCode: "FINANCIAL_CONTROLLER",
		Scope: staffctx.ScopeGlobal, Status: staffctx.StatusActive,
	})
}

// TestW25TheYearCloseReadsEveryMonthEnd closes a planted year whose next year
// moved the stock and wrote 1200 down book only: every month end of the closed
// year ties on its own figures, the close seals the year, and the next year's
// own month stays open with its discrepancy untouched.
func TestW25TheYearCloseReadsEveryMonthEnd(t *testing.T) {
	leg := w25Setup(t, "year")
	ctx := context.Background()
	closedYear := 2024 // the freshest year a fresh seeded database has no journals of its own in
	mustExec(t, ctx, leg.pool, `
		INSERT INTO fiscal_periods (year, month) SELECT $1, m FROM generate_series(1,12) AS m
		 ON CONFLICT (year, month) DO NOTHING`, closedYear)
	openedAt := time.Date(closedYear, time.January, 15, 12, 0, 0, 0, time.UTC)
	yearMonthEnd := time.Date(closedYear, time.February, 1, 0, 0, 0, 0, time.UTC)

	// The year's own book: the stock the company held at the year's month ends
	// against 1200, and a small P&L for the close to sweep. Only the planted
	// product holds stock that far back (every move-backed product's stock had
	// not arrived at any month end the year owns), so the opening journal is
	// the AS OF figure at the year's first month end, read through the
	// production adapter the pre flight reads through, not today's figure. The
	// seeded open invoices' opening AR rides the clock, so it belongs to the
	// running month and no month end the close reads.
	w25TieTheAR(t, leg, time.Time{})
	stock, err := glStockValueAdapter{svc: leg.money.inventorySvc}.StockValueCents(ctx, yearMonthEnd, "")
	if err != nil {
		t.Fatalf("the company's stock value at the year's first month end: %v", err)
	}
	gl1200, _, err := gl.NewRepository(leg.pool).AccountBalanceAsOf(ctx, "1200", yearMonthEnd, "")
	if err != nil {
		t.Fatalf("the company's 1200 at the year's first month end: %v", err)
	}
	if stock != int64(w25Units)*w25UnitCost {
		t.Fatalf("the company's stock at %04d-%02d holds %s, want the planted product's %s (every move-backed product stands at zero there)",
			closedYear, 2, fw1Dollars(stock), fw1Dollars(int64(w25Units)*w25UnitCost))
	}
	if delta := stock - gl1200; delta != 0 {
		lines := []gl.ExternalLine{
			{AccountCode: "1200", DebitCents: delta, Memo: "opening stock"},
			{AccountCode: "3000", CreditCents: delta, Memo: "opening equity"},
		}
		if delta < 0 {
			lines = []gl.ExternalLine{
				{AccountCode: "1200", CreditCents: -delta, Memo: "opening stock"},
				{AccountCode: "3000", DebitCents: -delta, Memo: "opening equity"},
			}
		}
		w25Post(t, leg, gl.ExternalJournal{
			Source: "MANUAL", IdempotencyKey: "w2-5:year:opening",
			Memo: "the closed year's opening stock", BranchID: leg.branchID, DocumentDate: openedAt,
			Lines: lines,
		})
	}
	const plantedRevenue, plantedExpense = int64(100000), int64(40000)
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "w2-5:year:revenue", Memo: "the closed year's revenue",
		BranchID: leg.branchID, DocumentDate: openedAt,
		Lines: []gl.ExternalLine{
			{AccountCode: "1000", DebitCents: plantedRevenue, Memo: "the year's cash sale"},
			{AccountCode: "4000", CreditCents: plantedRevenue, Memo: "the year's revenue"},
		},
	})
	w25Post(t, leg, gl.ExternalJournal{
		Source: "MANUAL", IdempotencyKey: "w2-5:year:expense", Memo: "the closed year's expense",
		BranchID: leg.branchID, DocumentDate: openedAt,
		Lines: []gl.ExternalLine{
			{AccountCode: "6100", DebitCents: plantedExpense, Memo: "the year's rent"},
			{AccountCode: "1000", CreditCents: plantedExpense, Memo: "the year's cash paid out"},
		},
	})

	// The NEXT year moves the stock for real, and writes 1200 down book only:
	// both land now, inside the next year, after every month end the closed
	// year owns.
	w25Move(t, leg, time.Now())
	w25BookOnly(t, leg, time.Time{}, "w2-5:year:write-down", "the next year's book only stock write down", true)

	// The year end close runs the pre flight for every month it seals.
	res, err := leg.money.glSvc.CloseYear(w25ControllerCtx(context.Background()), closedYear, "W2-5 year end close")
	if err != nil {
		t.Fatalf("the year end close of %d was refused although every month end of the year ties on its own figures: %v", closedYear, err)
	}
	if want := plantedRevenue - plantedExpense; res.NetIncomeCents != want {
		t.Fatalf("the year end close of %d reported a net income of %s, want the planted %s", closedYear, fw1Dollars(res.NetIncomeCents), fw1Dollars(want))
	}
	var sealed, total int
	if err := leg.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'CLOSED'), count(*) FROM fiscal_periods WHERE year = $1`, closedYear).Scan(&sealed, &total); err != nil {
		t.Fatalf("the closed year's period read: %v", err)
	}
	if sealed != 12 || total != 12 {
		t.Fatalf("the year end close of %d sealed %d of %d periods, want all twelve", closedYear, sealed, total)
	}
	var nextYearOpen string
	if err := leg.pool.QueryRow(ctx, `
		SELECT status FROM fiscal_periods WHERE year = date_part('year', now())::int AND month = date_part('month', now())::int`).Scan(&nextYearOpen); err != nil {
		t.Fatalf("the current month's period read: %v", err)
	}
	if nextYearOpen != "OPEN" {
		t.Fatalf("the year end close of %d touched the running month: it reads %s, want OPEN", closedYear, nextYearOpen)
	}
}
