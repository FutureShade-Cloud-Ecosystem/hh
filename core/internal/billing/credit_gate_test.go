package billing

// credit_gate_test.go, DIB-65 / DIB-52 unit tests over a fake store. The policy rule is a pure
// function precisely so it can be argued with here, without a database, a clock or a job runner.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// -- the pure policy rule ------------------------------------------------------------------------

func TestEvaluateAccount_TriggersAndTheirPrecedence(t *testing.T) {
	pol := CreditPolicy{
		AgingBucketDays: 60, AgingThresholdCents: 0, AgingHoldEnabled: true,
		LimitHoldEnabled: true, LimitToleranceCents: 0,
	}
	cases := []struct {
		name     string
		row      AgingRow
		wantKind string
		wantIn   string
	}{
		{
			name: "clean account on terms is not held",
			row:  AgingRow{TermsDays: 30, CreditLimitCents: 500000, TotalCents: 100000},
		},
		{
			name:     "AR past the bucket trips the aging hold",
			row:      AgingRow{TermsDays: 30, CreditLimitCents: 500000, TotalCents: 100000, PastBucketCents: 25000},
			wantKind: HoldARAging,
			wantIn:   "60 days past due",
		},
		{
			name:     "open AR over the limit trips the limit hold",
			row:      AgingRow{TermsDays: 30, CreditLimitCents: 100000, TotalCents: 150000},
			wantKind: HoldCreditLimit,
			wantIn:   "credit limit",
		},
		{
			// Both true → CREDIT_LIMIT, because it is the more actionable thing to say on the phone.
			name:     "both triggers report the limit breach",
			row:      AgingRow{TermsDays: 30, CreditLimitCents: 100000, TotalCents: 150000, PastBucketCents: 150000},
			wantKind: HoldCreditLimit,
		},
		{
			// A COD account has no credit to breach. Its enforcement is the dispatch gate, and holding
			// it here would stop a customer who is doing exactly what their terms require.
			name: "a COD account is never held by the credit rules",
			row:  AgingRow{TermsDays: 0, CreditLimitCents: 0, TotalCents: 900000, PastBucketCents: 900000},
		},
		{
			// A zero limit means "no limit recorded", not "a limit of zero". Reading it the other way
			// would hold every account that has never had a limit set, which, at go-live, is all of them.
			name: "a zero credit limit is not a limit of zero",
			row:  AgingRow{TermsDays: 30, CreditLimitCents: 0, TotalCents: 900000},
		},
		{
			name: "exactly at the limit is not over it",
			row:  AgingRow{TermsDays: 30, CreditLimitCents: 100000, TotalCents: 100000},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, reason := evaluateAccount(tc.row, pol)
			if kind != tc.wantKind {
				t.Fatalf("hold kind = %q, want %q (reason %q)", kind, tc.wantKind, reason)
			}
			if tc.wantKind != "" && tc.wantIn != "" && !strings.Contains(reason, tc.wantIn) {
				t.Fatalf("reason %q must explain itself to a human, want it to mention %q", reason, tc.wantIn)
			}
			if tc.wantKind == "" && reason != "" {
				t.Fatalf("a clean account produced a reason: %q", reason)
			}
		})
	}
}

func TestEvaluateAccount_ToleranceAndDisableSwitches(t *testing.T) {
	over := AgingRow{TermsDays: 30, CreditLimitCents: 100000, TotalCents: 100300}

	// A $3 drift on a $1,000 limit must not stop a truck.
	tolerant := CreditPolicy{LimitHoldEnabled: true, LimitToleranceCents: 500, AgingHoldEnabled: true}
	if kind, _ := evaluateAccount(over, tolerant); kind != "" {
		t.Fatalf("a breach inside the tolerance was held as %s", kind)
	}
	// ... but a breach past the tolerance still is.
	strict := CreditPolicy{LimitHoldEnabled: true, LimitToleranceCents: 100, AgingHoldEnabled: true}
	if kind, _ := evaluateAccount(over, strict); kind != HoldCreditLimit {
		t.Fatalf("a breach past the tolerance = %q, want CREDIT_LIMIT", kind)
	}
	// Each trigger can be turned off independently.
	off := CreditPolicy{LimitHoldEnabled: false, AgingHoldEnabled: false, AgingBucketDays: 60}
	if kind, _ := evaluateAccount(AgingRow{TermsDays: 30, CreditLimitCents: 1, TotalCents: 999999, PastBucketCents: 999999}, off); kind != "" {
		t.Fatalf("both triggers disabled still held the account as %s", kind)
	}
}

