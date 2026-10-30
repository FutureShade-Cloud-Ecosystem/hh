//go:build integration

// export_whole_year_integration_test.go, W3 AX-1 item 3, audit GL-08: the whole year export fails
// on the real core. The desk's "Whole year" pick sends month 0; the service reads the year's rows
// fine and then records the batch, and gl_export_batches.month's own CHECK (month BETWEEN 1 AND 12)
// refuses the 0, so the whole year export dies after reading every row it cannot keep. The failure
// at source is the stored check, and the fix is the stored check: 0 joins it as the wider than a
// month batch word, never a calendar month.

package gl

import (
	"context"
	"testing"
	"time"
)

// TestIntegration_Export_TheWholeYearRunsOnTheSeededCore is red leg 3 on the seeded 2026 books: the
// pick the desk's Whole year button sends must answer the year's rows and record its batch.
func TestIntegration_Export_TheWholeYearRunsOnTheSeededCore(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	svc := exportSvc(t, pool, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))

	var seeded int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM v_gl_detail WHERE period_year = 2026`).Scan(&seeded); err != nil {
		t.Fatalf("count the seeded 2026 rows: %v", err)
	}
	if seeded == 0 {
		t.Skip("no 2026 movement seeded")
	}

	res, err := svc.Export(ctx, ExportQuery{Year: 2026, Month: 0})
	if err != nil {
		t.Fatalf("the whole year export must run on the real core: %v", err)
	}
	if res.RowCount != int(seeded) {
		t.Fatalf("the whole year export reads %d rows, want every one of the year's %d", res.RowCount, seeded)
	}
	if res.BatchID == "" || res.Format != "GP_PARITY" || !res.IsDraft {
		t.Fatalf("the whole year export must record its GP-parity batch, got %+v", res)
	}
}

// TestIntegration_Export_AQuarterBatchRecordsMonth0 pins the wider than a month batch word: a
// quarter's export records month 0, and the stored check must carry it as it carries the months.
func TestIntegration_Export_AQuarterBatchRecordsMonth0(t *testing.T) {
	ctx := context.Background()
	pool := intPool(t)
	ensurePeriods(t, pool, 2095)

	seedExportJournal(t, pool, 2095, 4, "ax1-qa", "1000", "4000", 44400)
	seedExportJournal(t, pool, 2095, 6, "ax1-qb", "1000", "4000", 55500)

	svc := exportSvc(t, pool, time.Date(2095, 5, 10, 12, 0, 0, 0, time.UTC))
	res, err := svc.Export(ctx, ExportQuery{Year: 2095, Quarter: 2})
	if err != nil {
		t.Fatalf("the quarter export must run: %v", err)
	}
	if res.RowCount != 4 {
		t.Fatalf("the quarter export reads %d rows, want Q2's four", res.RowCount)
	}
	var month int
	if err := pool.QueryRow(ctx,
		`SELECT month FROM gl_export_batches WHERE id = $1::uuid`, res.BatchID).Scan(&month); err != nil {
		t.Fatalf("read the recorded batch: %v", err)
	}
	if month != 0 {
		t.Fatalf("a quarter's batch records month %d, want the wider than a month word 0", month)
	}
}
