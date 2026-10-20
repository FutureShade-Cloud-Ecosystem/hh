package assist

// ratelimit_test.go, slice 2 sub item 4: the per asker limit, the demo's per visitor, per address
// and box wide limits, and the address rule they share with the demo mint.

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/ratelimit"
)

func rateService(p Provider, set StaticSettings, demo *DemoLimits) (*Service, *time.Time) {
	clock := at(11)
	now := func() time.Time { return clock }
	svc := meteredService(p, &memSpend{}).WithRateLimits(NewRateLimits(demo, now))
	svc.settings = set
	return svc, &clock
}

func askOnce(svc *Service, c Caller) Answer {
	ans, _ := svc.Ask(context.Background(), c, "where is SO-4?", nil)
	return ans
}

func TestAskerOverItsRateLimitIsRefusedBeforeTheProvider(t *testing.T) {
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	svc, _ := rateService(p, StaticSettings{KeyRateRequests: "2", KeyRateWindowSeconds: "60"}, nil)
	who := manager()
	who.SessionKey = "jti-a"

	for i := 0; i < 2; i++ {
		if ans := askOnce(svc, who); ans.Text == RateLimitedMessage {
			t.Fatalf("request %d was refused inside the allowance", i+1)
		}
	}
	callsBefore := p.calls
	ans := askOnce(svc, who)
	if ans.Text != RateLimitedMessage {
		t.Fatalf("third request = %q, want the rate limit answer", ans.Text)
	}
	if p.calls != callsBefore {
		t.Fatalf("the provider received %d more requests after the limit, want 0", p.calls-callsBefore)
	}
}

func TestEachSessionOfAUserHoldsItsOwnBucket(t *testing.T) {
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	svc, _ := rateService(p, StaticSettings{KeyRateRequests: "1"}, nil)
	a, b := manager(), manager()
	a.SessionKey, b.SessionKey = "jti-a", "jti-b"
	askOnce(svc, a)
	if askOnce(svc, a).Text != RateLimitedMessage {
		t.Fatal("the second request in one session passed a limit of one")
	}
	if askOnce(svc, b).Text == RateLimitedMessage {
		t.Fatal("another session of the same user shared the first session's bucket")
	}
}

func TestTheAllowanceReopensAfterTheWindowAndFollowsTheSettingsLive(t *testing.T) {
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	set := StaticSettings{KeyRateRequests: "1", KeyRateWindowSeconds: "60"}
	svc, clock := rateService(p, set, nil)
	who := manager()
	askOnce(svc, who)
	if askOnce(svc, who).Text != RateLimitedMessage {
		t.Fatal("not limited at one per window")
	}
	set[KeyRateRequests] = "3" // live: no restart
	if askOnce(svc, who).Text == RateLimitedMessage {
		t.Fatal("a raised allowance did not apply to the next request")
	}
	*clock = clock.Add(2 * time.Minute)
	set[KeyRateRequests] = "1"
	if askOnce(svc, who).Text == RateLimitedMessage {
		t.Fatal("the window passed and the allowance did not reopen")
	}
}

func TestRateSettingsOutOfRangeReadAsTheDefaultNeverUnlimited(t *testing.T) {
	for _, raw := range []map[string]string{
		{KeyRateRequests: "0"}, {KeyRateRequests: "-4"}, {KeyRateRequests: "100000"}, {KeyRateRequests: "many"},
		{KeyRateWindowSeconds: "0"}, {KeyRateWindowSeconds: "999999"},
	} {
		got := RateSettingsFrom(raw)
		if got.Requests != DefaultRateRequests || got.Window != DefaultRateWindowSeconds*time.Second {
			t.Errorf("%v read as %+v, want the defaults", raw, got)
		}
	}
	svc := NewService(NewRegistry(seededReaders().deps(), nil), NewStubProvider(), &fakeLedger{}, newFakeLimits(), StaticSettings{}, nil)
	if err := svc.SetSetting(context.Background(), KeyRateRequests, "0", "u"); err == nil {
		t.Error("SetSetting accepted a zero allowance")
	}
	if err := svc.SetSetting(context.Background(), KeyRateRequests, "30", "u"); err != nil {
		t.Errorf("SetSetting refused a good allowance: %v", err)
	}
}

func demoCaller(session, addr string) Caller {
	c := manager() // demo personas are shared users: the same UserID for every visitor
	c.SessionKey, c.Address = session, addr
	return c
}

func TestDemoVisitorOverItsLimitIsRefusedAndAnotherVisitorIsNot(t *testing.T) {
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	now := func() time.Time { return at(11) }
	demo := &DemoLimits{
		Visitor: newTestLimit(2, now), Address: newTestLimit(100, now), Wide: newTestLimit(100, now),
	}
	svc, _ := rateService(p, StaticSettings{KeyRateRequests: "1000"}, demo)

	askOnce(svc, demoCaller("v1", "10.0.0.1"))
	askOnce(svc, demoCaller("v1", "10.0.0.1"))
	before := p.calls
	if askOnce(svc, demoCaller("v1", "10.0.0.1")).Text != RateLimitedMessage || p.calls != before {
		t.Fatal("a visitor over its limit reached the provider")
	}
	if askOnce(svc, demoCaller("v2", "10.0.0.1")).Text == RateLimitedMessage {
		t.Fatal("a second visitor shared the first visitor's bucket")
	}
}