// -- the dispatch gate ---------------------------------------------------------------------------

type fakeCreditStore struct {
	policy     CreditPolicy
	facts      DispatchFacts
	factsErr   error
	policyErr  error
	rows       []AgingRow
	placed     []string
	holdExists map[string]bool
	released   string
	overridden string
}

func (f *fakeCreditStore) CreditPolicy(context.Context, string) (CreditPolicy, error) {
	return f.policy, f.policyErr
}
func (f *fakeCreditStore) AgingRows(context.Context, time.Time, int, string) ([]AgingRow, error) {
	return f.rows, nil
}
func (f *fakeCreditStore) AgingRowsIn(context.Context, time.Time, int, string, BranchReach) ([]AgingRow, error) {
	return f.rows, nil
}
func (f *fakeCreditStore) ListHoldsIn(context.Context, string, BranchReach) ([]CreditHold, error) {
	return nil, nil
}
func (f *fakeCreditStore) PlaceHold(_ context.Context, customerID, kind, _, _ string) (string, bool, error) {
	if f.holdExists[customerID] {
		return "", false, nil
	}
	if f.holdExists == nil {
		f.holdExists = map[string]bool{}
	}
	f.holdExists[customerID] = true
	f.placed = append(f.placed, customerID+":"+kind)
	return "hold-" + customerID, true, nil
}
func (f *fakeCreditStore) ActiveHold(context.Context, string) (CreditHold, bool, error) {
	return CreditHold{}, false, nil
}
func (f *fakeCreditStore) ListHolds(context.Context, string) ([]CreditHold, error) { return nil, nil }
func (f *fakeCreditStore) OverrideHold(_ context.Context, holdID, _ string, _ time.Time, _ string) error {
	f.overridden = holdID
	return nil
}
func (f *fakeCreditStore) ReleaseHold(_ context.Context, holdID, _, _ string) (string, error) {
	f.released = holdID
	return HoldARAging, nil
}
func (f *fakeCreditStore) OrderDispatchFacts(context.Context, string) (DispatchFacts, error) {
	return f.facts, f.factsErr
}

