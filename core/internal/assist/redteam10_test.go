package assist

// redteam10_test.go, slice 2 sub item 7: red team case 10, spend. Every way a question can be
// over a limit, and in every one the fake provider has counted ZERO requests. The count is the
// proof: a refusal that arrives after the provider has been called has already spent the money.

import (
	"context"
	"errors"
	"testing"
	"time"
)

type redTeamCase struct {
	name string
	// build returns the service, the counting provider and the caller for the case.
	build func() (*Service, *pricedProvider, Caller)
	// wantText is the answer the user must get.
	wantText string
}

func redTeamService(sp *memSpend, lim *fakeLimits, demo *DemoLimits, set StaticSettings) (*Service, *pricedProvider) {
	p := &pricedProvider{reply: Reply{Text: "should never be reached", TokensIn: 10, TokensOut: 10}}
	clock := at(11)
	now := func() time.Time { return clock }
	svc := NewService(NewRegistry(seededReaders().deps(), nil), p, &fakeLedger{}, lim, set, nil).
		WithClock(now).
		WithMetering(sp, testPrices()).
		WithDealerCap(1 << 60).
		WithRateLimits(NewRateLimits(demo, now))
	return svc, p
}

func TestRedTeam10_EveryOverLimitCaseIsRefusedBeforeTheProvider(t *testing.T) {
	now := func() time.Time { return at(11) }
	cases := []redTeamCase{
		{
			name: "over the user budget",
			build: func() (*Service, *pricedProvider, Caller) {
				sp := &memSpend{}
				sp.rows = append(sp.rows, SpendRow{Kind: SpendSettlement, ExchangeID: "old", CallNo: 1, Audience: AudienceStaff,
					StaffUserID: "u-sm", RoleCode: "SALES_MANAGER", Alias: "a", Model: "m", TokensIn: 900, TokensOut: 100, At: at(3)})
				lim := newFakeLimits()
				lim.users["u-sm"] = 1000
				svc, p := redTeamService(sp, lim, nil, StaticSettings{})
				return svc, p, manager()
			},
			wantText: LimitReachedMessage,
		},
		{
			name: "over the role budget",
			build: func() (*Service, *pricedProvider, Caller) {
				sp := &memSpend{}
				sp.rows = append(sp.rows, SpendRow{Kind: SpendSettlement, ExchangeID: "old", CallNo: 1, Audience: AudienceStaff,
					StaffUserID: "u-sm", RoleCode: "SALES_MANAGER", Alias: "a", Model: "m", TokensIn: 300, At: at(3)})
				lim := newFakeLimits()
				lim.roles["SALES_MANAGER"] = 300
				svc, p := redTeamService(sp, lim, nil, StaticSettings{})
				return svc, p, manager()
			},
			wantText: LimitReachedMessage,
		},
		{
			name: "the dealer wide cap reached by other audiences",
			build: func() (*Service, *pricedProvider, Caller) {
				sp := &memSpend{}
				seedDealerSpend(sp, 8_000_000)
				svc, p := redTeamService(sp, newFakeLimits(), nil, StaticSettings{})
				svc.WithDealerCap(8_000_000)
				return svc, p, manager()
			},
			wantText: LimitReachedMessage,
		},
		{
			name: "a demo visitor over its limit",
			build: func() (*Service, *pricedProvider, Caller) {
				demo := &DemoLimits{Visitor: newTestLimit(1, now), Address: newTestLimit(100, now), Wide: newTestLimit(100, now)}
				svc, p := redTeamService(&memSpend{}, newFakeLimits(), demo, StaticSettings{KeyRateRequests: "1000"})
				c := demoCaller("visitor-1", "10.0.0.1")
				demo.Visitor.Record("v:visitor-1") // the visitor's one request is already spent
				return svc, p, c
			},
			wantText: RateLimitedMessage,
		},
		{
			name: "an address over its limit, with a fresh visitor each time",
			build: func() (*Service, *pricedProvider, Caller) {
				demo := &DemoLimits{Visitor: newTestLimit(100, now), Address: newTestLimit(1, now), Wide: newTestLimit(100, now)}
				svc, p := redTeamService(&memSpend{}, newFakeLimits(), demo, StaticSettings{KeyRateRequests: "1000"})
				demo.Address.Record("a:10.0.0.1")
				return svc, p, demoCaller("brand-new-visitor", "10.0.0.1")
			},
			wantText: RateLimitedMessage,
		},
		{
			name: "the demo wide cap reached",
			build: func() (*Service, *pricedProvider, Caller) {
				demo := &DemoLimits{Visitor: newTestLimit(100, now), Address: newTestLimit(100, now), Wide: newTestLimit(1, now)}
				svc, p := redTeamService(&memSpend{}, newFakeLimits(), demo, StaticSettings{KeyRateRequests: "1000"})
				demo.Wide.Record("wide")
				return svc, p, demoCaller("someone", "10.0.0.7")
			},
			wantText: RateLimitedMessage,
		},
		{
			name: "a per asker rate limit already spent",
			build: func() (*Service, *pricedProvider, Caller) {
				svc, p := redTeamService(&memSpend{}, newFakeLimits(), nil, StaticSettings{KeyRateRequests: "1", KeyRateWindowSeconds: "60"})
				c := manager()
				c.SessionID = "jti-1"
				_ = svc.rates.Admit(c, RateSettings{Requests: 1, Window: time.Minute})
				return svc, p, c
			},
			wantText: RateLimitedMessage,
		},
		{
			name: "the reservation write failing",
			build: func() (*Service, *pricedProvider, Caller) {
				svc, p := redTeamService(&memSpend{failReserve: errors.New("write refused")}, newFakeLimits(), nil, StaticSettings{})
				return svc, p, manager()
			},
			wantText: UnavailableMessage,
		},
		{
			name: "the spend table unreadable",
			build: func() (*Service, *pricedProvider, Caller) {
				svc, p := redTeamService(&memSpend{}, newFakeLimits(), nil, StaticSettings{})
				svc.WithMetering(&brokenSpend{}, testPrices())
				return svc, p, manager()
			},
			wantText: UnavailableMessage,
		},
		{
			name: "a paid model with no price row",
			build: func() (*Service, *pricedProvider, Caller) {
				svc, p := redTeamService(&memSpend{}, newFakeLimits(), nil, StaticSettings{})
				svc.WithMetering(&memSpend{}, memPrices{})
				return svc, p, manager()
			},
			wantText: UnavailableMessage,
		},
		{
			name: "a paid model with no dealer cap configured",
			build: func() (*Service, *pricedProvider, Caller) {
				svc, p := redTeamService(&memSpend{}, newFakeLimits(), nil, StaticSettings{})
				svc.WithDealerCap(0)
				return svc, p, manager()
			},
			wantText: UnavailableMessage,
		},
		{
			name: "a paid model with no spend table wired at all",
			build: func() (*Service, *pricedProvider, Caller) {
				svc, p := redTeamService(&memSpend{}, newFakeLimits(), nil, StaticSettings{})
				svc.WithMetering(nil, nil)
				return svc, p, manager()
			},
			wantText: UnavailableMessage,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, p, who := c.build()
			ans, err := svc.Ask(context.Background(), who, "where is SO-4?", nil)
			if err != nil {
				t.Fatalf("a refusal is an answer, not an error: %v", err)
			}
			if ans.Text != c.wantText {
				t.Fatalf("answer = %q, want %q", ans.Text, c.wantText)
			}
			if p.calls != 0 {
				t.Fatalf("the provider received %d requests, want 0", p.calls)
			}
		})
	}
}

