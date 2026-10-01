//go:build demo

package main

import (
	"strings"
	"testing"
)

// G2 through the serve entry: a box that has not said demo twice stops
// demoServe before anything else runs, with the offending variable named.
// The cases are the guard's table, replayed through the function the
// entrypoint dispatches to, so removing the guard call from demoServe
// fails here and not only in the boot package's own suite.
func TestDemoServeRefusesABoxThatDoesNotSayDemo(t *testing.T) {
	full := map[string]string{
		"DEMO_MODE":            "true",
		"APP_ENV":              "demo",
		"CORE_DATABASE_URL":    "postgres://b3:b3@127.0.0.1:5432/b3?sslmode=disable",
		"CORE_STAFF_AUDIENCE":  "hh-staff",
		"CORE_TENANT_AUDIENCE": "hh-tenant",
	}
	cases := []struct {
		name  string
		drop  string
		set   map[string]string
		inErr string
	}{
		{"environment unset", "APP_ENV", nil, "APP_ENV"},
		{"environment dev", "", map[string]string{"APP_ENV": "dev"}, "APP_ENV"},
		{"mode unset", "DEMO_MODE", nil, "DEMO_MODE"},
	}
	for _, c := range cases {
		env := map[string]string{}
		for k, v := range full {
			env[k] = v
		}
		if c.drop != "" {
			delete(env, c.drop)
		}
		for k, v := range c.set {
			env[k] = v
		}
		getenv := func(k string) string { return env[k] }
		lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
		err := demoServe(getenv, lookup)
		if err == nil {
			t.Fatalf("%s: demoServe accepted", c.name)
		}
		if !strings.Contains(err.Error(), c.inErr) {
			t.Fatalf("%s: refusal %q does not name %s", c.name, err, c.inErr)
		}
	}
}
