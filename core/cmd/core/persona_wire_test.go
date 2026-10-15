//go:build demo

package main

import (
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/persona"
)

func personaGetenv(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestLoadPersonaConfigReadsTheCallerRule(t *testing.T) {
	cfg, err := loadPersonaConfig(personaGetenv(map[string]string{
		"CORE_PERSONA_CALLER":   " partner ",
		"CORE_PERSONA_PARTNERS": "66a1b2c3d4e5f60718293a4b, 66a1b2c3d4e5f60718293a4c ,",
		"CORE_TRUSTED_PROXIES":  "127.0.0.1",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.mode != persona.CallerPartner || len(cfg.partners) != 2 || len(cfg.trusted) != 1 {
		t.Fatalf("%+v", cfg)
	}
	cfg, err = loadPersonaConfig(personaGetenv(map[string]string{"CORE_PERSONA_CALLER": "open"}))
	if err != nil || cfg.mode != persona.CallerOpen || len(cfg.partners) != 0 {
		t.Fatalf("%+v %v", cfg, err)
	}
	// An empty partner list is valid: it refuses every caller.
	cfg, err = loadPersonaConfig(personaGetenv(map[string]string{"CORE_PERSONA_CALLER": "partner"}))
	if err != nil || cfg.mode != persona.CallerPartner || len(cfg.partners) != 0 {
		t.Fatalf("%+v %v", cfg, err)
	}
}

func TestLoadPersonaConfigRefusals(t *testing.T) {
	for name, values := range map[string]map[string]string{
		"no caller rule":          {},
		"blank caller rule":       {"CORE_PERSONA_CALLER": "  "},
		"unknown caller rule":     {"CORE_PERSONA_CALLER": "both"},
		"upper case":              {"CORE_PERSONA_CALLER": "Partner"},
		"partner id with colon":   {"CORE_PERSONA_CALLER": "partner", "CORE_PERSONA_PARTNERS": "persona:sales"},
		"partner id not Console":  {"CORE_PERSONA_CALLER": "partner", "CORE_PERSONA_PARTNERS": "has space"},
		"partner id too long":     {"CORE_PERSONA_CALLER": "partner", "CORE_PERSONA_PARTNERS": strings.Repeat("a", 37)},
		"open copy with partners": {"CORE_PERSONA_CALLER": "open", "CORE_PERSONA_PARTNERS": "66a1b2c3d4e5f60718293a4b"},
		"proxy list not address":  {"CORE_PERSONA_CALLER": "open", "CORE_TRUSTED_PROXIES": "nope"},
	} {
		_, err := loadPersonaConfig(personaGetenv(values))
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		for _, v := range values {
			if v != "" && len(v) > 8 && strings.Contains(err.Error(), v) {
				t.Errorf("%s: the refusal echoes a value: %v", name, err)
			}
		}
	}
}

// The two new names are classed: allowed on a demo box, and in the maintained
// inventory.
func TestPersonaNamesAreClassed(t *testing.T) {
	for _, name := range []string{"CORE_PERSONA_CALLER", "CORE_PERSONA_PARTNERS"} {
		allowed, inventoried := false, false
		for _, n := range demoEnvAllowCoreNames {
			allowed = allowed || n == name
		}
		for _, n := range envInventoryFixture {
			inventoried = inventoried || n == name
		}
		if !allowed || !inventoried {
			t.Errorf("%s: allowed=%v inventoried=%v", name, allowed, inventoried)
		}
	}
}
