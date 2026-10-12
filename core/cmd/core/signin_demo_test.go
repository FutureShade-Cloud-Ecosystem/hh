//go:build demo

package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/membership"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// The demo build, demo mode: today's composition exactly. The in memory
// issuer, the flow table membership, the old mint routes, and neither a
// persistent session nor a JWK Set route.
func TestBootDemoBuildDemoCell(t *testing.T) {
	rec, graph := rgDemoGateCaseWith(t, nil)
	if graph.signin != signinDemo {
		t.Fatalf("signin = %q", graph.signin)
	}
	if graph.session != nil {
		t.Fatal("demo mode carries a persistent session composition")
	}
	if graph.demoMint == nil {
		t.Fatal("demo mode has no in memory mint")
	}
	if _, ok := graph.members.(*membership.Source); !ok {
		t.Fatalf("membership source is %T, want the flow table source", graph.members)
	}
	tok, err := graph.demoMint("platform-user-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := graph.verify.Verify(context.Background(), tok); err != nil {
		t.Fatalf("verify the in memory issuer's token: %v", err)
	}
	if c := serveOnce(rec.inner, http.MethodGet, "/api/v1/demo/flows").Code; c != http.StatusOK {
		t.Fatalf("demo flows answered %d", c)
	}
	if c := serveOnce(rec.inner, http.MethodGet, "/.well-known/jwks.json").Code; c != http.StatusNotFound {
		t.Fatalf("demo mode serves a JWK Set: %d", c)
	}
}

// The demo build, console mode: the persistent key source and issuer, FB
// Console membership behind the cache (the composite static persona source
// is item 7's, a named seam), the JWK Set route, and the old demo mint
// routes NOT registered.
func TestBootDemoBuildConsoleCell(t *testing.T) {
	appwrite := newAppwriteFake(t)
	keyFile, _ := writeSigninKey(t, t.TempDir(), "current.pem")
	rec, graph := rgDemoGateCaseWith(t, map[string]string{
		"CORE_SIGNIN_MODE":              "console",
		"CORE_TOKEN_ISSUER":             "hh-demo-testers",
		"CORE_APPWRITE_ENDPOINT":        appwrite.server.URL,
		"CORE_APPWRITE_PROJECT":         "fx-appwrite-project",
		"CORE_APPWRITE_API_KEY":         "fx-appwrite-key-placeholder",
		"CORE_SESSION_SIGNING_KEY_FILE": keyFile,
	})
	if graph.signin != signinConsole {
		t.Fatalf("signin = %q", graph.signin)
	}
	if graph.session == nil {
		t.Fatal("no persistent session composition")
	}
	if graph.demoMint != nil {
		t.Fatal("console mode carries the in memory mint")
	}
	if _, ok := graph.members.(*authz.CachedMembership); !ok {
		t.Fatalf("membership source is %T, want the cached FB Console source", graph.members)
	}
	tok, err := graph.session.minter.Mint("console-user-1", graph.session.exchangeTTL)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := graph.verify.Verify(context.Background(), tok); err != nil || got.Class != identity.ClassStaff {
		t.Fatalf("verify own token: %+v %v", got, err)
	}
	if c := serveOnce(rec.inner, http.MethodGet, "/.well-known/jwks.json").Code; c != http.StatusOK {
		t.Fatalf("jwks answered %d", c)
	}
	for _, c := range []struct{ m, p string }{{http.MethodGet, "/api/v1/demo/flows"}, {http.MethodPost, "/api/v1/demo/session"}} {
		if code := serveOnce(rec.inner, c.m, c.p).Code; code != http.StatusNotFound {
			t.Errorf("%s %s answered %d, want 404", c.m, c.p, code)
		}
	}
}

// The demo build refuses a missing mode, an unknown one, and a console
// mode without the names it needs.
func TestDemoBuildSigninModeRefusals(t *testing.T) {
	base := map[string]string{
		"CORE_DATABASE_URL":    "postgres://x:x@127.0.0.1:1/demo",
		"CORE_STAFF_AUDIENCE":  "hh-staff",
		"CORE_TENANT_AUDIENCE": "hh-tenant",
	}
	get := func(extra map[string]string) func(string) string {
		return func(k string) string {
			if v, ok := extra[k]; ok {
				return v
			}
			return base[k]
		}
	}
	for name, extra := range map[string]map[string]string{
		"no mode":              {},
		"unknown mode":         {"CORE_SIGNIN_MODE": "both"},
		"console without keys": {"CORE_SIGNIN_MODE": "console"},
	} {
		if _, err := loadDemoServeConfig(get(extra)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if cfg, err := loadDemoServeConfig(get(map[string]string{"CORE_SIGNIN_MODE": "demo"})); err != nil || cfg.signin != signinDemo {
		t.Errorf("demo mode refused: %v", err)
	}
}
