package assist

// aliases.go, TanyAI slice 1 sub item 5: HH code calls ALIASES, never models.
//
// An alias names a role (the dealer default, the alternate, the premium route, the Canadian lane),
// not a model. The alias table is the only place a model name exists; a string that is not an alias
// is never sent to any vendor. That closes the 0153 trap: the migration seeded the setting
// model='hos-stub-1', and on main switching only the provider to 'anthropic' sent that string to
// the vendor as a model name. Settings now choose aliases, and an alias that is unknown, disabled
// or unconfigured resolves to the deterministic stub.
//
// Configuration comes from deployment environment (ModelConfig), set by the operator at deploy and
// never by a dealer or a request. The names are HOS_ASSIST_*, which a demo box refuses until slice
// 3 opens them (demoenv_inventory.go).

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
)

// The four aliases.
const (
	AliasMain    = "tanyai-main"
	AliasAlt     = "tanyai-alt"
	AliasPremium = "tanyai-premium"
	AliasCA      = "tanyai-ca"
)

// The adapter kinds an alias row may name.
const (
	AdapterAnthropic        = "anthropic"
	AdapterOpenAICompatible = "openai_compatible"
	AdapterStub             = "stub"
)

// Deployment environment names. The values are read once at the composition root and handed in as
// ModelConfig; no other layer learns more than whether a key is present.
const (
	EnvGatewayURL = "HOS_ASSIST_GATEWAY_URL"
	EnvGatewayKey = "HOS_ASSIST_GATEWAY_KEY"
	EnvDealerTag  = "HOS_ASSIST_DEALER_TAG"
)

// ModelConfig is the deployment's model configuration.
type ModelConfig struct {
	AnthropicKey     Secret // HOS_ASSIST_ANTHROPIC_API_KEY
	AnthropicBaseURL string // empty is the vendor's own endpoint
	GatewayURL       string // HOS_ASSIST_GATEWAY_URL, the OpenAI compatible gateway, version prefix included
	GatewayKey       Secret // HOS_ASSIST_GATEWAY_KEY, this dealer's key
	DealerTag        string // HOS_ASSIST_DEALER_TAG, this dealer's tag
}

// String keeps every credential out of any log line that formats the config.
func (m ModelConfig) String() string {
	return "ModelConfig{anthropic_key_present:" + boolWord(m.AnthropicKey.Set()) +
		" gateway_url_set:" + boolWord(m.GatewayURL != "") +
		" gateway_key_present:" + boolWord(m.GatewayKey.Set()) + "}"
}

// GoString matches String for %#v.
func (m ModelConfig) GoString() string { return m.String() }

func boolWord(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// AliasRow is one row of the alias table.
type AliasRow struct {
	Alias       string `json:"alias"`
	AdapterKind string `json:"adapter_kind"`
	BaseURL     string `json:"base_url"`
	Model       string `json:"model"`
	Enabled     bool   `json:"enabled"`
	Licence     string `json:"licence"`
	Region      string `json:"region"`
}

// DefaultAliases is the table as seeded: tanyai-premium is Claude Sonnet 5.5 through the native
// adapter; the other three are unconfigured and resolve to the stub.
func DefaultAliases() []AliasRow {
	return []AliasRow{
		{Alias: AliasMain, AdapterKind: AdapterStub, Enabled: false},
		{Alias: AliasAlt, AdapterKind: AdapterStub, Enabled: false},
		{Alias: AliasPremium, AdapterKind: AdapterAnthropic, Model: "claude-sonnet-5-5", Enabled: true, Licence: "commercial API terms", Region: "us"},
		{Alias: AliasCA, AdapterKind: AdapterStub, Enabled: false},
	}
}

// AliasStore reads the alias table. It is read per request.
type AliasStore interface {
	Aliases(ctx context.Context) ([]AliasRow, error)
}

// StaticAliases is an AliasStore over a fixed table: the default and the test double.
type StaticAliases []AliasRow

func (s StaticAliases) Aliases(context.Context) ([]AliasRow, error) { return []AliasRow(s), nil }

// IsAliasName reports whether s is one of the four aliases HH code may name.
func IsAliasName(s string) bool {
	switch s {
	case AliasMain, AliasAlt, AliasPremium, AliasCA:
		return true
	}
	return false
}

// DefaultRoute is the escalation order of a routed deployment.
func DefaultRoute() []string { return []string{AliasMain, AliasAlt, AliasPremium} }

// Router resolves an alias to a provider, per request.
type Router struct {
	aliases AliasStore
	cfg     ModelConfig
	stub    Provider
	http    *http.Client
	log     *slog.Logger
}

// NewRouter builds the router. stub is the provider every unresolvable alias lands on.
func NewRouter(aliases AliasStore, cfg ModelConfig, stub Provider, log *slog.Logger) *Router {
	if aliases == nil {
		aliases = StaticAliases(DefaultAliases())
	}
	if stub == nil {
		stub = NewStubProvider()
	}
	return &Router{aliases: aliases, cfg: cfg, stub: stub, log: log}
}

// String and GoString print presence only: the router holds both keys in its config, and fmt does
// not call methods on unexported fields, so the router answers for itself.
func (r Router) String() string   { return "Router{" + r.cfg.String() + "}" }
func (r Router) GoString() string { return r.String() }

// KeyPresent reports whether the Anthropic credential is configured (a boolean, never the key).
func (r *Router) KeyPresent() bool { return strings.TrimSpace(r.cfg.AnthropicKey.Reveal()) != "" }

// Resolve returns the provider for an alias and whether it is a real, configured one. An alias not
// in the table, disabled, of the stub kind, or missing its configuration resolves to the stub with
// ok false: a model string that is not an alias row is never sent to any vendor.
func (r *Router) Resolve(ctx context.Context, alias string) (p Provider, ok bool) {
	rows, err := r.aliases.Aliases(ctx)
	if err != nil {
		if r.log != nil {
			r.log.Warn("assist: cannot read the alias table, using the stub", "err", err)
		}
		return r.stub, false
	}
	for _, row := range rows {
		if row.Alias != alias {
			continue
		}
		if !row.Enabled || strings.TrimSpace(row.Model) == "" || row.Model == stubModelName {
			return r.stub, false
		}
		switch row.AdapterKind {
		case AdapterAnthropic:
			if !r.KeyPresent() {
				return r.stub, false
			}
			base := firstNonEmpty(row.BaseURL, r.cfg.AnthropicBaseURL)
			return NewAnthropicProvider(r.cfg.AnthropicKey.Reveal(), row.Model, base), true
		case AdapterOpenAICompatible:
			base := firstNonEmpty(row.BaseURL, r.cfg.GatewayURL)
			if strings.TrimSpace(base) == "" {
				return r.stub, false
			}
			return NewOpenAICompatProvider(OpenAICompatConfig{
				BaseURL: base, Key: r.cfg.GatewayKey, DealerTag: r.cfg.DealerTag, Model: row.Model,
			}), true
		}
		return r.stub, false
	}
	return r.stub, false
}

// stubModelName is the model the stub reports, and the 0153 seed value. It is never a vendor model.
const stubModelName = "hos-stub-1"
