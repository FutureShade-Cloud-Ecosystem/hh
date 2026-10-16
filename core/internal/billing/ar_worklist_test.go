package billing

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

type worklistFakeStore struct {
	policy CreditPolicy
	aging  []AgingRow
	facts  map[string]WorklistAccountFacts
}

func (f worklistFakeStore) CreditPolicy(context.Context, string) (CreditPolicy, error) {
	return f.policy, nil
}

func (f worklistFakeStore) AgingRowsIn(context.Context, time.Time, int, string, BranchReach) ([]AgingRow, error) {
	return append([]AgingRow(nil), f.aging...), nil
}

func (f worklistFakeStore) WorklistFacts(context.Context, time.Time, string, BranchReach) (map[string]WorklistAccountFacts, error) {
	return f.facts, nil
}

func worklistTestService(aging []AgingRow, facts map[string]WorklistAccountFacts) *ARWorklistService {
	return NewARWorklistService(worklistFakeStore{
		policy: CreditPolicy{AgingBucketDays: 60, AgingThresholdCents: 1, AgingHoldEnabled: true},
		aging:  aging,
		facts:  facts,
	}).WithClock(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() })
}

func worklistAging(id, name string, current, days30, days60, days60To90, days90Plus int64) AgingRow {
	return AgingRow{
		CustomerID: id, CustomerName: name, AccountNo: "AC-" + id,
		CurrentCents: current, Days30Cents: days30, Days60Cents: days60,
		Days60To90Cents: days60To90, Days90PlusCents: days90Plus,
		TotalCents:       current + days30 + days60 + days60To90 + days90Plus,
		CreditLimitCents: 100_000, TermsDays: 30,
	}
}

func TestARWorklistSummaryReconcilesS0Buckets(t *testing.T) {
	first := worklistAging("a", "Alpha", 1_000, 200, 300, 400, 500)
	second := worklistAging("b", "Beta", 0, 0, 0, 100, 50)
	got, err := worklistTestService([]AgingRow{first, second}, map[string]WorklistAccountFacts{
		"a": {UnappliedCreditCents: 75, BrokenPromises: 2},
		"b": {UnappliedCreditCents: 25},
	}).Worklist(context.Background(), WorklistFilter{})
	if err != nil {
		t.Fatal(err)
	}
	wantPastDue := first.OverdueCents() + second.OverdueCents()
	wantOver60 := first.Days60To90Cents + first.Days90PlusCents + second.Days60To90Cents + second.Days90PlusCents
	if got.Summary.TotalPastDueCents != wantPastDue {
		t.Fatalf("past due %d, want %d", got.Summary.TotalPastDueCents, wantPastDue)
	}
	if got.Summary.Over60Cents != wantOver60 {
		t.Fatalf("over 60 %d, want %d", got.Summary.Over60Cents, wantOver60)
	}
	if got.Summary.BrokenPromises != 2 || got.Summary.UnappliedCreditsCents != 100 {
		t.Fatalf("summary does not include promise and credit facts: %+v", got.Summary)
	}
}

func TestARWorklistUrgencyFiltersAndSearch(t *testing.T) {
	aging := []AgingRow{
		worklistAging("open", "Open Current", 1_000, 0, 0, 0, 0),
		worklistAging("old", "Old Debt", 0, 0, 0, 400, 0),
		worklistAging("due", "Due Promise", 500, 0, 0, 0, 0),
		worklistAging("broken", "Broken Promise", 0, 100, 0, 0, 0),
		worklistAging("credit", "Credit Only", 500, 0, 0, 0, 0),
	}
	facts := map[string]WorklistAccountFacts{
		"due":    {DuePromises: 1, OpenPromises: 1},
		"broken": {BrokenPromises: 1},
		"credit": {UnappliedCreditCents: 50},
	}
	svc := worklistTestService(aging, facts)
	all, err := svc.Worklist(context.Background(), WorklistFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, row := range all.Rows {
		order = append(order, row.CustomerID)
	}
	want := []string{"broken", "old", "due", "credit", "open"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("urgency order %v, want %v", order, want)
	}
	if all.Rows[0].RecommendedAction != WorklistActionReviewPromise || all.Rows[1].RecommendedAction != WorklistActionRecordPayment {
		t.Fatalf("recommended actions do not follow urgency: %+v", all.Rows[:2])
	}

	action, err := svc.Worklist(context.Background(), WorklistFilter{Chip: WorklistChipActionNeeded})
	if err != nil {
		t.Fatal(err)
	}
	if len(action.Rows) != 4 {
		t.Fatalf("action needed returned %d rows, want 4", len(action.Rows))
	}
	old, err := svc.Worklist(context.Background(), WorklistFilter{Chip: WorklistChipOver60})
	if err != nil {
		t.Fatal(err)
	}
	if len(old.Rows) != 1 || old.Rows[0].CustomerID != "old" {
		t.Fatalf("over 60 filter returned %+v", old.Rows)
	}
	search, err := svc.Worklist(context.Background(), WorklistFilter{Query: "broken promise", Chip: WorklistChipActionNeeded})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Rows) != 1 || search.Rows[0].CustomerID != "broken" {
		t.Fatalf("search returned %+v", search.Rows)
	}
	if search.Summary != all.Summary || search.Counts != all.Counts {
		t.Fatal("search or a chip changed the global strip")
	}
}

func TestARWorklistIncludesCreditAndPromiseOnlyAccounts(t *testing.T) {
	promiseDay := time.Unix(1_700_000_000, 0).UTC()
	got, err := worklistTestService(nil, map[string]WorklistAccountFacts{
		"credit": {
			Account:              WorklistAccount{CustomerID: "credit", CustomerName: "Credit Balance", AccountNo: "CR-1"},
			UnappliedCreditCents: 250,
		},
		"promise": {
			Account:     WorklistAccount{CustomerID: "promise", CustomerName: "Promise Due", AccountNo: "PR-1"},
			DuePromises: 1, OpenPromises: 1, PromisedCents: 400, NextPromiseDate: &promiseDay,
		},
	}).Worklist(context.Background(), WorklistFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("got %d rows, want credit and promise accounts", len(got.Rows))
	}
	if got.Rows[0].CustomerID != "promise" || got.Rows[1].CustomerID != "credit" {
		t.Fatalf("unexpected rows: %+v", got.Rows)
	}
	if got.Summary.UnappliedCreditsCents != 250 || got.Counts.ActionNeeded != 2 {
		t.Fatalf("credit or promise account missing from strip: %+v", got)
	}
}

func TestARWorklistRejectsUnknownFilter(t *testing.T) {
	_, err := worklistTestService(nil, nil).Worklist(context.Background(), WorklistFilter{Chip: "unknown"})
	if err == nil || !strings.Contains(err.Error(), "worklist filter") {
		t.Fatalf("unknown filter error %v", err)
	}
}

func TestARWorklistEmptyRowsEncodeAsArray(t *testing.T) {
	got, err := worklistTestService(nil, nil).Worklist(context.Background(), WorklistFilter{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"rows":[]`) {
		t.Fatalf("empty rows encoded as null: %s", body)
	}
}
