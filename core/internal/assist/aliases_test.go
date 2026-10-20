package assist

// aliases_test.go, TanyAI slice 1 sub item 5: HH code calls aliases only, and a model string that is
// not an alias row is never sent to any vendor.

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/assist/redact"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// modelCapture is a fake vendor endpoint that records every model string it is sent, whatever the
// API flavour.
type modelCapture struct {
	srv    *httptest.Server
	mu     sync.Mutex
	models []string
}

func newModelCapture(t *testing.T) *modelCapture {
	t.Helper()
	m := &modelCapture{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Model string `json:"model"`
		}
		_ = json.Unmarshal(body, &req)
		m.mu.Lock()
		m.models = append(m.models, req.Model)
		m.mu.Unlock()
		w.Header().Set("content-type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/chat/completions") {
			_, _ = w.Write([]byte(oaiTextReply("ok")))
			return
		}
		_, _ = w.Write([]byte(antTextReply("ok")))
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *modelCapture) Models() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.models...)
}

// THE 0153 TRAP. The migration seeds model='hos-stub-1'. On main, switching only the provider to
// 'anthropic' sent that string to the vendor as the model name.
func TestAliases_StubSeedModelNeverReachesAVendor(t *testing.T) {
	vendor := newModelCapture(t)
	settings := SettingsFrom(map[string]string{KeyProvider: "anthropic", KeyModel: "hos-stub-1"})
	p := NewProviderFromConfig(settings, "test-key", vendor.srv.URL, nil)
	if _, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Text: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	got := vendor.Models()
	if len(got) != 1 || got[0] != "claude-sonnet-5-5" {
		t.Fatalf("the vendor was sent models %v, want exactly [claude-sonnet-5-5] (the tanyai-premium alias row's model)", got)
	}
}

func TestAliases_ResolveRules(t *testing.T) {
	vendor := newModelCapture(t)
	withKey := ModelConfig{AnthropicKey: redact.NewSecret("k"), AnthropicBaseURL: vendor.srv.URL, GatewayURL: vendor.srv.URL + "/v1", GatewayKey: redact.NewSecret("gk"), DealerTag: "tag"}
	noKey := ModelConfig{AnthropicBaseURL: vendor.srv.URL}

	cases := []struct {
		name     string
		cfg      ModelConfig
		rows     []AliasRow
		alias    string
		wantName string
		wantOK   bool
	}{
		{"premium with a key is the native adapter", withKey, DefaultAliases(), AliasPremium, "anthropic", true},
		{"premium without a key is the stub", noKey, DefaultAliases(), AliasPremium, "stub", false},
		{"main is unconfigured", withKey, DefaultAliases(), AliasMain, "stub", false},
		{"alt is unconfigured", withKey, DefaultAliases(), AliasAlt, "stub", false},
		{"ca is unconfigured", withKey, DefaultAliases(), AliasCA, "stub", false},
		{"a vendor model name is not an alias", withKey, DefaultAliases(), "claude-sonnet-4-5", "stub", false},
		{"the 0153 seed is not an alias", withKey, DefaultAliases(), "hos-stub-1", "stub", false},
		{"empty alias is the stub", withKey, DefaultAliases(), "", "stub", false},
		{"a disabled row is the stub", withKey, []AliasRow{{Alias: AliasPremium, AdapterKind: AdapterAnthropic, Model: "claude-sonnet-5-5", Enabled: false}}, AliasPremium, "stub", false},
		{"a vendor row whose model is the stub seed is the stub", withKey, []AliasRow{{Alias: AliasPremium, AdapterKind: AdapterAnthropic, Model: "hos-stub-1", Enabled: true}}, AliasPremium, "stub", false},
		{"a vendor row with no model is the stub", withKey, []AliasRow{{Alias: AliasPremium, AdapterKind: AdapterAnthropic, Enabled: true}}, AliasPremium, "stub", false},
		{"an unknown adapter kind is the stub", withKey, []AliasRow{{Alias: AliasMain, AdapterKind: "carrier-pigeon", Model: "m", Enabled: true}}, AliasMain, "stub", false},
		{"an openai compatible row uses the gateway", withKey, []AliasRow{{Alias: AliasMain, AdapterKind: AdapterOpenAICompatible, Model: "open-1", Enabled: true}}, AliasMain, "openai_compatible", true},
		{"an openai compatible row with no gateway is the stub", noKey, []AliasRow{{Alias: AliasMain, AdapterKind: AdapterOpenAICompatible, Model: "open-1", Enabled: true}}, AliasMain, "stub", false},
	}
	for _, c := range cases {
		r := NewRouter(StaticAliases(c.rows), c.cfg, NewStubProvider(), nil)
		p, ok := r.Resolve(context.Background(), c.alias)
		if p.Name() != c.wantName || ok != c.wantOK {
			t.Errorf("%s: got %s ok=%v, want %s ok=%v", c.name, p.Name(), ok, c.wantName, c.wantOK)
		}
	}
	if got := vendor.Models(); len(got) != 0 {
		t.Fatalf("resolving made vendor calls: %v", got)
	}
}

