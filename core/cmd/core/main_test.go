package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
)

func fullEnv() map[string]string {
	return map[string]string{
		"CORE_TOKEN_ISSUER":      "https://platform.example.test",
		"CORE_STAFF_AUDIENCE":    "hh-staff",
		"CORE_TENANT_AUDIENCE":   "hh-tenant",
		"CORE_JWKS_URL":          "https://platform.example.test/jwks",
		"CORE_DATABASE_URL":      "postgres://b3:b3@127.0.0.1:5432/b3?sslmode=disable",
		"CORE_APPWRITE_ENDPOINT": "https://platform.example.test/v1",
		"CORE_APPWRITE_PROJECT":  "b3-project",
		"CORE_APPWRITE_API_KEY":  "b3-key",
	}
}

// Configuration fails closed: every missing required value is named, equal
// audiences are refused, and the listen address alone defaults.
func TestLoadServeConfigFailsClosed(t *testing.T) {
	if _, err := loadServeConfig(func(string) string { return "" }); err == nil {
		t.Fatalf("empty environment accepted")
	} else {
		for _, name := range []string{
			"CORE_TOKEN_ISSUER", "CORE_STAFF_AUDIENCE", "CORE_TENANT_AUDIENCE",
			"CORE_JWKS_URL", "CORE_DATABASE_URL", "CORE_APPWRITE_ENDPOINT",
			"CORE_APPWRITE_PROJECT", "CORE_APPWRITE_API_KEY",
		} {
			if !strings.Contains(err.Error(), name) {
				t.Fatalf("missing value %s not named in: %v", name, err)
			}
		}
	}

	env := fullEnv()
	env["CORE_STAFF_AUDIENCE"] = env["CORE_TENANT_AUDIENCE"]
	if _, err := loadServeConfig(func(k string) string { return env[k] }); err == nil {
		t.Fatalf("equal audiences accepted")
	}

	env = fullEnv()
	cfg, err := loadServeConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("full environment: %v", err)
	}
	if cfg.listenAddr != ":8080" {
		t.Fatalf("listenAddr = %q", cfg.listenAddr)
	}
	if cfg.jwksCacheTTL != 0 || cfg.refreshFloor != 0 || cfg.clockSkew != 0 {
		t.Fatalf("timing knobs defaulted from somewhere: %+v", cfg)
	}
	env["CORE_LISTEN_ADDR"] = "127.0.0.1:9000"
	cfg, err = loadServeConfig(func(k string) string { return env[k] })
	if err != nil || cfg.listenAddr != "127.0.0.1:9000" {
		t.Fatalf("listenAddr override: %v %q", err, cfg.listenAddr)
	}
}

// The optional timing knobs: a set-but-unparseable or non-positive value is
// refused by name, and a valid one travels into the configuration.
func TestLoadServeConfigTimingKnobs(t *testing.T) {
	for _, raw := range []string{"not-a-duration", "0s", "-5m"} {
		env := fullEnv()
		env["CORE_JWKS_CACHE_TTL"] = raw
		if _, err := loadServeConfig(func(k string) string { return env[k] }); err == nil || !strings.Contains(err.Error(), "CORE_JWKS_CACHE_TTL") {
			t.Fatalf("value %q accepted or unnamed: %v", raw, err)
		}
	}
	env := fullEnv()
	env["CORE_JWKS_CACHE_TTL"] = "9m"
	env["CORE_JWKS_REFRESH_FLOOR"] = "45s"
	env["CORE_CLOCK_SKEW"] = "2m"
	cfg, err := loadServeConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("valid knobs: %v", err)
	}
	if cfg.jwksCacheTTL != 9*time.Minute || cfg.refreshFloor != 45*time.Second || cfg.clockSkew != 2*time.Minute {
		t.Fatalf("knobs = %+v", cfg)
	}
}

// garbageKidToken builds a compact JWS naming a kid the served set never
// carried. Verification stops at the key lookup, so the payload needs no
// valid signature: the token exists to trigger KeyByID, never to pass it.
func garbageKidToken(kid string) string {
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "kid": kid, "typ": "JWT"})
	claims, _ := json.Marshal(map[string]any{
		"iss": "https://platform.example.test", "sub": "attacker", "aud": "hh-staff",
		"iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	return b64(header) + "." + b64(claims) + "." + b64([]byte("not-a-signature"))
}

// The refresh floor default applies through the serve configuration path:
// with CORE_JWKS_REFRESH_FLOOR unset, loadServeConfig leaves the zero
// value, and the Config serve builds from it throttles unknown-kid
// refetches to the identity package default. An unthrottled default would
// let every unauthenticated garbage-kid request fetch the JWK Set, so the
// fetch count here is the availability claim, measured not asserted.
func TestServeRefreshFloorDefaultApplies(t *testing.T) {
	var mu sync.Mutex
	fetches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		fetches++
		mu.Unlock()
		fmt.Fprint(w, `{"keys":[]}`)
	}))
	defer srv.Close()

	env := fullEnv()
	env["CORE_JWKS_URL"] = srv.URL
	cfg, err := loadServeConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("loadServeConfig: %v", err)
	}
	if cfg.refreshFloor != 0 {
		t.Fatalf("unset CORE_JWKS_REFRESH_FLOOR must leave the zero value, got %v", cfg.refreshFloor)
	}
	verifier, err := identity.NewVerifier(identity.Config{
		JWKSURL:        cfg.jwksURL,
		Issuer:         cfg.issuer,
		StaffAudience:  cfg.staffAudience,
		TenantAudience: cfg.tenantAudience,
		KeyCacheTTL:    cfg.jwksCacheTTL,
		RefreshFloor:   cfg.refreshFloor,
		ClockSkew:      cfg.clockSkew,
		HTTPClient:     srv.Client(),
	})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		if _, err := verifier.Verify(ctx, garbageKidToken(fmt.Sprintf("garbage-kid-%d", i))); err == nil {
			t.Fatalf("garbage kid %d verified", i)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if fetches != 1 {
		t.Fatalf("unset floor through the serve path: %d fetches for 6 garbage-kid tokens, want 1 (the default floor must throttle)", fetches)
	}
}