// brokenSpend is a spend table that cannot be read.
type brokenSpend struct{ memSpend }

func (*brokenSpend) UserMonth(context.Context, string, time.Time, time.Time) (Spend, error) {
	return Spend{}, errors.New("table unreadable")
}

// TestRedTeam10_TheSecondCallOfAnExchangeIsMeteredToo: the budget is spent by the first call, so the
// summarising call is refused and the answer degrades to the headlines instead of spending past it.
func TestRedTeam10_TheSecondCallOfAnExchangeIsMeteredToo(t *testing.T) {
	sp := &memSpend{}
	lim := newFakeLimits()
	lim.users["u-sm"] = 100
	p := &twoCallProvider{}
	svc := NewService(NewRegistry(seededReaders().deps(), nil), p, &fakeLedger{}, lim, StaticSettings{}, nil).
		WithClock(func() time.Time { return at(11) }).
		WithMetering(sp, memPrices{"two/two-1": {ID: "p", Alias: "two", Model: "two-1", Seq: 1}}).
		WithDealerCap(1 << 60)

	ans, err := svc.Ask(context.Background(), manager(), "where is SO-4?", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.calls != 1 {
		t.Fatalf("provider calls = %d, want 1: the first call spent the allowance, the second must be refused", p.calls)
	}
	if len(ans.Results) != 1 || ans.Text == "" {
		t.Fatalf("the command results must still be answered from the headlines: %+v", ans)
	}
}

type twoCallProvider struct{ calls int }

func (p *twoCallProvider) Name() string  { return "two" }
func (p *twoCallProvider) Model() string { return "two-1" }
func (p *twoCallProvider) Complete(context.Context, CompletionRequest) (Reply, error) {
	p.calls++
	return Reply{ToolCalls: []ToolCall{{Command: "order.lookup", Args: Args{"order_no": "SO-4"}}}, TokensIn: 90, TokensOut: 20}, nil
}
