package main

import (
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

func exchangeEnv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestExchangeConfigDefaultsAndLists(t *testing.T) {
	cfg, err := loadExchangeConfig(exchangeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.domains) != 0 || len(cfg.addresses) != 0 {
		t.Fatalf("unset lists are not empty: %+v", cfg)
	}
	if cfg.jwtBound < identity.MinJWTClaimBound {
		t.Fatalf("default bound %v is below the console JWT life", cfg.jwtBound)
	}
	cfg, err = loadExchangeConfig(exchangeEnv(map[string]string{
		"CORE_SIGNIN_EMAIL_DOMAINS":   " FutureBuild.ai, hardscapehouse.com ",
		"CORE_SIGNIN_EMAIL_ADDRESSES": "One@Example.COM",
		"CORE_TRUSTED_PROXIES":        "127.0.0.1, ::1",
		"CORE_EXCHANGE_JWT_BOUND":     "20m",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.domains) != 2 || cfg.domains[0] != "futurebuild.ai" || cfg.addresses[0] != "one@example.com" || len(cfg.trusted) != 2 || cfg.jwtBound != 20*time.Minute {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestExchangeConfigRefusals(t *testing.T) {
	cases := map[string]map[string]string{
		"bound below the console JWT life": {"CORE_EXCHANGE_JWT_BOUND": "899s"},
		"bound not a duration":             {"CORE_EXCHANGE_JWT_BOUND": "soon"},
		"bound zero":                       {"CORE_EXCHANGE_JWT_BOUND": "0s"},
		"bound negative":                   {"CORE_EXCHANGE_JWT_BOUND": "-20m"},
		"proxy not an address":             {"CORE_TRUSTED_PROXIES": "proxy.example"},
		"proxy range":                      {"CORE_TRUSTED_PROXIES": "10.0.0.0/8"},
	}
	for name, env := range cases {
		if _, err := loadExchangeConfig(exchangeEnv(env)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// The bound exactly at the floor is fine.
	if _, err := loadExchangeConfig(exchangeEnv(map[string]string{"CORE_EXCHANGE_JWT_BOUND": "900s"})); err != nil {
		t.Fatal(err)
	}
}
