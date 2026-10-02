package main

import (
	"net/http"
	"testing"
)

// HH-200: the address serve binds is the one resolved at config load and carried on the graph, not a
// second reading of the environment. Drives the real registerRoutes over the composition database
// (gated on HH_CORE_TEST_DATABASE_URL like the other composition suites).
func TestRegisterRoutesCarriesTheResolvedListenAddr(t *testing.T) {
	env := newCompEnv(t)
	get := func(k string) string {
		switch k {
		case "PORT":
			return "8097"
		case "CORE_LISTEN_ADDR":
			return ""
		}
		return env.getenv(k)
	}
	rec := &recordingRegistrar{inner: http.NewServeMux()}
	graph, err := registerRoutes(rec, get)
	if err != nil {
		t.Fatalf("registerRoutes: %v", err)
	}
	t.Cleanup(graph.pool.Close)
	if graph.listenAddr != ":8097" {
		t.Fatalf("graph.listenAddr = %q, want :8097 (PORT honoured when CORE_LISTEN_ADDR is unset)", graph.listenAddr)
	}
}
