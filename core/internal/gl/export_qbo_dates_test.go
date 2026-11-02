package gl

// export_qbo_dates_test.go, the AX-2 fix round's P1-1: every entry of the QBO file is dated inside
// its own journal's period. The posted house day dates an entry when it falls in the period; a
// roll forward out of a closed month (posted day before the period) goes out dated the period's
// first day, and a late approval (posted day after the period) the period's last day, the
// Description saying the document's own day in words.

import (
	"context"
	"strings"
	"testing"

	identity "github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity/staffcontext"
)

func qboDatesFixture(rows []ExportRow) (*Service, *fakeStore) {
	f := newFakeStore()
	f.exportRows = rows
	f.mappings = []AccountMapping{
		{Target: TargetQuickBooksOnline, AccountCode: "1000", AccountName: "Cash Bank", PackageAccount: "10100 Checking"},
		{Target: TargetQuickBooksOnline, AccountCode: "4000", AccountName: "Sales", PackageAccount: "40100 Sales"},
	}
	return newTestServiceAs(f, identity.StaffContext{UserID: "u-ctrl", RoleCode: controllerRole, Tier: 1}), f
}

func qboPair(entry int, posted string, year, month int, cents int64, memo string) []ExportRow {
	return []ExportRow{
		{EntryNo: entry, PostedAt: posted, PeriodYear: year, PeriodMonth: month, AccountCode: "1000", AccountName: "Cash Bank", DebitCents: cents, Memo: memo},
		{EntryNo: entry, PostedAt: posted, PeriodYear: year, PeriodMonth: month, AccountCode: "4000", AccountName: "Sales", CreditCents: cents, Memo: memo},
	}
}

func qboAssertInPeriod(t *testing.T, recs [][]string, first, last string) {
	t.Helper()
	for _, r := range recs[1:] {
		if r[1] < first || r[1] > last {
			t.Fatalf("JournalDate %q falls outside the file's period %s..%s: %q", r[1], first, last, r)
		}
	}
}

// A June 28 payment that rolled into open July goes out dated July 1, saying its own day.
func TestExport_QBOARollForwardEntryIsDatedTheFirstDayOfItsPeriod(t *testing.T) {
	svc, _ := qboDatesFixture(qboPair(7, "2096-06-28", 2096, 7, 5000, "payment"))
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 7, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	recs := qboCSV(t, res.Content)
	qboAssertInPeriod(t, recs, "2096-07-01", "2096-07-31")
	if recs[1][1] != "2096-07-01" {
		t.Fatalf("the rolled forward entry is dated %q, want 2096-07-01", recs[1][1])
	}
	if !strings.Contains(recs[1][5], "dated 28 June 2096, posted to July 2096") || !strings.Contains(recs[1][5], "payment") {
		t.Fatalf("the Description must keep the memo and say the document's own day with the year, got %q", recs[1][5])
	}
}

// A May draft approved June 2 sits in May's file dated May 31.
func TestExport_QBOALateApprovedDraftIsDatedTheLastDayOfItsPeriod(t *testing.T) {
	svc, _ := qboDatesFixture(qboPair(8, "2096-06-02", 2096, 5, 7000, "parked draft"))
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 5, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	recs := qboCSV(t, res.Content)
	qboAssertInPeriod(t, recs, "2096-05-01", "2096-05-31")
	if recs[1][1] != "2096-05-31" {
		t.Fatalf("the late approved entry is dated %q, want 2096-05-31", recs[1][1])
	}
	if !strings.Contains(recs[1][5], "approved 2 June 2096, posted to May 2096") {
		t.Fatalf("the Description must say the approval day with the year, got %q", recs[1][5])
	}
}

// An ordinary journal is unchanged: its own day, its own memo.
func TestExport_QBOAnOrdinaryEntryIsUnchanged(t *testing.T) {
	svc, _ := qboDatesFixture(qboPair(9, "2096-02-29", 2096, 2, 900, "ordinary"))
	res, err := svc.Export(context.Background(), ExportQuery{Year: 2096, Month: 2, Target: TargetQuickBooksOnline})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	recs := qboCSV(t, res.Content)
	qboAssertInPeriod(t, recs, "2096-02-01", "2096-02-29")
	if recs[1][1] != "2096-02-29" || recs[1][5] != "ordinary" {
		t.Fatalf("an in period entry must go out untouched, got %q", recs[1])
	}
}
