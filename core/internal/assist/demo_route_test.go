package assist

// demo_route_test.go, TanyAI slice 3 sub item 4: the demo's route is tanyai-premium (Claude
// Sonnet 5.5, the native adapter) by the composition, never by a setting or a request, and the
// demo can reach no other alias.

import (
	"context"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
)

type demoRouteRig struct {
	svc           *Service
	router        *Router
	main, alt, ca *strictOpenAI
	premium       *strictAnthropic
}

func newDemoRouteRig(t *testing.T, set StaticSettings) *demoRouteRig {
	t.Helper()
	r := &demoRouteRig{}
	r.main = newStrictOpenAI(t, "gw-key", "dealer-1", oaiTextReply("main must never answer"))
	r.alt = newStrictOpenAI(t, "gw-key", "dealer-1", oaiTextReply("alt must never answer"))
	r.ca = newStrictOpenAI(t, "gw-key", "dealer-1", oaiTextReply("ca must never answer"))
	r.premium = newStrictAnthropic(t, antTextReply("premium answered"))
	rows := []AliasRow{
		{Alias: AliasMain, AdapterKind: AdapterOpenAICompatible, BaseURL: r.main.URL(), Model: "open-main", Enabled: true},
		{Alias: AliasAlt, AdapterKind: AdapterOpenAICompatible, BaseURL: r.alt.URL(), Model: "open-alt", Enabled: true},
		{Alias: AliasCA, AdapterKind: AdapterOpenAICompatible, BaseURL: r.ca.URL(), Model: "open-ca", Enabled: true},
		{Alias: AliasPremium, AdapterKind: AdapterAnthropic, Model: "claude-sonnet-5-5", Enabled: true},
	}
	r.router = NewRouter(StaticAliases(rows), ModelConfig{AnthropicKey: redact.NewSecret("k"), AnthropicBaseURL: r.premium.URL(), GatewayKey: redact.NewSecret("gw-key"), DealerTag: "dealer-1"}, NewStubProvider(), nil).
		Only(AliasPremium)
	r.svc = chatService(NewStubProvider(), &fakeLedger{}, newFakeLimits(), set, seededReaders()).
		WithRouter(r.router).
		WithMetering(&memSpend{}, premiumPrices).
		WithDealerCap(1 << 60).
		WithPinnedRoute(AliasPremium)
	return r
}

func (r *demoRouteRig) otherCalls() int {
	return len(r.main.Requests()) + len(r.alt.Requests()) + len(r.ca.Requests())
}

// Whatever the settings table says, the demo asks tanyai-premium and nothing else.
func TestDemoRoute_SettingsCannotMoveTheDemoOffPremium(t *testing.T) {
	cases := map[string]StaticSettings{
		"default (stub)":        {},
		"routed to main, alt":   {KeyProvider: ProviderRouted, KeyRoute: "tanyai-main,tanyai-alt,tanyai-premium"},
		"anthropic, model ca":   {KeyProvider: ProviderAnthropic, KeyModel: AliasCA},
		"anthropic, seed model": {KeyProvider: ProviderAnthropic, KeyModel: "hos-stub-1"},
		"stub provider":         {KeyProvider: ProviderStub, KeyModel: AliasMain},
	}
	for name, set := range cases {
		rig := newDemoRouteRig(t, set)
		ans, err := rig.svc.Ask(context.Background(), manager(), "hello", nil)
		if err != nil || ans.Text != "premium answered" {
			t.Fatalf("%s: ask = %q (%v)", name, ans.Text, err)
		}
		if n := rig.otherCalls(); n != 0 {
			t.Fatalf("%s: %d requests reached an alias that is not tanyai-premium", name, n)
		}
		reqs := rig.premium.Requests()
		if len(reqs) != 1 || !strings.Contains(string(reqs[0]), `"model":"claude-sonnet-5-5"`) {
			t.Fatalf("%s: premium requests = %d, want one for claude-sonnet-5-5", name, len(reqs))
		}
		if ans.Usage == nil || ans.Usage.Provider != "anthropic" || ans.Usage.Model != "claude-sonnet-5-5" {
			t.Fatalf("%s: usage = %+v", name, ans.Usage)
		}
	}
}

// The router itself refuses every other alias, even one whose row is configured and enabled.
func TestDemoRoute_RouterReachesNoOtherAlias(t *testing.T) {
	rig := newDemoRouteRig(t, StaticSettings{})
	for _, a := range []string{AliasMain, AliasAlt, AliasCA, "stub-fallback", "hos-stub-1", "claude-opus-5-5"} {
		p, ok := rig.router.Resolve(context.Background(), a)
		if ok || p.Name() != "stub" {
			t.Fatalf("alias %q resolved to %s (ok %v), want the stub", a, p.Name(), ok)
		}
	}
	if p, ok := rig.router.Resolve(context.Background(), AliasPremium); !ok || p.Model() != "claude-sonnet-5-5" {
		t.Fatalf("premium resolved to %v (ok %v)", p, ok)
	}
}

// The pin is the composition's: the settings the service reports carry it, and the dealer's chat
// switch is still the dealer's.
func TestDemoRoute_PinKeepsTheOtherSettings(t *testing.T) {
	rig := newDemoRouteRig(t, StaticSettings{KeyProvider: ProviderRouted, KeyChatEnabled: "false", KeySpendVisibleToNonAdmin: "true"})
	got := rig.svc.Settings(context.Background())
	if got.Provider != ProviderAnthropic || got.Model != AliasPremium || got.Route != "" {
		t.Fatalf("settings = %+v, want the premium pin", got)
	}
	if got.ChatEnabled || !got.SpendVisibleToNonAdmin {
		t.Fatalf("settings = %+v, the pin must leave the dealer's switches alone", got)
	}
	if rig.svc.PinnedRoute() != AliasPremium {
		t.Fatalf("PinnedRoute = %q", rig.svc.PinnedRoute())
	}
}

// DemoSettings is what the boot hands the providers that are built once (the quote rails).
func TestDemoRoute_DemoSettingsPinTheBootSettings(t *testing.T) {
	got := DemoSettings(SettingsFrom(map[string]string{KeyProvider: ProviderRouted, KeyRoute: "tanyai-main"}))
	if got.Provider != ProviderAnthropic || got.Model != AliasPremium || got.Route != "" {
		t.Fatalf("DemoSettings = %+v", got)
	}
	if r := got.RouteAliases(); len(r) != 1 || r[0] != AliasPremium {
		t.Fatalf("route = %v", r)
	}
}
