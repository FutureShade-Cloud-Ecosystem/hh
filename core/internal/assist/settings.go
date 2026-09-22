package assist

// settings.go, the four config keys (migration 0153) and the provider they select.
//
// EVERY KEY HAS A CODE DEFAULT AND AN ABSENT ROW RESOLVES TO IT. A half-seeded database, a
// deployment where 0153 has not run yet, an unreachable config table during startup, all three
// boot, with the assistant in its safest configuration: the stub provider, chat on, spend hidden
// from non-admins. The alternative (refuse to start) turns an optional feature's misconfiguration
// into an ERP outage.

import (
	"context"
	"log/slog"
	"strings"
)

// The config keys, as constants, so the migration, the store, the handler and the admin UI cannot
// disagree about a string.
const (
	KeyProvider               = "provider"
	KeyModel                  = "model"
	KeyChatEnabled            = "chat_enabled"
	KeySpendVisibleToNonAdmin = "spend_visible_to_non_admin"
)

// ProviderStub / ProviderAnthropic are the two legal values of KeyProvider.
const (
	ProviderStub      = "stub"
	ProviderAnthropic = "anthropic"
)

// Settings is the resolved configuration.
type Settings struct {
	Provider               string `json:"provider"`
	Model                  string `json:"model"`
	ChatEnabled            bool   `json:"chat_enabled"`
	SpendVisibleToNonAdmin bool   `json:"spend_visible_to_non_admin"`
}

// DefaultSettings is what an empty config table means.
func DefaultSettings() Settings {
	return Settings{
		Provider:               ProviderStub,
		Model:                  "hos-stub-1",
		ChatEnabled:            true,
		SpendVisibleToNonAdmin: false,
	}
}

// SettingStore reads and writes assist_settings.
type SettingStore interface {
	Settings(ctx context.Context) (map[string]string, error)
	SetSetting(ctx context.Context, key, value, actorID string) error
}

// StaticSettings is a SettingStore with no database, the test double, and the fallback shape.
type StaticSettings map[string]string

func (s StaticSettings) Settings(context.Context) (map[string]string, error) {
	return map[string]string(s), nil
}
func (s StaticSettings) SetSetting(_ context.Context, k, v, _ string) error { s[k] = v; return nil }

// SettingsFrom folds a raw key/value map onto the defaults.
//
// An unrecognised value for a boolean reads as FALSE except for chat_enabled, which reads as its
// default of true. That asymmetry is deliberate and is the "fail in the direction that is safe"
// rule applied twice: a typo must not silently EXPOSE spend to everyone, and a typo must not
// silently switch the pilot off.
func SettingsFrom(raw map[string]string) Settings {
	s := DefaultSettings()
	if v := strings.TrimSpace(raw[KeyProvider]); v != "" {
		s.Provider = strings.ToLower(v)
	}
	if v := strings.TrimSpace(raw[KeyModel]); v != "" {
		s.Model = v
	}
	if v, ok := raw[KeyChatEnabled]; ok {
		s.ChatEnabled = !isFalse(v)
	}
	if v, ok := raw[KeySpendVisibleToNonAdmin]; ok {
		s.SpendVisibleToNonAdmin = isTrue(v)
	}
	return s
}

func isTrue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}

func isFalse(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "no", "off":
		return true
	}
	return false
}

// NewProviderFromConfig picks the provider at boot.
//
// THE ONE PLACE THAT KNOWS FALLING BACK IS ACCEPTABLE. If 'anthropic' is configured and
// HOS_ASSIST_ANTHROPIC_API_KEY is not in the environment, this returns the stub and logs the fact
// ONCE, naming the variable and never its value. The app boots, the assistant works, and the
// operator has a log line that tells them exactly what to set.
//
// apiKey is passed IN rather than read here so this function has no dependency on the process
// environment and is therefore testable; main.go performs the one os.Getenv (PARK 1).
func NewProviderFromConfig(s Settings, apiKey, baseURL string, log *slog.Logger) Provider {
	if s.Provider != ProviderAnthropic {
		return NewStubProvider()
	}
	if strings.TrimSpace(apiKey) == "" {
		if log != nil {
			log.Warn("assist: provider 'anthropic' is configured but no API key is present, falling back to the deterministic stub provider",
				"env_var", EnvAPIKey)
		}
		return NewStubProvider()
	}
	if log != nil {
		// The MODEL is logged; the key is not, and there is no field here that could carry it.
		log.Info("assist: using the anthropic provider", "model", s.Model)
	}
	return NewAnthropicProvider(apiKey, s.Model, baseURL)
}