func TestCanDispatch_StopsAndReasons(t *testing.T) {
	ctx := context.Background()
	codPolicy := CreditPolicy{CODEnforced: true}

	t.Run("a clean account on terms dispatches", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{policy: codPolicy,
			facts: DispatchFacts{TermsDays: 30, UnpaidCents: 50000}})
		if ok, reason := s.CanDispatch(ctx, "order-1"); !ok {
			t.Fatalf("a net-30 account with an open balance must dispatch; blocked with %q", reason)
		}
	})

	t.Run("a COD account with an unpaid balance is stopped", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{policy: codPolicy,
			facts: DispatchFacts{TermsDays: 0, UnpaidCents: 42500}})
		ok, reason := s.CanDispatch(ctx, "order-2")
		if ok {
			t.Fatal("COD with money outstanding must not dispatch, that is the definition of COD")
		}
		if !strings.Contains(reason, "PAYMENT DUE") {
			t.Fatalf("the driver's banner needs the words PAYMENT DUE, got %q", reason)
		}
		if !strings.Contains(reason, "$425.00") {
			t.Fatalf("the reason must name the amount so the driver can collect it, got %q", reason)
		}
	})

	t.Run("a COD account that has paid dispatches", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{policy: codPolicy,
			facts: DispatchFacts{TermsDays: 0, UnpaidCents: 0}})
		if ok, reason := s.CanDispatch(ctx, "order-3"); !ok {
			t.Fatalf("a paid COD order must dispatch; blocked with %q", reason)
		}
	})

	t.Run("COD enforcement can be turned off", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{policy: CreditPolicy{CODEnforced: false},
			facts: DispatchFacts{TermsDays: 0, UnpaidCents: 42500}})
		if ok, _ := s.CanDispatch(ctx, "order-4"); !ok {
			t.Fatal("with cod_enforced FALSE the gate must not stop a COD order")
		}
	})

	t.Run("an active hold stops dispatch whatever the terms", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{policy: codPolicy,
			facts: DispatchFacts{TermsDays: 30, UnpaidCents: 0, HoldActive: true, HoldKind: HoldCreditLimit}})
		ok, reason := s.CanDispatch(ctx, "order-5")
		if ok {
			t.Fatal("an account on hold must not dispatch even with nothing outstanding on this order")
		}
		if !strings.Contains(reason, "ON HOLD") {
			t.Fatalf("reason = %q, want an ACCOUNT ON HOLD banner", reason)
		}
	})

	t.Run("an override in force lets it through", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{policy: codPolicy,
			facts: DispatchFacts{TermsDays: 30, HoldActive: true, HoldKind: HoldARAging, OverrideInForce: true}})
		if ok, reason := s.CanDispatch(ctx, "order-6"); !ok {
			t.Fatalf("a live override must permit dispatch; blocked with %q", reason)
		}
	})

	t.Run("an NSF hold names itself so the office knows what to fix", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{policy: codPolicy,
			facts: DispatchFacts{TermsDays: 30, HoldActive: true, HoldKind: HoldNSF}})
		_, reason := s.CanDispatch(ctx, "order-7")
		if !strings.Contains(reason, "returned") {
			t.Fatalf("an NSF hold's banner should say a payment was returned, got %q", reason)
		}
	})
}

// TestCanDispatch_FailsClosed is the property the whole seam rests on. It is separated out because it
// is the one somebody will be tempted to "fix" during an incident.
func TestCanDispatch_FailsClosed(t *testing.T) {
	ctx := context.Background()
	t.Run("an unreadable order does not dispatch", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{factsErr: errors.New("connection refused")})
		ok, reason := s.CanDispatch(ctx, "order-x")
		if ok {
			t.Fatal("a failed AR read must NOT allow dispatch, a read failure is not a licence to release goods")
		}
		if !strings.Contains(reason, "unavailable") {
			t.Fatalf("reason = %q, want it to say the check was unavailable", reason)
		}
	})
	t.Run("an unreadable policy does not dispatch", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{policyErr: errors.New("no policy row"),
			facts: DispatchFacts{TermsDays: 0, UnpaidCents: 1}})
		if ok, _ := s.CanDispatch(ctx, "order-y"); ok {
			t.Fatal("an unreadable credit policy must not allow dispatch")
		}
	})
	t.Run("an empty order id does not dispatch", func(t *testing.T) {
		s := NewCreditService(&fakeCreditStore{})
		if ok, _ := s.CanDispatch(ctx, ""); ok {
			t.Fatal("an empty order id must not allow dispatch")
		}
	})
}

// TestAllowAllDispatchGate_IsTheShippedDefault pins the stub-true default Swarm C wires against.
func TestAllowAllDispatchGate_IsTheShippedDefault(t *testing.T) {
	var g ARDispatchGate = AllowAllDispatchGate{}
	ok, reason := g.CanDispatch(context.Background(), "anything")
	if !ok || reason != "" {
		t.Fatalf("the default gate returned (%v, %q), want (true, \"\"), an unadopted branch must behave "+
			"exactly as it did before this seam existed", ok, reason)
	}
}

// -- the nightly sweep ---------------------------------------------------------------------------

