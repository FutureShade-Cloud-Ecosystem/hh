package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/identity"
	"github.com/FutureShade-Cloud-Ecosystem/hh/core/internal/platform/authz"
)

// serveOnce drives one request through the registered mux.
func serveOnce(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

// The default build, console mode: the persistent key source, the real
// issuer, FB Console membership behind its cache, the JWK Set route, and
// no demo route (the demo module is not compiled into this build).
func TestBootDefaultBuildConsoleCell(t *testing.T) {
	env := newCompEnv(t)
	rec, graph := registeredSurface(t, env)

	if graph.signin != signinConsole {
		t.Fatalf("signin = %q", graph.signin)
	}
	if graph.session == nil {
		t.Fatal("no persistent session composition")
	}
	if graph.demoMint != nil {
		t.Fatal("the default build carries a demo mint")
	}
	if _, ok := graph.members.(*authz.CachedMembership); !ok {
		t.Fatalf("membership source is %T, want the cached FB Console source", graph.members)
	}

	// The core verifies its own token with no HTTP hop: the verifier was
	// built over the session key set (no JWK Set URL is configured).
	tok, err := graph.session.minter.Mint("console-user-1", graph.session.exchangeTTL)
	if err != nil {
		t.Fatal(err)
	}
	got, err := graph.verify.Verify(context.Background(), tok)
	if err != nil || got.Subject != "console-user-1" || got.Class != identity.ClassStaff {
		t.Fatalf("verify own token: %+v %v", got, err)
	}

	// The JWK Set route is public and serves the same keys.
	rsp := serveOnce(rec.inner, http.MethodGet, "/.well-known/jwks.json")
	if rsp.Code != http.StatusOK {
		t.Fatalf("jwks status = %d", rsp.Code)
	}
	if rsp.Body.String() != string(graph.session.keys.JWKS()) {
		t.Fatal("the route does not serve the session key set")
	}
	if rsp.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("Cache-Control = %q", rsp.Header().Get("Cache-Control"))
	}

	// No old demo mint routes.
	for _, p := range []string{"/api/v1/demo/flows", "/api/v1/demo/session"} {
		for _, m := range []string{http.MethodGet, http.MethodPost} {
			if c := serveOnce(rec.inner, m, p).Code; c != http.StatusNotFound && c != http.StatusMethodNotAllowed {
				t.Errorf("%s %s answered %d", m, p, c)
			}
		}
	}
}

// A missing key refuses the boot of the full composition, not only the
// config loader.
func TestBootDefaultBuildRefusesWithoutAKey(t *testing.T) {
	env := newCompEnv(t)
	env.set("CORE_SESSION_SIGNING_KEY_FILE", "")
	if _, err := registerRoutes(&recordingRegistrar{inner: http.NewServeMux()}, env.getenv); err == nil {
		t.Fatal("booted without a session signing key")
	}
	env = newCompEnv(t)
	env.set("CORE_SIGNIN_MODE", "demo")
	if _, err := registerRoutes(&recordingRegistrar{inner: http.NewServeMux()}, env.getenv); err == nil {
		t.Fatal("the default build booted in demo mode")
	}
	env = newCompEnv(t)
	env.set("CORE_TOKEN_ISSUER", "hh-demo")
	if _, err := registerRoutes(&recordingRegistrar{inner: http.NewServeMux()}, env.getenv); err == nil {
		t.Fatal("booted with the hh-demo issuer")
	}
}

// The boot of the real composition logs the warning when the stale remote
// URL is still configured.
func TestBootDefaultBuildWarnsAboutAStaleJWKSURL(t *testing.T) {
	env := newCompEnv(t)
	env.set("CORE_JWKS_URL", "https://bridge.example.test/jwks")
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(captureLog(&buf))
	t.Cleanup(func() { slog.SetDefault(prev) })
	registeredSurface(t, env)
	if !strings.Contains(buf.String(), "CORE_JWKS_URL") {
		t.Fatalf("no warning at boot: %q", buf.String())
	}
	if strings.Contains(buf.String(), "bridge.example.test") {
		t.Fatal("the warning carries the value")
	}
}
