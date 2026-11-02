package gl

// export_sage50_dates_test.go, W3 AX-3 item 2's unit half: the Sage 50 body dates each
// transaction inside its own journal's period through the one clamp helper both import bodies
// share, the rule AX-2's QBO body proved (PR 742's P1-1) and its delta read carried forward. The
// legs mirror the QBO date legs; the database halves live in
// export_sage50_dates_integration_test.go.

import (
	"context"
	"strings"
	"testing"
)

// A June 28 payment that rolled into open July goes out dated July 1, the note naming the
// document's own day and carrying the year.
func TestExport_Sage50ARollForwardTransactionIsDatedTheFirstDayOfItsPeriod(t *testing.T) {
	svc, _ := sageFixture([]ExportRow{
		{EntryNo: 7, PostedAt: "2051-06-28", PeriodYear: 2051, PeriodMonth: 7, AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 5000, Memo: "payment"},
		{EntryNo: 7, PostedAt: "2051-06-28", PeriodYear: 2051, PeriodMonth: 7, AccountCode: "4000", AccountName: "Sales", CreditCents: 5000, Memo: "payment"},
	})
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2051, Month: 7, Target: TargetSage50})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	txs := sageFile(t, res.Content)
	sageAssertInPeriod(t, txs, 2051, 7)
	if txs[0].Date != "07-01-51" {
		t.Fatalf("the rolled forward transaction is dated %q, want the period's first day 07-01-51", txs[0].Date)
	}
	if !strings.Contains(txs[0].Comment, "dated 28 June 2051, posted to July 2051") || !strings.Contains(txs[0].Comment, "payment") {
		t.Fatalf("the comment must keep the memo and say the document's own day with the year, got %q", txs[0].Comment)
	}
}

// A May draft approved June 2 sits in May's file dated May 31, the note naming the approval day
// and carrying the year.
func TestExport_Sage50ALateApprovedTransactionIsDatedTheLastDayOfItsPeriod(t *testing.T) {
	svc, _ := sageFixture([]ExportRow{
		{EntryNo: 8, PostedAt: "2049-06-02", PeriodYear: 2049, PeriodMonth: 5, AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 7000, Memo: "parked draft"},
		{EntryNo: 8, PostedAt: "2049-06-02", PeriodYear: 2049, PeriodMonth: 5, AccountCode: "4000", AccountName: "Sales", CreditCents: 7000, Memo: "parked draft"},
	})
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2049, Month: 5, Target: TargetSage50})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	txs := sageFile(t, res.Content)
	sageAssertInPeriod(t, txs, 2049, 5)
	if txs[0].Date != "05-31-49" {
		t.Fatalf("the late approved transaction is dated %q, want the period's last day 05-31-49", txs[0].Date)
	}
	if !strings.Contains(txs[0].Comment, "approved 2 June 2049, posted to May 2049") {
		t.Fatalf("the comment must say the approval day with the year, got %q", txs[0].Comment)
	}
}

// An ordinary transaction is unchanged: its own day, its own memo, no note.
func TestExport_Sage50AnOrdinaryTransactionIsUnchanged(t *testing.T) {
	svc, _ := sageFixture([]ExportRow{
		{EntryNo: 9, PostedAt: "2048-02-29", PeriodYear: 2048, PeriodMonth: 2, AccountCode: "1000", AccountName: "Cash Bank", DebitCents: 900, Memo: "ordinary"},
		{EntryNo: 9, PostedAt: "2048-02-29", PeriodYear: 2048, PeriodMonth: 2, AccountCode: "4000", AccountName: "Sales", CreditCents: 900, Memo: "ordinary"},
	})
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2048, Month: 2, Target: TargetSage50})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	txs := sageFile(t, res.Content)
	sageAssertInPeriod(t, txs, 2048, 2)
	if txs[0].Date != "02-29-48" || txs[0].Comment != "ordinary" {
		t.Fatalf("an in period transaction must go out untouched, got dated %s comment %q", txs[0].Date, txs[0].Comment)
	}
}