func TestEvaluate_FlagOffIsADryRunThatReportsTheBlastRadius(t *testing.T) {
	store := &fakeCreditStore{
		policy: CreditPolicy{AgingBucketDays: 60, AgingHoldEnabled: true, LimitHoldEnabled: true, AutoHoldEnabled: false},
		rows: []AgingRow{
			{CustomerID: "over-limit", TermsDays: 30, CreditLimitCents: 1000, TotalCents: 90000},
			{CustomerID: "aged", TermsDays: 30, TotalCents: 50000, PastBucketCents: 50000},
			{CustomerID: "clean", TermsDays: 30, CreditLimitCents: 900000, TotalCents: 5000},
		},
	}
	res, err := NewCreditService(store).Evaluate(context.Background(), "")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !res.DryRun {
		t.Fatal("auto_hold_enabled FALSE must report DryRun")
	}
	if res.Placed != 0 || len(store.placed) != 0 {
		t.Fatalf("a dry run placed %d hold(s), flag-off must touch nobody", res.Placed)
	}
	if res.WouldHold != 2 {
		t.Fatalf("WouldHold = %d, want 2, the point of the dry run is that an operator sees how many "+
			"accounts a flip stops at the counter tomorrow", res.WouldHold)
	}
	if res.Evaluated != 3 {
		t.Fatalf("Evaluated = %d, want 3", res.Evaluated)
	}
}

func TestEvaluate_PlacesHoldsAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	store := &fakeCreditStore{
		policy: CreditPolicy{AgingBucketDays: 60, AgingHoldEnabled: true, LimitHoldEnabled: true, AutoHoldEnabled: true},
		rows: []AgingRow{
			{CustomerID: "over-limit", TermsDays: 30, CreditLimitCents: 1000, TotalCents: 90000},
			{CustomerID: "clean", TermsDays: 30, CreditLimitCents: 900000, TotalCents: 5000},
		},
	}
	svc := NewCreditService(store)

	first, err := svc.Evaluate(ctx, "")
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if first.Placed != 1 || len(store.placed) != 1 || store.placed[0] != "over-limit:"+HoldCreditLimit {
		t.Fatalf("first sweep placed %v, want exactly the over-limit account as CREDIT_LIMIT", store.placed)
	}

	// The runner has no lease and WILL start a job again. A second sweep must place nothing.
	second, err := svc.Evaluate(ctx, "")
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	if second.Placed != 0 || len(store.placed) != 1 {
		t.Fatalf("the sweep is not idempotent: second run placed %d, total %v", second.Placed, store.placed)
	}
}

// -- override / release rules --------------------------------------------------------------------

func TestOverride_RequiresAReasonAndAFutureExpiry(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	svc := NewCreditService(&fakeCreditStore{}).WithClock(func() time.Time { return now })

	if err := svc.Override(ctx, "hold-1", "", now.Add(time.Hour), "user", ""); err == nil {
		t.Fatal("an override with no reason must be refused, an override without a reason is an unsigned cheque")
	}
	if err := svc.Override(ctx, "hold-1", "cheque in the mail", time.Time{}, "user", ""); err == nil {
		t.Fatal("an override with no expiry must be refused")
	}
	if err := svc.Override(ctx, "hold-1", "cheque in the mail", now.Add(-time.Hour), "user", ""); err == nil {
		t.Fatal("an override expiring in the PAST must be refused, 'temporarily' must not mean 'forever'")
	}
	if err := svc.Override(ctx, "hold-1", "cheque in the mail", now.Add(72*time.Hour), "user", ""); err != nil {
		t.Fatalf("a well-formed override must succeed: %v", err)
	}
}

func TestRelease_RequiresAReason(t *testing.T) {
	svc := NewCreditService(&fakeCreditStore{})
	if err := svc.Release(context.Background(), "hold-1", "", "user", ""); err == nil {
		t.Fatal("a release with no reason must be refused")
	}
}

func TestMoney_RendersCentsForHumans(t *testing.T) {
	for _, tc := range []struct {
		cents int64
		want  string
	}{{0, "$0.00"}, {5, "$0.05"}, {100, "$1.00"}, {42500, "$425.00"}, {-1234, "-$12.34"}} {
		if got := Money(tc.cents); got != tc.want {
			t.Fatalf("Money(%d) = %q, want %q", tc.cents, got, tc.want)
		}
	}
}