func TestDemoAddressOverItsLimitIsRefusedWhateverTheVisitor(t *testing.T) {
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	now := func() time.Time { return at(11) }
	demo := &DemoLimits{Visitor: newTestLimit(100, now), Address: newTestLimit(2, now), Wide: newTestLimit(100, now)}
	svc, _ := rateService(p, StaticSettings{KeyRateRequests: "1000"}, demo)

	askOnce(svc, demoCaller("v1", "10.0.0.9"))
	askOnce(svc, demoCaller("v2", "10.0.0.9"))
	before := p.calls
	// A fresh cookie per request is the farm the address bucket exists to end.
	if askOnce(svc, demoCaller("v3", "10.0.0.9")).Text != RateLimitedMessage || p.calls != before {
		t.Fatal("an address over its limit reached the provider")
	}
}

func TestDemoWideCeilingStopsTheBox(t *testing.T) {
	p := &pricedProvider{reply: Reply{Text: "ok"}}
	now := func() time.Time { return at(11) }
	demo := &DemoLimits{Visitor: newTestLimit(100, now), Address: newTestLimit(100, now), Wide: newTestLimit(2, now)}
	svc, _ := rateService(p, StaticSettings{KeyRateRequests: "1000"}, demo)
	askOnce(svc, demoCaller("v1", "10.0.0.1"))
	askOnce(svc, demoCaller("v2", "10.0.0.2"))
	before := p.calls
	if askOnce(svc, demoCaller("v3", "10.0.0.3")).Text != RateLimitedMessage || p.calls != before {
		t.Fatal("the box wide ceiling did not hold")
	}
}

func TestARefusedRequestChargesNoBucket(t *testing.T) {
	now := func() time.Time { return at(11) }
	demo := &DemoLimits{Visitor: newTestLimit(100, now), Address: newTestLimit(1, now), Wide: newTestLimit(100, now)}
	l := NewRateLimits(demo, now)
	rs := RateSettings{Requests: 100, Window: time.Minute}
	if err := l.Admit(demoCaller("v1", "a"), rs); err != nil {
		t.Fatal(err)
	}
	if err := l.Admit(demoCaller("v2", "a"), rs); err == nil {
		t.Fatal("address allowance of one let two through")
	}
	if got := len(demo.Visitor.Stamps("v:v2")); got != 0 {
		t.Fatalf("a request refused at the address bucket still charged the visitor bucket %d times", got)
	}
}

func TestClientAddressMirrorsTheDemoMint(t *testing.T) {
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	cases := []struct{ name, remote, xff, want string }{
		{"direct client", "203.0.113.5:4000", "", "203.0.113.5"},
		{"direct client forging the chain", "203.0.113.5:4000", "198.51.100.1", "203.0.113.5"},
		{"behind the box proxy", "127.0.0.1:4000", "198.51.100.1, 10.0.0.2", "198.51.100.1"},
		{"proxy with no chain", "127.0.0.1:4000", "", "127.0.0.1"},
		{"proxy over ipv6 loopback", "[::1]:4000", "198.51.100.7", "198.51.100.7"},
		{"unsplittable peer", "weird", "198.51.100.1", "weird"},
	}
	for _, c := range cases {
		if got := clientAddress(mk(c.remote, c.xff)); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCallerFromCarriesTheTrustedAddressAndNoBearerParse(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "203.0.113.5:1"
	// A payload with a jti in the header must NOT become the session key here: the key is the
	// verified scope's (Handler.callerFor sets it), never a re read of the bearer.
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"u","jti":"abc123"}`))
	r.Header.Set("Authorization", "Bearer h."+payload+".s")
	c := CallerFrom(r)
	if c.SessionKey != "" || c.Address != "203.0.113.5" {
		t.Fatalf("caller = key %q address %q", c.SessionKey, c.Address)
	}
}

func newTestLimit(n int, now func() time.Time) *ratelimit.AddressLimit {
	return ratelimit.NewAddressLimit(n, time.Hour, now)
}

// Slice 3: the demo boot asks the service whether the demo's three buckets are in force.
func TestDemoLimitsInForce(t *testing.T) {
	now := time.Now
	if NewService(nil, nil, nil, nil, nil, nil).DemoLimitsInForce() {
		t.Fatal("a service with no limiter reports the demo limits in force")
	}
	if NewService(nil, nil, nil, nil, nil, nil).WithRateLimits(NewRateLimits(nil, now)).DemoLimitsInForce() {
		t.Fatal("a limiter with no demo buckets reports them in force")
	}
	partial := &DemoLimits{Visitor: newTestLimit(1, now), Address: newTestLimit(1, now)}
	if NewService(nil, nil, nil, nil, nil, nil).WithRateLimits(NewRateLimits(partial, now)).DemoLimitsInForce() {
		t.Fatal("a limiter missing the box wide bucket reports the demo limits in force")
	}
	if !NewService(nil, nil, nil, nil, nil, nil).WithRateLimits(NewRateLimits(NewDemoLimits(now), now)).DemoLimitsInForce() {
		t.Fatal("the full demo limits are not reported in force")
	}
}
