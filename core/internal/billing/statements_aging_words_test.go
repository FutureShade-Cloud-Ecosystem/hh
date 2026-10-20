package billing

import (
	"context"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/houseday"
	"strings"
	"testing"
	"time"
)

// The statement's aging heads, the over band notice and the footer date read in the screen's one
// vocabulary. The four stored bands are fixed at 30 day steps whatever the dealer's aging window is,
// so the words name the bands the figures really are.
func TestRenderStatementHTML_AgingWordsAndFooterDate(t *testing.T) {
	asOf := time.Date(2026, time.March, 1, 9, 0, 0, 0, time.FixedZone("dealer", -5*3600))
	r := AgingRow{CustomerName: "Customer one", AccountNo: "A1", CurrentCents: 100, Days30Cents: 200,
		Days60Cents: 300, Days60To90Cents: 400, Days90PlusCents: 500, TotalCents: 1500}
	lines := []StatementLine{{Date: time.Date(2026, time.February, 14, 0, 0, 0, 0, time.UTC), DocNo: "INV-1"}}
	out := RenderStatementHTML(StatementHeader{BranchName: "Branch"}, r, lines, asOf)

	for _, want := range []string{">Current<", ">1 to 30 days<", ">31 to 60 days<", ">Over 60 days<", ">Total due<",
		"is over 60 days past due", "As of Mar 1, 2026<", ">Feb 14, 2026<", "Statement generated Mar 1, 2026."} {
		if !strings.Contains(out, want) {
			t.Errorf("statement lacks %q", want)
		}
	}
	for _, bad := range []string{"1-30 days", "31-60 days", "60+ days", "more than 60 days", "generated 2026-03-01", "1 March 2026", "2026-02-14"} {
		if strings.Contains(out, bad) {
			t.Errorf("statement still carries %q", bad)
		}
	}
}

// The statement notice's reference is read aloud in the customer's text ("your account statement STMT
// ... is ready"), so its date is in the glossary form too.
func TestGenerateRun_NoticeReferenceReadsInTheGlossaryForm(t *testing.T) {
	store := &headerlessStore{}
	svc := NewStatementService(store)
	if _, _, err := svc.GenerateRun(context.Background(), RunModeMass, "", time.Date(2026, 3, 31, 12, 0, 0, 0, time.UTC), "actor"); err != nil {
		t.Fatalf("GenerateRun: %v", err)
	}
	if store.notice == nil {
		t.Fatal("no notice was built for a positive balance")
	}
	if got, want := StatementRef(store.notice.AsOfDate), "STMT Mar 31, 2026"; got != want {
		t.Errorf("notice reference = %q, want %q", got, want)
	}
}

// An activity line's date is the dealer's day, the same day the statement's cut off uses. A 9 pm sale
// on Mar 1 in a zone west of UTC is Mar 2 in UTC and must still print Mar 1 under "As of Mar 1".
// StatementLines needs a database, so the conversion is tested where it is made, in statementLineDate.
func TestStatementLineDate_IsTheDealersDay(t *testing.T) {
	zone, err := houseday.NewZone("America/Toronto")
	if err != nil {
		t.Fatalf("zone: %v", err)
	}
	at := time.Date(2026, time.March, 2, 2, 0, 0, 0, time.UTC) // 9 pm on Mar 1, Toronto
	lines := []StatementLine{{Date: statementLineDate(zone, at)}}
	out := RenderStatementHTML(StatementHeader{BranchName: "Branch"}, AgingRow{}, lines, houseClockIn(zone, at))
	if !strings.Contains(out, ">Mar 1, 2026<") || strings.Contains(out, ">Mar 2, 2026<") {
		t.Errorf("a 9 pm Mar 1 sale must print Mar 1: %s", out)
	}
}
