package billing

// customer_ar_read_test.go, one account's AR position, without a database.
//
// The load-bearing assertion here is TestCustomerAging_ReadsTheConsolesOwnQuery: the whole reason
// this read exists in billing rather than in a consumer is that it must not be a second opinion
// about what a customer owes. The test proves it by giving the store rows that could not be
// reproduced by any independent calculation and checking they come back unchanged.

import (
	"context"
	"errors"
	"testing"
	"time"
)

// arFake is the credit store fake plus the activity seam.
type arFake struct {
	*fakeCreditStore
	activity  []ARActivity
	actErr    error
	gotID     string
	gotLimit  int
	actCalled bool
}

func (f *arFake) CustomerActivity(_ context.Context, customerID string, limit int) ([]ARActivity, error) {
	f.gotID, f.gotLimit, f.actCalled = customerID, limit, true
	return f.activity, f.actErr
}

func newARSvc(rows []AgingRow, activity []ARActivity) (*CreditService, *arFake) {
	f := &arFake{fakeCreditStore: &fakeCreditStore{rows: rows, policy: CreditPolicy{AgingBucketDays: 60}}, activity: activity}
	return NewCreditService(f), f
}

var arAt = time.Date(2026, 8, 11, 9, 30, 0, 0, time.UTC)

// TestCustomerAging_ReadsTheConsolesOwnQuery, the numbers come back exactly as the AR worklist
// produced them, for the account asked about and no other.
func TestCustomerAging_ReadsTheConsolesOwnQuery(t *testing.T) {
	svc, _ := newARSvc([]AgingRow{
		{CustomerID: "c-other", TotalCents: 999999},
		{CustomerID: "c-1", CustomerName: "Northgate", CurrentCents: 100000,
			Days30Cents: 25000, Days60Cents: 10000, Days90PlusCents: 5000, TotalCents: 140000},
	}, nil)
	svc = svc.WithClock(func() time.Time { return arAt })

	row, asOf, found, err := svc.CustomerAging(context.Background(), "c-1")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if row.CustomerName != "Northgate" {
		t.Fatalf("the wrong account came back: %+v", row)
	}
	if row.OpenCents() != 140000 {
		t.Errorf("open = %d, want the worklist's own TotalCents 140000", row.OpenCents())
	}
	if !asOf.Equal(arAt) {
		t.Errorf("as-of = %v, want the service clock %v", asOf, arAt)
	}
}

// TestOverdueCents_IsTheLateBucketsAddedNotTotalMinusCurrent. The distinction is not cosmetic: the
// buckets and the total are separate FILTER aggregates over the same rows, so a subtraction would
// silently absorb any row that neither bucketed nor aged (a NULL due date, a new invoice status)
// into "overdue" and report money as late that nobody has aged.
func TestOverdueCents_IsTheLateBucketsAddedNotTotalMinusCurrent(t *testing.T) {
	// A deliberately inconsistent row: total is LARGER than the buckets sum to.
	row := AgingRow{CurrentCents: 100000, Days30Cents: 1000, Days60Cents: 2000, Days90PlusCents: 3000, TotalCents: 500000}

	if got := row.OverdueCents(); got != 6000 {
		t.Fatalf("overdue = %d, want 6000 (the three late buckets). Total−Current would have said %d and would have called %d of unaged money late.",
			got, row.TotalCents-row.CurrentCents, row.TotalCents-row.CurrentCents-6000)
	}
}

// TestCustomerAging_NoOpenBalanceIsNotFoundAndThatIsAGoodAnswer: agingSelect's HAVING drops accounts
// netting to zero, so an account in perfect standing is simply absent from the worklist.
func TestCustomerAging_NoOpenBalanceIsNotFoundAndThatIsAGoodAnswer(t *testing.T) {
	svc, _ := newARSvc([]AgingRow{{CustomerID: "c-other", TotalCents: 1}}, nil)

	row, asOf, found, err := svc.CustomerAging(context.Background(), "c-1")
	if err != nil {
		t.Fatalf("an account with nothing outstanding is an ANSWER: %v", err)
	}
	if found {
		t.Fatal("found=true for an account absent from the worklist")
	}
	if row.TotalCents != 0 {
		t.Fatalf("row = %+v, want a zero row", row)
	}
	if asOf.IsZero() {
		t.Error("as-of must be stamped even when the account is not in the worklist, a zero balance is still 'as of' a moment")
	}
}

func TestCustomerAging_AnEmptyCustomerIDAsksNothing(t *testing.T) {
	svc, _ := newARSvc([]AgingRow{{CustomerID: "c-1", TotalCents: 5}}, nil)
	_, _, found, err := svc.CustomerAging(context.Background(), "  ")
	if err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

// -- activity -----------------------------------------------------------------------------------

func TestCustomerActivity_PassesThroughAndBoundsTheLimit(t *testing.T) {
	svc, f := newARSvc(nil, []ARActivity{
		{Kind: "PAYMENT", Reference: "PMT-1", AmountCents: -1000, OccurredAt: arAt},
	})

	rows, err := svc.CustomerActivity(context.Background(), "c-1", 5)
	if err != nil {
		t.Fatalf("CustomerActivity: %v", err)
	}
	if len(rows) != 1 || rows[0].Reference != "PMT-1" {
		t.Fatalf("rows = %+v", rows)
	}
	if f.gotID != "c-1" || f.gotLimit != 5 {
		t.Fatalf("store received id=%q limit=%d", f.gotID, f.gotLimit)
	}

	// An absurd limit is capped rather than forwarded, a chat answer with 5000 ledger lines in it
	// is not an answer, and an unbounded LIMIT is an unbounded query.
	if _, err := svc.CustomerActivity(context.Background(), "c-1", 5000); err != nil {
		t.Fatal(err)
	}
	if f.gotLimit != 10 {
		t.Fatalf("limit = %d for an out-of-range request, want the default 10", f.gotLimit)
	}
	if _, err := svc.CustomerActivity(context.Background(), "c-1", 0); err != nil {
		t.Fatal(err)
	}
	if f.gotLimit != 10 {
		t.Fatalf("limit = %d for an unset request, want the default 10", f.gotLimit)
	}
}

func TestCustomerActivity_EmptyIsAnAnswerNotAnError(t *testing.T) {
	svc, _ := newARSvc(nil, nil)
	rows, err := svc.CustomerActivity(context.Background(), "c-1", 10)
	if err != nil {
		t.Fatalf("an account with no movements is an ANSWER: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %d", len(rows))
	}
}

func TestCustomerActivity_AStoreWithoutTheSeamSaysSo(t *testing.T) {
	svc := NewCreditService(&fakeCreditStore{}) // no activity seam
	_, err := svc.CustomerActivity(context.Background(), "c-1", 10)
	if !errors.Is(err, ErrCustomerActivityNotConfigured) {
		t.Fatalf("err = %v, want ErrCustomerActivityNotConfigured", err)
	}
}

// TestLooksLikeUUIDish grades the id before it can reach a uuid column, the DIB-443 rule applied
// one module over: a typo must not come back as an internal error.
func TestLooksLikeUUIDish(t *testing.T) {
	if !looksLikeUUIDish("11111111-2222-4333-8444-555555555555") {
		t.Error("a well-formed uuid was rejected")
	}
	for _, bad := range []string{"", "CUST-1001", "11111111-2222-4333-8444-55555555555", "zzzzzzzz-2222-4333-8444-555555555555", "11111111_2222-4333-8444-555555555555"} {
		if looksLikeUUIDish(bad) {
			t.Errorf("%q was accepted as a uuid", bad)
		}
	}
}
