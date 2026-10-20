package assist

// settings_live_test.go, TanyAI slice 1 sub item 7: the route and the alias choice are read per
// request. Nothing is cached keyed by anything but the deployment, so a changed setting or alias
// row applies to the very next request with no restart.

import (
	"context"
	"sync"
	"testing"
)

type mutableAliases struct {
	mu   sync.Mutex
	rows []AliasRow
}

func (m *mutableAliases) Aliases(context.Context) ([]AliasRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AliasRow(nil), m.rows...), nil
}

func (m *mutableAliases) set(rows []AliasRow) {
	m.mu.Lock()
	m.rows = rows
	m.mu.Unlock()
}

func TestSettingsLive_ProviderRouteAndAliasApplyToTheNextRequest(t *testing.T) {
	vendor := newModelCapture(t)
	table := &mutableAliases{rows: DefaultAliases()}
	router := NewRouter(table, ModelConfig{AnthropicKey: "k", AnthropicBaseURL: vendor.srv.URL, GatewayURL: vendor.srv.URL + "/v1", GatewayKey: "gk", DealerTag: "t"}, NewStubProvider(), nil)
	set := StaticSettings{}
	svc := chatService(NewStubProvider(), &fakeLedger{}, newFakeLimits(), set, seededReaders()).WithRouter(router)
	ctx := context.Background()
	ask := func() Answer {
		t.Helper()
		ans, err := svc.Ask(ctx, manager(), "hello there", nil)
		if err != nil {
			t.Fatal(err)
		}
		return ans
	}
	admin := "u-gm"

	// 1. The default is the stub: no vendor call.
	if ans := ask(); ans.Usage.Provider != "stub" || len(vendor.Models()) != 0 {
		t.Fatalf("default: provider %q, vendor calls %v", ans.Usage.Provider, vendor.Models())
	}
	// 2. The admin switches the provider; the very next request reaches the vendor.
	if err := svc.SetSetting(ctx, KeyProvider, ProviderAnthropic, admin); err != nil {
		t.Fatal(err)
	}
	if ans := ask(); ans.Usage.Provider != "anthropic" || ans.Usage.Model != "claude-sonnet-5-5" || len(vendor.Models()) != 1 {
		t.Fatalf("after provider change: %+v, vendor calls %v", ans.Usage, vendor.Models())
	}
	// 3. The alias row's model changes; the next request sends the new model.
	rows := DefaultAliases()
	rows[2].Model = "claude-opus-5-5"
	table.set(rows)
	ask()
	if got := vendor.Models(); got[len(got)-1] != "claude-opus-5-5" {
		t.Fatalf("an alias row change did not apply: %v", got)
	}
	// 4. A route over a gateway alias; the next request goes to the gateway adapter.
	rows[0] = AliasRow{Alias: AliasMain, AdapterKind: AdapterOpenAICompatible, Model: "open-1", Enabled: true}
	table.set(rows)
	if err := svc.SetSetting(ctx, KeyProvider, ProviderRouted, admin); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetSetting(ctx, KeyRoute, AliasMain, admin); err != nil {
		t.Fatal(err)
	}
	if ans := ask(); ans.Usage.Provider != "openai_compatible" || ans.Usage.Model != "open-1" {
		t.Fatalf("after route change: %+v", ans.Usage)
	}
	// 5. Back to the stub.
	if err := svc.SetSetting(ctx, KeyProvider, ProviderStub, admin); err != nil {
		t.Fatal(err)
	}
	n := len(vendor.Models())
	if ans := ask(); ans.Usage.Provider != "stub" || len(vendor.Models()) != n {
		t.Fatalf("after switching back: %+v, vendor calls %d -> %d", ans.Usage, n, len(vendor.Models()))
	}
}
