//go:build demo

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/flows"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/issuer"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/demo/membership"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
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
	// The demo mode has no exchange.
	if c := serveOnce(rec.inner, http.MethodPost, "/api/v1/auth/exchange").Code; c != http.StatusNotFound {
		t.Fatalf("demo mode serves the exchange: %d", c)
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
		"CORE_PERSONA_CALLER":           "open",
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
	if _, ok := graph.members.(*membership.Composite); !ok {
		t.Fatalf("membership source is %T, want the composite (static personas, FB Console for the rest)", graph.members)
	}
	// The outer type alone would pass a flow table source wrapped in the
	// cache. Behaviour decides: a flow table account in the demo org is a
	// member under the flow table source and is NOT under FB Console, whose
	// fake holds no team for it; and a real Console member is admitted, so
	// the answer provably came from the Console source.
	flowAccount := flows.All()[0].PlatformUserID
	if ok, err := graph.members.OrgMember(context.Background(), flowAccount, flows.OrgSlug); err != nil || ok {
		t.Fatalf("a flow table account is a member under the console mode (ok=%v err=%v): the flow table source answered", ok, err)
	}
	appwrite.setTeam("console-user-2", "org-"+flows.OrgSlug)
	if ok, err := graph.members.OrgMember(context.Background(), "console-user-2", flows.OrgSlug); err != nil || !ok {
		t.Fatalf("a Console team member is refused (ok=%v err=%v)", ok, err)
	}
	// The composite answers a persona subject from the static source, and a
	// subject with a colon that is not on the list is no member; neither
	// reaches FB Console (its fake holds no team for either).
	if ok, err := graph.members.OrgMember(context.Background(), "persona:sales", flows.OrgSlug); err != nil || !ok {
		t.Fatalf("persona:sales is not a member of the demo org (ok=%v err=%v)", ok, err)
	}
	for _, sub := range []string{"persona:nobody", "persona:contractor-owner", "console-user-2:x"} {
		appwrite.setTeam(sub, "org-"+flows.OrgSlug)
		if ok, err := graph.members.OrgMember(context.Background(), sub, flows.OrgSlug); err != nil || ok {
			t.Fatalf("%q is a member (ok=%v err=%v)", sub, ok, err)
		}
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
	// The console mode mounts the exchange: an empty body is the gate 1 400.
	if c := serveOnce(rec.inner, http.MethodPost, "/api/v1/auth/exchange").Code; c != http.StatusBadRequest {
		t.Fatalf("console mode exchange answered %d, want 400", c)
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
		"no mode":      {},
		"unknown mode": {"CORE_SIGNIN_MODE": "both"},
		"console without issuer and Console names": {"CORE_SIGNIN_MODE": "console"},
	} {
		if _, err := loadDemoServeConfig(get(extra)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if cfg, err := loadDemoServeConfig(get(map[string]string{"CORE_SIGNIN_MODE": "demo"})); err != nil || cfg.signin != signinDemo {
		t.Errorf("demo mode refused: %v", err)
	}
}

// The persistent minter refuses the demo issuer's claim value. The refusal
// in the identity package is a second copy of the constant; this pins it to
// the demo issuer's, so a rename there cannot silently stop the guard.
func TestPersistentMinterRefusesTheDemoIssuersClaimValue(t *testing.T) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identity.NewSessionMinter(k, issuer.ClaimIssuer, "hh-staff"); err == nil {
		t.Fatalf("a persistent key may mint as %q", issuer.ClaimIssuer)
	}
}

// The demo build in console mode refuses to boot without the key file and
// with the demo issuer, through the real demoRegisterRoutes (the shared
// loader is exercised through this build's own path, not only the default's).
func TestDemoBuildConsoleBootRefusals(t *testing.T) {
	appwrite := newAppwriteFake(t)
	keyFile, _ := writeSigninKey(t, t.TempDir(), "current.pem")
	names := func(extra map[string]string) map[string]string {
		m := map[string]string{
			"CORE_SIGNIN_MODE":       "console",
			"CORE_TOKEN_ISSUER":      "hh-demo-testers",
			"CORE_APPWRITE_ENDPOINT": appwrite.server.URL,
			"CORE_APPWRITE_PROJECT":  "fx-appwrite-project",
			"CORE_APPWRITE_API_KEY":  "fx-appwrite-key-placeholder",
			"CORE_PERSONA_CALLER":    "open",
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := map[string]map[string]string{
		"no key file":      names(nil),
		"the demo issuer":  names(map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": keyFile, "CORE_TOKEN_ISSUER": "hh-demo"}),
		"key file missing": names(map[string]string{"CORE_SESSION_SIGNING_KEY_FILE": filepath.Join(t.TempDir(), "absent.pem")}),
	}
	for name, extra := range cases {
		if _, err := rgDemoGateCaseErr(t, extra); err == nil {
			t.Errorf("%s: demoRegisterRoutes booted", name)
		}
	}
}
