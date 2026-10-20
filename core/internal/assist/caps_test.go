package assist

// caps_test.go, slice 2 sub item 3: the dealer wide monthly cap.

import (
	"context"
	"testing"
	"time"
)

// seedDealerSpend puts settled spend for other audiences in the table.
func seedDealerSpend(sp *memSpend, cost int64) {
	sp.rows = append(sp.rows,
		SpendRow{Kind: SpendSettlement, ExchangeID: "pro-1", CallNo: 1, Audience: AudiencePro, PortalUserID: "p-1", Alias: "a", Model: "m", CostMicroCents: cost / 2, At: at(2)},
		SpendRow{Kind: SpendSettlement, ExchangeID: "emb-1", CallNo: 1, Audience: AudienceEmbed, Alias: "a", Model: "m", CostMicroCents: cost - cost/2, At: at(3)},
	)
}

func TestDealerCapIsSharedAcrossAudiences(t *testing.T) {
	sp := &memSpend{}
	seedDealerSpend(sp, 10_000_000)
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	svc := meteredService(p, sp).WithDealerCap(10_000_000)

	_, err := svc.complete(context.Background(), manager(), CompletionRequest{})
	wantErrIs(t, err, ErrDealerCap)
	if p.calls != 0 {
		t.Fatalf("provider calls = %d, want 0 once the cap is reached by pro and embed spend", p.calls)
	}
}

func TestDealerCapRefusesAReservationThatWouldPassIt(t *testing.T) {
	sp := &memSpend{}
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	// One call reserves at least MaxOutputTokens of output at the test price; a cap below that is
	// passed by the reservation itself.
	svc := meteredService(p, sp).WithDealerCap(testPrice.Cost(0, MaxOutputTokens, 0) - 1)

	_, err := svc.complete(context.Background(), manager(), CompletionRequest{})
	wantErrIs(t, err, ErrDealerCap)
	if p.calls != 0 || len(sp.rows) != 0 {
		t.Fatalf("calls %d rows %d, want a refusal before any reservation", p.calls, len(sp.rows))
	}
}

func TestDealerCapUnsetRefusesPaidCallsAndAllowsTheStub(t *testing.T) {
	sp := &memSpend{}
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	svc := meteredService(p, sp).WithDealerCap(0)
	_, err := svc.complete(context.Background(), manager(), CompletionRequest{})
	wantErrIs(t, err, ErrMeterUnavailable)
	if p.calls != 0 {
		t.Fatalf("a paid call went out with no cap configured")
	}

	stub := NewService(NewRegistry(seededReaders().deps(), nil), NewStubProvider(), &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil).
		WithMetering(&memSpend{}, memPrices{}).WithDealerCap(0)
	if _, err := stub.complete(context.Background(), manager(), CompletionRequest{}); err != nil {
		t.Fatalf("the stub costs nothing and must not need a cap: %v", err)
	}
}

func TestAHugeUserBudgetCannotRaiseTheDealerCap(t *testing.T) {
	sp := &memSpend{}
	seedDealerSpend(sp, 5_000_000)
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	lim := newFakeLimits()
	lim.users["u-sm"] = 1 << 40
	svc := NewService(NewRegistry(seededReaders().deps(), nil), p, &fakeLedger{}, lim, StaticSettings{}, nil).
		WithClock(func() time.Time { return at(11) }).
		WithMetering(sp, testPrices()).
		WithDealerCap(5_000_000)

	ans, err := svc.Ask(context.Background(), manager(), "where is SO-4?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ans.LimitReached || ans.Text != LimitReachedMessage {
		t.Fatalf("answer = %+v, want the over budget answer", ans)
	}
	if p.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", p.calls)
	}
}

func TestADealerAdminCannotWriteTheCap(t *testing.T) {
	svc := NewService(NewRegistry(seededReaders().deps(), nil), NewStubProvider(), &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil)
	for _, key := range []string{"dealer_cap", "dealer_cap_micro_cents", "dealer_wide_cap", "cap"} {
		if err := svc.SetSetting(context.Background(), key, "999999999", "u-gm"); err == nil {
			t.Errorf("SetSetting accepted %q: the dealer wide cap is deployment configuration, not a setting", key)
		}
	}
}

func TestParseDealerCap(t *testing.T) {
	for raw, want := range map[string]int64{"": 0, " 250000000 ": 250000000, "abc": 0, "-5": 0, "1.5": 0, "0": 0} {
		if got := ParseDealerCap(raw); got != want {
			t.Errorf("ParseDealerCap(%q) = %d, want %d", raw, got, want)
		}
	}
	if EnvDealerCap != "HOS_ASSIST_DEALER_CAP_MICROCENTS" {
		t.Errorf("the variable name moved: %s", EnvDealerCap)
	}
}