// A model string that is not in the alias table is never sent: through the whole service, with a
// settings row that names a vendor model.
func TestAliases_ServiceNeverSendsAnUnknownModel(t *testing.T) {
	vendor := newModelCapture(t)
	router := NewRouter(StaticAliases(DefaultAliases()), ModelConfig{AnthropicKey: redact.NewSecret("k"), AnthropicBaseURL: vendor.srv.URL}, NewStubProvider(), nil)
	for _, model := range []string{"hos-stub-1", "claude-sonnet-4-5", "gpt-anything", "tanyai-nonesuch"} {
		set := StaticSettings{KeyProvider: ProviderAnthropic, KeyModel: model}
		svc := chatService(NewStubProvider(), &fakeLedger{}, newFakeLimits(), set, seededReaders()).WithRouter(router)
		if _, err := svc.Ask(context.Background(), manager(), "hello there", nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range vendor.Models() {
		if m != "claude-sonnet-5-5" {
			t.Fatalf("the vendor was sent %q", m)
		}
	}
	if len(vendor.Models()) != 4 {
		t.Fatalf("want one vendor call per question on the premium alias, got %v", vendor.Models())
	}
}

func TestAliases_GatewayAliasCarriesConfiguredKeyAndTag(t *testing.T) {
	srv := newStrictOpenAI(t, "gateway-key", "dealer-7", oaiTextReply("from the gateway"))
	rows := []AliasRow{{Alias: AliasMain, AdapterKind: AdapterOpenAICompatible, Model: "open-1", Enabled: true}}
	router := NewRouter(StaticAliases(rows), ModelConfig{GatewayURL: srv.URL(), GatewayKey: redact.NewSecret("gateway-key"), DealerTag: "dealer-7"}, NewStubProvider(), nil)
	set := StaticSettings{KeyProvider: ProviderRouted, KeyRoute: AliasMain}
	svc := chatService(NewStubProvider(), &fakeLedger{}, newFakeLimits(), set, seededReaders()).WithRouter(router)
	ans, err := svc.Ask(context.Background(), manager(), "hello there", nil)
	if err != nil {
		t.Fatal(err)
	}
	if ans.Text != "from the gateway" || len(srv.Violations()) != 0 {
		t.Fatalf("text %q violations %v", ans.Text, srv.Violations())
	}
	if ans.Usage == nil || ans.Usage.Provider != "openai_compatible" || ans.Usage.Model != "open-1" {
		t.Fatalf("usage = %+v, want the provider and model that answered", ans.Usage)
	}
}

func TestAliases_AdminSettingsAreAliasesOnly(t *testing.T) {
	svc := chatService(NewStubProvider(), &fakeLedger{}, newFakeLimits(), StaticSettings{}, seededReaders())
	ctx := context.Background()
	if err := svc.SetSetting(ctx, KeyModel, "claude-sonnet-4-5", "u"); err == nil {
		t.Fatal("a vendor model name was accepted as the model setting")
	}
	if err := svc.SetSetting(ctx, KeyModel, AliasPremium, "u"); err != nil {
		t.Fatalf("an alias was refused: %v", err)
	}
	if err := svc.SetSetting(ctx, KeyProvider, ProviderRouted, "u"); err != nil {
		t.Fatalf("routed refused: %v", err)
	}
	if err := svc.SetSetting(ctx, KeyRoute, "tanyai-main, tanyai-premium", "u"); err != nil {
		t.Fatalf("a route of aliases was refused: %v", err)
	}
	if err := svc.SetSetting(ctx, KeyRoute, "tanyai-main,gpt-5", "u"); err == nil {
		t.Fatal("a route naming a vendor model was accepted")
	}
}

func TestAliases_ConfigNeverPrintsACredential(t *testing.T) {
	cfg := ModelConfig{AnthropicKey: redact.NewSecret("sk-secret-a"), GatewayKey: redact.NewSecret("sk-secret-b"), GatewayURL: "http://gw", DealerTag: "t"}
	for _, s := range []string{cfg.String(), strings.TrimSpace(fmtAll(cfg))} {
		if strings.Contains(s, "sk-secret") {
			t.Fatalf("config formats with a credential: %q", s)
		}
	}
}

func fmtAll(cfg ModelConfig) string {
	return strings.Join([]string{sprint("%v", cfg), sprint("%+v", cfg), sprint("%#v", cfg)}, " ")
}

func TestAliases_EnvNames(t *testing.T) {
	if EnvGatewayURL != "HOS_ASSIST_GATEWAY_URL" || EnvGatewayKey != "HOS_ASSIST_GATEWAY_KEY" || EnvDealerTag != "HOS_ASSIST_DEALER_TAG" {
		t.Fatal("the deployment environment names moved; the demo inventory lists them")
	}
}

func sprint(format string, a ...any) string { return fmt.Sprintf(format, a...) }
